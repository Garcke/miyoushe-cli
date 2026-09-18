// probe.go 实现视频本地探测：容器识别、时长、codec 与流式 MD5。
//
// 设计约束（总体架构 §2）：Go 单二进制分发，不依赖外部 ffprobe；探测只需
// 提供 getToken 所需的 duration 与上传 preflight 的摘要/大小，因此仅解析
// MP4/MOV 盒子结构（实抓 Commit SourceInfo：Format=MP4 / Codec=h264）。
// 其他容器（mkv/avi/ts 等 App 虽可播放）在 CLI 明确报"暂不支持"，不猜测
// 其字节布局。时长/大小上限由服务端校验，本地不硬编码。
package video

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"

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

// Probe 打开文件完成探测。同一个文件句柄先流式算 MD5，再解析结构，
// 避免 hash 与读取之间文件被替换（社区功能设计 §9 的同句柄原则）。
func Probe(path string) (ProbeResult, *output.Error) {
	fail := func(format string, args ...any) (ProbeResult, *output.Error) {
		return ProbeResult{}, output.Err(output.CodeInputInvalid, format, args...)
	}
	f, err := os.Open(path)
	if err != nil {
		return fail("无法打开视频文件 %q", path)
	}
	defer f.Close()

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

	hasher := md5.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fail("读取视频文件失败")
	}
	res := ProbeResult{
		Absolute: path,
		Size:     st.Size(),
		MD5Hex:   hex.EncodeToString(hasher.Sum(nil)),
	}

	if oerr := inspectMP4(f, st.Size(), &res); oerr != nil {
		return fail("%s", oerr.Message)
	}
	return res, nil
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
