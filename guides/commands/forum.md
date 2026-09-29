# `mys-cli forum`

浏览米游社游戏社区：游戏目录、单游戏的分区与规则、分区帖子流（登录），以及匿名热帖/最新帖。

| 命令 | 是否需要登录 | 作用 |
|---|---|---|
| `forum games` | 不需要 | 列出全部游戏（GID、en_name、名称） |
| `forum list --game <选择器>` | 不需要 | 单游戏的分区目录与规则描述 |
| `forum posts --game --forum` | **需要** | 分区帖子流（服务端约 20 条/页） |
| `forum feed --game --forum [--order hot\|recent]` | 不需要 | 分区热帖（默认）或最新帖 |

`forum games` 与 `forum list` 是匿名目录请求，不会携带你的 Cookie/DS；`forum feed` 走匿名入口，也不要求登录；只有 `forum posts` 使用登录会话。

## 选择器

`--game` 只接受两种取值：

- 正整数 GID（例如 `2`、`8`）；
- `forum games` 输出里的 `en_name`（例如 `ys`、`zzz`）。

不接受中文游戏名、拼音或其他别名；`原神` 这样的输入会失败，并在错误上下文里列出候选。GID 不在当前目录中同样会失败。

`--forum` 只接受两种取值：

- 正整数 forum ID（例如 `26`）；
- 该游戏目录内**精确且唯一**的服务端分区名称（例如 `酒馆`）。

纯数字输入始终按 ID 解析；名称不存在时错误上下文会列出该游戏的全部分区候选，重名时只列出同名候选，不会自动选择近似项。

## `forum games`

```bash
mys-cli forum games
```

输出示例（CLI 界面为英文，远端中文名称保持原样）：

```text
2  ys  原神
8  zzz  绝区零
```

`--json` 保留上游原始字段：

```json
{
  "schema_version": 1,
  "ok": true,
  "data": {
    "kind": "list",
    "context": {},
    "items": [
      {"gids": "2", "name": "原神", "en_name": "ys", "op_name": "hk4e", "has_wiki": true}
    ],
    "has_more": false,
    "pagination": {"mode": "cursor", "resumable": false, "next_args": []}
  },
  "next_cursor": "",
  "warnings": [],
  "notices": [],
  "error": null
}
```

人类可读输出只显示 GID / en_name / 名称；`has_wiki` 只是上游集成标记，不代表该游戏实际有百科内容，因此只在 JSON 中出现。

## `forum list`

```bash
mys-cli forum list --game ys
mys-cli forum list --game 2      # 与 --game ys 等价
```

输出示例：

```text
原神 (ys, gids=2)
  26  酒馆
      Description: 冒险传说
```

分区名称或描述缺失时显示 `Not provided`，不会推断发帖权限；`discussion_id` 或分区 ID 缺失时按响应结构错误处理。

JSON 中每个分区是 `{forum_id, name, des, game_gids, en_name, game_name}`，`context` 记录解析后的 `gids` 与 `en_name`。

## `forum posts`（需要登录）

先完成扫码登录与会话验证：

```bash
mys-cli auth login
mys-cli auth verify
mys-cli forum posts --game ys --forum 26
```

输出示例：

```text
71234567  Image/text post  2026-09-24 12:00  今天的酒馆见闻
More results are available.
Next cursor: <cursor>
```

- 服务端固定每页约 20 条并忽略页大小参数，因此**没有 `--limit`**，不要期待可调页大小。
- `--cursor` 是服务端不透明游标，原样传回即可，不要解析或修改；`has_more=false` 时 `next_cursor` 一定为空字符串。
- `forum posts` 不提供排序参数，固定服务端默认序；排序语义（最新回复/最新发布）只是协议层观察，不暴露为命令选项。
- 校验顺序是语法 → 游戏 → 分区归属 → 会话 → 请求：分区不属于所选游戏时，命令在发出帖子请求前就失败。

## `forum feed`（匿名热帖/最新帖）

```bash
mys-cli forum feed --game ys --forum 26                 # 默认热帖
mys-cli forum feed --game 2 --forum "酒馆" --order recent
mys-cli forum feed --game ys --forum 26 --cursor <cursor>
```

- `--order hot`（默认）走热帖入口，`--order recent` 走最新帖入口；其他取值报输入错误。
- `--cursor` 是**频道相关**的不透明游标：热帖对应 `last_id`，最新帖对应 `page`；同样在 `has_more=false` 时不会返回游标。
- 冷门分区的热帖可能为 0 条，人类输出显示 `No results found in <分区> (hot feed).`，这是正常空结果而不是错误；最新帖仍会返回整页。
- JSON 的 `context` 会带上 `order` 字段，便于脚本区分两个频道。

## 错误与诊断

失败时 JSON envelope 固定为：

```json
{
  "schema_version": 1,
  "ok": false,
  "data": null,
  "next_cursor": "",
  "warnings": [],
  "notices": [],
  "error": {
    "code": "INPUT_INVALID",
    "kind": "input",
    "message": "Unknown game selector \"原神\"; use a positive GID or the en_name shown by forum games",
    "remote_code": null,
    "remote_message": null,
    "retryable": false,
    "context": {"candidates": [{"gids": "2", "en_name": "ys", "name": "原神"}]},
    "action": {"type": "run_command", "executable": "mys-cli", "args": ["forum", "games"]},
    "partial_data": null,
    "resume_cursor": null,
    "resume_args": []
  }
}
```

人类模式则输出一行 `Error [INPUT_INVALID]: …` 和至多一条 `Next:` 建议。Agent 应依赖 `code`/`kind`/`context.candidates`/`action.args`，不要解析英文句子。

## 常见问题

### 提示缺少 `--game` 或 `--forum`

两个选择器都是必填。先运行 `mys-cli forum games` 找到 GID/en_name，再用 `mys-cli forum list --game <选择器>` 确认分区 ID 或精确名称。

### 中文游戏名被拒绝

设计上只接受正整数 GID 或 `en_name`，避免自造别名与模糊匹配。请改用 `ys`、`zzz` 这类目录输出值。

### 分区名不唯一或不存在

改用 `forum list` 输出中的数字 forum ID；纯数字输入始终按 ID 解析，不会退化为名称匹配。

### 热帖没有结果

冷门分区的热帖可能为空，这是服务端行为；改用 `--order recent` 查看最新帖。

### `--sort` 报输入错误

公开版 v0.1.0 的 `forum posts --sort` 已移除；新版 `forum posts` 固定使用服务端默认顺序。`forum feed` 是新增的匿名命令，排序选项为 `--order hot|recent`（默认 hot），并非旧版 `--sort` 的直接改名。

### `feed` 需要登录吗

不需要。`feed` 与 `games`、`list` 都是匿名请求；只有 `forum posts` 需要有效会话。

### 命令拼写或子命令不存在

未知子命令会以 `Error [INPUT_INVALID]` 失败并列出可用命令（例如 `Available commands: games, images, list, posts`），不会静默显示帮助并返回成功。

## 相关命令

- `mys-cli forum --help`：命令组帮助。
- `mys-cli forum games --json`：游戏目录（GID ↔ en_name ↔ 名称）。
- `mys-cli forum list --game <选择器> --json`：分区目录与规则。
- `mys-cli forum posts --game <选择器> --forum <选择器> --json`：登录态帖子流。
- `mys-cli forum feed --game <选择器> --forum <选择器> --order hot|recent --json`：匿名帖子流。

[返回使用指南](../README.md)
