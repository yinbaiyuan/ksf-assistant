# 架构说明

```text
macOS SwiftUI/AppKit ─┐
                      ├─ private stdio JSON-RPC ─ codex-usage-core (Go)
Windows Electron ─────┘                              ├─ Codex App Server/Desktop IPC
                                                    ├─ optional KSF catalog adapter
                                                    └─ supervised codex-feishu-bridge (Go)
                                                         ├─ official Go SDK WebSocket
                                                         ├─ pinned lark-cli 1.0.92
                                                         └─ temporary Codex/lark-cli children
```

## 边界

- 平台宿主负责原生窗口、托盘/菜单栏、目录选择、登录项和安全外部链接。
- Shared Core 负责跨平台业务状态、Token、项目投影、RPC 和飞书桥 supervisor。
- 飞书桥保持独立进程，隔离网络、OAuth、队列与外部命令故障。它不能注册为系统常驻服务，生命周期唯一所有者是 CodexAssistant。
- KSF 是可选只读集成，不属于启动前置条件。
- renderer 只接收脱敏 DTO；秘密和真实飞书 ID 不跨越渲染边界。

## 飞书迁移

Go 入口维持 Bridge/Capability `1.0.0`、队列 schema `2` 与 `client.json` schema v4。23 类固定非 Approval 事件由 `oapi-sdk-go/v3.11.0` 接入，219 项固定能力继续委托随包 `lark-cli 1.0.92`。Node 与 Go 只能对同一冻结样本做离线回放，不得同时消费真实事件。

macOS arm64 已按用户确认的先行策略切换为 Go 生产入口，Node 只能通过显式人工回退启用，禁止自动故障回退。Windows 在实机等价验收完成前继续使用 Node；macOS x64 与 Windows 两种架构完成后，再移除安装包中的 Node runtime、npm 生产依赖和 Node 服务源码。

## 生命周期

macOS 的按钮退出只把一次退出请求交给 AppKit 运行循环；按钮与系统退出均由 `AppDelegate.applicationShouldTerminate` 统一等待 `UsageViewModel.shutdown`。不得在持有主队列的 Swift Task 内调用 `NSApplication.terminate`，否则 `terminateLater` 的嵌套模态循环会阻塞负责确认退出的 MainActor 任务。重复按钮请求合并为一次。Core 的 `shutdown` 在停止受管服务、写出确认后立即结束 RPC 读取循环，不依赖宿主再发送输入或关闭 stdin。回归入口为 `bash scripts/test-quit-lifecycle.sh` 及 Go RPC shutdown 测试。

Core 启动桥，桥通过父级控制管道感知所有者。正常退出先等待最多 5 秒，再结束进程组或 Windows kill-on-close Job Object。异常退出按 1、2、5 秒退避；滚动 5 分钟最多重启 3 次，持续健康 5 分钟后清零。超过阈值进入 `degraded`，只允许用户“重新启动”。
