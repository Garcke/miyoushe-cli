# 米游社 v2.114.0 脱壳报告(2026-08-30)

> 目标:`com.mihoyo.hyperion` v2.114.0(base.apk 128MB,MuMu 模拟器实机提取)
> 结论:**该 APK 未加壳。静态提取即完整脱壳结果,无需运行时脱壳。**

## 一、证据链

### 1. 静态检查——无壳特征

- APK 内含 **14 个标准 dex**(classes.dex ~ classes14.dex,共 88MB),魔数 `dex\n035` 全部正确。
- 全部 72 个 native so 中**无任何已知壳组件**(libjiagu / libDexHelper / libSecShell / libnesec / bangcle / ijiami 均无)。
  - `libsgmain*/libsgsecuritybody*`(阿里聚安全)与 `libmsaoaidsec*` 是**风控/反调试 SDK,不是 dex 壳**。
- AndroidManifest 无 stub/proxy Application 特征。
- jadx 冒烟测试:classes14.dex(2.5MB)成功反编译出 655 个 Java 文件,类体完整(非加密占位)。

### 2. 运行时交叉验证(FRIDA-DEXDump)

- 环境:frida-server 16.5.9(`/data/local/tmp/clr`,端口 37901),宿主 frida 16.5.9(**必须与 server 大版本一致**,17.x 会报 `unable to communicate`)。
- 对运行中主进程(pid 13739)全内存扫描,dump 出 **33 个 dex**。
- 比对结果:
  - **14/14 与 APK 内 dex MD5 字节级一致** → 内存中的 dex 未被解密/替换/篡改,即不存在壳。
  - 其余 19 个经字符串池解析确认全部为 **ART boot classpath 系统库**(core-oj 的 java/*、android/icu、conscrypt 的 com/android、apache harmony 等),**不含任何 `com/mihoyo`、`hyperion`、RN、字节跳动代码** → 无隐藏动态加载的业务 dex。

### 3. 注意事项(复现时)

- App 有风控,主进程可能自行死亡(`:pushcore` 还在 = 主进程已死),重新 force-stop + 启动即可。
- dump 前宿主/设备 frida 版本必须匹配:`python -m pip install frida==16.5.9`。
- 命令:`frida-dexdump -H 127.0.0.1:37901 -p <pid> -o <outdir>`(先 `adb forward tcp:37901 tcp:37901`)。

## 二、产物与存放

| 路径 | 内容 | 大小 | 入库 |
|---|---|---|---|
| `apk/mihoyobbs_2.114.0_base.apk` | 实机提取的原始 APK | 128MB | ❌ .gitignore |
| `dex/v2.114.0/classes*.dex` × 14 | 静态提取的完整 dex 集(即脱壳产物) | 88MB | ❌ .gitignore(体积过大,本地保留) |
| `dex_dump_2.114.0/classes*.dex` × 33 | 运行时内存 dump(14 交叉样本 + 19 系统) | 101MB | ❌ .gitignore |
| `dex/classes2.dex`、`dex/classes4.dex` | 旧版 2.113.1 产物(保留不动) | 11MB | ✅ 已在库 |

> 注:`dex/` 下原有的 classes2/4 与 v2.114.0 版本 MD5 不同(2.113.1 遗留),分析时以 `dex/v2.114.0/` 为准。

## 三、后续分析入口(按需)

- passport SDK(第三套 DS):`dex/v2.114.0/classes4.dex`
- DS 主算法 native 侧:`lib/arm64-v8a/libdddd.so`(so/ 下已有副本)
- 加密/风控:libMHYComboCrypto.so、libxxxx.so、libxxxxxx.so(见 HANDOFF.md 新算法章节)

## 四、复查(2026-08-30 第二轮,针对"是否存在风控壳"的质疑)

初轮方法(FRIDA-DEXDump 靠 dex magic 扫内存)存在两个盲区:**改魔数的内存 dex 会漏检**、**阿里聚安全动态插件未针对性排查**。本轮补齐:

| 检查项 | 方法 | 结果 |
|---|---|---|
| dex 完整性(防伪造) | 全 14 个 dex 的 adler32 checksum + SHA-1 signature 逐字节校验 | **14/14 全过** → 非加密/截断/伪装 dex |
| Application 真伪 | Manifest + 反编译 | `com.mihoyo.hyperion.app.HyperionApplication`(classes11),真实业务代码,无壳代理 stub |
| assets 动态载荷 | 全 APK 条目清点 | 无 jar/dex/zip 载荷(rncache 为 RN JS bundle,正常) |
| DexClassLoader 引用 | 全 dex 字符串+反编译 | 仅 2 处:`com.alibaba.wireless.security.framework`(聚安全插件加载器,运行时**无插件落地/加载**);`Jb.c`(仅注入 native 库目录,不加载 dex) |
| 热修复框架 | 反编译 `HyperionApplication` | 发现米哈游自家 `com.mihoyo.hotfix.runtime.patch.RuntimeDirector`(Booster 构建期插桩):**仅接口+PatchMetaInfo 共 22 行,无实现类、无 setter、无反射字符串**,全 dex 无 `RuntimeDirectorImpl`/`setRuntimeDirector` → 补丁桩未接线,处于惰性状态 |
| 运行时 classloader | frida `enumerateClassLoaders`(主进程) | 仅 4 个:Boot / 主 APK / "." / 系统 Trichrome,**无插件 loader** |
| 内存全量比对 | FRIDA-DEXDump 主进程 + `:pushcore` 进程各 33 个 | 均为 14 APK dex + 19 系统 boot dex;额外 dex 中 `com/alibaba`、`com/mihoyo`、`hotfix` **零命中** |
| 数据目录 | adb root find | 无任何落地 .jar/.dex/.odex/.vdex(除 code_cache) |

**结论(复查后维持):无 dex 壳。** 但"风控"确实存在,与"壳"是两回事:

- **风控(存在)**:阿里聚安全 `libsgmain*/sgmiddletier/sgsecuritybody`(5.5.x)+ `libmsaoaidsec` + 米哈游自研 `libxxxx/libxxxxxx`(内含 DS salt 解密与花指令,见 HANDOFF.md)。它们做**反调试/反模拟器/反 hook**(DS 工作期间进程被杀、spawn 被 detach 均为其实际行为),但**不加密 dex**。
- **壳(不存在)**:所有 14 个 dex 明文完整,静态提取即全量代码,任何动态加载通道(聚安全插件、热修复)在本构建中均未启用或未触发。

> 备注:第二轮最后一步(运行时直读 `m__m` 字段)因模拟器关闭未执行,但该字段无任何静态赋值路径 + 双进程内存无补丁 dex,结论不依赖此项。
