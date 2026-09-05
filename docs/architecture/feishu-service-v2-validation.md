# 飞书统一服务 v2：工程验收记录

日期：2026-09-05。范围：本轮 Core 业务集成、飞书统一服务和 CLI 客户端改造，不含生产切换。

## 结论

Go 全量、八个相关包 Race、vet、Swift XCTest、Windows Node、冻结飞书 Node 回放、四目标 Go 编译和 macOS universal2 包构建通过。额外执行旧 Swift 兜底时曾发现两处既有失败；用户随后确认只支持新测试，旧兜底已从运行脚本退役，不再作为当前验收阻塞项。这不是修复或通过旧测试，历史失败记录保留。Windows 实机与真实业务迁移仍待验收。

## 运行环境和工程结果

本机 macOS arm64；Go 1.25.14、Apple Swift 5.10、Node 22.22.1。Go 验证与构建设置 `GOPROXY=off GOSUMDB=off`，固定 lark-cli 使用已校验的本地缓存。

| 检查 | 结果 | 本机原始日志 |
| --- | --- | --- |
| `cd Core && go test ./...` | 通过，包含最终 CLI 确认信息透传测试 | `/tmp/ksfassistant-go-delivery-test.log` |
| 八个相关包 `go test -race` | 全通过，无数据竞争报告 | `/tmp/ksfassistant-race-final-test.log` |
| `cd Core && go vet ./...` | 通过 | `/tmp/ksfassistant-vet-final.log` |
| `bash scripts/run-tests.sh` 正常 SDK 路径 | 通过，含身份迁移隔离测试、原生管道和退出测试 | `/tmp/ksfassistant-full-refactor-acceptance.log` |
| Swift XCTest | 43 个通过 | 同上 |
| Windows Node 测试 | 63 个通过 | 同上 |
| 冻结飞书 Node 回放 | 284 个通过；不是第二套生产服务 | 同上 |
| `bash scripts/build-core.sh` | macOS arm64/x64、Windows x64/arm64 的 Core 和飞书二进制均编译成功 | `/tmp/ksfassistant-cross-final-build.log` |
| `bash scripts/build-app.sh` | universal2、包内资源校验和 adhoc 签名验证通过；SBOM 含 301 个包 | `/tmp/ksfassistant-package-final-build.log` |
| 额外执行旧 Swift 直接编译兜底（历史，已退役） | 两处既有失败，不再支持；详见下一节 | `/tmp/ksfassistant-swift-fallback-final.log`、`/tmp/ksfassistant-swift-fallback-remaining.log` |

Race 包：`internal/feishu`、`internal/integration`、`internal/privateipc`、`internal/localipc`、`internal/service`、`internal/feishucli`、`internal/feishucommands`、`cmd/ksf-assistant-feishu-bridge`。

构建产物为 `dist/KSFAssistant.app`，仅构建和验签，未安装或启动。`dist` 中已有的旧名构建缓存未删除，不代表被装入新应用包。临时日志不是长期保证保存的构建制品，可按构建说明重新运行。

## Swift 兜底退役与历史问题

用户确认后，`scripts/run-tests.sh` 只运行 Swift XCTest 和现行专项测试，缺少 SDK PlatformPath 时立即报错；`test-identity-migration.sh --with-view-model` 只额外检查当前宿主源码类型，不再编译旧 UsageViewModel 测试。旧测试源文件暂留作历史参考，不修改、不运行、不继续维护。

退役后的统一入口已重新完整执行通过：Go 全量、Swift 43 项、Windows Node 63 项、冻结飞书 Node 284 项、身份迁移/管道/退出专项测试和 universal2 构建；日志为 `/tmp/ksfassistant-current-tests-only.log`。`--with-view-model` 的隔离迁移测试及当前宿主类型检查也通过，日志为 `/tmp/ksfassistant-identity-current-host.log`。另用假 `xcrun` 验证缺失 SDK 时返回状态 1 和明确错误，不进入旧兜底。脚本语法与差异空白检查通过。

退役前，正常入口在本机走 SDK 路径，不执行旧兜底；额外逐段执行旧分支曾发现：

1. `Tests/Standalone/main.swift:299` 断言 Token 历史位于 `runtime-data/ksf-assistant`，而 `Sources/KSFAssistantCore/LocalTokenHistoryStore.swift` 保留外部兼容目录 `runtime-data/codex-usage-bar`。模型测试因此失败；本轮不修改 KSF 外部路径协议。
2. `Tests/UsageViewModelStandalone/main.swift:46` 调用已不存在的 `UsageViewModel.applyTaskActivityUpdate`，直接编译失败。

上述四个文件分别与实施前源码归档逐一核对 SHA-256，完全相同，证明不是本轮改造新增。项目工作台 6 项、任务打开、状态图标和 UI 布局的直接编译测试通过；后两项日志为 `/tmp/ksfassistant-swift-fallback-ui.log`。不把模型测试失败后未执行的断言推断为通过。

## 行为回归证据

| 风险 | 正式测试入口 |
| --- | --- |
| 两个文档父任务、四个 SDK 消息父任务占满容量；禁止下级工作箱嵌套等待 | `Core/internal/feishu/work_recovery_review_test.go`：`TestReviewNestedSchedulerCapacity`、`TestReviewFourSDKParentsExhaustScheduler` |
| 入队后撤权、dry-run 切换、伪造 OperationID、跨 claim 中断 | 同文件的 `TestReviewQueuedChildChecksExecutionBoundary`、`TestReviewDocboxRejectsFictitiousOperation`、`TestReviewCrashBetweenQueueClaimAndOperationClaim`；以及 `direct_execution_test.go` |
| 原始迁移备份、完成标记、写入失败不放行 | `Core/internal/feishu/direct_execution_test.go` 的 v3/v4 迁移与备份失败测试 |
| 丢 ACK、重复卡片交付、恢复与最新目标授权 | `Core/internal/feishu/inbound_processor_delivery_test.go` |
| Core 持久接收后 ACK、稳定 ID 去重、同会话顺序和有界并发、中断后不重放 running | `Core/internal/integration/event_inbox_test.go`、`health_test.go` |
| 释放/过期链接、旧卡片、未知字段、输入选择和任务观察 | `Core/internal/integration/runtime_ports_test.go`、`task_links_test.go`、`inbound_runtime_test.go` |
| 实际 SDK 响应校验旧卡片所有权、拒绝空 verifier、应用身份变化重新验证 | `Core/internal/feishu/service_transport_restore_test.go` |
| 共享握手 DTO、错误协议、自动/手动重启、低 revision、旧连接迟到 | `Core/internal/service/bridge_epoch_test.go`、`Core/internal/feishu/supervisor_epoch_test.go` |
| 对端不读、处理器/请求有界、取消和 EOF | `Core/internal/privateipc/peer_lifecycle_test.go`、`connection_test.go` |
| CLI 离线静态读取、服务停止无副作用、原 JSON 与结果 ID、30 MiB 双跳 | `Core/internal/feishucli/request_test.go`、`upload_test.go`、`upload_process_test.go`、`Core/internal/feishucommands/*_test.go` |
| 首次授权 challenge 保留、非零退出、不自动重放 | `Core/internal/feishucli/upload_authorization_test.go` |
| Core 业务存储损坏时飞书通用能力仍可用；业务健康降级 | `Core/internal/service/integration_gateway_test.go` |
| 防止飞书重新依赖 Codex/Desktop/KSF；禁止 Core 直接打开飞书配置/审计 | `Core/internal/service/architecture_boundary_test.go` |

测试只使用隔离目录、测试子进程、假 CLI 和 SDK/消息传输，不使用真实身份、真实消息、生产凭据或队列。

## 保留的兼容入口

- `codex-feishu-task-link-v1` response v2、旧卡片动作与链接 ID：外部调用方和存量卡片兼容，解释权在 Core。
- `corebridge` 控制 DTO：仅 Core 内部使用；旧跨进程 Codex 方法已移除，不自动降级协议。
- `workbox-v3` 目录、V3 类型别名和旧 schema 备份：原位迁移和可核对回退；实际工作项 schema 为 4。
- `control-inbox` 相关类型与结果查询：仅退役、保留历史结果、一次性拒绝遗留请求；不能再提交或执行，不启动旧 HTTP 唤醒服务。
- 共享数据根、KSF 外部协议及冻结 Node 回放：保留必要兼容，不构成新旧双消费者。KSF Skill 未修改。
- 本验收记录明确引用既有 Token 缓存的旧目录名作为失败证据，已加入产品身份检查的文档兼容白名单，不拆分字符串规避残留检查。

## 尚需实机验收

- Windows 当前用户 Named Pipe ACL、双实例竞争、实际进程树退出和更新安装后的路径行为。
- 真实机器上旧数据升级、故障中断和恢复；核对结果未知动作后再决定重试或降级。
- 真实飞书权限下的消息读写、附件、旧卡片恢复，以及真实 Codex Plan/输入选择与不可用时的基础能力。
- 旧 Swift 兜底已退出支持范围，无须修复两处历史失败；KSF 外部 Token 路径及业务接口不因此改变。

保留用户原有未提交修改。实施前源码归档、哈希及差异位于 `/var/folders/gz/pp1mj3w56k13d73j91v0zd2c0000gn/T/ksfassistant-service-baseline-qy0sp1gu`。未暂存、提交、安装、发布或迁移生产数据；运行数据回退约束见 [迁移说明](feishu-service-v2-migration.md)。
