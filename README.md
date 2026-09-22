# miyoushe-cli

<p align="center">
  <img src="assets/miyoushe-cli-banner.png" alt="miyoushe-cli blue 3D-ASCII banner">
</p>

米游社社区命令行工具，使用 Go 与 Cobra 构建，支持 Windows、macOS 和 Linux。

当前公开版本以只读操作为主，提供扫码登录、会话验证、绑定角色、帖子、草稿、收藏、搜索和社区分区浏览。

> 本项目是非官方社区工具，与米哈游、HoYoverse 无隶属或授权关系。上游接口可能随时调整，请勿将真实 Token、Cookie 或其他凭据提交到仓库或日志。

## 可用功能

- Passport 扫码登录并保存 SToken v2；同时保留 HK4E 兼容登录模式。
- 查看登录状态并在线验证当前会话。
- 查看绑定角色、帖子详情与列表、草稿和收藏。
- 搜索帖子、话题和综合内容。
- 浏览游戏社区、讨论区和分区帖子流。
- 使用稳定的 JSON envelope 和固定退出码进行脚本集成。
- 使用原子写入保存凭据；Unix 使用 0700/0600 权限，Windows 使用受保护 DACL。

发帖、删除帖子、保存或发布草稿、图片及视频上传等写操作尚未加入公开命令树。

## 安装

推荐使用 npm 安装器：

```bash
npx @garcke/miyoushe-cli@latest install
```

安装器需要 Node.js 18 或更高版本。它会校验 GitHub Release 中的 SHA-256，
并将 `mys` 安装到当前用户目录；如果目录尚未加入 `PATH`，会给出提示。
默认目录为 Windows 的 `%LOCALAPPDATA%\miyoushe-cli\bin`，以及 macOS/Linux
的 `~/.local/bin`。可使用 `--install-dir <路径>` 指定其他目录。

### 从源码构建

需要 Go 1.24 或更高版本：

```bash
git clone https://github.com/Garcke/miyoushe-cli.git
cd miyoushe-cli
go build -trimpath -o mys ./cmd/mys
```

Windows 可以将输出文件名改为 `mys.exe`。

## 快速开始

```bash
mys auth login                         # Passport 扫码登录
mys auth login --mode hk4e             # HK4E 兼容登录
mys auth status                        # 离线查看本地登录状态
mys auth verify                        # 在线验证会话
mys auth logout                        # 删除本地凭据

mys role list [--game-biz hk4e_cn] [--json]
mys post list [--uid ...] [--cursor ...] [--limit ...] [--json]
mys post show <post-id> [--json]
mys draft list [--view-type 1|2|5] [--cursor ...] [--json]
mys favorite list [--role game_biz:game_uid:region] [--full] [--json]

mys search posts <关键词> [--gids 2] [--order ...] [--json]
mys search topics <关键词> [--json]
mys search all <关键词> [--gids 2] [--json]

mys forum games [--json]
mys forum discussion <gids> [--json]
mys forum posts <forum-id> --gids <gids> [--json]
```

使用 `mys <command> --help` 查看完整参数。

```bash
mys --version
```

## 扫码登录行为

- 默认登录模式为 Passport，默认总等待时间为 300 秒，可通过 `--timeout` 调整。
- 达到总等待时限时返回 `LOGIN_TIMEOUT`，退出码为 3。
- 用户按 Ctrl+C 或进程收到终止信号时返回 `CANCELLED`，退出码为 1。
- HK4E 模式失败时不会自动回退到 Passport 模式。
- JSON 模式下，临时二维码 PNG 路径写入 stderr，stdout 只输出最终 JSON envelope；登录结束后临时文件会被清理。

## JSON 与退出码

所有命令都支持 `--json`。自动化脚本应优先判断 `error.code`，不要匹配人类可读错误文案。

| 退出码 | 含义 |
| ---: | --- |
| 0 | 成功 |
| 1 | 本地错误或用户取消 |
| 2 | 输入错误或功能未开放 |
| 3 | 登录、凭据或协议认证错误 |
| 4 | 服务端明确拒绝 |
| 5 | 服务端结果未知或操作状态未决 |

## 当前限制

- 只支持一个默认社区账号。
- `search posts` 和 `search all` 必须指定有效 `gids`；未指定时默认使用 `2`（原神）。
- `search topics` 是跨社区搜索，不支持按游戏过滤。
- 写操作尚未开放。
- macOS 和 Linux 的凭据权限仍需要更多真实环境验证。

## 开发与测试

```bash
go test -count=1 ./...
go vet ./...
go build ./...
```

测试只使用本地模拟服务和合成凭据，不应加入任何真实账号数据。

## License

[MIT](LICENSE)
