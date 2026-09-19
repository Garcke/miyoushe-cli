# 会话笔记：扫码登录、写链路适配器与视频帖（2026-09-15 ~ 09-18）

> 本文是开发会话的**公开摘要**，已脱敏（无 Token、无真实账号标识、无抓包原文）。
> 详细的协议证据与设计文档按仓库策略保留在本地 `docs/`（不入库）。

## 1. 登录：ma-cn-passport 扫码直出 SToken

- 链路：`POST /account/ma-cn-passport/app/createQRLogin`（body `{}`，身份由
  `x-rpc-app_id=bll8iq97cem8` 等头决定）→ 返回
  `user.mihoyo.com/login-platform/mobile.html?…&tk=<ticket>&token_types=1`
  （二维码内容）→ `POST …/queryQRLoginStatus`（`{"ticket":…}`）轮询
  `Created → Scanned → Confirmed`。
- **Confirmed 直接签发 SToken v2**（`tokens[{token_type:1}]`）+ `user_info.{aid,mid}`，
  无需 Game Token 交换；二维码失效返回 `retcode=-3501`。
- 凭证可用性实测：`getLTokenBySToken`、`getUserGameRolesByStoken`、
  `genAuthKey`（stoken 派生的限域签名凭据）全部 rc=0。
- 实现：`internal/auth/login_passport.go`（`mys auth login --mode=passport` 默认；
  `--mode=hk4e` 保留原游戏码+交换链路）。凭据新增
  `FlowV2 = ma_cn_passport_qr_login`，与 v1 双格式兼容。

## 2. 写链路的字段级契约（二分实测定位）

- `POST /post/api/draft/save`：新建**不带** `draft_id`；`is_profit/is_original/topic_ids`
  全部可选；**`block_reply_img` 必须是 JSON number 0/1——boolean 直接让服务端返回 -502**
  （单变量对照复现）。精简可用 body：`{subject, content(HTML), structured_content
  (delta 的 JSON 字符串), forum_id(字符串), view_type, cover, gids}`。
- `releasePost/v2`：文本帖与视频帖**字段集不同**；成功响应三种形态：
  正式发布（`post_id` 签发）、版区等级门槛（`rc=0 + post_id=0 +
  release_check_result.can_release=false + 人话 msg`）、进审核（`post_id=0 +
  post_review_id`）。
- `deletePost {operate_type:0, post_id}`；审核中帖子的“删除”= `review/undo
  {"review_id"}`（rc=0）。
- **构造纪律**：`structured_content`/`meta_content` 必须程序化序列化
  （`json.Marshal`），手写转义串会让内嵌 JSON 混入真实换行，服务端内层解析失败，
  表现为 `-1 草稿为空/内容错误/服务器出现了点问题`（与 `-502` 不同源）。

## 3. 线上响应形态陷阱（只有真数据能发现）

| 陷阱 | 实测表现 | 修复 |
|---|---|---|
| 空列表 `next_offset` 是 JSON number | `{"is_last":true,"next_offset":0}` | `ListMeta` 用 `FlexString`，0 视同无游标 |
| 读接口 `content` 是字符串 | 纯文本/HTML（发布域才是 `{describe,imgs}` 对象） | `contentRaw` 双形态 Unmarshal |
| `getPostFull` 双层包装 | 帖子本体在 `data.post.post` | `postWrapper` 解析 + 单层回退 |
| `draft/list` 的 `view_type` 按类型分桶 | 1/2/5 各一桶，“全部草稿”=并集 | `List` 分桶合并（默认） |

以上均由 `internal/api`、`internal/post`、`internal/draft` 的契约测试锁定。

## 4. 视频上传与视频帖（App HAR 实抓 + 服务端实测）

- **秒传分支**：`video/api/isExist` 命中返回 `video_id` + `video_info.duration`，
  可完全跳过 VOD 上传链（永久有效）。
- **视频帖发布**：`releasePost/v2`，`view_type=5`，
  `meta_content = {"describe":[…],"vods":[{"id":"<video_id>"}]}` + 封面 OSS URL +
  `topic_ids`；`gids` 为**字符串**；`block_reply_img` int；服务端会把它合并进
  `structured_content`（内联 vod 块自动补封面）；无需先存草稿。
- **首传分支（平台策略限制）**：CLI 以自身身份走 `getToken → ApplyUploadInfo`
  拿到的 SpaceKey 凭据为占位值（JWT 内 `accessKey="fake_access_key"`），
  transfer 被拒（4007，服务端 client/server hash 实为同值）；连续尝试后
  `getToken` 升级为 `16003`。结论：**视频首传需在 App 内完成**，CLI 走秒传
  分支引用其 `video_id`；`video_id` 只能被**同账号**帖子引用（跨账号引用报
  `rc=2000 已存在相同的视频`）。
- 封面链路（可完全由 CLI 完成）：`getUploadParams` → OSS multipart 直传
  （表单必须含 `x-oss-content-type`，policy 校验）→ 回调注册 → `upload-bbs` URL 可达。
- 实现：`internal/media/video`（秒传/权限/配额/getToken/Apply→transfer→finish→Commit
  SigV4 直传/updateCover）；`internal/post.PublishVideo` + `UndoReview`。

## 5. 代码变更清单

- `internal/auth`：`login_passport.go`（新链路）、`authkey.go`（genAuthKey）、
  `login.go`（Service 增加 PassportClient）。
- `internal/post`：`Publish`/`PublishVideo`/`Delete`/`UndoReview` + 统一响应解析；
  `contentRaw` 双形态；`postWrapper` 双层解析。
- `internal/draft`：`List` 分桶（1/2/5 合并）+ 单桶游标；`Save`/`Delete`；
  `block_reply_img` int 契约。
- `internal/api`：`ListMeta` 容错（number/string）。
- `internal/protocol`：`AppIDPassport`、`PassportQRHeaders`。
- `internal/store`：`FlowV2` 与 `NewCredentialsFlow`。
- `internal/content`、`internal/media/video`：内容模型与视频上传适配器（含 SigV4）。
- `internal/cli`：`auth login --mode`（passport 默认）。
- 全部包含契约测试，`go build ./... && go test ./...` 13 包全绿，
  `gofmt`/`go vet` 干净。

## 6. 已知边界与后续

- 视频**首传**受平台策略限制（见 §4），CLI 侧走秒传。
- 点赞/评论/私聊/签到不在设计范围；帖子编辑、收藏变更仍为 P-级证据（未实测）。
- 实机验收需用真实账号手工触发；仓库不保存任何账号数据与抓包原文。
