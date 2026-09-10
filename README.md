# miyoushe-cli

米游社社区 CLI（Go + Cobra）。当前包含阶段 0 + 阶段 A 的实现：扫码登录与凭据安全存储、会话验证，以及角色、帖子、草稿、收藏的只读查看。

## 实现状态

已实现：

- `mys auth login / status / verify / logout`：HK4E 扫码状态机、Game Token 严格交换 SToken、跨平台凭据安全存储（Unix 0700/0600；Windows 以当前用户 SID 限制 DACL）、原子替换写入。
- `mys role list`、`mys post list/show`、`mys draft list/show`、`mys favorite list`：只读适配器 + 稳定 JSON envelope + 游标分页。
- 协议层：BBS DS1 与 passport DS、公共 x-rpc 头组合、host 白名单、1 MiB 响应上限、拒绝重定向与脱敏错误。
- 测试：协议/存储/登录状态机/各只读适配器/命令层均以 `httptest` + 合成 Token 覆盖；无任何真实凭据。

未实现（按协议证据门禁保持不注册，不提供运行到一半才发现未实现的写命令）：

- `post create/edit/delete`、`draft save/publish/delete`、`favorite add/remove`、`operation` 与视频链路——待对应脱敏 fixture 与契约测试齐备后分阶段开放。
- Game Token → SToken 交换的线上可行性仍是阶段 0 门禁：上游证据记录该交换曾被服务端拒绝（-3005/-5300），实现如实报告失败并保留旧凭据，不设计只读回退。

平台状态（区分编译与实机验证）：

| 平台 | 交叉编译 | 安全存储实机验证 |
|---|---|---|
| Windows amd64/arm64 | ✅ | ✅（开发平台） |
| macOS amd64/arm64 | ✅ | ❌ 待实机验证 |
| Linux amd64/arm64 | ✅ | ❌ 待实机验证 |

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

架构与协议设计文档不在本仓库中（`docs/` 已加入 .gitignore，仅本地保留）。
