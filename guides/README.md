# miyoushe-cli 使用指南

这里存放面向 CLI 用户与 Agent 的公开文档，只描述当前已发布或已合并到开发版本的行为；尚未实现的设计方案不会作为可用命令展示。

本版对应 v4 命令面（可执行文件名为 `mys-cli`）：CLI 自带的帮助、标签和本地错误为英文，远端业务内容（昵称、标题、分区名）保持 Unicode 原样；上游错误原文可能回显凭据，CLI 只公开错误分类与远端返回码，不展示原文。`--json` 是唯一承诺稳定的机器接口，带整数 `schema_version`。

## 账号与角色

- [`mys-cli role`](commands/role.md)：查看绑定角色与实时便签（原神/绝区零；`--game-biz` 精确过滤与部分失败语义）。

## 搜索

- [`mys-cli search`](commands/search.md)：帖子/话题/综合搜索、强制 `--gids`、`--order` 排序与 JSON 分页契约。

## 社区与分区

- [`mys-cli forum`](commands/forum.md)：查看游戏社区目录、单游戏分区与规则，以及分区帖子流（含匿名热帖/最新帖）。

## 通用约定

- 成功结果写 stdout，进度与警告写 stderr；`--json` 时 stdout 始终只有一个 JSON 文档——失败也写 stdout 并返回非零退出码。
- 退出码：`0` 成功、`1` 内部错误、`2` 输入/能力门禁、`3` 认证、`4` 服务端明确拒绝、`5` 结果未知/部分失败。机器判断请使用 `error.code`，不要解析英文句子。
- 未知子命令以 `Error [INPUT_INVALID]` 失败并列出可用命令，不会退回父命令帮助并返回 `0`。

返回[项目 README](../README.md)。
