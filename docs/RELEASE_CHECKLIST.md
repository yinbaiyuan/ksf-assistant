# 团队内部发布清单

## 发布前

- 工作区干净，完整测试和 universal2 release 构建退出码为 0。
- `lipo -info` 同时包含 `arm64` 与 `x86_64`。
- `scripts/check-release-hygiene.sh` 未发现用户名、个人绝对路径、私有证书名、凭据或运行数据。
- Apple Silicon 与 Intel 实机均完成首次安装和核心功能烟测；缺少 Intel 实测时只能发布 internal 预览版。
- README、第三方声明、MIT License、版本号和兼容表一致。

## 产物

运行 `scripts/package-release.sh`，核对 ZIP、源码归档、`SHA256SUMS` 和 `RELEASE.txt`。共享 ZIP 使用 ad-hoc 签名且未公证，禁止描述为 Apple 可信发行版。

## GitLab

- 推送 `main` 后创建带说明的 `v0.4.0-internal.1` 标签。
- GitLab Release 附上 universal ZIP、源码归档、校验文件和未公证声明。
- 发布后在一台全新用户环境复核下载、校验、解压和右键打开路径。
