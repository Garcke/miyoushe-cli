package video

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/content"
	"mihoyo_cli/internal/session"
)

// ---- 合成 MP4 构造 ----

func mp4Box(typ string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(8+len(payload)))
	copy(b[4:8], typ)
	copy(b[8:], payload)
	return b
}

func mp4FullBox(typ string, ver byte, flags uint32, payload []byte) []byte {
	head := []byte{ver, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	return mp4Box(typ, append(head, payload...))
}

// buildMP4 构造最小合法 MP4：ftyp + moov{mvhd, trak(vide/avc1), trak(soun/mp4a)}。
// 真实播放器不会接受它（无 mdat 媒体数据），但盒子结构足以驱动探测。
func buildMP4(brand string, timescale, duration uint32) []byte {
	ftyp := mp4Box("ftyp", append([]byte(brand), 0, 0, 0, 0))

	mvhd := mp4FullBox("mvhd", 0, 0, []byte{
		0, 0, 0, 0, // creation
		0, 0, 0, 0, // modification
		byte(timescale >> 24), byte(timescale >> 16), byte(timescale >> 8), byte(timescale),
		byte(duration >> 24), byte(duration >> 16), byte(duration >> 8), byte(duration),
	})

	hdlrVide := mp4FullBox("hdlr", 0, 0, []byte{0, 0, 0, 0, 'v', 'i', 'd', 'e'})
	stsdVide := mp4FullBox("stsd", 0, 0, append([]byte{0, 0, 0, 1}, mp4Box("avc1", make([]byte, 8))...))
	stblVide := mp4Box("stbl", stsdVide)
	minfVide := mp4Box("minf", stblVide)
	mdiaVide := mp4Box("mdia", append(hdlrVide, minfVide...))
	trakVide := mp4Box("trak", mdiaVide)

	hdlrSoun := mp4FullBox("hdlr", 0, 0, []byte{0, 0, 0, 0, 's', 'o', 'u', 'n'})
	stsdSoun := mp4FullBox("stsd", 0, 0, append([]byte{0, 0, 0, 1}, mp4Box("mp4a", make([]byte, 8))...))
	stblSoun := mp4Box("stbl", stsdSoun)
	minfSoun := mp4Box("minf", stblSoun)
	mdiaSoun := mp4Box("mdia", append(hdlrSoun, minfSoun...))
	trakSoun := mp4Box("trak", mdiaSoun)

	moov := mp4Box("moov", append(mvhd, append(trakVide, trakSoun...)...))
	return append(ftyp, moov...)
}

func writeTempMP4(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写临时文件: %v", err)
	}
	return path
}

func TestProbe_MPV4DurationAndCodecs(t *testing.T) {
	data := buildMP4("isom", 1000, 46613)
	path := writeTempMP4(t, data)

	res, oerr := Probe(path)
	if oerr != nil {
		t.Fatalf("Probe: %v", oerr)
	}
	if res.Container != "mp4" {
		t.Errorf("container = %s", res.Container)
	}
	if res.DurationMS != 46613 {
		t.Errorf("duration = %d", res.DurationMS)
	}
	if res.VideoCodec != "h264" || res.AudioCodec != "aac" {
		t.Errorf("codecs = %s/%s", res.VideoCodec, res.AudioCodec)
	}
	if res.Size != int64(len(data)) {
		t.Errorf("size = %d", res.Size)
	}
	// MD5 与独立计算一致。
	want := fmt.Sprintf("%x", md5.Sum(data))
	if res.MD5Hex != want {
		t.Errorf("md5 = %s, want %s", res.MD5Hex, want)
	}
}

func TestProbe_QuickTimeBrand(t *testing.T) {
	path := writeTempMP4(t, buildMP4("qt  ", 600, 3000))
	res, oerr := Probe(path)
	if oerr != nil {
		t.Fatalf("Probe: %v", oerr)
	}
	if res.Container != "mov" {
		t.Errorf("container = %s", res.Container)
	}
}

func TestProbe_Errors(t *testing.T) {
	t.Run("非 MP4 数据", func(t *testing.T) {
		path := writeTempMP4(t, []byte("this is not a video, just plain text padding...."))
		if _, oerr := Probe(path); oerr == nil || !strings.Contains(oerr.Message, "MP4") {
			t.Errorf("oerr = %v", oerr)
		}
	})
	t.Run("缺 moov", func(t *testing.T) {
		onlyFtyp := mp4Box("ftyp", []byte("isom\x00\x00\x00\x00"))
		path := writeTempMP4(t, onlyFtyp)
		if _, oerr := Probe(path); oerr == nil || !strings.Contains(oerr.Message, "moov") {
			t.Errorf("oerr = %v", oerr)
		}
	})
	t.Run("目录路径", func(t *testing.T) {
		if _, oerr := Probe(t.TempDir()); oerr == nil || !strings.Contains(oerr.Message, "常规文件") {
			t.Errorf("oerr = %v", oerr)
		}
	})
	t.Run("不存在", func(t *testing.T) {
		if _, oerr := Probe(filepath.Join(t.TempDir(), "nope.mp4")); oerr == nil {
			t.Error("期望报错")
		}
	})
}

// ---- 输入侧串联：ContentSpec → Probe → getToken ----

// TestInputChain_SpecToGetToken 验证输入侧关键断点：
// ContentSpec 解析出的视频块路径 → 探测出的 md5/duration → getToken 参数。
func TestInputChain_SpecToGetToken(t *testing.T) {
	data := buildMP4("isom", 1000, 46613)
	mp4Path := writeTempMP4(t, data)
	dir := filepath.Dir(mp4Path)

	specJSON := `{
		"schema_version": 1,
		"kind": "video",
		"gids": 8,
		"forum_id": 57,
		"subject": "标题",
		"blocks": [{"type": "video", "path": "video.mp4", "cover": "cover.png"}]
	}`
	spec, oerr := content.Parse([]byte(specJSON), dir)
	if oerr != nil {
		t.Fatalf("content.Parse: %v", oerr)
	}
	block, ok := spec.VideoBlock()
	if !ok {
		t.Fatal("缺少视频块")
	}

	probed, oerr := Probe(block.Video.Path.Absolute)
	if oerr != nil {
		t.Fatalf("Probe: %v", oerr)
	}
	if probed.DurationMS != 46613 {
		t.Errorf("duration = %d", probed.DurationMS)
	}

	var gotMD5, gotDuration, gotSize string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotMD5, gotDuration, gotSize = q.Get("md5"), q.Get("duration"), q.Get("size")
		w.Write([]byte(`{"retcode":0,"message":"OK","data":{"token":"{\"AccessKeyID\":\"a\",\"SecretAccessKey\":\"b\",\"SessionToken\":\"c\"}","callback_args":"cb"}}`))
	}))
	defer srv.Close()
	c, _ := api.New(srv.URL)
	sess := session.Session{UID: "1", MID: "m", Stoken: "s"}

	_, oerr = New(c).GetUploadToken(context.Background(), sess, probed.MD5Hex, probed.Size, probed.DurationMS, "video.mp4")
	if oerr != nil {
		t.Fatalf("GetUploadToken: %v", oerr)
	}
	wantMD5 := fmt.Sprintf("%x", md5.Sum(data))
	if gotMD5 != wantMD5 || gotDuration != "46613" || gotSize != strconv.Itoa(len(data)) {
		t.Errorf("getToken参数 md5=%s duration=%s size=%s", gotMD5, gotDuration, gotSize)
	}
}
