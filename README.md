# miyoushe-cli

米游社社区 CLI（Go + Cobra）。当前包含阶段 0 + 阶段 A 的实现：扫码登录与凭据安全存储、会话验证，以及角色、帖子、草稿、收藏的只读查看。

新的目标架构：[App Passport SToken 与社区内容管理设计 v2](ARCHITECTURE-V2.md)（2026-09-19，v2.1 修订）。此分支代码已包含 Passport 默认登录（`auth login --mode=passport`）、草稿/帖子写适配器与火山 VOD 上传底座（合并自提交 `871e059`）；但按 adapter_ready 门禁，**写命令仍未在命令树注册**，适配器缺陷修正清单见 v2 §10。

## 实现状态

已实现：

- `mys auth login / status / verify / logout`：Passport 扫码直出 SToken v2（默认）与 HK4E Game Token 交换（`--mode=hk4e` 兼容路径）、跨平台凭据安全存储（Unix 0700/0600；Windows 以当前用户 SID 限制 DACL）、原子替换写入。
- `mys role list`、`mys post list/show`、`mys draft list/show`、`mys favorite list`：只读适配器 + 稳定 JSON envelope + 游标分页。
- 协议层：BBS DS1 与 passport DS、公共 x-rpc 头组合、host 白名单、1 MiB 响应上限、拒绝重定向与脱敏错误。
- 写适配器（未注册命令）：`draft save/delete`、`post publish/delete/review undo`（视频帖专用 body）、`internal/media/video`（秒传/getToken/SigV4 Apply→分片→Commit/封面）及 `internal/content` 内容模型，全部带契约测试。
- 测试：协议/存储/登录状态机/读写适配器/命令层均以 `httptest` + 合成 Token 覆盖；无任何真实凭据。

未实现（按协议证据门禁保持不注册，不提供运行到一半才发现未实现的写命令）：

- 写命令注册（`post create/delete`、`draft save/publish/delete` 等）与 `operation` journal、`media/image`、1034 人工验证交互。
- 阶段 D 前代码修正：分片 CRC32 十进制→8 位小写十六进制、`getVideoID` 16006 有界退避、草稿跨桶列表预览/分页契约（详见 ARCHITECTURE-V2.md §10）。
- macOS/Linux 安全存储实机验证。

## 快速开始

```bash
go build -o mys ./cmd/mys

mys auth login            # 扫码登录（默认总等待 300s，--timeout 可调）
mys auth status           # 离线查看本地登录状态
mys auth verify           # 在线验证会话与能力（只读）
mys auth logout           # 删除本地凭据（幂等）
mys role list [--game-biz hk4e_cn] [--json]
mys post list [--uid ...] [--cursor ...] [--limit ...] [--json]
mys post show <post-id> [--json]
mys draft list [--kind image|article|video] [--json]
mys favorite list [--role game_biz:game_uid:region] [--full] [--json]
```

所有命令支持 `--json` 输出稳定 envelope；机器判断使用 `error.code`，退出码固定为 0/1/2/3/4/5。

## 当前约束

- 首版只支持一个默认社区账号。
- Game Token 必须成功交换为 SToken；不设计只读回退。
- 未取得完整脱敏请求样本与契约测试的写接口不会开放。
- 视频上传必须先完成火山 VOD 临时凭据协议验证。
- Token、Cookie、上传签名、临时密钥，以及带有效会话/账号上下文的实时 DS 不得进入仓库、日志或测试 fixture。

v2 目标架构见 [ARCHITECTURE-V2.md](ARCHITECTURE-V2.md)；其余历史架构与协议设计文档不在本分支中（`docs/` 已加入 .gitignore，仅本地保留）。
