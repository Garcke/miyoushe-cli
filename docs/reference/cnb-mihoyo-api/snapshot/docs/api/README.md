# 米游社接口文档导航

> 项目:米游社 Android `com.mihoyo.hyperion` **v2.114.0** 逆向
> 环境:MuMu 模拟器 + frida-server 16.5.9;DS 生成器 [`tools/mys_ds_gen.py`](https://cnb.cool/NRD-Tech/Reverse_Project/-/blob/7afa9b30602cea79ce32b3ff70f7c8b352b3d825/mihoyo_bbs/tools/mys_ds_gen.py)(自测 ALL PASS)
> 更新:2026-08-30

## 文档清单(按用途)

| 文档 | 内容 | 什么时候看 |
|---|---|---|
| [接口面矩阵_全量.md](接口面矩阵_全量.md) | dex 提取 **336 条接口** × 14 域分类 × 验证状态;live 探测总账 | 找接口/查某个路径能不能调 |
| [米游社接口清单_DS闭环验证.md](米游社接口清单_DS闭环验证.md) | **38 个业务接口**的参数要点 + 请求头组合 + DS 算法结论 + 复现指引 | 要**调用**接口时 |
| [扫码登录与收藏夹_旧版服务整理.md](扫码登录与收藏夹_旧版服务整理.md) | 两种扫码流(hk4e GameToken 流 vs **hoyolab 完整登录态流**)、值传递链、失败原因实验、old_api 复活指南 | 需要**登录态**时 |
| [视频上传与发布.md](视频上传与发布.md) | 视频上传链(火山 VOD)+ structured_content 视频/图片混排块格式 + 审核流 | 要发**带视频的帖子**时 |
| [getFp_设备指纹.md](getFp_设备指纹.md) | `x-rpc-device_fp` 的获取接口(公开,无需鉴权) | 组装请求头时 |

## 核心结论速查(TL;DR)

1. **DS 算法两套**(与 UIGF 社区文档互相印证):
   - bbs 域(client_type=2):`md5("salt={K2盐}&t={t}&r={r}")`,r=6 位 [a-zA-Z0-9];**K2 盐按 App 版本轮换**(2.114.0 = `d64014da690671f8704695e993130f4c`)
   - passport 域:`md5("salt={盐}&t={t}&r={r}&b={body}&q=")`,r 含大写;盐 `JwYDpKvLj6MrMqqYU6jTKF17KNO2PXoS` **硬编码不轮换**
   - 生成器:`gen_ds()` / `gen_ds_passport(body)`
2. **请求头纪律**:bbs-api/takumi 网关要求**完整 15 项 `x-rpc-*` 头组合**,缺任何一个报 `-10001 invalid request`(与 DS 无关)——直接复用 `tools/probe_api_matrix.py` 的头集
3. **凭据分级**(决定接口可达性):
   - 匿名(仅 DS 头):社区首页聚合/信息流/讨论区/搜索等公开内容 ✅
   - web 登录态(`ltoken_v2`+`cookie_token_v2`,扫码 hoyolab 流可得):收藏夹/roles(LToken 路线)/用户信息等 ✅
   - App 会话(`stoken`,只能 App 内抓包):chat/任务/genAuthKey/exchange 等强登录接口 ❌ 扫码拿不到(服务端强制 `token_types=4`)
4. **凭据/账号事实**:hoyolab 扫码测试账号(`<QR_TEST_ACCOUNT>`)与 HAR 主测试账号(`<HAR_TEST_ACCOUNT>`)是两个账号;主账号 SToken 路线已完整复现游戏账号信息(原神+绝区零 2 角色)并跑通收藏夹翻页
5. **社区切换 = 换 `gids` 参数**(2026-08-30 权威映射,来源:App 抓包 `apihub/wapi/getGameList` 响应):
   `1`=崩坏3 · `2`=原神 · `3`=崩坏学园2 · `4`=未定事件簿 · `5`=大别野 · `6`=星穹铁道 · `8`=绝区零 · `9`=崩坏:因缘精灵(hna) · `10`=星布谷地(planet) ;`gids=7` 无效(rc=1)
   全部 10 社区已实测匿名可达(仅 DS 头):home/new + feeds/posts 均返回真实帖子
   适用接口:`apihub/api/home/new`(首页聚合)、`painter/api/feeds/posts`(信息流)、`forum/api/getDiscussionByGame`(版块)、`apihub/sapi/querySignInStatus`(签到,需登录)
   Web 版社区页另有 `wapi` 变体:`getOfficialRecommendedPosts`/`getHomeReception`/`getUniversalNavigators`/`getPCBanner`(www.miyoushe.com/hna 等)

## 相关文档(上级目录)

- [../DS_CAPTURE_RECORD.md](../DS_CAPTURE_RECORD.md) — DS 逆向全过程 + 盐值体系对照(UIGF 核对)
- [../UNPACK_REPORT_2.114.0.md](../UNPACK_REPORT_2.114.0.md) — 脱壳报告(未加壳,静态 14 dex 即全量)
- [HANDOFF_2026-08-30.md](https://cnb.cool/NRD-Tech/Reverse_Project/-/blob/7afa9b30602cea79ce32b3ff70f7c8b352b3d825/HANDOFF_2026-08-30.md) — 会话交接总文档(上游固定快照)

## 工具速查

| 工具 | 用途 |
|---|---|
| `tools/mys_ds_gen.py` | 双 DS 生成器(`verify` 自测 / `gen-passport` body 版) |
| `tools/build_api_matrix.py` | 从 dex 提取物重建接口矩阵(读 `probe_results.json` 回填状态) |
| `tools/probe_api_matrix.py` | live 只读探测(`--creds credentials_local_hoyolab.json` 或默认 HAR) |
| `tools/qr_login_hoyolab.py` | 扫码获取完整 web 登录态(推荐);`qr_login.py` 为 hk4e 游戏码流(仅游戏凭据) |
| `tools/qr_login.py::get_device_fp` | getFp 指纹获取(platform=2 安卓 32 字段) |

> ⚠️ 凭据文件(`credentials_local*.json`)与二维码图片均已 .gitignore,**严禁提交入库**;HAR 原始文件含账号 Cookie,只在本机使用。
