# 协议证据清单

状态：可更新清单

观察日期：2026-09-07

## 1. 用途

本清单固定架构设计所依据的上游快照，并把“接口证据等级”与“实现可发布状态”分开。它不包含真实 Token、Cookie、上传签名、完整 HAR 或其他秘密。

本文只证明设计审阅时使用了哪些材料。目标实现仍须在自己的仓库中保存脱敏 fixture、契约测试结果和适配器状态；仅更新本清单不能让写接口自动变成 `adapter_ready`。

## 2. 固定上游快照

- 仓库：[NRD-Tech/Reverse_Project](https://cnb.cool/NRD-Tech/Reverse_Project/-/tree/mihoyo-api)
- 分支名：`mihoyo-api`
- 固定 commit：[`7afa9b30602cea79ce32b3ff70f7c8b352b3d825`](https://cnb.cool/NRD-Tech/Reverse_Project/-/commit/7afa9b30602cea79ce32b3ff70f7c8b352b3d825)
- 固定原则：评审应按 commit 和 blob 校验，不能只依赖可变分支页面。

| 上游路径 | Git blob | 用途 |
|---|---|---|
| `mihoyo_bbs/docs/api/米游社接口清单_DS闭环验证.md` | `a643140361a4c6b07f07f64edac9f4e8aa4b3769` | DS、角色、帖子、草稿、图片等接口复现摘要 |
| `mihoyo_bbs/docs/api/视频上传与发布.md` | `ae3027cf9c3df9759b5c0c5743cf8e248cd60352` | App 视频上传与发布链路 |
| `mihoyo_bbs/docs/api/扫码登录与收藏夹_旧版服务整理.md` | `bf69d1d9fc8fdbcf4ee9fbba00dd76fdee2be1eb` | 二维码、登录限制和收藏夹历史服务 |
| `mihoyo_bbs/docs/api/接口面矩阵_全量.md` | `118d830dadbf6c6c6ef288152c7bd555fbf32560` | 路径盘点；部分方法为启发式推断 |
| `mihoyo_bbs/tools/qr_login.py` | `a51759247fbe6d9baebcc5505eb0737649dbda64` | HK4E 扫码和交换尝试的参考实现 |
| `mihoyo_bbs/tools/mys_ds_gen.py` | `7aaee2d3ba97b327c790511346635d7b5b97d69a` | DS 参考和测试向量 |

## 3. 证据等级

- **V（Verified）**：上游至少成功复现过目标端点的一种场景，方法、路径和该场景主要参数可信。
- **O（Observed）**：真实 App 流程中观察到请求，但仍缺独立复现或关键字段样本。
- **P（Path only）**：只有路径或矩阵记录，方法/body 仍可能是推断。

等级不代表适配器可发布。实现成熟度另用以下状态：

```text
documented → fixture_missing → fixture_ready → contract_ready → adapter_ready
```

- `fixture_ready` 要求成功与安全错误响应都已脱敏并通过秘密扫描。
- `contract_ready` 要求测试锁定方法、参数位置、必要 headers、响应校验和错误分类。
- `adapter_ready` 还要求领域门禁、dry-run、未知结果和恢复策略通过验收。

## 4. 能力证据矩阵

| 能力 | 设计证据 | 当前成熟度 | 未满足门禁 |
|---|---:|---|---|
| HK4E 二维码 fetch/query | V | `fixture_missing` | 目标实现仓库缺脱敏成功/过期/取消 fixture |
| Game Token → SToken | O | `fixture_missing` | 缺成功交换 fixture；失败时不得保存伪 SToken |
| SToken 角色列表 | V | `fixture_missing` | 缺脱敏 query/response fixture 与失效 Token 样本 |
| 用户帖子列表 | V | `fixture_missing` | query 参数组合仍需固定 |
| 帖子详情 | V | `fixture_missing` | 需固定图文/长文/视频响应变体 |
| 草稿列表/详情/删除 | V | `fixture_missing` | 需成功和权限/不存在错误样本 |
| 草稿保存/发布 | V（部分场景） | `fixture_missing` | 新建 `draft_id` 表示、发布后生命周期和完整 body 未固定 |
| 图文/长文发布 | V（部分 body） | `fixture_missing` | 两种 kind、draft-id 变体和审核结果 fixture 未齐 |
| 帖子删除 | V | `fixture_missing` | 需作者/非作者和视频帖提示行为测试 |
| 帖子编辑 | P | `documented` | 方法/body、revision/覆盖语义未确认 |
| 收藏列表 | V | `fixture_missing` | 角色、region 和游标样本需固定 |
| 收藏/取消收藏 | P | `documented` | 双向 body、幂等状态和 collection 语义未确认 |
| 图片上传参数与 OSS 上传 | V | `fixture_missing` | 上传参数、host 白名单、multipart 和结果未知样本未固化 |
| 视频权限/配额/Token/登记/封面 | O | `fixture_missing` | 米游社侧关键字段及错误响应需脱敏 fixture |
| 火山 VOD 分片上传 | O | `documented` | 第三方临时凭据映射、分片协议、checkpoint 和小视频 spike 未完成 |

因此，当前文档可以批准架构方向，但不能据此声称任一真实写适配器已经可用。

## 5. 外部参考

以下页面是辅助资料，不替代固定上游快照或实现 fixture：

- [UIGF Game Token 扫码](https://uigf.org/zh/mihoyo-api-collection/hoyolab/login/qrcode_hk4e.html)
- [UIGF Token 交换](https://uigf.org/zh/mihoyo-api-collection/hoyolab/user/token.html)
- [UIGF 鉴权与 Token 区别](https://uigf.org/zh/mihoyo-api-collection/other/authentication.html)
- [UIGF 游戏账号信息](https://uigf.org/zh/mihoyo-api-collection/hoyolab/user/game_account_info.html)
- [UIGF 米游社论坛文章](https://uigf.org/zh/mihoyo-api-collection/hoyolab/article/article.html)
- [火山引擎 Go VOD SDK 集成](https://www.volcengine.com/docs/4/65654)
- [火山引擎 Go SDK](https://github.com/volcengine/volc-sdk-golang)

外部页面会变化。若其内容影响方法、签名、Token 语义或安全边界，必须记录新的观察日期，并在目标实现仓库中更新 fixture/contract test；不能只修改文字结论。

## 6. 更新流程

1. 记录新的上游 commit 和相关 blob，不覆盖旧值而不说明原因。
2. 标明新增证据是 V、O 还是 P，并列出仍缺的请求/响应场景。
3. fixture 在目标实现仓库完成字段级脱敏和秘密扫描。
4. 契约测试通过后才能把成熟度提升为 `contract_ready`。
5. 领域安全门禁和恢复测试通过后，架构评审才可批准 `adapter_ready`。
6. 任何真实账号验证都由用户显式发起；设计仓库不保存账号数据或抓包原文。
