# CodexAssistant 公开预览发布清单

## 源码与安全

- 工作区变更已经审阅，版本统一为 `0.10.0-preview.1`。
- Go、Race Detector、Swift、Windows、UI 规范与 239 项 Node 契约测试通过。
- `scripts/check-release-hygiene.sh`、秘密扫描、依赖许可检查与 SPDX SBOM 生成通过。
- README、MIT License、贡献指南、安全政策、隐私说明、支持范围、行为准则和第三方声明一致。
- 公开源码由 `scripts/export-public-source.sh` 导出；快照不包含 `.git`、私有 remote、refs、reflog、对象库或历史提交。

## 构建与产物

- macOS Universal App 同时包含 arm64/x86_64；Windows x64/arm64 安装包均已生成。
- 四个平台包都携带匹配架构的 Go 飞书桥和固定 `lark-cli 1.0.92`，普通用户安装后不下载运行组件。
- 产物包含 SHA-256、来源清单、第三方声明和 SPDX 2.3 SBOM。
- 未签名或未公证的预览包明确标注 `preview`、系统拦截风险和校验方法，禁止描述为受信任发行版。

## 真实硬件门槛

- 当前 macOS arm64：安装、启动、无 KSF 首启、飞书向导、收发、队列恢复、退出和升级通过。
- macOS x64、Windows x64、Windows arm64：分别完成同一验收。
- macOS arm64 已按确认的先行例外使用 Go 生产入口；Node 仅允许显式人工回退，不自动运行，也不得与 Go 同时消费真实事件。
- Windows 在对应实机门槛完成前保持 Node 默认。四平台全部完成后删除 Node 专用运行时、npm 生产依赖、后台安装脚本和 Node 服务源码。

## 发布授权

本里程碑不创建公开远端、不推送、不发布。未来只有在用户明确确认托管平台与发布动作后，才可从干净导出目录初始化单一首提交、配置公开 remote 和上传资产。
