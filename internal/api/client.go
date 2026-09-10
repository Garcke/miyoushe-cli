// Package api 实现有界 HTTP 响应读取、retcode envelope 解析与脱敏错误。
//
// 信任边界（总体架构 §6）：
//   - 单个 JSON 响应上限 1 MiB，超出即失败；
//   - 不跟随重定向（含凭据的请求绝不重定向）；
//   - 错误消息不包含原始请求/响应或带敏感参数的 URL；
//   - 拒绝缺少成功标志（retcode）的响应。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mihoyo_cli/internal/output"
)

const (
	// DefaultMaxBytes 是单个 JSON 响应的上限（1 MiB）。
	DefaultMaxBytes = 1 << 20
	// DefaultRequestTimeout 是单次请求超时。
	DefaultRequestTimeout = 15 * time.Second
)

// Retcode 语义分类（快照实测）：-100 登录失效/未登录；-10001 网关拒绝
// （缺头等 protocol profile 问题）。
const (
	retcodeAuthInvalid     = -100
	retcodeProfileRejected = -10001
)

// ListMeta 是列表接口分页元数据的容错解析：
// 收藏夹/草稿箱返回 is_last + next_offset；动态列表使用 last_id。
type ListMeta struct {
	IsLast     *bool  `json:"is_last"`
	NextOffset string `json:"next_offset"`
	LastID     string `json:"last_id"`
}

// Cursor 返回下一个不透明游标；CLI 不把它转换成页码。
func (m ListMeta) Cursor() string {
	if m.NextOffset != "" {
		return m.NextOffset
	}
	return m.LastID
}

// HasMore 判断是否还有下一页。is_last 存在时以它为准。
func (m ListMeta) HasMore() bool {
	cur := m.Cursor()
	if m.IsLast != nil {
		return !*m.IsLast && cur != ""
	}
	return cur != ""
}

// Client 绑定一个固定 host（测试中可指向 httptest）。
type Client struct {
	// Base 形如 https://bbs-api.miyoushe.com（结尾不带 /）。
	Base string
	// Host 是 Base 的 host:port 部分，仅用于诊断，不进入错误消息。
	Host string

	HTTP           *http.Client
	MaxBytes       int64
	RequestTimeout time.Duration
}

// New 构造绑定 base 的客户端；base 必须是 http/https URL。
func New(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("api: 无效 base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("api: base URL 必须是 http/https 绝对地址")
	}
	return &Client{
		Base: strings.TrimRight(base, "/"),
		Host: u.Host,
		HTTP: &http.Client{
			// 不跟随重定向：3xx 直接作为响应返回，由 Do 按非 200 处理，
			// 避免把含凭据的请求带到其它 host。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		MaxBytes:       DefaultMaxBytes,
		RequestTimeout: DefaultRequestTimeout,
	}, nil
}

// envelope 是米游社标准返回结构。Retcode 用指针检测“缺少成功标志”。
type envelope struct {
	Retcode *int            `json:"retcode"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// Do 发起请求并返回 envelope 的 data 字段。
//
// header 由调用方通过 protocol 构造；body 非 nil 时按 application/json 发送。
// 失败一律返回 *output.Error；调用方取消（ctx.Err() != nil）时返回原始
// ctx.Err()，以便登录流程区分用户取消与网络失败。
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body []byte, header http.Header) (json.RawMessage, *output.Error) {
	if c.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.RequestTimeout)
		defer cancel()
	}

	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rdr)
	if err != nil {
		return nil, output.Err(output.CodeRemoteRejected, "构造请求失败")
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errFromCtx(ctxErr)
		}
		return nil, sanitizeNetErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, output.Err(output.CodeRemoteRejected, "远端返回 HTTP 状态 %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, c.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errFromCtx(ctxErr)
		}
		return nil, transportErr(output.Err(output.CodeRemoteRejected, "读取响应失败"))
	}
	if int64(len(data)) > c.MaxBytes {
		return nil, output.Err(output.CodeRemoteRejected, "响应超过 %d 字节上限", c.MaxBytes)
	}

	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, output.Err(output.CodeRemoteRejected, "响应不是有效的 JSON envelope")
	}
	if env.Retcode == nil {
		return nil, output.Err(output.CodeRemoteRejected, "响应缺少 retcode 成功标志")
	}
	if *env.Retcode != 0 {
		oe := output.Err(output.CodeRemoteRejected, "远端返回错误码 %d: %s", *env.Retcode, env.Message)
		switch *env.Retcode {
		case retcodeAuthInvalid:
			oe.Code = output.CodeAuthInvalid
			oe.Exit = output.ExitAuth
		case retcodeProfileRejected:
			oe.Code = output.CodeProtocolRejected
			oe.Exit = output.ExitAuth
		}
		return nil, oe.WithRetcode(*env.Retcode)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, output.Err(output.CodeRemoteRejected, "响应缺少 data 字段")
	}
	return env.Data, nil
}

// DoJSON 在 Do 之上把 data 解码到 out。
func (c *Client) DoJSON(ctx context.Context, method, path string, query url.Values, body []byte, header http.Header, out any) *output.Error {
	raw, oerr := c.Do(ctx, method, path, query, body, header)
	if oerr != nil {
		return oerr
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return output.Err(output.CodeRemoteRejected, "响应 data 字段结构与预期不符")
	}
	return nil
}

// errFromCtx 把上下文错误映射为可分类错误。
func errFromCtx(ctxErr error) *output.Error {
	if errors.Is(ctxErr, context.Canceled) {
		return output.Err(output.CodeCancelled, "已取消")
	}
	return transportErr(output.Err(output.CodeLoginTimeout, "请求超时"))
}

// sanitizeNetErr 剥掉 *url.Error 中的 URL 与底层细节，避免 ticket 等敏感
// query 参数进入错误消息。
func sanitizeNetErr(err error) *output.Error {
	oe := output.Err(output.CodeRemoteRejected, "网络请求失败")
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return transportErr(output.Err(output.CodeRemoteRejected, "请求超时"))
		}
		if errors.Is(urlErr.Err, context.DeadlineExceeded) {
			return transportErr(output.Err(output.CodeRemoteRejected, "请求超时"))
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return transportErr(output.Err(output.CodeRemoteRejected, "请求超时"))
		}
		return transportErr(oe)
	}
	return transportErr(oe)
}

func transportErr(e *output.Error) *output.Error {
	e.Transport = true
	return e
}
