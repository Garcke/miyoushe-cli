# miyoushe-cli

米游社社区 CLI 的架构与协议设计仓库。

当前仓库只保存设计文档，不包含可执行代码。目标实现采用 Go + Cobra，覆盖扫码登录、角色查看、帖子与草稿管理、收藏，以及图文、长文和视频发布。

## 设计文档

- [总体架构](docs/architecture/README.md)
- [扫码登录与凭据保存](docs/architecture/0002-qr-auth-and-credential-storage.md)
- [社区功能与内容发布](docs/architecture/0003-community-features.md)

## 当前约束

- 首版只支持一个默认社区账号。
- Game Token 必须成功交换为 SToken；不设计只读回退。
- 未取得完整脱敏请求样本与契约测试的写接口不会开放。
- 视频上传必须先完成火山 VOD 临时凭据协议验证。
- Token、Cookie、DS、上传签名和临时密钥不得进入仓库、日志或测试 fixture。

## 状态

项目处于架构设计阶段。设计成熟度和实施顺序以[总体架构](docs/architecture/README.md)中的门禁为准。
