# miyoushe-cli

米游社社区 CLI 的架构与协议设计仓库。

当前仓库只保存设计文档，不包含可执行代码。目标实现采用 Go + Cobra，覆盖扫码登录、角色查看、帖子与草稿管理、收藏，以及图文、长文和视频发布。

## 设计文档

- [总体架构](docs/architecture/README.md)
- [扫码登录与凭据保存](docs/architecture/authentication.md)
- [社区功能与内容发布](docs/architecture/community-features.md)
- [协议证据清单](docs/architecture/evidence-manifest.md)

## 上游接口参考

- [CNB `mihoyo-api` 脱敏快照](docs/reference/cnb-mihoyo-api/README.md)

参考快照用于追溯接口发现、DS、扫码、收藏、帖子/草稿和视频链路，不表示所有接口已经达到可发布状态。

## 当前约束

- 首版只支持一个默认社区账号。
- Game Token 必须成功交换为 SToken；不设计只读回退。
- 未取得完整脱敏请求样本与契约测试的写接口不会开放。
- 视频上传必须先完成火山 VOD 临时凭据协议验证。
- Token、Cookie、上传签名、临时密钥，以及带有效会话/账号上下文的实时 DS 不得进入仓库、日志或测试 fixture。参考文档可以保留不含账号数据、已经失效且仅用于算法校验的 DS 向量。

## 状态

项目处于架构设计阶段。设计成熟度和实施顺序以[总体架构](docs/architecture/README.md)中的门禁为准。
