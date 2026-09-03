# 团队内部发布清单

## 发布前

- 工作区干净，Go、Swift、Node 完整测试，universal2 构建及 Windows x64/arm64 构建退出码均为 0。
- `lipo -info` 同时包含 `arm64` 与 `x86_64`。
- Windows x64 与 arm64 NSIS 安装包均生成，并在对应实机验证托盘、动态高度、Codex 深链、目录选择与卸载。
- `scripts/check-release-hygiene.sh` 未发现用户名、个人绝对路径、私有证书名、凭据或运行数据。
- Apple Silicon 与 Intel 实机均完成首次安装和核心功能烟测；缺少 Intel 实测时只能发布 internal 预览版。
- README、第三方声明、MIT License、版本号和兼容表一致。

## 产物

运行 `scripts/package-release.sh`，核对 ZIP、源码归档、`SHA256SUMS` 和 `RELEASE.txt`。共享 ZIP 使用 ad-hoc 签名且未公证，禁止描述为 Apple 可信发行版。

在 `Windows` 目录运行 `npm ci && npm run dist:win`，核对两个架构的安装包。未配置 Windows 代码签名时必须明确标注“内部未签名预览”，不得描述为受信任发行版。

## GitLab

- 推送 `main` 后创建带说明的 `v0.8.0-internal.1` 标签。
- GitLab Release 附上 universal ZIP、源码归档、校验文件和未公证声明。
- 发布后在一台全新用户环境复核下载、校验、解压和右键打开路径。
