# 开发者构建说明

本页只面向源码贡献者，不是普通用户安装步骤。

## 依赖

- Go 1.23+
- macOS 构建：Swift 5.8+；正式测试还要求工具链提供支持 `swift test` 的 macOS SDK PlatformPath
- SBOM、Windows 宿主及冻结 Node 回放基线：Node.js 20+ 与 npm（仅构建/测试期）
- 仅使用 KSF 开发集成时：Ruby 3.2+

macOS 与 Windows 安装包都会携带运行所需的 Go 飞书服务；服务直接链接飞书官方 Go SDK，并以产品自有 OpenAPI 白名单完成任务消息与卡片操作。安装包不再携带 lark-cli、飞书 Skills、独立 Node runtime、飞书 npm 生产依赖或 Node 服务源码。Windows 的 Electron 宿主仍使用其内建 Node，但飞书业务不会在 renderer 或 JavaScript 主进程中执行。

## 验证

`0.11.0-preview.25` 的发布边界、兼容入口和实机门槛见 [本机预览改造](architecture/preview-0.11.md)。本机预览使用 `bash scripts/package-preview.sh --mac` 冻结当前工作树；不要为了运行公共发布脚本而暂存、提交或用 HEAD 冒充新源码。产品版本规则见 [版本管理](architecture/versioning.md)。

统一入口为 `bash scripts/run-tests.sh`。Swift 测试只支持 Swift Package Manager / XCTest，不再支持缺少 SDK PlatformPath 时的旧 standalone 兜底分支；工具链不满足要求时立即报错，不切换旧测试。身份迁移、Core 管道和 AppKit 退出链路的独立隔离测试仍是正式测试，继续保留。应用包本身的构建方式不因测试入口收敛而改变。

产品身份与迁移边界见 [产品身份与升级](architecture/product-identity-migration.md)。改名回归先运行 `node scripts/check-product-identity.mjs` 与 `bash scripts/test-identity-migration.sh`。

```bash
cd Core && go test ./... && go vet ./...
go test -race ./internal/feishu ./internal/integration ./internal/privateipc ./internal/localipc ./internal/service ./internal/feishucli ./internal/feishucommands ./cmd/ksf-assistant-feishu-bridge -timeout 180s
cd .. && swift test
npm --prefix Windows test
scripts/build-core.sh
scripts/check-release-hygiene.sh
```

macOS 通用包：

```bash
SIGNING_MODE=adhoc scripts/build-app.sh
```

Windows 安装包在 Windows 主机的 `Windows` 目录运行：

```powershell
npm ci
npm run dist:win
```

预览包可以未签名/未公证，但发布说明必须明确系统拦截风险并提供 SHA-256。真实硬件验收结果不可用交叉编译替代。

飞书统一服务的边界、运行时迁移与回退见 [v2 迁移说明](architecture/feishu-service-v2-migration.md)，工程结果及旧 Swift 兜底退役说明见 [验收记录](architecture/feishu-service-v2-validation.md)。测试使用隔离目录和假传输；不要用真实凭据、生产队列或真实消息替代测试夹具。
