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

Go 入口维持 Bridge/Capability `1.0.0`、队列 schema `2` 与 `client.json` schema v4。23 类固定非 Approval 事件由 `oapi-sdk-go/v3.11.0` 接入，219 项固定能力继续委托随包 `lark-cli 1.0.92`。生产切换前 Node 与 Go 只能对同一冻结样本做回放，不得同时消费真实事件。

当前公开预览源码会构建并打包 Go 桥，但真实硬件矩阵全部通过前仍以 Node 兼容实现为生产入口。切换是一次性动作，不长期维护双实现。

## 生命周期

Core 启动桥，桥通过父级控制管道感知所有者。正常退出先等待最多 5 秒，再结束进程组或 Windows kill-on-close Job Object。异常退出按 1、2、5 秒退避；滚动 5 分钟最多重启 3 次，持续健康 5 分钟后清零。超过阈值进入 `degraded`，只允许用户“重新启动”。
