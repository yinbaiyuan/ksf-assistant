# 遗留飞书能力清理

2026-09-07，按用户要求删除 `ksf-feishu-bridge` Skill 和 docbox。当前 Doc/Wiki Skill 的链路为：`ksfas-lark` → 固定官方 CLI；身份、命令语义、单次用户批准和能力策略继续由受管工具链校验。

本次移除：

- KSF Skill 包、运行清单项及该 Skill 的旧演练目录。
- docbox 请求模型、执行器、后台调度、恢复处理、队列查询和设置界面。
- `client doc create/update`、`docs.service.document.create/append/overwrite`、`docs.whiteboard.insert` 及对应 XML/机器人文档适配器。
- 过时的 `documentWrites`、`skillCompatibility: phase2_pending` 能力声明。原生 Doc/Wiki/Whiteboard 命令继续使用固定官方能力契约。

旧 docbox 配置在设置保存时去除；历史队列和审计文件不删除、不调度、不重放。它们仍作为切换应用时的历史归属证据。旧文档权限 ID 只兼容读取，既有禁用和逐次确认限制继续生效；更新其他权限可以原样保留这些旧条目，但不能新增旧能力授权。新文档覆盖与版本创建使用规范命令权限标识。

本轮同类能力审查：

| 对象 | 判断与处理 |
| --- | --- |
| 旧 Skill 演练目录、旧文档 XML 和 bot 适配器 | 与已退役入口绑定，本轮一并清理。 |
| Go 飞书服务、任务卡片、outbox/actionbox | 仍有当前调用方，承担消息投递与任务交互，保留。 |
| `client knowledge/calendar/task/sheets/base/meeting` 便捷别名及 targets 文档别名 | 与受管官方 CLI 有重叠；可作为下一轮迁移候选，需要先核对外部调用方，不能按 docbox 附属代码删除。 |
| 旧 event profile 的只读兼容查询 | 无可选角色、无写入口；可在旧调用方迁移完成后去除。 |
| 官方 `lark-note/lark-vc/lark-minutes` 等转发 Skill | 属于固定官方 Skill 包的兼容路由；保留，随官方包升级统一审查。 |
| 管理类、凭据类能力限制 | 属于权限边界，不是历史遗留能力，本轮不改变。 |

验证覆盖：旧入口不可调用、旧设置保存后清理且其他配置不变、历史权限不放宽、当前文档创建和写入批准继续工作、历史文档记录不重放。另执行 Go、Swift、Windows 回归、固定 CLI 契约与跨平台构建。

本机交付验证：Go 全量回归、关键权限与文档路径 race、Swift 88 项、Windows 145 项、KSF runtime 验真、四平台核心构建和 macOS 双架构包均通过；776 项固定 CLI 契约校验通过。已替换本机应用并更新受管工具链，工具链状态 healthy。安装后 snapshot 为 ready，inboundConnection/taskLinkReady 均为 true，readinessBlockers 为空；只剩 outbox/actionbox 队列。旧 `client doc create` 被拒绝，受管 Doc/Wiki help 可用。没有通过创建远端文档或发送消息来验证本次删除。
