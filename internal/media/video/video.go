// video.go 实现米游社侧视频接口。接口定义来自 DEX 反编译的
// VideoApiService（Retrofit，全部挂 API_BBS 剖面），字段与参数类型已由
// 2026-09-11 实抓样本核对。
package video

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"mihoyo_cli/internal/api"
	"mihoyo_cli/internal/output"
	"mihoyo_cli/internal/protocol"
	"mihoyo_cli/internal/session"
)

// video_provider 恒为 "1"（VideoUploadPresenter 硬编码 P.W()）。
const VideoProvider = "1"

// Scene 是 isExist 的 scene 取值：普通上传 0（等级分享场景为 1）。
const SceneDefault = 0

// PreUpload 对应米游社 VideoPreUploadBean。isExist 与 getToken 共用该模型；
// 实测 isExist 命中时 data 额外携带 video_info.duration，getToken 未命中时
// 仅返回 token 与 callback_args，其余字段为零值。
type PreUpload struct {
	IsExist       bool           `json:"is_exist"`
	Token         api.FlexString `json:"token"`
	CallbackArgs  api.FlexString `json:"callback_args"`
	UploadAddress api.FlexString `json:"upload_address"`
	MediaID       api.FlexString `json:"media_id"`
	DurationMS    int64          `json:"duration"`
	VideoID       api.FlexString `json:"video_id"`
	VideoInfo     *videoInfoRaw  `json:"video_info"`
}

type videoInfoRaw struct {
	DurationMS int64 `json:"duration"`
}

// Duration 返回 video_info.duration（秒传命中场景由服务端确认）；
// 缺省时回退顶层 duration。
func (p *PreUpload) VideoDurationMS() int64 {
	if p.VideoInfo != nil && p.VideoInfo.DurationMS > 0 {
		return p.VideoInfo.DurationMS
	}
	return p.DurationMS
}

// ParseUploadToken 把 getToken 返回的 token JSON 字符串解析为 STS2
// 凭据。token 是 JSON 字符串（非对象），字段名逐字来自 DEX 的 Token 模型。
func ParseUploadToken(token string) (STSCredential, *output.Error) {
	var cred STSCredential
	if err := json.Unmarshal([]byte(token), &cred); err != nil {
		return STSCredential{}, output.Err(output.CodeRemoteRejected, "token 字段结构不符")
	}
	if cred.AccessKeyID == "" || cred.SecretAccessKey == "" || cred.SessionToken == "" {
		return STSCredential{}, output.Err(output.CodeRemoteRejected, "token 缺少必要凭据字段")
	}
	return cred, nil
}

// PublishPerm 是 /post/api/check/publishVideoPerm 的响应。
// 字段名以 2026-09-11 实抓样本为准（{"can_publish":true,"toast":""}）。
type PublishPerm struct {
	CanPublish bool   `json:"can_publish"`
	Toast      string `json:"toast"`
}

// CheckPublishPerm 查询当前账号是否有发布视频权限（上传前 preflight）。
func (s *Service) CheckPublishPerm(ctx context.Context, sess session.Session) (*PublishPerm, *output.Error) {
	var data PublishPerm
	if oerr := s.Client.DoJSON(ctx, "GET", "/post/api/check/publishVideoPerm", nil, nil, headers(sess), &data); oerr != nil {
		return nil, oerr
	}
	return &data, nil
}

// ResidualQuota 是 /video/api/residualTimes 的响应（上传配额）。
//
// CLI 以 App 剖面为准：App 端 DEX 的 VideoResidualBean 字段为
// count/maxCount（线上拼写按米游社 snake_case 策略推断为 count/max_count，
// 该端点尚未实抓）。网页端 wapi 端点实抓为 times/max_times，仅作参考——
// 两套拼写都容错解析，字段取值优先 App 端的 count 系。
type ResidualQuota struct {
	Count       int `json:"count"`
	MaxCount    int `json:"max_count"`
	MaxCountCam int `json:"maxCount"`
	Times       int `json:"times"`
	MaxTimes    int `json:"max_times"`
}

// Remaining 返回剩余上传次数：优先 App 端 bean 的 count，回退 web 的 times。
func (q *ResidualQuota) Remaining() int {
	if q.Count != 0 {
		return q.Count
	}
	return q.Times
}

// MaxCountBoth 返回配额上限：优先 App 端的 count 系字段，回退 web 的
// max_times。
func (q *ResidualQuota) MaxCountBoth() int {
	if q.MaxCount != 0 {
		return q.MaxCount
	}
	if q.MaxCountCam != 0 {
		return q.MaxCountCam
	}
	return q.MaxTimes
}

// ResidualTimes 查询剩余上传配额。
func (s *Service) ResidualTimes(ctx context.Context, sess session.Session) (*ResidualQuota, *output.Error) {
	var data ResidualQuota
	if oerr := s.Client.DoJSON(ctx, "GET", "/video/api/residualTimes", nil, nil, headers(sess), &data); oerr != nil {
		return nil, oerr
	}
	return &data, nil
}

// Service 是米游社视频接口服务。
type Service struct {
	Client *api.Client
}

// New 构造服务；Client 指向 bbs-api.miyoushe.com（测试可注入）。
func New(c *api.Client) *Service { return &Service{Client: c} }

func headers(sess session.Session) http.Header {
	h := protocol.CommonHeaders(protocol.ClientTypeAndroid, sess.Device())
	protocol.WithDS(h, protocol.NewDSBBS())
	protocol.WithCookie(h, sess.Cookie())
	return h
}

// IsExist 秒传检查。命中时直接返回服务端 video_id，调用方可跳过整个
// 上传层（实抓样本背书）。
func (s *Service) IsExist(ctx context.Context, sess session.Session, md5 string, scene int) (*PreUpload, *output.Error) {
	if md5 == "" {
		return nil, output.Err(output.CodeInputInvalid, "md5 不能为空")
	}
	q := url.Values{}
	q.Set("md5", md5)
	q.Set("scene", fmt.Sprintf("%d", scene))
	q.Set("video_provider", VideoProvider)

	var data PreUpload
	if oerr := s.Client.DoJSON(ctx, "GET", "/video/api/isExist", q, nil, headers(sess), &data); oerr != nil {
		return nil, oerr
	}
	return &data, nil
}

// GetUploadToken 获取上传凭据。size 为文件字节数，duration 为毫秒。
// 未命中秒传时 data 只含 token 与 callback_args。
func (s *Service) GetUploadToken(ctx context.Context, sess session.Session, md5 string, sizeBytes, durationMS int64, name string) (*PreUpload, *output.Error) {
	if md5 == "" {
		return nil, output.Err(output.CodeInputInvalid, "md5 不能为空")
	}
	if sizeBytes <= 0 {
		return nil, output.Err(output.CodeInputInvalid, "文件大小无效")
	}
	q := url.Values{}
	q.Set("size", fmt.Sprintf("%d", sizeBytes))
	q.Set("duration", fmt.Sprintf("%d", durationMS))
	q.Set("name", name)
	q.Set("md5", md5)
	q.Set("video_provider", VideoProvider)

	var data PreUpload
	if oerr := s.Client.DoJSON(ctx, "GET", "/video/api/getToken", q, nil, headers(sess), &data); oerr != nil {
		return nil, oerr
	}
	return &data, nil
}

// retcodeUploadCallbackPending 是 Commit 后异步回调未完成时 getVideoID
// 返回的明确状态；只对它做有界退避重试（ARCHITECTURE-V2 §8.2），
// 其它错误不泛化为“等等就好”。
const retcodeUploadCallbackPending = 16006

// getVideoIDMaxPollAttempts 是 16006 状态下的最大请求次数（含首次），
// 退避 1s/2s/4s/8s，总等待上界 15s。
const getVideoIDMaxPollAttempts = 5

// GetVideoID 用 VOD 返回的 file_id（BDVideoInfo.mVideoId / Commit 的 Vid）
// 登记并换取米游社 video_id。返回 video_id 与服务端确认的时长（毫秒）。
func (s *Service) GetVideoID(ctx context.Context, sess session.Session, fileID, md5 string) (string, int64, *output.Error) {
	if fileID == "" || md5 == "" {
		return "", 0, output.Err(output.CodeInputInvalid, "file_id 与 md5 不能为空")
	}
	q := url.Values{}
	q.Set("file_id", fileID)
	q.Set("md5", md5)
	q.Set("video_provider", VideoProvider)

	backoff := time.Second
	var data PreUpload
	for attempt := 1; ; attempt++ {
		var oerr *output.Error
		if oerr = s.Client.DoJSON(ctx, "GET", "/video/api/getVideoID", q, nil, headers(sess), &data); oerr == nil {
			break
		}
		if oerr.Retcode != retcodeUploadCallbackPending || attempt >= getVideoIDMaxPollAttempts {
			return "", 0, oerr
		}
		if !sleepBackoff(ctx, backoff) {
			return "", 0, output.Err(output.CodeCancelled, "已取消")
		}
		backoff *= 2
	}
	vid := string(data.VideoID)
	if vid == "" {
		return "", 0, output.Err(output.CodeRemoteRejected, "getVideoID 未返回 video_id")
	}
	return vid, data.VideoDurationMS(), nil
}

// sleepBackoff 可取消的退避休眠；返回 false 表示 ctx 已取消。
func sleepBackoff(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// UpdateCover 设置视频封面。body 为 {"video_id","cover_url"}。
func (s *Service) UpdateCover(ctx context.Context, sess session.Session, videoID, coverURL string) *output.Error {
	if videoID == "" || coverURL == "" {
		return output.Err(output.CodeInputInvalid, "video_id 与 cover_url 不能为空")
	}
	body, _ := json.Marshal(struct {
		VideoID  string `json:"video_id"`
		CoverURL string `json:"cover_url"`
	}{VideoID: videoID, CoverURL: coverURL})
	_, oerr := s.Client.Do(ctx, "POST", "/video/api/updateCover", nil, body, headers(sess))
	return oerr
}
