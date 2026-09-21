// probe.go 实现视频本地探测：容器识别、时长、codec 与流式 MD5。
//
// 设计约束（总体架构 §2）：Go 单二进制分发，不依赖外部 ffprobe；探测只需
// 提供 getToken 所需的 duration 与上传 preflight 的摘要/大小，因此仅解析
// MP4/MOV 盒子结构（实抓 Commit SourceInfo：Format=MP4 / Codec=h264）。
// 其他容器（mkv/avi/ts 等 App 虽可播放）在 CLI 明确报"暂不支持"，不猜测
// 其字节布局。时长/大小上限由服务端校验，本地不硬编码。
package video

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"mihoyo_cli/internal/output"
)

// ProbeResult 是一次视频探测的结果。
type ProbeResult struct {
	// Absolute 是传入的路径（调用方先解析为绝对路径）。
	Absolute string
	// Size 是文件字节数。
	Size int64
	// MD5Hex 是整文件 MD5（十六进制小写），与 getToken 的 md5 参数一致。
	MD5Hex string
	// Container 是 "mp4" 或 "mov"。
	Container string
	// VideoCodec 是首个视频轨 codec（h264/h265/…），来自 stsd 条目。
	VideoCodec string
	// AudioCodec 是首个音频轨 codec（aac/…），无音轨时为空。
	AudioCodec string
	// DurationMS 是 mvhd 时长换算的毫秒数（向上取整）。
	DurationMS int64
}

// codec 四字符码映射。清单来自 App 媒体类型映射（VideoUploadPresenter.U
// 的容器清单）与实抓 Commit SourceInfo（h264/aac）。
var (
	videoCodecFourCC = map[string]string{
		"avc1": "h264", "avc3": "h264",
		"hev1": "h265", "hvc1": "h265",
		"av01": "av1", "vp08": "vp8", "vp09": "vp9",
		"mp4v": "mpeg4",
	}
	audioCodecFourCC = map[string]string{
		"mp4a": "aac", "ac-3": "ac3", "ec-3": "eac3",
		"opus": "opus", "fLaC": "flac",
	}
)

// PreparedVideo 是一次准备完成、与仍打开的文件句柄绑定的视频源。
// MD5/SHA-256/大小/修改时间/容器/时长来自同一次准备读取，供 getToken、
// isExist、getVideoID 与上传阶段共同复用（R2）；不保存 Token、STS 或
// 服务端临时授权。准备失败时句柄由 PrepareVideo 自行关闭；
// 成功后由调用方 defer Close() 管理到流程结束。
type PreparedVideo struct {
	file       *os.File
	size       int64
	modTime    time.Time
	md5Hex     string
	sha256     []byte
	container  string
	videoCodec string
	audioCodec string
	durationMS int64
}

// PrepareVideo 打开普通文件，在同一遍有界读取中计算 MD5 与 SHA-256，
// 再从同一句柄解析容器/时长；读取循环检查 ctx（取消返回 CANCELLED），
// 并核对实际读取量与准备前后文件状态。失败时自行关闭句柄。
func PrepareVideo(ctx context.Context, path string) (*PreparedVideo, *output.Error) {
	fail := func(format string, args ...any) (*PreparedVideo, *output.Error) {
		return nil, output.Err(output.CodeInputInvalid, format, args...)
	}
	f, err := os.Open(path)
	if err != nil {
		return fail("无法打开视频文件 %q", path)
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()

	st, err := f.Stat()
	if err != nil {
		return fail("无法读取视频文件状态")
	}
	if !st.Mode().IsRegular() {
		return fail("视频路径不是常规文件: %q", path)
	}
	if st.Size() == 0 {
		return fail("视频文件为空")
	}

	md5h := md5.New()
	shah := sha256.New()
	buf := make([]byte, 1<<20)
	var read int64
	for read < st.Size() {
		if ctx.Err() != nil {
			return nil, output.Err(output.CodeCancelled, "已取消")
		}
		n := int64(len(buf))
		if remain := st.Size() - read; remain < n {
			n = remain
		}
		got, rerr := f.ReadAt(buf[:n], read)
		if int64(got) != n || (rerr != nil && rerr != io.EOF) {
			return fail("读取视频文件不完整（%d/%d 字节），文件可能已变化", got, n)
		}
		md5h.Write(buf[:n])
		shah.Write(buf[:n])
		read += n
	}

	// 准备前后状态核对（早期变化信号；摘要比对才是权威判据）。
	st2, err := f.Stat()
	if err != nil {
		return fail("无法复核视频文件状态")
	}
	if st2.Size() != st.Size() || !st2.ModTime().Equal(st.ModTime()) {
		return fail("视频文件在准备期间发生变化")
	}

	res := ProbeResult{Absolute: path, Size: st.Size(), MD5Hex: hex.EncodeToString(md5h.Sum(nil))}
	if oerr := inspectMP4(f, st.Size(), &res); oerr != nil {
		return fail("%s", oerr.Message)
	}

	ok = true
	return &PreparedVideo{
		file:       f,
		size:       res.Size,
		modTime:    st.ModTime(),
		md5Hex:     res.MD5Hex,
		sha256:     shah.Sum(nil),
		container:  res.Container,
		videoCodec: res.VideoCodec,
		audioCodec: res.AudioCodec,
		durationMS: res.DurationMS,
	}, nil
}

// ReaderAt 返回仍打开的文件句柄（同一句柄贯穿本次流程）。
func (p *PreparedVideo) ReaderAt() io.ReaderAt { return p.file }

// Size 是准备阶段记录的文件字节数。
func (p *PreparedVideo) Size() int64 { return p.size }

// MD5Hex 是准备阶段计算的整文件 MD5（小写十六进制），与 getToken 一致。
func (p *PreparedVideo) MD5Hex() string { return p.md5Hex }

// SHA256 返回准备阶段摘要的副本。
func (p *PreparedVideo) SHA256() []byte { return append([]byte(nil), p.sha256...) }

// Snapshot 返回准备阶段记录的大小与修改时间。
func (p *PreparedVideo) Snapshot() (int64, time.Time) { return p.size, p.modTime }

// State 返回调用时该文件的当前大小与修改时间（复核用）。
func (p *PreparedVideo) State() (int64, time.Time, *output.Error) {
	st, err := p.file.Stat()
	if err != nil {
		return 0, time.Time{}, output.Err(output.CodeInputInvalid, "无法读取视频文件状态")
	}
	return st.Size(), st.ModTime(), nil
}

// Container / VideoCodec / AudioCodec / DurationMS 暴露准备阶段的元数据。
func (p *PreparedVideo) Container() string  { return p.container }
func (p *PreparedVideo) VideoCodec() string { return p.videoCodec }
func (p *PreparedVideo) AudioCodec() string { return p.audioCodec }
func (p *PreparedVideo) DurationMS() int64  { return p.durationMS }

// Path 返回准备时的路径（仅用于展示，不用于重新打开文件）。
func (p *PreparedVideo) Path() string { return p.file.Name() }

// ProbeResult 返回与 Probe 兼容的元数据快照。
func (p *PreparedVideo) ProbeResult() ProbeResult {
	return ProbeResult{
		Absolute:   p.file.Name(),
		Size:       p.size,
		MD5Hex:     p.md5Hex,
		Container:  p.container,
		VideoCodec: p.videoCodec,
		AudioCodec: p.audioCodec,
		DurationMS: p.durationMS,
	}
}

// Close 关闭文件句柄；幂等（重复调用返回 nil）。
func (p *PreparedVideo) Close() error {
	if p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	return err
}

// Probe 是纯本地查看的兼容包装：准备后立即关闭句柄，只返回元数据。
// 上传流程必须使用 PrepareVideo 返回的仍打开的 PreparedVideo（R2），
// 不能拿 ProbeResult 的路径重新打开文件。
func Probe(path string) (ProbeResult, *output.Error) {
	pv, oerr := PrepareVideo(context.Background(), path)
	if oerr != nil {
		return ProbeResult{}, oerr
	}
	defer pv.Close()
	return pv.ProbeResult(), nil
}

// inspectMP4 扫描顶层盒子：ftyp 定容器，moov 内解析 mvhd 与各 trak。
func inspectMP4(f *os.File, size int64, res *ProbeResult) *output.Error {
	failf := func(format string, args ...any) *output.Error {
		return output.Err(output.CodeInputInvalid, format, args...)
	}

	var moovOff, moovBody int64
	off := int64(0)
	for off < size {
		// 尾部不足一个盒子头（部分封装工具会补齐到扇区）：容忍并停止扫描。
		if off+8 > size {
			break
		}
		typ, bodyOff, total, err := readBoxHeader(f, off, size)
		if err != nil {
			return failf("视频不是有效的 MP4/MOV 结构（%v）", err)
		}
		switch typ {
		case "ftyp":
			if brand, err := readAt(f, off+bodyOff, 4); err == nil && res.Container == "" {
				res.Container = containerOf(string(brand))
			}
		case "moov":
			moovOff, moovBody = off+bodyOff, total-bodyOff
		}
		if total <= 0 {
			return failf("视频盒子结构损坏")
		}
		off += total
	}
	if res.Container == "" {
		return failf("视频缺少 ftyp 盒子，不是 MP4/MOV")
	}
	if moovBody == 0 {
		return failf("视频缺少 moov 盒子（可能未完成写入或非普通媒体文件）")
	}

	// 遍历 moov 子盒子：mvhd 取时长，trak 取 codec。
	end := moovOff + moovBody
	off = moovOff
	for off < end {
		typ, bodyOff, total, err := readBoxHeader(f, off, end)
		if err != nil {
			return failf("moov 盒子损坏（%v）", err)
		}
		absBody := off + bodyOff
		switch typ {
		case "mvhd":
			if oerr := parseMVHD(f, absBody, res); oerr != nil {
				return oerr
			}
		case "trak":
			parseTrak(f, absBody, total-bodyOff, res)
		}
		if total <= 0 {
			return failf("moov 子盒子结构损坏")
		}
		off += total
	}
	if res.DurationMS <= 0 {
		return failf("无法从 mvhd 解析出时长")
	}
	return nil
}

// containerOf 按 ftyp major brand 区分 mp4/mov（QuickTime 的 brand 为 "qt  "）。
func containerOf(brand string) string {
	if brand == "qt  " {
		return "mov"
	}
	return "mp4"
}

// parseMVHD 解析媒体头：version 0（32 位）或 1（64 位）的 timescale/duration。
func parseMVHD(f *os.File, bodyOff int64, res *ProbeResult) *output.Error {
	vb, err := readAt(f, bodyOff, 1)
	if err != nil {
		return output.Err(output.CodeInputInvalid, "mvhd 过短")
	}
	switch vb[0] {
	case 0:
		// creation(4) modification(4) timescale(4) duration(4)
		b, err := readAt(f, bodyOff+4, 16)
		if err != nil {
			return output.Err(output.CodeInputInvalid, "mvhd v0 过短")
		}
		timescale := int64(binary.BigEndian.Uint32(b[8:12]))
		duration := int64(binary.BigEndian.Uint32(b[12:16]))
		res.DurationMS = msFromUnits(timescale, duration)
	case 1:
		// creation(8) modification(8) timescale(4) duration(8)
		b, err := readAt(f, bodyOff+4, 28)
		if err != nil {
			return output.Err(output.CodeInputInvalid, "mvhd v1 过短")
		}
		timescale := int64(binary.BigEndian.Uint32(b[16:20]))
		duration := int64(binary.BigEndian.Uint64(b[20:28]))
		res.DurationMS = msFromUnits(timescale, duration)
	default:
		return output.Err(output.CodeInputInvalid, "未知 mvhd 版本 %d", vb[0])
	}
	return nil
}

// parseTrak 在 trak 内定位 mdia。
func parseTrak(f *os.File, off, size int64, res *ProbeResult) {
	end := off + size
	for off < end {
		typ, bodyOff, total, err := readBoxHeader(f, off, end)
		if err != nil || total <= 0 {
			return
		}
		if typ == "mdia" {
			parseMdia(f, off+bodyOff, total-bodyOff, res)
			return
		}
		off += total
	}
}

// parseMdia 在 mdia 内解析 hdlr（轨类型）并进入 minf。
func parseMdia(f *os.File, off, size int64, res *ProbeResult) {
	handler := ""
	end := off + size
	for off < end {
		typ, bodyOff, total, err := readBoxHeader(f, off, end)
		if err != nil || total <= 0 {
			return
		}
		absBody := off + bodyOff
		switch typ {
		case "hdlr":
			// version+flags(4) pre_defined(4) handler_type(4)。
			if b, err := readAt(f, absBody+8, 4); err == nil {
				handler = string(b)
			}
		case "minf":
			parseMinf(f, absBody, total-bodyOff, handler, res)
		}
		off += total
	}
}

// parseMinf 在 minf 内定位 stbl。
func parseMinf(f *os.File, off, size int64, handler string, res *ProbeResult) {
	end := off + size
	for off < end {
		typ, bodyOff, total, err := readBoxHeader(f, off, end)
		if err != nil || total <= 0 {
			return
		}
		if typ == "stbl" {
			parseStbl(f, off+bodyOff, total-bodyOff, handler, res)
			return
		}
		off += total
	}
}

// parseStbl 在 stbl 内定位 stsd 并按轨类型登记 codec。轨信息是尽力而为：
// 任何解析失败都静默返回，不阻塞时长已知的探测结果。
func parseStbl(f *os.File, off, size int64, handler string, res *ProbeResult) {
	end := off + size
	for off < end {
		typ, bodyOff, total, err := readBoxHeader(f, off, end)
		if err != nil || total <= 0 {
			return
		}
		if typ == "stsd" {
			absBody := off + bodyOff
			bodyLen := total - bodyOff
			// stsd: version+flags(4) entry_count(4) entries…
			if bodyLen < 8 {
				return
			}
			countBuf, err := readAt(f, absBody+4, 4)
			if err != nil {
				return
			}
			entryCount := binary.BigEndian.Uint32(countBuf)
			entryEnd := absBody + bodyLen
			entryOff := absBody + 8
			for i := uint32(0); i < entryCount && entryOff+8 <= entryEnd; i++ {
				eTyp, _, eTotal, err := readBoxHeader(f, entryOff, entryEnd)
				if err != nil || eTotal <= 0 {
					return
				}
				switch handler {
				case "vide":
					if name, ok := videoCodecFourCC[eTyp]; ok && res.VideoCodec == "" {
						res.VideoCodec = name
					}
				case "soun":
					if name, ok := audioCodecFourCC[eTyp]; ok && res.AudioCodec == "" {
						res.AudioCodec = name
					}
				}
				entryOff += eTotal
			}
			return
		}
		off += total
	}
}

// readBoxHeader 读取盒子头：返回类型、body 相对偏移与盒子总大小。
// size==1 读 64 位 largesize；size==0 表示延伸到 limit（仅顶层合法）。
func readBoxHeader(r io.ReaderAt, off, limit int64) (typ string, bodyOff int64, total int64, err error) {
	if off+8 > limit {
		return "", 0, 0, fmt.Errorf("盒子头越界")
	}
	h := make([]byte, 8)
	if _, err := r.ReadAt(h, off); err != nil {
		return "", 0, 0, err
	}
	size := int64(binary.BigEndian.Uint32(h[0:4]))
	typ = string(h[4:8])
	bodyOff = 8
	switch size {
	case 1:
		if off+16 > limit {
			return "", 0, 0, fmt.Errorf("largesize 越界")
		}
		lb := make([]byte, 8)
		if _, err := r.ReadAt(lb, off+8); err != nil {
			return "", 0, 0, err
		}
		size = int64(binary.BigEndian.Uint64(lb))
		bodyOff = 16
	case 0:
		size = limit - off
	}
	if size < bodyOff || off+size > limit {
		return "", 0, 0, fmt.Errorf("盒子大小 %d 越界", size)
	}
	return typ, bodyOff, size, nil
}

// readAt 是 os.File.ReadAt 的便捷包装。
func readAt(f *os.File, off int64, n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := f.ReadAt(b, off)
	return b, err
}

// msFromUnits 把 timescale/duration 换算成毫秒（向上取整）。
func msFromUnits(timescale, duration int64) int64 {
	if timescale <= 0 {
		return 0
	}
	return (duration*1000 + timescale - 1) / timescale
}
