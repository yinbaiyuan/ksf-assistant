# ExecPlan: Docbox 文档任务队列

## 目标

将 docbox 定义为通用文档任务队列。其他本机项目通过 JSONL 提交飞书文档创建或编辑任务；飞书桥负责编排、权限、状态、日志、审计和唤醒；文档相关能力统一通过 `lark-cli` 执行。创建文档使用固定 `docs +create → fetch` 链路；更新既有文档由飞书桥执行基础版本保护更新。

## 当前状态

- 飞书桥已有 outbox 出站消息 worker、wake server、结果日志和命令查询。
- 飞书桥已有 Codex app-server 调用链路和任务日志。
- 项目规则要求飞书桥不承接业务语义判断，编辑既有文档前必须创建飞书官方版本。

## 边界

- 第一版支持 `type: "document_task"`。
- 第一版支持 `action: "create_document"` 和 `action: "update_document"`。
- 飞书桥不实现桥内文档替换、预览、确认或内容哈希状态机。
- docbox 不移动 Wiki 节点，不修改协作者权限，不直接执行复杂 docx block 编排。
- `update_document` 默认采用 `versionPolicy: "official_before_update"`，编辑前必须创建飞书官方文档版本。
- `FEISHU_DOCBOX_DRY_RUN=true` 时只记录任务意图；不创建版本、不创建或编辑文档。
- 其他项目只写 `logs/docbox.jsonl`，不直接调用飞书文档 OpenAPI。

## 改动方案

- 保留 `FEISHU_DOCBOX_*` 环境变量。
- 保留 `logs/docbox.jsonl`、`logs/docbox-results.jsonl`、`logs/docbox-state.json`。
- 保留 `POST /internal/docbox/wake`，仅绑定 `127.0.0.1`，请求体忽略。
- docbox worker 校验 `document_task` 请求。
- `create_document` 固定调用 `lark-cli docs +create`，正文只走 stdin，随后通过 `docs +fetch` 复读；不得启动自由 Codex 后台任务。
- `update_document` 解析目标、读取原文、创建官方版本、更新正文、再次读取验证，并写入 revision 与 version 结果字段。
- `cmd 最近文档` 和 `cmd 文档 <DOC-id>` 查询文档任务状态。
- `cmd 状态` 展示 docbox worker、wake、dry-run 和最近错误状态。

## 安全策略

- source 必须命中 `FEISHU_DOCBOX_ALLOWED_SOURCES`。
- create 任务只允许固定 `lark-cli` 链路创建飞书新版文档并返回脱敏结果。
- update 任务必须先创建官方文档版本，再按 instruction 执行基础更新；若版本创建失败、授权不足或目标不清晰，应停止并返回失败原因。
- 飞书桥只记录 `accepted`、`completed`、`dry_run`、`failed`、`denied`、`duplicate`、`invalid` 等状态，不判断文档内容业务语义。

## 验证

- `node --check bot-bridge.js`
- `node --check scripts/start-bridge.js`
- `git diff --check`
- 验证 source 拒绝、重复 ID、坏 JSON、半行跳过、dry-run、版本创建失败和文档更新失败记录。
- 验证 create 请求只执行固定 `docs +create → fetch`，正文不进入 argv 或日志。
- 验证 update 请求会先创建官方版本，再更新正文。
- 确认 `messages.jsonl`、`docbox-results.jsonl` 和项目内审计页均有记录。

## 回滚

- 设置 `FEISHU_DOCBOX_ENABLED=false` 停止 docbox worker。
- 设置 `FEISHU_DOCBOX_WAKE_ENABLED=false` 停止 docbox wake server。
- 回滚 `bot-bridge.js` 中 docbox 区块和文档配置更新。
