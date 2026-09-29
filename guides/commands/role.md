# `mys-cli role`

查看当前账号绑定的游戏角色，以及已验证游戏的实时便签（原神 / 绝区零）。

| 命令 | 作用 |
|---|---|
| `role list [--game-biz <业务标识>]` | 只列绑定角色，不触发 LToken 交换或便签查询 |
| `role note [--game-biz <业务标识>]` | 列出所选绑定角色的实时便签 |

需要先 `mys-cli auth login`。`role note` 会在查询前用 SToken 换取 LToken
（便签接口要求 LToken）。

## `--game-biz` 选择规则

- 取值是**角色接口**返回的业务标识，例如 `hk4e_cn`（原神）、`nap_cn`（绝区零）；
- 精确匹配、大小写敏感：`HK4E_CN` 不会匹配 `hk4e_cn`；
- 两端空白会被去除；显式传入空串或纯空白直接报 `INPUT_INVALID`，不发任何请求；
- 不接受论坛的 GID（如 `2`）或 `en_name`（如 `ys`）作为别名；
- 无匹配角色时返回正常空列表（`ok:true`），这与接口失败不同；
- `role list` 与 `role note` 使用同一选择规则。

## 便签支持范围

- 已验证：原神 `hk4e_cn`、绝区零 `nap_cn`；
- 其他游戏（星铁、崩坏3 等）如实标注"暂不支持"，不猜测端点；
- 过滤后没有任何已验证角色时，不会为了展示"暂不支持"而换取 LToken。

## `role note` 的状态与部分失败

每个角色条目都有机器可判断的 `status`：

| status | 含义 |
|---|---|
| `available` | 查询成功 |
| `unsupported` | 该游戏的便签端点未验证（不是远端故障） |
| `failed` | 已验证游戏的查询失败 |

- 全部可查询角色成功、其余仅为 `unsupported`：`ok:true`、退出码 0；
- 任一已支持角色查询失败：继续处理其余角色，最终 `ok:false`、退出码 5、
  `error.code=PARTIAL_FAILURE`；成功/暂不支持/失败的角色状态完整保留在
  `error.partial_data`（与正常 `data` 同形状），`error.context` 只含
  `available_count`/`unsupported_count`/`failed_count` 三个计数；
- 换取 LToken 失败按原始认证/远端错误分类（如 `AUTH_INVALID`），不会伪装
  成"暂不支持"。

## JSON 示例（role note --json，全部成功）

```json
{
  "schema_version": 1,
  "ok": true,
  "data": {
    "kind": "list",
    "context": {"game_biz": "hk4e_cn"},
    "items": [
      {
        "game_biz": "hk4e_cn", "region": "cn_gf01", "uid": "770000001",
        "status": "available", "supported": true, "kind": "genshin",
        "summary": ["Resin 200/200 (next recovery in 0 s)", "..."],
        "raw": {"current_resin": 200},
        "reason": "", "error_code": null
      }
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

未指定 `--game-biz` 时 `context` 为 `{}`，未验证游戏的出现为
`"status":"unsupported"`、`"supported":false`、`"summary":[]`、
`"raw":null`。

## 常见问题

### 提示 Not logged in

先运行 `mys-cli auth login` 扫码登录。凭据目录与 SToken 数据格式保持不变，
升级后无需重新扫码。

### 为什么别的游戏显示暂不支持

便签端点只在实测验证后开放（目前原神与绝区零）。未验证的游戏不会猜测
端点，也不会被误报为查询失败。

[返回使用指南](../README.md)
