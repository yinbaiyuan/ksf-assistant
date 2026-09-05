# 贡献指南

感谢你改进 KSFAssistant。提交变更即表示你有权按本项目 MIT License 提供相关内容。

## 开始前

- 行为与沟通遵守 [行为准则](CODE_OF_CONDUCT.md)。
- 安全问题不要创建公开 Issue，请按 [安全政策](SECURITY.md) 私下报告。
- 先搜索已有 Issue。较大的产品、协议或架构变化应先提出设计讨论。
- 不要提交凭据、真实飞书 ID、聊天内容、个人路径、私有仓库地址、签名材料或构建产物。

## 变更要求

- 业务与协议规则进入 Go Shared Core；SwiftUI/AppKit 和 Electron 只承担平台宿主与界面职责。
- KSF 必须保持可选。任何普通用户流程都不得要求命令行、运行服务或编辑配置文件。
- 飞书真实消费者任一时刻只能有一个。Node/Go 对照必须使用冻结样本、回放或假服务。
- 协议或行为变更先补测试。保持秘密不进入 renderer、JSON-RPC 响应、日志或命令行。
- 不降低 macOS arm64/x64、Windows x64/arm64 的目标矩阵。

构建、测试和打包命令见 [开发者构建说明](docs/CONTRIBUTING_BUILD.md)。提交前运行完整验证与 `scripts/check-release-hygiene.sh`，并说明已测试的平台以及尚未验证的真实硬件。

维护者可能要求拆分提交、补充回归测试、更新隐私说明或记录兼容/迁移策略。通过测试不等于自动获得发布资格。
