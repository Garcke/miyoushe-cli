# `mys-cli search`

搜索米游社帖子、话题与综合结果。三个命令均匿名可调（无需登录）。

`search posts` 与 `search all` 必须显式传入 `--gids`（正整数）；没有默认
社区，也没有全站模式。用 `mys-cli forum games` 查看全部 GID。

| 命令 | `--gids` | 作用 |
|---|---|---|
| `search posts <关键词> --gids <GID>` | 必需 | 帖子搜索（按社区过滤；`--order 2` 为最新优先） |
| `search topics <关键词>` | 不支持 | 跨社区话题搜索（端点不按 GID 过滤，不接受该参数） |
| `search all <关键词> --gids <GID>` | 必需 | 综合搜索（topics/users/wikis 分组；GID 解锁对应游戏 wiki 组） |

## 排序（--order）

- 只接受已验证的两个协议取值：`1`=最热（等价服务端默认）、`2`=最新
  （按发布时间倒序，翻页延续）；
- 其他取值（含 `0`、`3` 及以上、非数字）在本地报 `INPUT_INVALID`，不发请求；
- 省略时保持服务端默认；显式设置时 JSON `context.order` 记录 `"1"`/`"2"`。

## 通用规则

- `--limit` 下限为 3：服务端对小于 3 的页大小静默返回空列表；
- 非法 GID 由服务端决定（实测返回空列表），CLI 原样上报；
- `search posts --forum/--channel`（分区内搜索与攻略/视频/图片/官方频道）
  端点已验证但尚未开放为命令。

## JSON 契约

- posts 的 `context` 为 `{"query","gids","scope":"posts"}`；topics 为
  `{"query","scope":"topics"}`（不伪造 GID）；
- `pagination.mode="cursor"`；`resumable` 仅在 `has_more=true` 且游标非空时
  为 `true`；
- 可续页时 `pagination.next_args` 是参数数组（不含可执行文件名），固定顺序
  为关键词、`--gids`（posts）、`--limit`、`--order`（显式设置时）、
  `--cursor`、`--json`，例如：

  ```json
  ["search", "posts", "原神", "--gids", "2", "--limit", "20", "--cursor", "9", "--json"]
  ```

- 末页或空游标时 `next_args` 为 `[]`、顶层 `next_cursor` 为 `""`；
- 远端原文（昵称、标题、话题名）保留在 JSON 字段里；人类输出会将换行与
  控制字符转义，不会伪造额外输出行。

## 常见问题

### 提示缺少 --gids

`search posts` 与 `search all` 的 GID 必须显式给出。先运行
`mys-cli forum games` 获取 GID 列表。

### 话题搜索为什么没有 --gids

实测话题端点（`topic/api/searchTopic`）对 GID、分区、频道参数均无过滤
效果，因此命令不提供看似有效的过滤参数；结果是跨社区的。

[返回使用指南](../README.md)
