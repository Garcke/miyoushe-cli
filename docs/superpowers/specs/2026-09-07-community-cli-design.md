# 米游社社区 CLI 功能设计

日期：2026-09-07

状态：设计草案，待按接口证据分阶段实现
依赖：[Go QR 登录与本地凭据保存设计](2026-09-04-go-qr-auth-design.md)

## 1. 目标和边界

在现有 Go + Cobra CLI 上增加社区账户的常用查看与内容管理能力：

- 查看绑定游戏角色。
- 查看自己的帖子、任意帖子详情与收藏夹内容。
- 保存、查看、发布和删除草稿。
- 发布、完整替换编辑和删除帖子。
- 支持纯文字、本地图片、长文，以及视频/图片/文字混排。
- 支持收藏与取消收藏；收藏夹目录管理不进入第一轮实现。
- Windows、macOS、Linux 使用同一命令和内容文件格式。

继续沿用首版约束：只有一个默认社区账号，不设计 profile、账号列表或账号切换；不加入 Viper、TUI 或内置编辑器。评论、私聊、点赞、签到、定时发布、批量养号和大规模自动发帖不在本设计范围。

本设计以“已经取得有效 SToken”为业务命令前置条件，不设计 Game Token 交换失败后的只读回退。若凭据没有对应能力，命令应明确失败且不执行部分写入。

## 2. 证据等级与实现纪律

仓库中的“接口存在”“抓包出现”和“已经复现”不是同一件事。每个适配器必须标记以下等级：

- **V（Verified）**：仓库至少成功复现过该端点的一种场景，方法、路径和该场景的主要参数可信；不代表所有 body 变体都完整。
- **O（Observed）**：真实 App 流程中抓到完整链路，但本文档仍缺部分请求/响应字段样本。
- **P（Path only）**：仅从 dex 或接口矩阵得到路径，方法或 body 可能是推断。

证据等级描述“见过并验证到什么程度”，不等于发布就绪。表格中的“参数待补”“body 部分记录”等缺口是独立门禁：只有目标命令所需的完整脱敏 fixture、错误样本和契约测试齐全，适配器才可标记为 `ready`。

实现规则：

1. V 级接口可以直接进入单元测试和只读实现，但写接口仍须先制作脱敏 fixture。
2. O 级接口先补齐脱敏请求/响应 fixture，再写真实适配器。
3. P 级写接口只在 Cobra 中保留规划，不根据名称猜 body，也不进行线上试错。
4. 所有真实写操作均由用户显式命令触发；自动测试只使用 `httptest` 和合成 Token。

## 3. Cobra 命令树

```text
mys
├─ auth
│  ├─ login
│  ├─ status                 # 保持离线
│  ├─ verify                 # 新增：在线验证社区能力，不打印凭据
│  └─ logout
├─ role
│  └─ list [--game-biz ...] [--json]
├─ post
│  ├─ list [--uid ...] [--gids ...] [--cursor ...] [--limit ...] [--json]
│  ├─ show <post-id> [--json]
│  ├─ create (--from spec.json | 快速参数) [--dry-run] [--allow-duplicate --operation-id ...] [--json]
│  ├─ edit <post-id> --from spec.json --expected-sha256 ... [--dry-run] --yes [--json]
│  └─ delete <post-id> --yes [--accept-orphan-media] [--json]
├─ draft
│  ├─ list [--kind ...] [--cursor ...] [--limit ...] [--json]
│  ├─ show <draft-id> [--json]
│  ├─ save (--from spec.json | 快速参数) [--draft-id ... --expected-sha256 ... --yes] [--dry-run] [--json]
│  ├─ publish <draft-id> [--dry-run] [--allow-duplicate --operation-id ...] [--yes] [--json]
│  └─ delete <draft-id> --yes [--json]
├─ operation
│  ├─ list [--state ...] [--json]
│  ├─ show <operation-id> [--json]
│  ├─ reconcile <operation-id> [--json]
│  └─ cleanup --older-than 168h --yes [--json]
└─ favorite
   ├─ list [--role <selector>] [--cursor ...] [--limit ...] [--full] [--json]
   ├─ add <post-id> [--collection-id ...] [--json]
   └─ remove <post-id> [--json]
```

行为约定：

- `post list` 默认当前账号，`--uid` 只用于查看其他用户公开帖子。
- `favorite list` 需要 `game_uid` 和 `region`。`--role` 接受 `game_biz:game_uid:region` 组合选择器；为方便交互也接受能唯一匹配的 `game_uid`。未指定且只有一个角色时自动使用，存在多个角色或短选择器不唯一时列出候选并退出，不静默挑选。
- `--cursor` 是服务端不透明字符串；CLI 不将它转换成页码。
- `--limit` 限制本次输出总数；单页大小由适配器控制。默认不无限翻页。
- 跨页读取若中途失败，整条命令返回 `ok:false`，不把部分结果伪装成成功；JSON error 可带 `partial_data` 和最后一个**已完整消费页**的 `resume_cursor`，人类输出则把部分结果写入 stderr 摘要。
- `post edit` 是完整内容替换，不提供容易产生歧义的字段级 patch。
- 删除、编辑和发布已有草稿属于明显写操作。脚本中必须显式传 `--yes`；首版不引入交互式确认库。
- `--dry-run` 只解析内容、读取和校验本地媒体、计算摘要、生成待提交模型；不获取上传凭据、不上传、不保存草稿、不发布。
- 列表和详情的人类可读输出写 stdout，进度写 stderr。`--json` 时 stdout 只包含一个稳定 JSON 文档。
- `operation` 只管理本地写操作记录；`reconcile` 仅做远端只读对账，`cleanup` 仅删除已完成/过期的本地记录，不承诺删除已上传媒体。

上面的命令树是目标形态。分阶段发布时，只注册 `ready` 适配器对应的命令；例如阶段 A 不显示 `post edit`、`favorite add/remove`，阶段 E 前 `kind=video` 返回 `FEATURE_UNAVAILABLE`。不提供会在运行到一半才发现“尚未实现”的写命令。

## 4. 统一内容输入格式

复杂的长文和视频混排不能只靠重复 flags 准确表达顺序，因此以 JSON `ContentSpec` 作为规范输入；简单图文同时提供快速参数。

```json
{
  "schema_version": 1,
  "kind": "image",
  "gids": 8,
  "forum_id": 47,
  "forum_cate_id": 0,
  "subject": "标题",
  "blocks": [
    {"type": "text", "text": "第一段正文\n"},
    {"type": "image", "path": "./assets/a.png"},
    {"type": "text", "text": "图片后的说明\n"}
  ],
  "topics": [{"id": "123", "name": "话题名"}],
  "is_original": true
}
```

字段约束：

- `kind` 仅允许 `image`、`article`、`video`。
- `blocks` 保留顺序；类型仅允许 `text`、`image`、`video`。
- `image.path`、`video.path` 和 `cover` 只接受本地文件。首版不下载远程 URL，避免 SSRF、超时和远程内容变化。
- `cover` 顶层字段只用于长文封面；视频块允许自己的可选 `cover`。首版拒绝在 image kind 使用顶层 cover，也拒绝在 video kind 同时设置顶层 cover，避免优先级歧义。
- 视频块未提供 `cover` 时是否自动截帧要等媒体探测实现确定；在该能力成为 `ready` 前要求显式封面，不在编译器中伪造。
- 相对路径以 ContentSpec 文件所在目录解析，而不是当前 shell 目录。
- 解析时拒绝未知字段、重复 JSON 键、非法 UTF-8、空正文块和不匹配的 `kind`/block 组合。
- `image` 是短帖：至少一个文字或图片块，不允许视频；纯文字也明确使用该 kind，并映射为 `view_type=2`。
- `article` 是长文：要求非空标题和至少一个文字块，允许穿插图片，不允许视频。
- `video` 首版要求非空标题、恰好一个视频块和显式视频封面，可与文字、图片混排。多视频或自动截帧必须先有真实发布 fixture 证明服务端支持。
- 不接受用户提供的原始 HTML 或 Quill JSON。两种服务端表示都由同一个 block 模型生成，避免 `content` 和 `structured_content` 内容不一致。

block 的 v1 结构固定为：`text` 只接受 `type/text`，`image` 只接受 `type/path`，`video` 只接受 `type/path/cover`；各字段按类型必填，额外字段一律报错。标题、正文、图片数量、文件大小、图片 MIME、视频容器和 codec 的具体上限由带来源的 protocol profile 提供；没有可信 fixture 时拒绝写入，不能用猜测的宽松默认值。

快速参数只用于 `kind=image`，等价于生成临时 ContentSpec：

```text
mys post create --kind image --gids 8 --forum 47 \
  --text-file body.txt --image a.png --image b.jpg
```

快速参数固定为“全部文字在前、图片按参数顺序在后”；需要交错内容、长文或视频时必须使用 `--from`。`--from` 与任意内容快速参数互斥，不做隐式覆盖。

## 5. 内容编译

`internal/content` 只负责确定性转换，不发网络请求：

```go
type Spec struct {
    SchemaVersion int
    Kind          Kind
    GIDs          int
    ForumID       int64
    ForumCateID   int64
    Subject       string
    Blocks        []Block
    Topics        []Topic
    Cover         string
    IsOriginal    bool
}

type UploadedAsset struct {
    SourcePath string
    URL        string
    VideoID    string
    DurationMS int64
    CoverURL   string
}

type Compiled struct {
    ViewType          int
    Describe          string
    ImageURLs         []string
    LinkCardIDs       []int64
    StructuredContent string
    HTMLContent       string
    Subject           string
}
```

v1 ContentSpec 不提供 link card 输入，所以 `LinkCardIDs` 必须为空切片；保留该字段是为了让 legacy payload 的边界明确，而不是把不透明 JSON 塞进字符串。post/draft 适配器只负责将上述强类型字段包进各自经过 fixture 锁定的外层 body。

映射规则：

| kind | `view_type` | 输出 |
|---|---:|---|
| `image` | 2 | 强类型的 `describe` + `imgs` + 空 `link_card_ids`；具体外层 body 由 post/draft 适配器生成 |
| `article` | 5 | Quill delta `structured_content` + 从相同 blocks 生成的受控 HTML |
| `video` | 1 | Quill delta，视频块为 `{insert:{vod:{cover,duration,id}}}`，同时生成对应 HTML |

图片或视频尚未上传时，编译器返回“待解析媒体引用”；上传成功后注入 `UploadedAsset` 再完成最终编译。HTML 必须使用 `html/template` 或等效 escaping 构造，不能字符串拼接未经转义的正文。

## 6. Go 模块边界

```text
mihoyo_cli/internal/
├─ api/          # 有界响应读取、retcode、重定向策略、脱敏错误
├─ protocol/     # App 版本、BBS DS、公共 x-rpc 头与 host 白名单
├─ session/      # 从凭据派生 Cookie，能力检查；不持久化派生 Cookie
├─ role/         # 绑定角色查询与 role 解析
├─ post/         # 列表、详情、创建、编辑、删除、审核状态
├─ draft/        # 草稿列表、详情、保存、发布、删除
├─ favorite/     # 收藏列表与收藏操作
├─ content/      # ContentSpec、验证、Quill/HTML/legacy 编译
├─ operation/    # 写操作 journal、防重复、只读对账和恢复
├─ media/
│  ├─ image/     # MD5、上传参数、OSS multipart、最终 URL
│  └─ video/     # 探测、秒传、VOD 上传、登记、封面、checkpoint
├─ output/       # 表格/JSON、稳定错误码、进度事件
└─ cli/          # Cobra 组合和依赖注入
```

现有 `internal/auth` 中通用 BBS DS 与请求头在实现社区功能时迁至 `internal/protocol`，登录状态机只保留 passport/二维码专用逻辑。迁移时保持兼容测试，避免出现两份可漂移的 salt 或 App 版本常量。

核心接口：

```go
type SessionProvider interface {
    Load(context.Context) (Session, error)
    Require(context.Context, Capability) (Session, error)
}

type RoleService interface {
    List(context.Context, Session, string) ([]Role, error)
}

type PostService interface {
    List(context.Context, Session, PostListOptions) (PostPage, error)
    Get(context.Context, Session, string) (Post, error)
    Create(context.Context, Session, ReleaseInput) (WriteResult, error)
    Replace(context.Context, Session, string, ReleaseInput) (WriteResult, error)
    Delete(context.Context, Session, string) error
}

type DraftService interface {
    List(context.Context, Session, DraftListOptions) (DraftPage, error)
    Get(context.Context, Session, string) (Draft, error)
    Save(context.Context, Session, SaveDraftInput) (Draft, error)
    Publish(context.Context, Session, string, ReleaseInput) (WriteResult, error)
    Delete(context.Context, Session, string) error
}

type ImageUploader interface {
    Upload(context.Context, Session, OpenedAsset) (UploadedImage, error)
}

type VideoUploader interface {
    Upload(context.Context, Session, OpenedAsset, ProgressFunc) (UploadedVideo, error)
}
```

服务层只接收已经验证的 domain 类型，不接收 Cobra command、原始 map 或任意 endpoint URL。

## 7. 会话与能力验证

社区请求从当前凭据在内存中派生：

```text
Cookie: stuid={uid}; stoken={stoken}; mid={mid};
```

SToken、MID、Cookie、DS 原文不得进入普通日志、JSON 输出、错误或 operation checkpoint。`auth status` 继续保持离线；新增 `auth verify` 发起安全的只读请求并报告：

- 凭据文件是否有效。
- 服务端是否接受 SToken。
- BBS protocol profile（App 版本/salt/完整头）是否仍被接受。
- `read-account`、`write-post`、`upload-image`、`upload-video` 能力的状态与判断依据。

能力状态只允许 `available`、`account_denied`、`not_implemented`、`unknown`。`auth verify` 能通过角色/帖子等只读请求确认会话，也可调用已经证明无副作用的权限/配额端点；它不能为了“验证”而取上传凭据或试发内容。`write-post`、`upload-image` 等状态因此同时取决于本地适配器是否 ready、protocol profile 是否有效，以及是否存在安全的账号权限检查；缺少任一依据时报告 `unknown`，不能把 `retcode=0` 推断为全部可写。

“接口返回 retcode 0”不自动代表所有写能力都通过。每个写命令仍运行自己的非破坏性 preflight。登录失效、接口权限不足和 protocol profile 过期必须是不同错误码：

```text
AUTH_INVALID
AUTH_CAPABILITY_REQUIRED
PROTOCOL_PROFILE_REJECTED
REMOTE_REJECTED
REMOTE_RESULT_UNKNOWN
FEATURE_UNAVAILABLE
INPUT_INVALID
CONTENT_CONFLICT
OPERATION_UNRESOLVED
ORPHAN_MEDIA_ACK_REQUIRED
```

写命令的通用 preflight 固定检查：本地适配器为 ready、会话只读验证通过、目标版区/角色参数可解析、ContentSpec 与本地媒体完全有效。视频再检查发布权限和剩余次数。图片上传参数会签发短期凭据，因此不属于 `--dry-run`；只有确认执行写操作后才获取。

## 8. 接口映射

| 功能 | 上游 | 证据 | 设计决定 |
|---|---|---:|---|
| SToken 角色 | `GET api-takumi.miyoushe.com/binding/api/getUserGameRolesByStoken` | V | `role list` 主路径，`client_type=2` + BBS DS + SToken Cookie |
| 用户帖子列表 | `GET bbs-api.miyoushe.com/painter/api/user_instant/list` | V（参数待补） | `post list`；实现前补脱敏 query/response fixture |
| 帖子详情 | `GET /post/api/getPostFull` | V | `post show`；列表缺 `vod_list` 时也用它补全 |
| 发布 | `POST /post/api/releasePost/v2` | V（body 部分记录） | 三种 kind 共用；实现前固化三类完整 body fixture |
| 编辑 | `POST /post/api/editPost` | P | 命令保留，补抓包前不实现线上调用 |
| 删除 | `POST /post/api/deletePost` | V | body `{"operate_type":0,"post_id":"..."}`，要求 `--yes` |
| 草稿列表 | `GET /post/api/draft/list` | V | query `view_type=7&offset=&size=20` |
| 草稿详情 | `GET /post/api/draft/detail` | V | query `draft_id` |
| 保存草稿 | `POST /post/api/draft/save` | V（新建语义待补） | 复用 ContentSpec；先验证新建 `draft_id` 的精确表示 |
| 删除草稿 | `POST /post/api/draft/delete` | V | body `{"draft_id":"..."}`，要求 `--yes` |
| 收藏列表 | `GET /painter/api/userFavouritePostList` | V | 使用角色的 `game_uid`/`region`，游标分页 |
| 收藏/取消 | `POST /post/api/collectPost` | P | 补齐 body 与操作语义后才实现 `add/remove` |
| 图片上传参数 | `GET /apihub/sapi/getUploadParams` | V | MD5/ext/source 参数，随后 OSS multipart |
| 视频权限/配额 | `/post/api/check/publishVideoPerm`、`/video/api/residualTimes` | O | 视频上传 preflight |
| 视频凭据/秒传/登记/封面 | `/video/api/getToken`、`isExist`、`getVideoID`、`updateCover` | O | 完成 VOD 上传 spike 和脱敏 fixture 后实现 |
| 审核详情/撤回 | `/post/api/review/detail`、`/post/api/review/undo` | O | 发布结果若返回 review_id，显示“审核中”；撤回命令后续增加 |

矩阵对部分方法是启发式推断；真实视频抓包文档优先记录 `getToken` 为 GET。实现必须以脱敏 HAR fixture 为准，并在测试中锁定方法、参数位置和签名。

## 9. 图片上传状态机

```text
打开本地文件一次
  → 校验 regular file / 大小 / 扩展名 / MIME
  → 在同一文件句柄上流式计算 MD5
  → getUploadParams(md5, ext, support_content_type=1, upload_source=1)
  → 校验服务端返回的 HTTPS host 在 OSS 白名单
  → multipart 直传（不跟随跨 host 重定向）
  → 构造并校验 upload-bbs.miyoushe.com 最终 URL
  → 返回 UploadedImage
```

不先整文件读入内存。散列后上传使用同一个打开的文件对象，并再次检查大小，防止本地路径在 hash 与 upload 之间被替换。多个相同 MD5 的图片在同一次命令中只上传一次。并发默认最多 2 个，服务端拒绝或限流时不扩大并发。

获取上传参数和同一对象的幂等分片可以有限重试；最终 OSS 成功与否不明时先检查对象状态，不盲目重复。服务端返回的 host、dir、file_name 不得直接拼成本地路径。

## 10. 视频上传状态机

```text
本地媒体探测(size, duration, container, codec, md5)
  → publishVideoPerm
  → residualTimes
  → isExist(md5)
     ├─ 已存在：复用 file_id/登记信息
     └─ 不存在：getToken(size,duration,name,md5,video_provider=1)
          → VOD uploader 分片直传/断点续传
  → getVideoID(file_id,md5,video_provider=1)
  → 上传或选取封面
  → updateCover
  → 返回 video_id + duration + cover URL
```

视频是本项目中唯一需要单独技术验证的上传层。仓库证明米游社侧链路已成功，但 App 使用火山 VOD 客户端 SDK，尚没有可直接照搬的普通 REST 上传请求。实现前需要完成一个不发布帖子的 protocol spike：

1. 获取并脱敏记录 `/video/api/getToken` 响应字段。
2. 确认临时凭据、space、endpoint 和 session token 如何映射到上传客户端。
3. 评估火山引擎 Go VOD SDK 是否支持这类由第三方业务签发的临时凭据；普通 SDK 的自有 AK/SK 示例不能直接证明兼容。
4. 用小型测试视频只完成上传和 `getVideoID`，不调用 `releasePost/v2`。
5. 固化 fixture、分片协议和 checkpoint 格式后，才接入 `post create`/`draft save`。

checkpoint 只保存规范化绝对路径的 SHA-256、大小、mtime、文件 MD5、upload id、已完成分片编号和失效时间；不得保存 SToken、VOD 临时密钥或完整服务端响应。文件放在 CLI 数据根目录的 `operations/` 中，与凭据文件隔离，使用用户私有权限、临时文件 + 原子 rename 写入，并用跨进程文件锁保护。Windows 路径哈希前统一盘符大小写和分隔符。临时上传凭据只驻留内存。文件发生变化或凭据过期时废弃 checkpoint；已完成或过期记录只由 `operation cleanup` 清除，不能继续拼接旧分片。

删除帖子或撤回审核不等于删除火山 VOD 文件；仓库未发现视频文件删除接口。CLI 必须在删除视频帖前提示这个事实，不提供伪造的 `media delete`。

## 11. 帖子与草稿工作流

### 创建帖子

1. 读取并验证 ContentSpec。
2. 执行账号、版区、发布权限和媒体规则 preflight。
3. `--dry-run` 到此为止，输出编译摘要。
4. 上传图片；视频 kind 再执行视频状态机。
5. 用上传结果确定性编译内容。
6. 调用 `releasePost/v2` 一次。
7. 返回 `post_id` 或 `review_id`，两者不能混为“已公开”。

上传开始前创建不含秘密的 operation journal，记录随机 `operation_id`、ContentSpec SHA-256、目标版区、媒体摘要、上传后的服务端资源 ID、状态和时间。发布请求出现连接中断或响应不完整时返回 `REMOTE_RESULT_UNKNOWN` 和 `operation_id`，不自动重试；相同内容摘要的未决操作会阻止再次发布。

恢复时先用自己的近期帖子/审核结果做只读查询，并用版区、标题、时间窗口及规范化后的最终内容摘要匹配：唯一匹配则补记成功，零个或多个匹配仍保持 `OPERATION_UNRESOLVED`。在不能确认服务端结果时，只有用户显式传 `--allow-duplicate --operation-id <id>` 才允许重新发布。若媒体已上传但帖子最终未创建，journal 标记 `orphaned_media`；没有可信删除接口时只报告和复用，不声称已清理。

### 保存与发布草稿

- `draft save` 与 `post create` 共用 ContentSpec、媒体上传和编译器，只由 draft adapter 构造经过 fixture 锁定的 `forum_id`、`draft_id` 等外层字段。
- 创建新草稿的 `draft_id` 表示必须由脱敏抓包确认，不能假设数字 0 与字符串 `"0"` 等价。
- 更新已有草稿必须带 `--yes --expected-sha256 <digest>`；digest 由 `draft show` 对规范化完整内容计算。保存前重新读取并比较，不一致返回 `CONTENT_CONFLICT`。若服务端没有 CAS/revision 能力，CLI 明确将其标为“尽力并发保护”，不声称能消除最后一次读取后的竞态。
- `draft publish` 先读取服务器草稿详情，映射回 domain model，再由 `DraftService.Publish` 将 draft id 放入 fixture 已确认的位置调用统一发布接口。响应 fixture 还必须明确发布成功后草稿是删除、归档还是保留；在此之前该命令不注册。
- 如果服务端草稿含客户端未知 block，CLI 拒绝覆盖或发布并要求导出/更新工具，不能丢弃未知内容后写回。
- 草稿发布也使用 operation journal。结果未知时不得重新提交，先查询帖子/审核结果以及草稿当前状态。

### 编辑与删除

- `post edit` 在适配器完成前只提供设计，不以 `releasePost/v2` 猜测编辑语义。
- 完整替换编辑需要先 `getPostFull` 验证当前用户是作者和帖子仍可编辑，并要求 `--expected-sha256` 匹配当前规范化内容，再显示将被替换的 post id，要求 `--yes`。若服务端不支持 revision/CAS，此检查同样只是尽力保护；冲突时直接退出，不提供隐式绕过。
- `post delete` 和 `draft delete` 不自动重试；成功后可做一次只读确认，但确认失败不能把已成功删除报告为失败。
- `post delete` 先读取详情识别视频帖。视频帖即使已有 `--yes`，仍需额外 `--accept-orphan-media` 承认 VOD 可能保留；`--json` 在执行前失败为 `ORPHAN_MEDIA_ACK_REQUIRED`，执行成功后也在 `warnings` 返回该事实。

## 12. 收藏工作流

第一阶段实现 `favorite list`：

1. 解析角色；多个角色时要求用户明确选择。
2. 调用 `userFavouritePostList`，从空 offset 开始。
3. 输出 `post_id`、标题、作者、社区、发布时间、view_type 和 next cursor。
4. 只有用户显式 `--full` 时才逐条补调 `getPostFull`；限制并发并保留原顺序。

`favorite add/remove` 依赖 `collectPost` 的真实 body 和取消语义。接口矩阵只有路径，不能仅根据命令名假设 `is_cancel`、`post_id` 或 `collection_id` 字段。获得一组收藏/取消收藏的脱敏请求后同时实现两个命令和幂等状态测试。

收藏夹目录的 create/edit/del/list/sort 等 14 个 collection 接口另设后续设计，不混进“收藏帖子”第一阶段。

## 13. 输出、错误和隐私

稳定 JSON envelope：

```json
{
  "ok": true,
  "data": {},
  "next_cursor": "",
  "warnings": []
}
```

失败写 stderr，`--json` 时使用：

```json
{
  "ok": false,
  "error": {"code": "AUTH_INVALID", "message": "登录凭据已失效"}
}
```

每个命令的 `data` 使用本项目定义的结构，不透传上游任意 JSON；新增字段保持向后兼容，破坏性变化提升 `schema_version`。列表统一返回 `items`、`next_cursor`、`has_more`，详情统一包含资源 id 和 `content_sha256`；写操作统一返回 `operation_id`、`state`、可用时的 `post_id`/`draft_id`/`review_id`。

进程退出码固定为：`0` 成功，`2` 输入/确认/功能门禁/内容冲突，`3` 登录或 protocol profile，`4` 服务端明确拒绝，`5` 服务端结果未知或未决操作，`1` 仅用于未分类的本地内部错误。具体机器判断必须使用 JSON `error.code`，不能依赖本地化 message。

不得输出请求头、Cookie、Token、DS、上传 policy/signature、VOD 临时密钥、ticket 或未经筛选的服务器响应。调试模式也只记录 endpoint 名称、HTTP 状态、retcode、耗时、请求体字段名和文件摘要；不记录字段值。

帖子正文、标题、公开图片 URL 是用户要求查看/发布的数据，可以出现在命令结果中；本地绝对路径默认只在进度输出中显示，`--json` 返回规范化输入引用而不是遍历同目录文件。

所有远程 URL 均由内置 endpoint 或经过 host 白名单验证的上传响应产生。CLI 不公开 `--endpoint`，测试通过依赖注入替换服务。

## 14. 测试与验收

### 协议层

- 每个 V/O 接口有脱敏 request/response fixture。
- `httptest` 精确断言方法、query/body 位置、BBS DS、Cookie 名和必要 x-rpc 头。
- retcode 缺失、错误 HTTP、超大响应、重定向、超时、取消和响应字段变化全部失败且不泄密。

### 内容层

- 三种 kind 使用 golden tests 固定 legacy、Quill delta 和 HTML 输出。
- 文本 escaping、换行、emoji、中文、纯文字短帖、空 block、未知字段、重复 JSON 键、非法 UTF-8、相对路径与重复文件覆盖。
- 同一 Spec 多次编译产生字节完全一致的 JSON。

### 媒体层

- 合成图片上传服务验证 multipart 字段、MD5、顺序、并发限制和 host 白名单。
- 文件在 hash 后变化、符号链接替换、过大响应与上传结果不明均不发布。
- 视频 uploader 先以 fake 覆盖状态机；真实 adapter 只有 protocol spike 通过后启用。
- checkpoint 不含任何合成秘密，过期/文件变化后不会复用。
- operation journal 用临时文件原子提交；并发进程不会同时推进同一个 operation；相同内容摘要的未决发布会被阻止。

### 命令层

- 人类输出和 JSON 输出都不泄露凭据。
- `status` 离线，`verify` 在线；二者测试不能混淆。
- 缺少 `--yes`、无权限、媒体失败、编译失败时写接口调用次数为 0。
- 发布结果未知时不重试；旧帖子/草稿保持不变。
- Windows/macOS/Linux amd64、arm64 交叉编译；平台声明区分编译与实机验证。

### 实机证据升级

V/O/P 升级到可写的 `ready` 必须走一次人工授权的最小验收：使用用户明确指定的账号与小型媒体、先 `--dry-run`、只执行预先说明的单次写入、记录成功和一个安全错误响应、立即脱敏 fixture 并做秘密扫描。若产生帖子/草稿，清理也由用户显式命令执行；若媒体没有删除接口，验收记录必须保留 orphan 事实。任何真实调用都不进入普通 `go test`。

## 15. 实现顺序

### 阶段 A：共享协议和只读能力

- 抽取 `api/protocol/session/output`。
- `auth verify`、`role list`、`post list/show`、`favorite list`。
- 完成稳定 JSON 输出、游标和能力错误。

### 阶段 B：ContentSpec、图片和草稿

- ContentSpec parser/validator/compiler。
- 图片 OSS 上传。
- operation journal 基础、`draft list/show/save/delete` 和 `--dry-run`。

### 阶段 C：图文/长文发布与删除

- 为 image/article 两类发布 body 以及带 draft id 的发布变体建立脱敏 fixture。
- `post create/delete`、审核结果展示、`draft publish`、`operation reconcile/cleanup`。

### 阶段 D：补证据后的编辑与收藏写操作

- 抓取并脱敏 `editPost`、`collectPost` 的双向操作。
- `post edit`、`favorite add/remove`。

### 阶段 E：视频

- 完成 VOD temporary credential protocol spike。
- 视频探测、秒传、分片上传、checkpoint、getVideoID/updateCover。
- 接入 `draft save` 和 `post create` 的 video kind。

每个阶段独立提交和验收；不因后续视频未完成阻塞前面的只读、图文和长文能力。

阶段完成门槛：

| 阶段 | 完成标准 |
|---|---|
| A | 所有只读命令通过 fixture/`httptest`；失效 Token、过期 protocol profile 和普通远端错误可区分；三平台交叉编译通过。 |
| B | ContentSpec golden tests 通过；`--dry-run` 网络调用数为 0；图片上传、草稿写入失败时不会继续到下一写步骤。 |
| C | image/article 及 draft-id 发布变体都有脱敏真实 fixture；结果明确区分公开、审核中和未知；网络结果未知时不重试。 |
| D | 编辑以及收藏/取消各有成对 fixture；补齐幂等和所有权检查后才在 release build 暴露命令。 |
| E | 小视频 spike 在不发布帖的前提下完成上传与 `getVideoID`；临时密钥不落盘；失败恢复和重复上传测试通过后才接入发布。 |

线上写入不是自动测试的一部分。需要真实账号验收时，由用户显式运行命令，并先使用 `--dry-run`；CLI 不内置隐式“测试发帖”。

## 16. 参考依据

- `mihoyo_bbs/docs/api/米游社接口清单_DS闭环验证.md`
- `mihoyo_bbs/docs/api/视频上传与发布.md`
- `mihoyo_bbs/docs/api/扫码登录与收藏夹_旧版服务整理.md`
- `mihoyo_bbs/docs/api/接口面矩阵_全量.md`
- [UIGF 用户 Token](https://uigf.org/zh/mihoyo-api-collection/hoyolab/user/token.html)
- [UIGF 游戏账号信息](https://uigf.org/zh/mihoyo-api-collection/hoyolab/user/game_account_info.html)
- [UIGF 米游社论坛文章](https://uigf.org/zh/mihoyo-api-collection/hoyolab/article/article.html)
- [火山引擎 Go VOD SDK 集成](https://www.volcengine.com/docs/4/65654)
- [火山引擎 Go SDK](https://github.com/volcengine/volc-sdk-golang)
