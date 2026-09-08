# 米游社业务接口清单 + DS 闭环验证(2026-08-30)

> 数据来源:Reqable 抓包 5 个 HAR(搜索 / 图文发帖 / 删帖 / 草稿箱,App v2.114.0,MuMu 模拟器)
> 验证方式:用运行时 dump 的当前 salt,对抓包中 55 个去重 DS 样本**离线复算 MD5 完全复现**
> 结论:**当前版本 DS 可以生成**。算法 = 旧版 v1 结构,salt 随版本轮换。

---

## 1. 结论(TL;DR)

| 项 | 内容 |
|---|---|
| 当前版本 | 米游社 Android `com.mihoyo.hyperion` **v2.114.0** |
| DS 算法 | **`DS = md5("salt={salt}&t={t}&r={r}")`**(旧版 v1 结构,**不含 query/body**) |
| 当前 salt | **`d64014da690671f8704695e993130f4c`**(2.114.0;2.113.1 为 `897878226392bd988a289cb7a589cb7a589ee52`,**salt 随版本轮换**) |
| DS 头格式 | `{t},{r},{md5}`,t 为秒级时间戳,r 为 6 位 `[0-9a-z]` 随机串 |
| 验证结果 | 55 个去重样本复现 **53 个**;38 个业务接口全部通过(含发帖/删帖/草稿等写操作);2 个搜索接口**无需 DS 实测直连可用**;`app/exchange` 的独立生成器已静态还原并实测 MATCH → **全部 HAR 接口均可调用** |
| 生成器 | [`tools/mys_ds_gen.py`](https://cnb.cool/NRD-Tech/Reverse_Project/-/blob/7afa9b30602cea79ce32b3ff70f7c8b352b3d825/mihoyo_bbs/tools/mys_ds_gen.py)(`python mys_ds_gen.py verify` 自测;上游固定快照) |
| 相关文档 | 全量接口矩阵:[接口面矩阵_全量.md](接口面矩阵_全量.md) · 扫码登录/收藏夹:[扫码登录与收藏夹_旧版服务整理.md](扫码登录与收藏夹_旧版服务整理.md) · 设备指纹:[getFp_设备指纹.md](getFp_设备指纹.md) · 脱壳:[UNPACK_REPORT_2.114.0.md](../UNPACK_REPORT_2.114.0.md) |

关键行为特征(抓包实证):
- **同一秒内多个不同请求复用同一个 DS**(t、r、sig 完全相同)——因为 native 侧 `srand(time(NULL))` 后首次 `rand()` 结果相同;这也反证 DS 不含请求内容。
- GET / POST / 静态资源 CDN 请求全部带 DS 头,且同一 salt 通用。
- 头部搭配:`x-rpc-device_fp`(13 位十六进制,获取方式见 [getFp_设备指纹.md](getFp_设备指纹.md))、`x-rpc-device_id`、`x-rpc-app_version`、`x-rpc-verify_key` 等。

### 来源 HAR 可用性对照

| HAR 文件(Reqable 导出) | 抓包时间 | 业务接口可用性 | 说明 |
|---|---|---|---|
| `log-upload.mihoyo.com_删帖.har` | 11:42 | **27/27 全部可用 ✅** | 删帖全流程:getPostFull / getPostReplies / deletePost / genAuthKey / getUserGameRolesByStoken / batchGetRedDot / timeline / chat 等 |
| `log-upload.mihoyo.com0-图文发帖.har` | 11:41 | 24/24 可用 ✅ | 发帖全流程:releasePost/v2 / getUploadParams / OSS 直传 / searchTopic / 任务/通知/用户信息;`passport-api app/exchange` 走 passport DS(见 §3)✅ |
| `log-upload.mihoyo.com_草稿箱.har` | 11:48 | 21/21 可用 ✅ | draft/save / draft/list / draft/detail / draft/delete / getRecommendedTopicsForText / forumMain / remained_times;`passport-api app/exchange` 走 passport DS ✅ |
| `bbs-miyoushe-api-v2-search.har` | 11:20 | **直接可用 ✅** | `/apihub/api/v2/search` **无需 DS**(实测无 DS/无效 DS/错误 FP 均 `retcode 0`);抓包样本的 DS 与当前 salt 不匹配无影响 |
| `米游社搜索接口-searchPosts.har` | 11:19 | **直接可用 ✅** | `/painter/api/searchPosts` **无需 DS**(实测仅带 User-Agent 裸调用即返回完整结果) |

> 注:两个搜索 HAR 抓取于 11:19-11:20,其余三个在 11:41-11:48;11:41 之后 App 进程使用的 salt 即当前 dump 到的 `d64014da...`。搜索功能本身不受影响——`topic/api/searchTopic`(图文发帖.har)已验证可用。

## 2. 已验证接口清单(✅ = DS 可用当前 salt 复现)

### 帖子与草稿(bbs-api.miyoushe.com)

| 方法 | 接口 | 用途 | 抓包要点 |
|---|---|---|---|
| POST | `/post/api/releasePost/v2` | **发布图文帖** | body: `{block_reply_img, collection_id, content(JSON字符串:describe/imgs/link_card_ids), cover, draft_id, forum_cate_id, ...}`;成功返回 `post_id` |
| POST | `/post/api/deletePost` | **删帖** | body: `{"operate_type":0,"post_id":"..."}` |
| POST | `/post/api/draft/save` | 保存草稿 | body 含 `content(JSON字符串)`、`cover`、`draft_id`(新建传 0? 抓包为已有 id)、`subject` 等 |
| POST | `/post/api/draft/delete` | 删除草稿 | body: `{"draft_id":"..."}` |
| GET | `/post/api/draft/list` | 草稿箱列表 | query: `view_type=7&offset=&size=20` |
| GET | `/post/api/draft/detail` | 草稿详情 | query: `draft_id=...` |
| GET | `/post/api/getPostFull` | 帖子全文 | query: `post_id=...&csm_source=...` |
| GET | `/post/api/getPostReplies` | 帖子回复 | query: `post_id&order_type&size&only_master&last_id&is_hot&from_external_link&game_uid&region` |
| GET | `/post/api/getPostDelConfigList` | 删帖原因列表 | 无参数 |
| GET | `/post/api/getEasterEggAssets` | 彩蛋资源 | query: `post_id=...` |

### 搜索与话题

| 方法 | 接口 | 用途 | DS 要求 | 抓包/实测要点 |
|---|---|---|---|---|
| GET | `/apihub/api/v2/search` | 综合搜索(帖子/话题/用户/ wiki) | **无需 DS**,直接调用 | query: `keyword&preview&gids&search_from_gid`;实测无 DS/无效 DS/错误 device_fp 均 `retcode 0` |
| GET | `/painter/api/searchPosts` | 画友帖子搜索 | **无需 DS**,直接调用 | query: `keyword&last_id&gids&forum_id&size&order_type`;仅带 User-Agent 裸调用也返回完整列表 + 翻页 `token_list` |
| GET | `/topic/api/searchTopic` | 话题搜索 | 需 DS(✅ 已验证) | query: `keyword&last_id&size` |
| POST | `/topic/outerApi/getRecommendedTopicsForText` | 按正文推荐话题 | 需 DS(✅ 已验证) | body: `{content, game_id, ignore_topic_ids, limit, scene:"post", title}` |
| GET | `/apihub/api/forumMain` | 版区主页 | 需 DS(✅ 已验证) | query: `forum_id=...` |

### 用户 / 通知 / 任务 / 其它

| 方法 | 接口 | 用途 |
|---|---|---|
| GET | `/user/api/getUserFullInfo` | 用户完整信息(query: uid) |
| GET | `/user/api/replyPermission` | 回复权限(返回 max_image_number) |
| GET | `/user/api/recommendActive` | 推荐活动 |
| GET | `/user/api/notify/settings` | 通知设置 |
| GET | `/user/api/blockWord/check` | 屏蔽词检查(需等级 5+) |
| GET | `/user_instant/api/entity/review` · `/user_instant/api/sticky_post/id` | 动态/置顶 |
| GET | `/painter/api/timeline/list` · `/painter/api/user_instant/list` | 画友时间线 |
| GET | `/timeline/api/getUnreadInfo` · POST `/timeline/api/markAlreadyRead` | 时间线未读/已读 |
| GET | `/notification/api/v2/notificationHome` | 通知首页 |
| GET | `/apihub/api/unreadMessageCnt` · `/apihub/api/myselfPageConfig` · `/apihub/api/getShareConf` | 未读数/个人页配置/分享配置 |
| GET | `/apihub/sapi/getUserMissionsState` | 任务状态 |
| GET | `/chat/api/getChatList` · `/chat/api/getUserSettings` | 私聊 |
| GET | `/vila/api/getEmoticonInfo` | 小别墅表情 |
| GET | `/community_lottery/api/remained_times` | 社区抽奖剩余次数 |

### 上传(发图流程)

| 方法 | 接口 | 用途 | 要点 |
|---|---|---|---|
| GET | `/apihub/sapi/getUploadParams` | 获取 OSS 上传参数 | query: `md5(文件md5)&ext=png&support_content_type=1&upload_source=1`;返回 OSS `accessid/policy/signature/dir/host` |
| POST | `plat-sh-community-prod-upload-ugc.oss-cn-shanghai.aliyuncs.com` | 直传阿里云 OSS | 表单上传,与 DS 无关 |
| GET | `upload-bbs.miyoushe.com/upload/...` | 上传后的最终图片 URL | URL 由 getUploadParams 返回的 `file_name` 拼接 |

### 账号 / 游戏区服(takumi)

| 方法 | 接口 | 用途 | 要点 |
|---|---|---|---|
| POST | `api-takumi.miyoushe.com/account/auth/api/genAuthKey` | 生成 authkey | body: `{auth_appid, game_biz, game_uid, region}`;✅ DS 可复现;返回的 authkey 用于 game_record 等查询 |
| GET | `api-takumi.miyoushe.com/binding/api/getUserGameRolesByStoken` | 拉 SToken 绑定的游戏角色 | 需 Cookie(SToken);✅ DS 可复现 |
| GET | `api-takumi-record.mihoyo.com/game_record/card/api/getGameRecordCard` | 游戏战绩卡片 | query: uid;✅ |
| GET | `api-takumi.mihoyo.com/common/csc_qna/public/batchGetRedDot` | 红点 | query 含 authkey;✅ |

## 3. 第三套算法:passport 接口(account SDK)✅ 已还原

| 接口 | 算法 |
|---|---|
| `passport-api.mihoyo.com/account/ma-cn-session/app/exchange` 等 passport SDK 接口 | `DS = md5("salt={salt}&t={t}&r={r}&b={请求体JSON}&q=")`,头仍为 `{t},{r},{md5}`;t=秒级时间戳,**r 为 6 位 [a-zA-Z0-9](含大写)**;**b 为请求体原始 JSON 字符串**(必须与实际发送字节完全一致),q 在 2.114.0 恒为空 |

来源:`classes4.dex` → `com.mihoyo.platform.account.sdk.network.RequestUtils.createSign()`(2026-08-30 静态还原,jadx 反编译)。生产 salt `JwYDpKvLj6MrMqqYU6jTKF17KNO2PXoS` 为 `SALT_PROD` 硬编码,env ∈ {PRODUCT, PRE, PTS, SANDBOX} 时启用(另有 DEV salt `IZPgfb0dRPtBeLuFkdDznSZ6f4wWt6y2`);**该 salt 硬编码在 SDK 内,不随 App 版本轮换**。HAR 中 2 个真实 exchange 样本 md5 全部 MATCH。生成器:[`tools/mys_ds_gen.py`](https://cnb.cool/NRD-Tech/Reverse_Project/-/blob/7afa9b30602cea79ce32b3ff70f7c8b352b3d825/mihoyo_bbs/tools/mys_ds_gen.py) 的 `gen_ds_passport(body)`(上游固定快照)。

> 已排除的疑似例外(2026-08-30 实测):`/apihub/api/v2/search` 与 `/painter/api/searchPosts` 的抓包 DS 与当前 salt 不匹配,是因为样本来自旧 App 进程——**这两个接口本身不校验 DS**(无 DS / 无效 DS / 错误 device_fp 均返回 `retcode 0` 及完整结果),可直接调用。

## 4. 复现指引

```bash
# 生成一个当前可用的 DS 头
python tools/mys_ds_gen.py

# 生成 passport 接口 DS(body 必须与实际发送字节一致)
python tools/mys_ds_gen.py gen-passport '{"src_token":{"token":"..."}}'

# 自测(bbs 3 个抓包样本 + passport 合成样本闭环)
python tools/mys_ds_gen.py verify
```

请求头组合(2.114.0 抓包实测;注意是**下划线**命名):

```
DS: {t},{r},{md5}
x-rpc-device_fp: {13位指纹, 见 getFp 文档}
x-rpc-device_id: {UUID}
x-rpc-app_version: 2.114.0
x-rpc-client_type: 2                (Android)
x-rpc-sys_version: 12
x-rpc-channel: miyousheluodi
x-rpc-device_name: {机型} / x-rpc-device_model: {型号}
x-rpc-verify_key: bll8iq97cem8
User-Agent: okhttp/4.9.3
Cookie: {账号凭据(stuid/stoken 等), 按接口要求}
```

> 注:抓包 HAR 含账号 Cookie/SToken/authkey 等敏感凭据,本文档一律不落盘;需要时请直接查原始 HAR(勿提交入库)。

## 5. 抓取与验证过程记录

1. Reqable(MuMu 共享文件夹)导出 5 个 HAR → 解析出 55 个去重 DS 样本、36+ 个业务接口。
2. 离线尝试:已知旧 salt / 静态还原 salt / dex 字符串池 32 位 hex 候选 / v1 与 v2(b/q) 拼装格式 → 全部不匹配。
3. 运行时:Frida 16.5.9(spawn 门控 + TracerPid 伪造/maps 过滤/ptrace 拦截 + System.exit 拦截)attach `com.mihoyo.hyperion`,hook `net.bbbbb.a2222(String salt)` → 直接得到当前 salt `d64014da690671f8704695e993130f4c` 及实时 DS 输出。
4. 用该 salt 离线复算 55 样本 → 51 复现(38 业务接口全通过)。
5. 证据稳定性:11:41-11:48 抓包进程与 12:26 hook 进程为两个不同进程、同一 salt → salt 按版本固定,不按会话轮换。
