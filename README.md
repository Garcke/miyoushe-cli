# miyoushe-cli

<p align="center">
  <img src="assets/miyoushe-cli-banner.png" alt="miyoushe-cli blue 3D-ASCII banner">
</p>

米游社社区命令行工具，使用 Go 与 Cobra 构建，支持 Windows、macOS 和 Linux。

当前源码以只读操作为主，提供扫码登录、会话验证、绑定角色、帖子、草稿、收藏、搜索和社区分区浏览。

> 本项目是非官方社区工具，与米哈游、HoYoverse 无隶属或授权关系。上游接口可能随时调整，请勿将真实 Token、Cookie 或其他凭据提交到仓库或日志。

使用指南见 [guides/](guides/)；设计文档与开发记录不会移入公共仓库。

## 可用功能

- Passport 扫码登录并保存 SToken v2；同时保留 HK4E 兼容登录模式。
- 查看登录状态并在线验证当前会话。
- 查看绑定角色、帖子详情与列表、草稿和收藏。
- 搜索帖子、话题和综合内容。
- 浏览游戏社区、讨论区和分区帖子流。
- 使用稳定的 JSON envelope 和固定退出码进行脚本集成。
- 使用原子写入保存凭据；Unix 使用 0700/0600 权限，Windows 使用受保护 DACL。

发帖、删除帖子、保存或发布草稿、图片及视频上传等写操作尚未加入公开命令树。

## 构建

需要 Go 1.24 或更高版本。

npm 安装器（`@garcke/miyoushe-cli`）已发布；已发布的 `v0.1.0` Release 资产使用
旧的 `mys_*` 命名，安装 `mys-cli` 需要配套 `mys-cli_*` 命名的新版本。配套版本
发布后即可使用：

```bash
npx @garcke/miyoushe-cli@latest install
```

安装器需要 Node.js 18 或更高版本。它会校验 GitHub Release 中的 SHA-256，
并将 `mys-cli` 安装到当前用户目录；如果目录尚未加入 `PATH`，会给出提示。

在配套版本发布前，请使用源码构建方式：

```bash
git clone https://github.com/Garcke/miyoushe-cli.git
cd miyoushe-cli
go build -trimpath -o mys-cli ./cmd/mys-cli
```

Windows 可以将输出文件名改为 `mys-cli.exe`。

## 快速开始

```bash
mys-cli auth login                         # Passport 扫码登录
mys-cli auth login --mode hk4e             # HK4E 兼容登录
mys-cli auth status                        # 离线查看本地登录状态
mys-cli auth verify                        # 在线验证会话
mys-cli auth logout                        # 删除本地凭据

mys-cli role list [--game-biz hk4e_cn] [--json]    # 只列绑定角色
mys-cli role note [--game-biz hk4e_cn] [--json]    # 实时便签（原神/绝区零）
mys-cli post list [--uid ...] [--cursor ...] [--limit ...] [--json]
mys-cli post show <post-id> [--json]
mys-cli draft list [--view-type 1|2|5] [--cursor ...] [--json]
mys-cli favorite list [--role game_biz:game_uid:region] [--full] [--json]

mys-cli search posts <关键词> --gids <正整数> [--json]
mys-cli search topics <关键词> [--json]              # 跨社区搜索，不接受 --gids
mys-cli search all <关键词> --gids <正整数> [--json]

mys-cli forum games [--json]                         # 全部游戏（GID / en_name / 名称）
mys-cli forum list --game <GID|en_name> [--json]     # 单游戏分区与规则（匿名）
mys-cli forum posts --game <GID|en_name> --forum <ID|精确名称> [--json]   # 需登录
mys-cli forum feed --game <GID|en_name> --forum <ID|精确名称> [--order hot|recent] [--cursor ...] [--json]
                                                    # 匿名热帖/最新帖（默认 hot）
mys-cli forum images --game <GID|en_name> --forum <ID|精确名称> [--json]  # 图片分区榜单（需登录）
```

使用 `mys-cli <command> --help` 查看完整参数。

```bash
mys-cli --version
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

## 从公开版 v0.1.0 迁移

| 旧用法 | 现在 |
| --- | --- |
| `mys …` | `mys-cli …`（可执行文件名） |
| `forum discussion <gids>`、`forum posts <id> --gids <gid>` | `forum list --game <选择器>`、`forum posts --game --forum` |
| `search posts/all` 省略 `--gids`（旧默认 `2`） | 必须显式传入 `--gids` |
| `forum posts --sort <n>` | 已移除（无排序参数） |
| `search all --preview` | 已移除 |
| `search posts --order <任意值>` | 仅支持 `--order 1`（最热）或 `--order 2`（最新） |
| JSON 字段 `uid_masked` | `uid`（完整值） |

`role note` 与 `forum feed` 是此版本新增的命令，不属于旧版命令迁移。公开版 v0.1.0 没有 `remote_message` 字段；当前源码的错误输出不会回显上游错误原文，JSON 错误对象中的 `remote_message` 为 `null`。

## 当前限制

- 只支持一个默认社区账号。
- `search posts` 和 `search all` 必须显式传入正整数 `--gids`（没有默认社区，也不提供全站搜索）；缺失或非法值在本地失败且不发出网络请求；可用 `forum games` 查询 GID。
- `search topics` 是跨社区搜索，不接受 `--gids`（该端点不按 GID 过滤）。
- Forum 选择器：`--game` 接受正整数 GID 或 `forum games` 显示的 en_name；`--forum` 接受正整数 ID 或该游戏内精确唯一的服务端名称。
- `forum feed` 是匿名分区帖子流（热帖/最新帖，默认热帖，服务端固定约 20 条/页）；与需要登录的 `forum posts` 明确分离，匿名请求不携带 Cookie/DS。
- CLI 固有界面（Help、标签、本地错误）为英文；远端昵称、标题等展示内容保持 Unicode 原样；上游错误原文不回显（JSON 中 `remote_message` 为 `null`），人类输出会转义控制字符。
- `search posts --order` 只接受已验证的 `1`（最热，等价服务端默认）与 `2`（最新）；其他取值本地失败且不发出请求。
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
