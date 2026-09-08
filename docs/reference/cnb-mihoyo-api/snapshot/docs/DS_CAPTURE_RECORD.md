# 米游社 2.113.1 DS 签名动态抓取记录（Frida）

> 目标: `com.mihoyo.hyperion` (2.113.1, miyousheluodi)
> 环境: MuMu 模拟器 (V2203A, x86_64, Magisk root), frida 17.17.0
> 抓取方式: hook `com.mihoyo.hyperion.net.bbbbb.a2222` (旧) / `net.aaaaa.a2222`/`b5555` (新)
> 时间: 2026-08-26

## 1. 已确认的算法结构

### 旧版 (libdddd.so, class `net.bbbbb`)
```
native a2222(String salt) -> String  // 单参数
返回格式: "{t},{r},{md5}"
md5 = MD5("salt={salt}&t={t}&r={r}")   // 已验证 100% 匹配
```

已验证样本（仅含公开旧 salt、时间戳、随机串和 MD5；不含账号/会话数据且早已失效，只用于算法校验，不能作为当前请求凭据）:
| salt | t | r | md5 | 验证 |
|---|---|---|---|---|
| 897878226392bd988a289cb7a589ee52 | 1787734195 | 51j16n | 80527a703d202151107366b8ba0f0abc | ✅ |
| 897878226392bd988a289cb7a589ee52 | 1787734199 | c14700 | 93bede568758a017d7551451e9cab655 | ✅ |
| 897878226392bd988a289cb7a589ee52 | 1787734218 | fh145k | a923f25ca9555f7edaa8e24bf73c2f0b | ✅ |

### 新版 (libxxxx.so / libxxxxxx.so, class `net.aaaaa`)
```
native a2222(String s1, String s2) -> String   // 双参数
native b5555(String s1, String s2) -> String   // 与 a2222 同实现 (lib 副本)
native a3333() -> void                          // 初始化
native b7777() -> void                          // 初始化(副本)
返回格式: "{t},{r},{md5}"  (同旧版结构)
```

已捕获的新版调用样本（仅 1 次触发成功）:
```
NEW b5555("", "uid=<REDACTED_UID>") => <REDACTED_VECTOR_1>
NEW b5555("", "uid=<REDACTED_UID>") => <REDACTED_VECTOR_2>
```
- 参数1 = 空串
- 参数2 = "uid=<REDACTED_UID>" (POST body/query 字符串;账号与对应 DS 向量已在 GitHub 快照中脱敏)
- salt **不经过 Java 层** → 在 native so 内 (RequestSalt 动态)

## 2. 关键发现: 多个 salt 并存

抓取中出现至少 2 个 salt:
| salt | 用途 |
|---|---|
| `897878226392bd988a289cb7a589ee52` | 旧算法 `bbbbb.a2222` 常态使用 (首页/详情等 GET) |
| `dd6d1560beaf2ed93d84ded0a5aabe70` | 另一种 salt，曾出现在 OLD a2222 调用中 |

说明 salt 不是单一固定值，可能存在:
- 按接口/域名分 salt
- 或动态下发 (RequestSalt TCP 网络层)

## 3. 新版算法触发点 (key)

之前 hook 到调用栈 (b5555 被调用时):
```
java.lang.Throwable
	at com.mihoyo.hyperion.net.aaaaa.b5555(Native Method)
	at ks.t.w(Unknown Source:71)
	at ks.t.t(Unknown Source:0)
	at ks.s.invoke(Unknown Source:6)     ← Retrofit 动态代理
	at ls.a.m / ls.a.n / ls.a.f / ls.a.j  ← 网络拦截器
	...
	at ou.d$a.d
```
→ 新版算法由 **Retrofit 动态代理 + 网络拦截器** 调用，用于部分 POST 请求。

## 4. 触发新版算法的业务

仅 1 次捕获到 NEW b5555, 发生在登录后某 POST 请求 (参数为 uid=...)。
登录前后行为差异明显:
- 未登录点击帖子 → 全部走 OLD bbbb.a2222
- 登录后 → 出现 NEW b5555 (POST 类请求)

待确认: 具体哪个接口触发新算法 (点赞/评论/收藏/转发/发帖?)。

## 5. lib 对应关系 (ELF 符号偏移确认)

| so | Java 类 | JNI 符号 | 偏移 (file) | 大小 | 角色 |
|---|---|---|---|---|---|
| libdddd.so | net.bbbbb | bbbbb.a2222 | 0x244b8 | 1900 | 旧版 DS |
| libdddd.so | net.bbbbb | bbbbb.a222 | 0x24000 | 1208 | 旧版辅助 |
| libxxxxxx.so | net.aaaaa | aaaaa.a2222 | 0x9d2e0 | 5396 | 新版 DS |
| libxxxxxx.so | net.aaaaa | aaaaa.a3333 | 0x9e7f4 | 344 | 新版初始化 |
| libxxxx.so | net.aaaaa | aaaaa.b5555 | 0x9d2e0 | 5396 | 新版 DS (副本) |
| libxxxx.so | net.aaaaa | aaaaa.b7777 | 0x9e7f4 | 344 | 新版初始化 (副本) |

注: libxxxx.so 与 libxxxxxx.so 是 same code 双份 (md5 不同, 函数偏移相同)。

## 6. 反风控措施 (已生效)

- frida-server 改名 `clr`, 端口 37901 (默认 27042 规避)
- Hook fopen/fgets 伪造 `/proc/self/status` 的 `TracerPid: 0`
- Hook java.io.File.exists 阻断 su/magisk/frida 路径
- Hook android.os.Debug.isDebuggerConnected → false
- 登录后 app 未被踢出 (hook 稳定工作)

## 7. 待办

- [ ] 精确找出触发 NEW b5555 的业务接口
- [ ] 反汇编 libxxxxxx.so 的 SaltDecrypt / RequestSalt 网络逻辑, 提取新版 salt
- [ ] 对比 web 版 DS 确认算法是否一致

---

# 2026-08-30 更新: v2.114.0 当前 salt 已闭环 ✅

> App 已升级到 **v2.114.0**,经运行时 hook 确认:常态业务流量**仍走旧版路径** `net.bbbbb.a2222(String salt)`,
> 算法结构不变,但 **salt 随版本轮换**。已完成 55 个真实抓包样本的离线闭环验证。

## 新确认事实

| 项 | 值 |
|---|---|
| 当前 salt (v2.114.0) | `d64014da690671f8704695e993130f4c` |
| 旧 salt (v2.113.1, 2026-08-26) | `897878226392bd988a289cb7a589cb7a589ee52`(已失效) |
| 算法 | `DS = md5("salt={s}&t={t}&r={r}")`,**不含 query/body** |
| r 格式 | 6 位 `[0-9a-z]`;同一秒多次调用结果相同(srand(time) 语义) |
| 稳定性 | 两个不同 App 进程(11:41 抓包 / 12:26 hook)使用同一 salt → **按版本固定,不按会话轮换** |

## 验证结果(5 个 HAR,55 去重样本)

- **53/55 完全复现**;38 个业务接口全部通过:发帖 `releasePost/v2`、删帖 `deletePost`、草稿 `draft/save|list|detail|delete`、
  `genAuthKey`、`getUserGameRolesByStoken`、搜索 `topic/api/searchTopic`、上传 `getUploadParams` 等。
- 例外 2 条(非接口特殊):`/apihub/api/v2/search`、`/painter/api/searchPosts` 各 1 条,样本来自 11:19 旧进程,salt 已轮换;
  且实测这两个接口**不校验 DS**,可直接调用。
- **`passport-api .../app/exchange` 已解决**(2026-08-30 当日追补):见下方"passport 独立 DS 算法"章节。

## passport 独立 DS 算法 ✅(2026-08-30 静态还原 + 实测 MATCH)

- 来源:`classes4.dex` → `com.mihoyo.platform.account.sdk.network.RequestUtils.createSign()`(jadx 反编译)。
- 算法:`DS = md5("salt={salt}&t={t}&r={r}&b={请求体JSON}&q={query}")`,头仍为 `{t},{r},{md5}`。
- t 仍为秒级(`Br.a.d = 1000`);**r 为 6 位 [a-zA-Z0-9](含大写)** —— 与抓包 `AOtZdq` 吻合。
- salt:`SALT_PROD = JwYDpKvLj6MrMqqYU6jTKF17KNO2PXoS`(env=PRODUCT/PRE/PTS/SANDBOX;DEV = `IZPgfb0dRPtBeLuFkdDznSZ6f4wWt6y2`),**硬编码于 SDK,不随 App 版本轮换**。
- `b` 必须是实际发送的请求体原文(Gson 序列化结果);`q` 在 2.114.0 恒为空串。
- 验证:HAR 中 2 个真实 exchange 样本,md5 全部 **MATCH**。生成器 `tools/mys_ds_gen.py::gen_ds_passport(body)`。

## 抓取方法(本次成功路径)

1. frida-server 部署为 `/data/local/tmp/clr`,`-l 0.0.0.0:37901` + adb forward tcp:37901。
2. **frida 16.5.9**(17.x 无内置 Java bridge;且 spawn 后 App 初始化在 MuMu 翻译层下 >100s,hook 窗口要留足)。
3. 伪装:TracerPid 伪造(fgets 过滤)、/proc/net/unix & maps 行过滤、ptrace→0、`System.exit/Runtime.exit/halt` 拦截(本次未触发,即未被检测)。
4. **可靠 recipe:spawn(仅伪装,不挂 DS 钩子)预热 → App 完全启动后按 pid 二次 attach 挂 `bbbbb.a2222` 钩子**(直接得到 salt 参数)。
5. 2.114.0 实际加载 `lib/arm64/libdddd.so`(dlopen 监控确认);挂 dlopen 钩子会加速暴露(第二次 spawn 被 detach,疑似此因)。

## 产物

- [`tools/mys_ds_gen.py`](https://cnb.cool/NRD-Tech/Reverse_Project/-/blob/7afa9b30602cea79ce32b3ff70f7c8b352b3d825/mihoyo_bbs/tools/mys_ds_gen.py) — 当前版本 DS 生成器(bbs + passport 双算法,自测 ALL PASS;上游固定快照)
- [`docs/api/米游社接口清单_DS闭环验证.md`](api/米游社接口清单_DS闭环验证.md) — 38 接口清单 + DS 适用性
- [`docs/api/getFp_设备指纹.md`](api/getFp_设备指纹.md) — 设备指纹接口(x-rpc-device_fp)

## 遗留

- [x] ~~`app/exchange` 的独立 DS 生成器定位(r 含大写)~~ ✅ 2026-08-30 已还原(classes4.dex RequestUtils,HAR 实测 MATCH)
- [ ] 静态 `gen_salt()` 仍产乱码(NEW_DS_REMAINING 遗留问题),当前已无必要——业务流量走 bbbbb 路径,salt 直接 hook 得到
- [ ] salt 轮换规律(每次版本更新?值从 dex/网络来?)可留待下次升级时复跑本文档方法(passport SDK 的 salt 为硬编码,不受影响)
## 盐值体系对照(UIGF 社区文档,2026-08-30 核对)

来源:[UIGF mihoyo-api-collection](https://uigf.org/zh/mihoyo-api-collection/) 鉴权页 + [盐值历史 issue](https://github.com/Kamisato-Ayaka-233/mihoyo-api-collect/issues/1)。与我们逆向还原的结论**完全一致**:

| 算法 | 公式 | 对应我们的还原 |
|---|---|---|
| DS1(client_type=2/4) | `md5("salt={salt}&t={t}&r={r}")`,r=6位[a-zA-Z0-9] | = 我们的"bbs DS"(libdddd 路径) |
| DS2(client_type=5) | `md5("salt={salt}&t={t}&r={r}&b={body}&q={query}")` | = 我们的"passport DS"变体(classes4 RequestUtils) |

盐值分类:
- **K2 salt**(client_type=2,DS1):**按 App 版本轮换**——与实测"2.113.1→2.114.0 salt 轮换"吻合;社区表最新到 2.52.1,尚未收录 2.114.0(我们 dump 的 `d64014da...` 是目前唯一公开来源)
- **LK2 salt**(client_type=4,DS1):同样按版本轮换
- **4X salt**(client_type=5,DS2):`xV8v4Qu54lUKrEYFZkJhB8cuOh9Asafs`,**版本无关、长期不变**
- 个别 API 有独立 salt(如米游社签到)

**实用结论**:
1. binding/getUserGameRolesByStoken 的官方要求 = `api-takumi.miyoushe.com` + GET + `client_type:2` + DS1(K2salt) + Cookie SToken——我们的请求构造与规范完全一致,失败原因确证为**凭据本身不是真 SToken**(游戏码 token 不通过"验证 Cookie SToken")
2. 游戏账号信息有三条获取路径:SToken(上文)/ LToken(`getUserGameRolesByCookie`,client_type=5+DS2+4Xsalt)/ ActionTicket(`getUserGameRoles?action_ticket=`,无需验证头)——后两者的前置 token 同样只能由真登录态签发
3. **DS2 + 4X salt 的接口不受版本轮换影响**(盐固定),bbs-api web 侧端点可长期使用
4. 下次版本升级时,可先查社区盐值表是否已更新 2.114.x,再决定是否重跑 Frida 配方
