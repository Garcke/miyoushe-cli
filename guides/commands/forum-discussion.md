# `mys forum discussion`

查看指定游戏社区的讨论区以及所属分区。该命令是匿名只读命令，不需要先执行扫码登录。

## 用法

```bash
mys forum discussion <gids>
```

`<gids>` 是米游社为游戏社区分配的标识。第一次使用或不确定 GID 时，先查看当前游戏社区目录：

```bash
mys forum games
```

从输出中选择对应的 `game_id`，然后作为 `<gids>` 传给 `forum discussion`。当前版本的目录输出尚不包含游戏名称，需要根据它列出的分区名称识别社区。无法确认时，依次用目录返回的候选 `game_id` 运行只读的 `mys forum discussion <game_id>`，根据返回的讨论区名称和分区名称确认目标社区，不要猜测或长期硬编码映射。已经知道 GID 时可以直接运行查询，不必每次先执行 `forum games`。

## 示例

查看 GID 为 `2` 的游戏社区：

```bash
mys forum discussion 2
```

输出格式示例：

```text
游戏 gids: 2
讨论区: 2 (旅行者讨论区)
  26  酒馆
      描述: 冒险传说
```

这里的 `26` 是分区 ID，可以继续用于浏览该分区的帖子。名称和描述来自远端服务，实际内容可能变化。讨论区名称、分区名称或描述缺失时，人类可读输出会显示 `未提供`；`discussion_id` 或分区 ID 缺失时，命令会将响应视为无效并报错。

## JSON 输出

脚本或 Agent 应使用 `--json`：

```bash
mys forum discussion 2 --json
```

成功响应示例：

```json
{
  "ok": true,
  "data": {
    "discussion_id": "2",
    "subject": "旅行者讨论区",
    "forums": [
      {
        "id": "26",
        "name": "酒馆",
        "des": "冒险传说"
      }
    ]
  },
  "next_cursor": ""
}
```

字段含义：

| 字段 | 含义 |
|---|---|
| `data.discussion_id` | 讨论区标识 |
| `data.subject` | 讨论区名称 |
| `data.forums[].id` | 分区 ID；可传给 `forum posts` |
| `data.forums[].name` | 分区名称 |
| `data.forums[].des` | 分区规则或描述 |

ID 在 JSON 中使用字符串表示。调用方不应假设 `discussion_id`、GID 和 forum ID 必然相同，也不应根据名称推断 ID。`next_cursor` 是统一 JSON envelope 的字段；该命令不分页，因此这里始终为空字符串。

## 浏览分区帖子

选择 `forums[].id` 后，使用同一个 GID 浏览对应分区的帖子。`forum posts` 需要有效的本地登录凭据；尚未登录时先执行：

```bash
mys auth login
mys auth verify
```

登录说明见项目 README 的[快速开始](../../README.md#快速开始)和[扫码登录行为](../../README.md#扫码登录行为)。随后运行：

```bash
mys forum posts 26 --gids 2
```

`forum discussion` 本身只返回讨论区和分区元数据，不返回帖子列表。

当帖子列表提示还有下一页时，把输出的 cursor 原样传回 `--cursor`，并保留相同的 forum ID 与 GID：

```bash
mys forum posts 26 --gids 2 --cursor <cursor>
```

cursor 是远端生成的不透明值，不要解析或自行修改。使用 `mys forum posts --help` 查看当前版本支持的其他选项。

## 常见问题

### 提示缺少参数

该命令只接受一个 GID。下面的命令缺少参数：

```bash
mys forum discussion
```

先运行 `mys forum games` 找到 GID，再重新执行查询。

### 查询被远端拒绝

GID 不存在、已经失效或远端服务拒绝请求时，命令会失败。重新运行 `mys forum games` 获取当前游戏社区目录，不要依赖长期硬编码的示例 ID。

### 查不到帖子

`forum discussion` 不查询帖子。请从它的输出中取得分区 ID，再运行：

```bash
mys forum posts <forum-id> --gids <gids>
```

### 命令拼写

正确的子命令名称是 `discussion`，不是 `disussion`。可以使用下面的命令查看当前版本的完整帮助：

```bash
mys forum discussion --help
```

## 相关命令

- `mys forum games`：列出游戏社区及其分区目录。
- `mys forum posts <forum-id> --gids <gids>`：浏览指定分区的帖子。
- `mys forum --help`：查看 Forum 命令组帮助。

[返回使用指南](../README.md)
