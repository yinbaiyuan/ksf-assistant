# 飞书实验台验收记录

日期：2026-09-06。对象是独立 `FeishuLab` 开发子项目，不是安装版飞书服务切换；本轮未安装、提交或发布后续修正。

## 已验证

| 范围 | 命令 / 证据 | 结果 |
| --- | --- | --- |
| HTTP 与原生进程边界 | `npm --prefix FeishuLab test` 中 Node 测试 | 16 项通过：同源会话、Host/Origin、固定查询、参数/文件限制、一次性校验、执行去重、过期、撤权、原 challenge、版本冲突、超时、取消及输出上限 |
| Vue 页面状态与浏览器 API | 同一命令中 Vitest + jsdom | 16 项通过：用例加载、重复模式切换、参数/策略变更失效、附件替换/读取失败/乱序完成、禁止门禁、页内二次确认、响应丢失未知状态、刷新保留草稿及显式差异合并、scope 缺项、跨页面 Operation 成功/未知状态与原 ID 恢复 |
| Vue 生产构建 | `npm --prefix FeishuLab run build` | 通过，静态资产在被忽略的 `FeishuLab/dist` |
| 开发模式启动 | 隔离端口启动 `node server/main.mjs --dev`，读取 `/` 与 `/src/main.js` 后停止 | 均 HTTP 200，Vite 入口正确；未发起飞书操作 |
| 主工程回归 | `bash scripts/run-tests.sh` | Go 全量通过；冻结 Node 回放 284 项、Windows Node 63 项、Swift 43 项通过；独立生命周期测试及 macOS universal2 包构建通过 |
| Go 静态与竞态 | `go vet ./...`；`go test -race ./internal/feishucli ./internal/feishucommands ./internal/localipc ./internal/privateipc -timeout 180s` | 通过，部分结果使用 Go 有效测试缓存 |
| 交叉编译 | `bash scripts/build-core.sh` | macOS arm64/x64、Windows arm64/x64 的 Core 与飞书客户端均通过 |
| 源码卫生 | `bash scripts/check-release-hygiene.sh`；`git diff --check` | 通过；扫描已纳入 FeishuLab，不跟踪 node_modules、dist 或测试截图 |

组件测试在 jsdom 中使用假 API，不等于真实浏览器或 Windows 硬件验收。测试代码及进程夹具不连接飞书、不读取凭据，不消费生产队列。

## 已安装服务只读烟测

实际链路为本机 HTTP 适配器 → 安装版原生 CLI → Core 本机网关 → 飞书服务。仅调用 `catalog`、`policy`、`snapshot`、`permissions`，均 HTTP 200；没有执行消息、文档、授权修改或策略写入。

- 安装版目录返回 812 项已发布能力，治理 revision 为 1；读/写默认直接放行，高影响与远端操作逐次确认，破坏性默认禁止。
- 机器人、用户身份均 ready；用户必要 scope 192 项、有效授权 203 项、缺失 44 项、额外 55 项。数量只是当次检查事实，页面每次重新读取，不硬编码此结论。
- 服务快照 `processRunning=true`、`availability=ready`，但 `configured=false`；`businessIntegration`/`cardBindings` 降级，前者报告任务卡片协调失败。actionbox/docbox 禁用，outbox 和基础飞书收发 ready。
- 测试台如实展示这些限制，不把基础连通、身份在线或某项绿灯解释为所有功能通过；这些现有服务问题未在本轮旁路修复。

## 浏览器验收与实机边界

- 隔离夹具使用真实静态目录与假内存服务。新版在真实内置浏览器完成参数校验、意图勾选、直通执行、禁止门禁、页内策略二次确认、Operation 确认/取消和记录查询。
- 确认发生在“运行记录”后，返回原工作区显示同一个 Operation 的 `succeeded`，旧确认入口消失；取消后显示 `cancelled`。这些是隔离操作，不是真实飞书消息。
- 保存带 `page-size: 3` 的用例，切换到另一能力后重新加载，参数仍正确；再次点击 JSON 不覆盖内容。身份页准确显示假授权缺项；诊断页成功读取固定事件目录。
- 1440px 桌面与 390px 窄屏的目录、权限及记录视图有有效截图。目录在 390、620、621、820、821、1150、1440px 的 DOM 测量均无横向溢出；权限与记录页在 390px 亦无溢出。键盘可由 JSON 模式按钮进入编辑器，再进入校验按钮。不是完整无障碍审计。
- 旧标签页的原生 JavaScript 弹窗曾阻塞验收；旧页清理后已恢复，新版页内二次确认不再出现该阻塞。上述浏览器操作完成后控制台无 error/warn。完整截图接口曾产生缩放/黑区，异常截图不作为验收证据。
- 独立审阅对列出的源码/测试修正给出限定范围的 `ship`：旧附件残留、响应丢失后的旧结果/旧确认入口、重复 JSON 点击覆盖、刷新丢草稿均已关闭。这不是全界面视觉结论。
- 设计文档已按实际实现更新 `FeishuLab/DESIGN.md` 与 `.impeccable/design.json`。文档 lint 0 错误、16 警告（15 项未被组件 token 引用、1 项透明背景对比度）；sidecar 校验通过。文档结果与实际浏览器操作证据分开记录。
- Windows 真机客户端路径、Named Pipe、关闭链路和真实租户读写仍需人工验收；Go 交叉编译与 jsdom 不能替代。

## 交付边界

安装版能力目录未公开全部枚举和参数范围，表单只生成当前可见类型与必填约束。浏览器不支持任意服务器输出文件路径，必填输出能力有明确限制说明；不因此删减目录或伪称执行通过。测试台不随桌面包运行，凭据仍只归飞书服务管理。使用步骤见 [FeishuLab README](../../FeishuLab/README.md)。
