# 飞书桥 1.0.0 能力与权限矩阵

更新日期：2026-09-02

## 固定基线

- 飞书桥 / capability：`1.0.0`
- npm package：`1.2.0`
- KSF Skill 兼容：`1.0.x`
- `lark-cli`：固定 `1.0.92`
- 官方 SDK：固定 `1.73.0`，维持一条入站 WebSocket，并复用常驻客户端发送高频状态卡
- 队列状态：schema v2，兼容既有 outbox/docbox/actionbox 结果
- 注册能力：219 项；依赖升级不会自动增加命令、参数或 EventKey

## 风险与执行路径

| 风险 | 执行要求 | 路径 |
|---|---|---|
| `read` | 明确目标及页数、时间窗、范围或字符上限 | 受控 `lark-cli` 直接读取 |
| `write` | 本轮显式授权、请求 ID、队列、审计 | outbox / docbox / actionbox |
| `high-impact-write` | 额外确认；预检或快照成功后才写；写后复读 | docbox / actionbox |
| `remote-operation` | 队列提交；固定状态读能力轮询；超时保留原 ID | actionbox |

消息正文、文档正文、Apps 提示和结构化 payload 从 stdin 或 `0600` 私有文件进入。所有队列写请求必须携带本轮显式授权标记。公开 JSON 默认只显示别名、类型、长度和不可逆指纹；本机请求 ID 保留原值供异步查询，飞书远端 ID 继续脱敏。

注册表中受审查的创建能力可用 `--save-as 'Codex桥测试…'` 将固定结果 ID 保存到 `client.json`（schema v4），后续通过类型匹配的 `asset:<别名>` 引用。dry-run 不保存绑定；原始 ID 不进入仓库、Skill、命令行或公开输出。

## 领域清单

| 领域 | 注册项 | 1.0.0 安全子集 |
|---|---:|---|
| 消息与群聊 | 25 | 群查询/创建/名称描述更新、成员列表、消息批读/已读、回复/编辑/转发/合并转发、附件下载、Feed/书签/置顶/表情读写、应用内加急 |
| 通讯录 | 4 | 用户详情、用户/机器人搜索、批量资料；缓存仍只保留姓名、部门与解析字段 |
| Event 订阅 | 11 | Task、Whiteboard、会议、Note、录制、Minutes 的固定类型订阅/取消；Task 不提供取消 |
| Docs | 10 | 历史、回退、媒体预览/下载/上传/插入、资源读写、草稿预检、窄类型文档内 Whiteboard |
| Whiteboard | 3 | preview/SVG/source/raw 导出；追加；确认后整板覆盖 |
| Mindnotes | 3 | 节点列表、子节点新增、确认后节点更新；不能新建 Mindnote |
| 飞书 Markdown | 5 | create/fetch/diff/overwrite/patch；更新前后读取验证 |
| Wiki | 7 | 空间/节点/成员读取，空间创建、节点创建与复制 |
| 评论 | 7 | 批量评论、回复列表、回复新增/更新、解决/恢复、回复表情 |
| 日历 | 8 | 日历读写、日程分享加入、日程到会议映射、会议室、智能时间建议 |
| 任务 | 16 | 评论、关注人新增、父子任务、清单及加任务、附件、分组、自定义字段 |
| Sheets | 56 | 工作簿、导入导出、历史、工作表、行列、样式/图片/合并/替换/区域操作、图表/透视/条件格式/筛选/下拉/迷你图/浮动图片 |
| Base | 43 | 数据查询、记录历史/附件、模板/工作区、Base/BaseApp、表/字段/视图/表单/仪表盘/页面块/工作区的创建及受控更新 |
| 会议 / 妙记补充 | 2 | 会议事件读取、Minutes 结构化详情；既有稳定性基线能力继续保留 |
| 妙搭 Apps | 19 | 应用/会话/发布/日志/指标/Trace/PV-UV 读取；应用、会话、Chat、停止与 Release 主链路写入 |

完整机器清单以以下命令为准：

```bash
npm run bridge -- capability catalog
npm run bridge -- capabilities
```

## 固定事件目录

`lark-cli 1.0.92` 的事件目录共 25 项。桥固定排除：

- `approval.instance.status_changed_v4`
- `approval.task.status_changed_v4`

其余 23 项固定接入，覆盖应用菜单、Whiteboard、卡片、IM、Minutes、Task 与 VC。事件只进入私有收件箱，不自动发消息、改任务、写文档或触发审批。

## 权限检查

能力声明按 bot/user 身份分开。实际缺口必须从当前 profile 动态检查，不在文档复制 token 或真实资源 ID：

- 个人云空间文档写入：`user`。
- Wiki 节点、Wiki 内文档和桥管理的 Wiki Mindnote 写入：`bot`。
- 读取仍按能力声明执行；写入权限不足时禁止跨身份回退。

```bash
npm run bridge -- permissions
npm run bridge -- doctor
```

`permissions` 只比较桥已实现能力的最小 scope；多余 OAuth scope 不会自动成为桥能力。`doctor` 还检查固定 CLI 版本、23 个事件、官方 SDK 单连接、私有收件箱、队列 wake 和本机配置权限。权限不足或控制台事件未发布时，对应真实验收停止，不修改飞书控制台。

## 明确排除

- Approval API 与 Approval Event。
- 删除、清空、消息删除、移除类动作。
- 群成员/管理员/审核权限修改，通讯录写入，Wiki 成员修改/节点移动/移出 Drive。
- 电话或短信加急、后台全量抓取、任意 OpenAPI、任意 shortcut、任意 EventKey。
- 实时入会/离会/结束会议、会中控制、妙记原始媒体下载、权限申请、待办删除。
- Sheets 删除/清空及含未审查删除子操作的批处理。
- Base 分享链接、权限角色、公开设置、Workflow、按钮工作流绑定及删除。
- Apps 可见范围、成员/角色、OpenAPI Key、环境变量、数据库、自动化、缓存、插件、Git 凭据及删除。
