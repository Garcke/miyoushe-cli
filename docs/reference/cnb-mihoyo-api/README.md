# CNB `mihoyo-api` 参考快照

本目录保存架构设计所依据的米游社接口资料，来源于 CNB 仓库 `NRD-Tech/Reverse_Project` 的 `mihoyo-api` 分支。

## 来源

- 上游仓库：[NRD-Tech/Reverse_Project](https://cnb.cool/NRD-Tech/Reverse_Project/-/tree/mihoyo-api)
- 固定 commit：[`7afa9b30602cea79ce32b3ff70f7c8b352b3d825`](https://cnb.cool/NRD-Tech/Reverse_Project/-/commit/7afa9b30602cea79ce32b3ff70f7c8b352b3d825)
- 快照导入日期：2026-09-08
- 原始目录：`mihoyo_bbs/docs`
- 许可说明：固定快照中未发现独立 `LICENSE`/`NOTICE` 文件；本仓库保留来源标识，不据此宣称上游材料采用开源许可证。

该 commit 是当前架构设计已经审阅并固定的证据版本。本目录不声称与 CNB 可变分支的最新 HEAD 永远一致；更新时必须按下方流程重新审阅。

## 内容

- [API 文档索引](snapshot/docs/api/README.md)
- [设备指纹 getFp](snapshot/docs/api/getFp_设备指纹.md)
- [扫码登录与收藏夹](snapshot/docs/api/扫码登录与收藏夹_旧版服务整理.md)
- [全量接口矩阵](snapshot/docs/api/接口面矩阵_全量.md)
- [接口清单与 DS 闭环](snapshot/docs/api/米游社接口清单_DS闭环验证.md)
- [视频上传与发布](snapshot/docs/api/视频上传与发布.md)
- [DS 抓取记录](snapshot/docs/DS_CAPTURE_RECORD.md)
- [2.114.0 脱壳报告](snapshot/docs/UNPACK_REPORT_2.114.0.md)

## 导入处理

参考内容尽量保留原文，只做以下仓库安全与可读性处理：

1. 删除与 CLI 接口设计无直接关系、且包含个人电脑绝对路径的三份外围分析报告。
2. 将部分遮罩或完整出现的实测账号 UID，以及依赖该 UID 的 DS 向量，改成无身份信息的占位符。
3. 将指向未复制工具脚本或会话交接文档的相对链接改为固定 commit 的上游链接。
4. 不复制 HAR、凭据文件、二维码图片、Token、Cookie、上传签名或任何抓包原文。

因此本目录是“脱敏参考快照”，不是与上游 Git tree 字节完全一致的镜像。各原始文件 blob 与设计证据状态见[协议证据清单](../../architecture/evidence-manifest.md)。

## 使用规则

- `🧪rc=0`、V/O/P 等标记描述历史观察，不代表接口今天仍可用。
- 接口矩阵中的部分 HTTP 方法是启发式推断，真实抓包和脱敏 fixture 优先。
- 不根据路径名猜测写请求 body，不使用真实账号做自动化测试。
- App 版本、DS salt、`x-rpc-*` 头和 Token 能力都可能变化。
- 实现是否开放以架构文档的 `adapter_ready` 门禁为准，而不是以本快照中的单句结论为准。

## 更新流程

1. 使用经过授权且有效的 CNB 只读凭据获取 `mihoyo-api` 新 HEAD。
2. 记录新旧 commit，审阅 `mihoyo_bbs/docs` 的差异。
3. 只导入与接口、鉴权、DS、上传或协议来源直接相关的 Markdown。
4. 扫描并移除真实账号标识、绝对个人路径、Token、Cookie、HAR payload 和临时签名。
5. 修复本地相对链接，并保持外部工具链接固定到具体 commit。
6. 更新[协议证据清单](../../architecture/evidence-manifest.md)中的 commit、blob、观察日期和成熟度。
7. 通过独立读者检查后再提交；不得让文档同步自动提升适配器成熟度。
