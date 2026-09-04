# 开发者构建说明

本页只面向源码贡献者，不是普通用户安装步骤。

## 依赖

- Go 1.23+
- macOS 构建：Swift 5.8+ Command Line Tools
- Windows 宿主：Node.js 20+ 与 npm
- 仅使用 KSF 开发集成时：Ruby 3.2+

普通用户安装包会携带运行所需的 Go 飞书桥和固定版 lark-cli。迁移期安装包仍携带 Windows 默认入口及人工回退所需的 Node 兼容运行时；macOS arm64 健康路径不启动 Node。

## 验证

```bash
cd Core && go test ./... && go test -race ./internal/feishu ./internal/service
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
