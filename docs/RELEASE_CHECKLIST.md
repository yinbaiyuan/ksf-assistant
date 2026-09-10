# KSFAssistant 公开预览发布清单

本页是未来发布门槛，不代表项目已通过。0.11 本机范围及尚未验收项见 [本机预览边界](architecture/preview-0.11.md)；本轮使用工作树快照，不提交、不公开发布。

## 源码与安全

- 工作区变更已经审阅，`version.json` 中的产品版本已按本次业务变化递增，并已运行 `node scripts/sync-versions.mjs`。
- Go、Race Detector、Swift、Windows、UI 规范与 284 项 Node 契约测试通过。
- `scripts/check-release-hygiene.sh`、秘密扫描、依赖许可检查与 SPDX SBOM 生成通过。
- README、MIT License、贡献指南、安全政策、隐私说明、支持范围、行为准则和第三方声明一致。
- 公开源码由 `scripts/export-public-source.sh` 导出；快照不包含 `.git`、私有 remote、refs、reflog、对象库或历史提交。

## 构建与产物

- macOS Universal App 同时包含 arm64/x86_64；Windows x64/arm64 安装包均已生成。
- 四个平台包都携带匹配架构的飞书服务、任务 CLI、工具链管理器和固定受管 `lark-cli 1.0.93-ksfassistant.1`；其上游源码与 Skills 固定为 `1.0.93`，普通用户安装后不下载运行组件。
- 产物包含 SHA-256、来源清单、第三方声明和 SPDX 2.3 SBOM。
- 未签名或未公证的预览包明确标注 `preview`、系统拦截风险和校验方法，禁止描述为受信任发行版。

## 真实硬件门槛

- 当前 macOS arm64：安装、启动、无 KSF 首启、飞书向导、收发、队列恢复、退出和升级通过。
- macOS x64、Windows x64、Windows arm64：分别完成同一验收。
- macOS 通用包只允许 Go 生产入口，且不得包含 Node runtime、npm 生产依赖或 Node 服务源码；失败时不得自动回退，也不得启动第二个真实事件消费者。
- Windows x64/arm64 只允许 Go 生产入口；在对应实机门槛完成前不得发布。冻结 Node 基线只用于离线回放，四平台全部完成后再删除。
- 飞书 Plan 单选在保留多 App Server 的实机环境连续完成至少三轮；每轮 Desktop 解除等待，卡片不自行报错、不恢复旧选项，审计只有一个脱敏终态。
- 101 项破坏性能力治理资产无冲突且只能逐项 `confirm_each`；V3 队列迁移、并发限制、留存边界、原始 payload 清理和子服务诊断环测试通过。

## 发布授权

本里程碑不创建公开远端、不推送、不发布。未来只有在用户明确确认托管平台与发布动作后，才可从干净导出目录初始化单一首提交、配置公开 remote 和上传资产。
