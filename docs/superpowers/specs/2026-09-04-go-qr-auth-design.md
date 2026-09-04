# Go CLI 扫码登录与本地凭据保存设计

日期：2026-09-04

范围：用户已确认的首版 Go 登录模块。本文不扩展帖子、草稿、图片或视频功能。

## 1. 目标与实现边界

- 使用 Go 实现 `mys auth login` 和 `mys auth status`，支持 Windows、macOS、Linux。
- 保留现有 Python 工具作为协议参考，不修改其行为，不将 Python 作为运行时依赖。
- 采用已经讨论并确认的 HK4E 扫码链路：获取二维码、等待手机确认、取得 Game Token、交换 SToken、保存凭据。
- 不实现 Web 扫码只读回退、不自动切换登录方案、不实现短信或密码登录。
- 不把 Game Token 拼接、改名或复制成 SToken；只有交换接口明确成功且响应完整才保存 SToken。
- 本轮不执行真实发帖、上传、删帖或其它社区写操作。

现有仓库记录了 Game Token 交换 SToken 曾失败的情况。用户选择暂不展开这项兼容性研究，不意味着可以假定服务端成功。CLI 必须如实报告交换失败，并保留此前保存的凭据。

## 2. 方案选择

选用独立 Go 模块、直接 HTTP 调用及用户配置目录中的受权限保护 JSON 文件。相对于包装 Python，部署不需要第二个运行时；相对于系统钥匙串，能覆盖无桌面 Linux 环境。

凭据文件不是加密保险库。它防止普通其他本地用户读取，不承诺抵御当前用户权限下的恶意进程、管理员访问或磁盘被离线读取。系统钥匙串或口令加密不属于本轮范围。

建议编码阶段使用独立 Git worktree，保持用于同步上游资料的 `mihoyo-api` 检出不混入实现修改；创建前取得用户同意。当前只新增本设计文档。

## 3. 命令与使用体验

### `mys auth login`

1. 创建本次设备上下文；复用已有凭据中的有效设备标识，不将旧 Token 发送给二维码接口。
2. 获取设备指纹及二维码 ticket，终端显示二维码，提供私有临时 PNG 文件作为兼容方案。
3. 提示使用米游社 App 扫码并在手机上确认，显示“等待扫码”“等待确认”等状态。
4. 默认总等待上限 300 秒，允许 `--timeout` 设置合法的正时长。单次请求上限 15 秒且不能超出总时限，Ctrl+C 可取消整个流程。
5. 默认每两秒查询一次；明确的二维码过期响应触发重建二维码，但不会重置总等待上限。
6. 确认后解析 Game Token，调用 `getTokenByGameToken`。要求返回码为 0、`token_type=1`、非空 Token、交换响应 UID 与扫码 UID 相同。MID 从交换响应取得；扫码响应也有 MID 时两者必须一致。V2 Token 必须有 MID，不拼接 MID 与 Token。
7. 安全落盘成功后显示脱敏账号、Token 类型与保存位置。除用户扫码所需的二维码图案外，不打印完整 Token、Cookie、ticket、二维码原始 URL 或响应体。
8. 正常退出或可处理的错误/取消时清理本次临时二维码文件。异常断电后可能残留的临时文件仍须受用户权限保护。

重建二维码必须使用新二维码对象/矩阵，不能将新 URL 追加到旧二维码内容。

登录成功提示仅表示“服务端签发 SToken 且已保存”，不表示已验证所有社区接口权限。

### `mys auth status`

只读取本地凭据，显示：是否存在、脱敏 UID、Token 类型、保存时间、保存路径，以及“未在线验证”。到期时间没有可靠来源时显示“未知，以服务端鉴权为准”。

不读取或输出账号邮箱、电话、实名信息；不通过只读状态命令发起鉴权、交换或其它网络请求。文件缺失、格式损坏或权限不安全时给出明确提示。

## 4. 协议与模块边界

新增模块位置：`mihoyo_cli/`，入口为 `cmd/mys/`。

- CLI 层：解析命令与超时，展示二维码和脱敏进度，映射退出状态。
- Auth 层：设备上下文、二维码状态机、Game Token 到 SToken 的交换、响应校验。HTTP 客户端可注入，以便使用本地测试服务器。
- Store 层：凭据序列化、权限检查、完整写入与原子替换。Windows 与 Unix 的权限实现分开。
- QR 层：从返回 URL 编码终端二维码及临时 PNG，不调用第三方在线二维码服务。

主要服务：

- 二维码：`hk4e-sdk.mihoyo.com/hk4e_cn/combo/panda/qrcode/fetch`、`query`，保持同一 `app_id=12`、device 和 ticket 上下文。
- 指纹：`public-data-api.mihoyo.com/device-fp/api/getFp`。
- 交换：`api-takumi.mihoyo.com/account/ma-cn-session/app/getTokenByGameToken`，JSON 包含 `account_id` 和 `game_token`，请求头与 DS 依现有参考实现逐项测试。

二维码 HTTP 方法及参数位置采用仓库已验证的 GET fetch / POST query（query 参数）形式，与 UIGF 的 POST JSON 示例区分记录，不悄悄混合两种调用形式。

使用标准 TLS 校验，不关闭证书验证。含凭据的请求不跟随重定向，错误消息不包含原始请求/响应或带敏感参数的 URL。单个 JSON 响应上限为 1 MiB，拒绝缺少成功标志或必需字段的响应。

## 5. 保存格式与安全

默认路径由 `os.UserConfigDir()` 解析，追加 `mys/credentials.json`。不默认写入仓库、不自动发现或导入现有 HAR/凭据文件。

保存字段：

- `schema_version`：格式版本 1。
- `flow`：`hk4e_game_token_exchange`。
- `uid`、`mid`：账号标识，按字符串存储。
- `token_kind`：`stoken`。
- `token_type`：交换响应中的类型，本实现仅接受 SToken 对应值 1。
- `stoken`：交换后收到的 Token。
- `device_id`、`device_fp`：与交换时一致的设备上下文。
- `saved_at`：RFC 3339 UTC 时间。
- `expires_at`：未知时为 null，不按保存时间伪造固定有效期。

Game Token 仅用于当前交换，不额外落盘；不保存整份响应、手机号、邮箱或实名信息。

权限与写入：

- macOS/Linux：私有目录 0700、凭据文件与临时文件 0600。
- Windows：使用当前用户 SID 限制 DACL，不将 `chmod(0600)` 误认为 Windows 访问控制。
- 新文件从创建时就受保护；拒绝凭据目标或专用目录是符号链接/reparse point 的情况。
- 检查现有专用目录及凭据文件的访问范围，发现不安全状态时终止并提示，不修改用户配置根目录的 ACL。
- 在同目录创建随机名称的私有临时文件，完成写入和同步后使用平台支持的原子替换。替换失败保留旧文件，不采用“先删旧文件再改名”。
- 只在全部请求校验与存储操作完成后报告成功；失败不覆盖有效旧凭据。

## 6. 有效期与错误处理

二维码 ticket 的短时有效期与 SToken 有效期是两回事。现有实测记录二维码约 2–3 分钟失效；实现以响应、URL 中的有效期和总等待截止时间为依据，而非保证固定时长。

现有资料未提供可保证的 SToken 固定有效天数。UIGF 说明修改密码后 SToken 会变化；文档中的 30 分钟指 login_ticket。保存到本地不延长服务端有效期，也不等于永久有效。

- 可识别的过期二维码：在总时限内重新生成。
- 查询网络瞬断：仅对安全的状态查询重试，最多连续失败三次，每次间隔两秒，不超出总时限；一次有效响应重置连续失败计数。
- 设备指纹、创建二维码或交换失败：报告阶段和安全的错误码，不批量尝试未知接口。
- 未知二维码状态、空 payload、缺少 Token、账号不一致、非法返回类型：失败，不保存。
- 交换请求结果不明：报告交换未确认，不无条件重试，也不保存 Game Token 伪装成登录成功。
- 文件写入或权限操作失败：报告保存失败，保留原有文件；不降级为明文公共路径。

## 7. 验证与验收

编码采用先失败测试、再最小实现的方式。测试使用合成 Token 和本地 HTTP 测试服务器，不将真实凭据录入 fixture。

覆盖项：

- Init、Scanned、Confirmed、二维码过期与重新生成。
- 总超时、取消、请求超时、异常返回和轮询节流。
- UID/MID 解析、交换请求的 body/header/DS、Token 类型校验和账号一致性。
- Game Token 不会被当作 SToken 保存。
- 成功保存与读取、未知到期时间、损坏文件、不安全权限、旧文件保留及替换失败。
- 终端、日志、错误与状态输出不泄露合成秘密。
- 二维码内容解码/编码一致性及重建后不残留旧内容。
- 执行 Go 单元测试与 `go vet`；交叉编译 Windows/macOS/Linux 的 amd64、arm64 目标。

平台声明区分“编译通过”和“运行验证”。在当前 Windows 主机验证文件权限；未实际运行的 macOS/Linux 安全行为不声称已实机测试。

真实登录需要用户使用自己的 App 扫码确认。没有完成真实交换与保存时，只报告本地测试/编译结果，不声称线上登录已跑通。

## 8. 参考依据

- `mihoyo_bbs/tools/qr_login.py`：现有 HK4E 扫码及交换尝试，不继承其交换失败后保存伪 SToken 的行为。
- `mihoyo_bbs/tools/mys_ds_gen.py`：现有 DS 参考与测试向量。
- `mihoyo_bbs/docs/api/扫码登录与收藏夹_旧版服务整理.md`：已测二维码请求和已知登录限制。
- [UIGF Game Token 扫码](https://uigf.org/zh/mihoyo-api-collection/hoyolab/login/qrcode_hk4e.html)。
- [UIGF Token 交换](https://uigf.org/zh/mihoyo-api-collection/hoyolab/user/token.html)。
- [UIGF 鉴权与 Token 区别](https://uigf.org/zh/mihoyo-api-collection/other/authentication.html)。
