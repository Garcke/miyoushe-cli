# 米游社 CLI 架构设计 v2：App Passport SToken 与社区内容管理

状态：设计基线（v2.1），写命令仍按门禁逐项开放

日期：2026-09-19（v2.0 17:31 / v2.1 同日修订）

目标平台：Windows、macOS、Linux；Go 单二进制 + Cobra

修订记录：

| 版本 | 时间（UTC+08） | 变更 |
|---|---|---|
| v2.0 | 2026-09-19 17:31 | 初版：Passport SToken 默认登录、写操作状态机、1034 验证设计、媒体管线 |
| v2.1 | 2026-09-19（晚） | 基线更新（871e059 已进入本地 main）；§11 三项待确认决策落定；§9.1 写结果状态与新增错误码定义；§4.2 能力常量定稿；§10 附经核实的代码修正清单（含 `vod.go` CRC 十进制缺陷定位） |

## 1. 决策摘要

CLI 以 `ma-cn-passport` App 扫码作为默认登录链路：用户在米游社 App 确认后，请求方直接获得 `token_type=1` 的 SToken v2，并安全保存在本地。HK4E 扫码取得 Game Token 的链路与此不同；旧的 Game Token → SToken 交换不再是默认登录的前置条件，也不自动回退。SToken 的固定有效期未知，以服务端在线校验为准，不以本地保存时间推断。

取得 SToken 消除了先前的**凭据获取障碍**，但不自动批准所有写命令。每项能力仍需同时满足：目标场景的脱敏请求/响应 fixture、契约测试、适配器、错误/结果未知处理、用户显式操作和跨平台安全验收。本设计允许逐项打开草稿、图文、长文和视频工作流；编辑帖子、收藏变更等缺证据能力继续关闭。

本文是新目标架构，不声称 `docs` 分支已实现下文全部命令。分支基线为 GitHub `main` 的 `8da9e31`；下文提及的 Passport、草稿写适配器和 VOD 代码现状来自**提交 `871e059`**（v2.1 更新：该提交现为本仓库本地 `main` 的 HEAD，包含 Passport 登录、写适配器与视频底座，尚未推送远端；`docs` 分支仍基于 `8da9e31`，不含该代码）。与本地保留的 `docs/architecture/` 下旧版总体架构、认证设计、社区功能设计和视频协议记录冲突时，以本文的决策为准；旧文是历史设计与证据轨迹，不应按其中过时的 HK4E 默认链路、视频首传阻塞结论或全局 `view_type` 映射开发。

## 2. 证据、现状与边界

| 事项 | 已有证据或代码现状 | v2 解释 |
|---|---|---|
| App Passport 扫码 | CNB `mihoyo-api` 2026-09-15 实测记录（权威来源，见 §12）；本地 `passport-qr-login.md` 为同源工作区笔记，可能与本 checkout 不同步 | Confirmed 响应直出 SToken v2；默认登录用此链路 |
| 账户只读 | 远端基线已有角色、帖子、草稿、收藏适配器；本地开发快照的 Passport SToken 曾通过实机只读验证 | 保留并补齐凭据失效、空列表、分页和权限错误契约 |
| 草稿与文本发布 | CNB 记录了 `draft/save/delete`、`releasePost/v2`、`deletePost` 的成功与拒绝样本；本地开发快照存在尚未注册的写适配器 | 逐命令通过门禁后启用，不因适配器存在就注册 |
| 视频首传 | CNB 2026-09-18/19 记录首次 VOD 上传、封面 OSS、草稿及视频发布复现；最新固定上游 commit 为 [`0f9fcac`](https://cnb.cool/NRD-Tech/Reverse_Project/-/commit/0f9fcacc6af1524b8a5ab570f789f34580cbea39) | 可进入实现与验收，不再判定“仅能秒传”；仍需本项目完成适配和测试 |
| 发布安全验证 | CNB 记录 `releasePost/v2` 首次返回 1034，用户人工完成验证后重发成功，并可能进入审核 | 设计显式的人工验证与审核状态；不绕过验证码，不无人值守发布 |
| 旧汇总文档 | CNB 部分 `INTERFACES_STATUS`/旧视频段落未同步 9/19 结论 | 以有日期的最新实测记录为事实依据，保留冲突说明 |

证据等级继续沿用本地 `docs/architecture/evidence-manifest.md` 的 V/O/D/P 与 `documented → fixture_ready → contract_ready → adapter_ready` 分离原则：V 为目标场景成功复现，O 为真实流量观察，D 为 App 静态分析，P 为仅知路径。上游私有脚本和 HAR 只作为参考，不直接执行、不导入原始账号数据，也不把上游的“rc=0”当成本项目的发布验收。

### 2.1 范围

- 单一默认账号；查看绑定角色、本人帖子和详情、收藏、草稿。
- 用户显式创建和管理草稿，发布纯文字、图文、长文与本地视频，查看公开/审核状态，并在有证据时撤回或删除本人内容。
- 内容输入使用版本化 `ContentSpec`；简单图文可用快捷参数，复杂混排使用文件。
- 图片、视频只接受本地文件；支持 Windows/macOS/Linux 的同一命令与 JSON 输出。

不做批量/定时发帖、验证码自动求解、账号轮换养号、未经验证的编辑与收藏写接口、评论/点赞/私聊，以及远程 URL 媒体下载。Web 页面的“发布”按钮不构成“不需要 SToken”的证据。

## 3. 分层与依赖

```text
cmd/mys + internal/cli (Cobra、参数、提示、退出码)
  → internal/app      (用例编排、能力门禁、状态机；待抽取)
    → internal/content   (ContentSpec、验证、确定性编译)
    → internal/operation (写操作 journal、恢复与只读对账；待实现)
    → internal/role|post|draft|favorite (领域服务、强类型结果)
    → internal/media/image|video (上传适配器)
    → internal/auth|session|store (扫码、会话、私有凭据)
      → internal/api|protocol (HTTP、DS/x-rpc、固定 host、retcode)
  → internal/output (稳定 JSON、脱敏人类输出)
```

Cobra 不拼上游 body；领域服务不读 flags；内容编译器不发请求；HTTP 层不决定是否重试发布。`app` 负责将“验证凭据 → 校验内容 → 准备媒体 → 发布 → 解析审核结果”作为一次有状态用例协调。`operation` 与 `media/image` 目前是目标模块，不应写成已实现。依赖通过接口注入，以 `httptest` 和合成凭据完成离线测试。

### 3.1 协议配置

按端点而非“所有 BBS 接口”统一配置方法、host、DS 变体、请求头、鉴权要求、最大响应和可否安全重试。只允许固定的米游社/OSS/VOD host 与 TLS；含凭据、签名或上传地址的请求不得跨 host 跟随重定向。App 版本、盐值及字段随上游变化时，应更新协议配置、脱敏 fixture 和测试，不通过线上猜测宽松回退。

## 4. 身份、会话与能力门禁

### 4.1 登录和生命周期

```text
mys auth login
  → 建立/复用设备上下文
  → createQRLogin（请求方标识、token_types=1）
  → 展示二维码并轮询 queryQRLoginStatus
  → 用户在 App 扫码并确认
  → 验证 Confirmed、token_type=1、aid/mid/token 非空且一致
  → 私有临时文件 + 原子替换保存 SToken
```

超时、取消、二维码失效、账号信息不一致或保存失败时保留旧凭据；不把 Game Token 改名为 SToken。`--mode=passport` 为默认并已在提交 `871e059` 实现（Passport 二维码失效码 `-3501`，处理与 HK4E 的 -104/-106 同策略：重建但不重置总时限）；`--mode=hk4e` 保留为显式选择的兼容/诊断路径，不自动切换，也不放宽其交换成功条件。不要在登录时顺带发布、上传或创建草稿。

凭据保持 `schema_version=1`，Passport 链路使用 `flow=ma_cn_passport_qr_login`（提交 `871e059` 已实现，与 v1 `hk4e_game_token_exchange` 双格式兼容），保存 UID/MID/SToken、设备上下文与保存时间。`expires_at` 无可靠来源时为 `null`。`auth status` 完全离线；`auth verify` 用严格 SToken 只读端点在线检查，并将“Token 有效”“账号有绑定角色”“账号有某版区发布权限”区分，不因角色列表为空就断定凭据失效。不自动刷新/交换 Token；服务端判失效时提示重新扫码。

从存储加载后才在内存中派生 `stuid/stoken/mid` Cookie。凭据、派生 Cookie、二维码 URL/ticket、临时上传凭据、验证码数据和签名 URL 不进入 stdout、日志、fixture 或 journal。Unix 私有目录/文件权限与 Windows 当前用户 DACL 沿用现有存储契约；写入前必须通过权限检查，不能仅给警告后继续提交。

### 4.2 能力是交集，不是 Token 类型推断

一次命令可执行，当且仅当：`已验证的会话能力 ∩ 端点证据/adapter_ready ∩ 当前用户显式操作 ∩ 本地安全条件` 都满足。SToken 表示可尝试相关鉴权，不保证版区等级、账号权限、上传配额或发布审核通过。未准备好的写命令不注册；已注册命令遇到服务端权限变化则给出稳定错误，不自动改走另一端点。

能力常量定稿（v2.1，与代码 `internal/session` 对齐；新增项为纯增量，不重命名既有常量）：

| 能力常量 | 覆盖命令域 | 门禁状态（871e059） |
|---|---|---|
| `read-account` | `auth verify`、`role list` | `available`（已有实机只读验证） |
| `write-post` | `post create/delete`、`draft publish` | 未放行（适配器存在，命令未注册） |
| `draft-write`（新增） | `draft save/delete` | 未放行（适配器存在，命令未注册） |
| `image-upload` | 图片/封面上传（随写命令内部使用） | 未放行（`media/image` 适配器待建） |
| `video-upload` | 视频秒传引用、首传、封面 | 未放行（底座 + 契约测试就绪） |
| `verification-interactive`（新增） | 1034 人工验证流程 | 未实现 |

`session.Require` 按此表放行；任何能力的判定依据（会话有效性、protocol profile、非破坏性 preflight）缺失时报告 `unknown`，不推断。

## 5. 命令面与开放顺序

| 命令域 | 目标命令 | 开放依据 |
|---|---|---|
| 认证 | `auth login/status/verify/logout` | 基线已有 HK4E；提交 `871e059` 已实现 `--mode=passport` 默认；目标是 status 离线 + Passport 为默认 |
| 查看 | `role list`、`post list/show`、`draft list/show`、`favorite list` | 远端基线已有只读命令；目标需补分页与响应变体测试 |
| 草稿 | `draft save/delete/publish` | 完整 body、覆盖语义、审核与失败 fixture；显式写确认 |
| 帖子 | `post create/delete`、`post review show/undo` | 按内容类型分别就绪；删除/撤回先核对归属与状态 |
| 媒体 | `media video probe`（本地）以及 `post create --kind video` | VOD/封面/挑战/审核全流程验收后开放，不单独暴露长期有效上传密钥 |
| 暂缓 | `post edit`、`favorite add/remove` | 尚缺足够写契约，不因 SToken 可得而启用 |

沿用 `--json` 单一稳定 envelope、`--dry-run` 仅本地解析/校验/摘要、`--yes` 用于删除/撤回等明显写操作。`post create`、`draft publish` 本身是明确的写命令，必须在输出中标明目标账号、版区与内容摘要；非交互脚本的危险写操作需要显式确认。读取命令不产生草稿、媒体上传、验证会话或其它写副作用。不要为了显示一条 `post show` 自动触发登录。

### 5.1 `ContentSpec` 与服务端表示

`schema_version=1` 的 `kind=image|article|video`、有序 text/image/video blocks、标题、版区、话题与本地封面仍是唯一内容输入。相对媒体路径以 spec 文件目录为基准，拒绝未知字段、重复 JSON 键、非普通文件和越限媒体。先得到单一规范化内容模型，再分别编译服务端的 `content`、`structured_content`、`meta_content`；内嵌 JSON 必须程序化序列化，不手写转义字符串。

**不能把 `view_type` 当全局、单值的内容类型枚举。** 草稿列表的 `view_type=1/2/5` 是查询桶；不同发布体中数字及字段组合存在差异，App 视频发布实测为 `view_type=5` 且依赖 `meta_content.vods`，与长文不能仅靠数字区分。每种 `ContentSpec.kind × 目标端点` 使用独立、经 fixture 锁定的 payload 编译器；解析草稿类型时结合响应结构，不仅看桶号。`block_reply_img` 若出现必须是 JSON number `0/1`，不是 boolean。

## 6. 读操作与分页

首版把 `draft list` 的默认模式明确定义为**跨桶首页预览**：分别取 `view_type=1/2/5` 首页，按 `draft_id` 去重，以 `updated_at` 降序、`draft_id` 升序稳定排序，再取 `--limit` 条；只要任一桶还有后续或合并结果被截断，就标记 `has_more=true`，同时给出“预览不能续页，请指定 `--view-type`”的 warning，不返回虚假的全局 `next_cursor`。默认预览与 `--cursor` 同时使用时必须报 `INPUT_INVALID`，不能静默忽略游标。需要遍历时使用 `draft list --view-type 1|2|5 --cursor ...`，每个桶分别沿用服务端不透明游标。未实测内容结构前，`--kind` 不能靠 `view_type` 直接判定长文或视频；要么读取详情可靠分类后再过滤，要么暂时移除该过滤。未来如提供“全部草稿”可续页功能，再设计带版本号、各桶 offset、排序边界与去重策略的复合游标。不能把 `view_type=7` 当“所有草稿”，也不能把首页预览说成全量分页。

帖子/收藏列表保留不透明服务端游标；`next_offset` 可为 number/string，空列表的 `0` 不代表下一页。分页中途失败返回错误和可恢复位置，不返回假完整结果。

`getPostFull` 按 `data.post.post` 双层结构解析并对已证实变体兼容；正文可能是字符串或对象，作者也可为 null。所有响应先经大小限制、retcode 与形态校验，再进入领域模型，不直接把原始上游 JSON 作为 `--json` 输出。

## 7. 写操作状态机

```text
本地校验 → journal: prepared → 媒体准备（如需） → ready_to_submit
→ 单次 releasePost/v2 调用（草稿 API 使用独立结果模型）
  ├─ 未收到确定响应：unresolved → 只读对账；禁止盲目重试
  ├─ 1034（已验证的内容类型）：verification_required → 用户人工验证后同一 payload 重发
  ├─ 1034（未验证的内容类型）：FEATURE_UNAVAILABLE → 保留操作，不擅自重发
  ├─ 其他非零 retcode：rejected；保留可修复草稿
  └─ rc=0：先检查 release_check_result
       ├─ can_release=false + 两个 ID 均空：rejected
       ├─ can_release=false + 任一有效 ID：unresolved（协议冲突，先只读对账）
       ├─ 有效 post_id：published → 可只读确认
       ├─ post_id 空 + 有效 post_review_id：under_review → review/detail
       └─ 两个 ID 均空且无明确门槛：unresolved → 只读对账
```

`draft save` 与 `post create` 分别建模；保存草稿不等于发布成功。新建草稿不带 `draft_id`；保存后记录服务端返回 ID。`draft publish` 必须先读详情、核对当前内容及所属账号，再按其内容类型编译发布请求；发布后草稿是否仍存在以只读结果为准，不预设服务端自动删除。编辑已有草稿若无可靠版本/冲突条件，必须显式 `--yes` 并做发布前快照核对。

`release_check_result.can_release=false` 且两个 ID 均空时，即使外层 `rc=0` 也是业务拒绝；若同时返回有效 ID，则是协议矛盾，必须标记 `unresolved` 并只读对账，不能据此断言未发布。`post_id=0` 且有 `post_review_id` 才是审核中。删除公开帖子与撤回审核中帖子是不同适配器，不能以一个通用 `delete` body 猜测。发帖或删除调用在网络断开后结果不明时只读对账；上游未证明有幂等键，不自动重发。

### 7.1 1034 安全验证

1034 是**视频发帖链路中已复现**的安全验证门禁，不等同于封号或永久失败；图文/长文遇到同码时，只有该内容类型的 fixture 证实相同流程后才能复用处理。目标协议为 `createVerification` 获取短期 challenge → 用户在官方极验组件点选 → `verifyVerification` 校验 → `releasePost/v2` 携 `x-rpc-challenge` 重发**同一个规范化发布 payload**。具体字段、时效和错误响应先制作脱敏 fixture 与契约测试；现有代码尚未实现此流程，不能据此开放真实发布。

交互载体默认采用只绑定 `127.0.0.1` 随机端口的短期本地页面，并以一次性随机 nonce 关联本次操作；页面不接收 SToken、Cookie 或完整正文，仅传递官方验证组件所必需的挑战字段。用户本人完成验证，CLI 不接入打码服务、不模拟人机动作、不缓存挑战用于未来发布。端口关闭、浏览器失败或非交互环境返回 `INTERACTION_REQUIRED` 和可恢复操作 ID；挑战过期回到待验证状态，不无限循环。若首次发布不是明确的 1034，而是网络结果未知，先只读对账，不能直接重发。官方验证服务必需的数据传输须在隐私说明中披露，不额外发送给其它服务。

### 7.2 Journal 与对账

Journal 只保存操作 ID、账号脱敏标识、内容/媒体摘要、目标版区、阶段、公开帖 ID/审核 ID 以及必要的无秘密恢复元数据。不得保存 SToken、Cookie、STS、验证码、OSS 签名、原始完整请求体或含私密正文的日志。对同一账号、相同内容摘要的未决发布阻止再次提交；用户若要承担重复风险，需明确指定 operation ID 并确认。对账仅使用帖子/草稿/审核只读接口；无法证明结果时持续标记 `unresolved`，不把“没查到”当作“肯定没发”。

## 8. 媒体管线

### 8.1 图片、封面、长文

本地校验大小/MIME/普通文件 → 从同一打开的文件句柄计算摘要并上传 → 获得经过 host 和形态校验的 OSS URL → 内容编译。图文、长文和视频封面可共用底层上传客户端，但业务字段及结果模型分开。上传完成但发布失败时提示可能存在孤儿媒体；没有可信删除接口时不声称已清理。

### 8.2 视频

```text
本地探测时长/格式/摘要 → publishVideoPerm 与配额
→ isExist：命中则取得本账号 video_id；未命中继续
→ getToken → SigV4 ApplyUploadInfo → 分片 transfer → finish
→ CommitUploadInfo → 有界等待 getVideoID → 上传/关联封面
→ 视频专用 releasePost/v2 → 验证/审核状态机
```

首传请求的 `x-upload-content-crc32` 使用**8 位小写十六进制**，finish 的分片 CRC 表与该表示保持一致；`SessionKey` 来自 `Result.Data.UploadAddress.SessionKey`。VOD 返回的 `fake_access_key` 字样本身不判定凭据无效；但签名、STS 和主机仍必须严格校验。`getVideoID` 的 16006 在 Commit 后可能是异步回调未完成，只对这一明确状态做有界退避重试；其它错误不泛化为“等等就好”。`isExist` 命中只允许本账号媒体 ID，不复用他人视频。视频发布使用已实测的专用 body（`meta_content.vods`、顶层封面等），不得套用长文的同名 `view_type` 模板。

断点续传仅在能验证上传会话、文件摘要与服务端分片状态一致时开放；checkpoint 不落任何临时密钥、签名 URL 或可重放挑战。不能安全恢复时丢弃本地上传进度并明确提示，不能擅自删除远端媒体。

**首传结论的演进（v2.1 记录，避免再被旧结论误导）**：2026-09-18 的会话实测中，CLI 以自身身份走 `getToken → ApplyUploadInfo` 拿到的 SpaceKey 为占位值（JWT 内 `accessKey="fake_access_key"`），transfer 被拒（4007），重试后 `getToken` 升级为 16003，当时结论为“首传需在 App 内完成，CLI 仅走秒传”。2026-09-19 上游（CNB `0f9fcac`）记录了首次 VOD 上传复现成功，推翻该结论；结合新证据，当时的 4007 更可能是**请求形态缺陷**（如上述 CRC 十进制表示）而非平台策略。执行顺序：先修正 CRC hex 与 16006 处理，再以小视频重试 CLI 首传；若修正后仍稳定复现 4007/16003，则回退“秒传 + 同账号 video_id 引用”作为最终形态，保留失败证据并下调成熟度，不循环重试。`fake_access_key` 字样本身不作为凭据无效的判据，是否可传以 transfer/finish/Commit 的实际响应为准。

## 9. 错误、隐私与平台约束

- 维持稳定的 `--json` 成功/失败 envelope 与退出码；新增 `INTERACTION_REQUIRED`、`PUBLISH_REVIEW_PENDING`、`PUBLISH_CHECK_REJECTED` 等状态应先定义机器语义和兼容策略，审核中属于已接受的结果而非传输失败。
- 区分：凭据无效、能力未开放、版区门槛、内容格式错误、需要人工验证、审核中、远端明确拒绝、远端结果未知。人类提示可本地化，脚本只依赖 code/state。
- stdout 只放最终结果；进度和可操作提示走 stderr。诊断日志不得输出原始请求/响应、正文、文件绝对路径、Token、二维码、签名 URL 或用户完整标识。
- Windows 权限与原子替换需要真实 Windows 测试；macOS/Linux 的 0700/0600 和 symlink 检查分别测试。交叉编译通过不代表运行时安全行为通过。
- CLI 仅操作当前用户显式授权的社区账号；尊重服务端限流、安全验证和审核，不提供规避风控或无人值守批量写入机制。

### 9.1 写结果状态与新增错误码（v2.1 定义）

写命令的成功 envelope 在 `data` 中携带状态机结果，脚本只依赖以下机器字段：

```json
{
  "ok": true,
  "data": {
    "operation_id": "…",
    "state": "published | under_review | rejected | unresolved | verification_required | saved",
    "post_id": "…",
    "post_review_id": "…",
    "draft_id": "…"
  },
  "warnings": []
}
```

状态语义（与 §7 状态机一一对应）：

| state | 含义 | 退出码 |
|---|---|---:|
| `published` | 已发布，`post_id` 有效 | 0 |
| `saved` | 草稿已保存，`draft_id` 有效 | 0 |
| `under_review` | **已接受进入审核**，`post_review_id` 有效；这是成功结果而非错误，不设独立错误码 | 0 |
| `rejected` | 业务拒绝（`release_check_result.can_release=false` 且两 ID 均空，或非零 retcode） | 4 |
| `unresolved` | 结果未知/协议矛盾，需只读对账 | 5 |
| `verification_required` | 收到已验证内容类型的 1034，等待用户人工验证 | 2 |

新增失败错误码（纯增量，旧消费者按未知 code 处理即可）：

| 错误码 | 退出码 | 触发 |
|---|---:|---|
| `INTERACTION_REQUIRED` | 2 | 交互载体不可用（端口关闭/非交互环境）或收到 1034 但 CLI 无法呈现验证页面；error 携带 `operation_id` 供恢复 |
| `PUBLISH_CHECK_REJECTED` | 4 | `release_check_result.can_release=false` 且两 ID 均空——服务端**业务门槛拒绝**（如版区等级）；与传输层 `REMOTE_REJECTED` 同为退出码 4，脚本用 code 区分，message 保留服务端人话说明 |

注意：`post_id` 为字符串 `"0"` 时视为空；`post_review_id` 优先于 `post_id` 判定审核态。`state=rejected` 与错误码 `PUBLISH_CHECK_REJECTED` 的边界：前者用于“部分流程已走完、journal 已建立”的写命令结果，后者用于 preflight/单发场景的直接失败。

## 10. 分阶段实施与验收

| 阶段 | 交付 | 必须通过的门禁 |
|---|---|---|
| A：基线对齐 | Passport 默认登录、严格只读校验；修正草稿首页预览、单桶游标及 `--kind` 语义 | 登录/失效/角色空列表 fixture；存储权限和跨平台测试；命令树仍无写命令 |
| B：草稿与图片 | `ContentSpec` 编译、图片/封面上传、草稿保存/删除 | 合成 fixture、真实样本脱敏扫描；dry-run 无网络写入；保存覆盖与孤儿媒体处理 |
| C：图文/长文发布 | `post create/delete`、`draft publish`、journal、审核状态 | 两种内容 body、版区门槛、审核/公开/结果未知及只读对账测试；若遇 1034，须先有该内容类型的验证 fixture，否则明确返回待支持状态，不能复用视频验证流程；用户授权实机验收 |
| D：视频 | 首传+秒传、封面、视频专用发布、审核撤回 | CRC hex/SessionKey/16006/跨账号拒绝/挑战过期 fixture；真实账号的显式单次验收；Windows/macOS/Linux 编译和相关运行检查 |
| E：后续能力 | 编辑、收藏写操作等 | 每个接口独立补证据与冲突/重复写入测试，不因前阶段通过而自动开放 |

提交 `871e059`（现本地 `main` HEAD）已有 `auth`（Passport 默认 + HK4E 兼容）、只读命令、草稿/帖子写适配器与 VOD 底座，但 `session.Require` 仍只放行 `read-account`、Cobra 未注册写命令、`media/image`、`operation` 与 1034 交互尚缺完整实现。经 v2.1 逐项核实，该提交在阶段 D/A 前必须修正的点：

1. `internal/media/video/vod.go:276`：分片 CRC32 以**十进制**写入 `x-upload-content-crc32`（`strconv.FormatUint(...,10)`），与最新实测的 **8 位小写十六进制**不符——这是首传 transfer 被拒（4007）的头号嫌疑，须先修正并按 hex 重跑契约测试；
2. `getVideoID` 无针对 **16006**（Commit 后异步回调未完成）的有界退避重试；
3. 草稿跨桶列表（`internal/draft`）在各桶首页拼接后截断，忽略 `--cursor`、未按 `updated_at` 降序稳定去重、`--kind` 在截断后过滤——**不满足** §6 的预览/分页契约，须按契约重写；
4. `KindFromViewType(5)` 固定解释为长文，不能覆盖视频帖实测（`view_type=5` + `meta_content.vods`）；`--kind` 过滤须改为按详情分类或先行移除；
5. 帖子发布结果解析须按 §7 的 `release_check_result` 四分支与 §9.1 状态字段重新核对（旧实现未区分“业务拒绝”与“协议矛盾”）。

该提交中已验证**无需**修正的点：交换与 Passport 登录 host（`passport-api.mihoyo.com`，实捕确认）、`block_reply_img` number 契约、`ListMeta` number/string 容错、`getPostFull` 双层包装解析。阶段完成的判断以代码、脱敏 fixture、自动测试和用户明确授权的实机验收为准，不以本文状态表替代。

## 11. 决策记录（v2.1 落定）

v2.0 的三项待确认决策按下述结论执行；如需推翻，先在此处记录新证据与理由再改代码。

1. **第一批开放的写命令：先草稿（save/delete），后发布。** 依据：`draft/save` 的精简可用 body 与 `block_reply_img` number 契约已由单变量对照实测锁定，失败面小（无审核态、无媒体强制），且草稿→发布为 `releasePost/v2` 提供可复用的内容编译与错误消化路径。图文/长文发布在草稿路径验收通过且发布 body fixture 齐备后同批开放，不拆散。
2. **审核能力：首个写版本提供只读 `post review show`（含 review_id、audit 状态、App 内处理指引）；`review undo`（写）延后**，待撤回请求与“撤回后帖子状态”成对脱敏 fixture 齐备。任何审核中帖子的输出都必须携带 `post_review_id`，即使用户尚不能在 CLI 内撤回。
3. **跨桶复合游标：推迟。** 首版维持“跨桶首页预览 + 单桶续页”（§6）。触发重新设计的条件：用户实测中预览+单桶遍历确实无法覆盖“全量导出/清理”场景，且能接受带版本号的复合游标复杂度；届时按 §6 预留的版本号、各桶 offset、排序边界与去重要素另行设计，不渐进塞进现有 `--cursor`。

## 12. 依据与维护

- [CNB `mihoyo-api` 分支](https://cnb.cool/NRD-Tech/Reverse_Project/-/tree/mihoyo-api)，固定审阅提交 [`0f9fcacc6af1524b8a5ab570f789f34580cbea39`](https://cnb.cool/NRD-Tech/Reverse_Project/-/commit/0f9fcacc6af1524b8a5ab570f789f34580cbea39)（2026-09-19）；重点是 `mihoyo_bbs/docs/api/ma-cn-passport扫码登录_2026-09-15实测.md` 的 §4、§9、§11。该文按时间追加，旧段落的“视频首传失败/1034 未解”已被后续章节推翻。
- 本地保留的 `docs/architecture/` 资料：`passport-qr-login.md`（注意：该文件仅存在于编写 v2 的开发工作区，本 checkout 的 `docs/` 可能不含它，Passport 链路证据以上游 `0f9fcac` 实测记录为权威来源）、`community-features.md`、`evidence-manifest.md`、`video-upload-protocol.md`、`api-reference.md`。这些历史资料未随本文件纳入版本控制；其中旧协议记录的 CRC 十进制结论已在 `video-upload-protocol.md` 加注修正（2026-09-19）。
- 每次上游变化必须记录 commit、影响端点、实测级别、脱敏 fixture、契约测试与设计决策；不能只更新“接口能用”的文字。若 CNB 结论与本项目实机行为冲突，应降低能力成熟度、保留失败证据并停止该写命令发布。
