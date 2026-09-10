# 0.11.0-preview.1 本机预览

本轮交付开发工作树与本机预览包，不暂存、提交、推送、公证或公开发布。构建通过不等于真实飞书/Codex Desktop 验收通过；实际结果单独记入验收记录。

## 职责与数据流

- Codex 决定当前做什么、怎么做和何时调用 Skill；任务记录 CLI 不具备大模型能力，不调度岗位或 Agent。
- `ksf-assistant-task` 独立记录 Agent 上报，Core 只读取已经被 Desktop 观察到的顶层任务。任务数及运行/等待状态仍来自宿主，不根据报告制造任务或将一轮结束判定为业务验收。
- KSF 现行 v6 验真、类别、岗位责任—基本功关系、Skill 门禁和旧目录桥保留；新 System Skill 仅 trial，不建立基本功关系、编排模板或新的治理状态。
- Core `integration` 保留链接、问题、Plan、卡片业务和 Desktop 请求解释权。飞书进程负责通信、接入授权及现有持久化机制；不创建替代 App Server 会话。

## 官方 CLI 与授权

`lark-cli v1.0.93` 与同一源标签的 28 个官方 Skills 随应用固定。二进制下载哈希、源码归档哈希、逐文件哈希、MIT 许可证和包清单纳入资产与 SBOM；客户端不自行升级。构建期使用 Node 准备资产，生产飞书服务无 Node 运行时或直接 Go SDK 依赖。

桌面只提供一次“扫码连接飞书”。创建或选择已有应用均在飞书官方网页完成；受控 `lark-cli` 在注册完成时把当前应用下的 `open_id` 送入 Core 私有管道，一次完成应用接入、本人绑定和机器人连接。只有上游未返回该身份时，桌面才显示“补充本人授权”，并只申请 `contact:user.base:readonly`，绑定后立即删除临时用户令牌。

本轮仅固定 `default` profile 和 `feishu` 品牌。受管配置固定在 `~/.config/feishu-bridge/lark-cli`，安全存储命名空间固定为 `ksfassistant-lark-cli`，并过滤外部同名环境变量。基础就绪只核验机器人消息、私聊接收、卡片发送/更新、按钮回调及附件所需应用权限；用户身份能力按受管命令描述符推导缺少 scope，授权成功后不自动重放原操作。

受控 CLI 以官方 1.0.93 源码归档、与该发行版 250 项 schema 对齐的飞书 API 元数据和仓库内补丁构建；清单固定源码、元数据归一化版本、补丁及四平台产物哈希，任一输入漂移都会终止构建。详细流程见[首次配置扫码创建流程](feishu-app-registration.md)。官方审核、应用发布及管理员批准不由产品绕过；注销也不会删除飞书开放平台中的应用。

## 事件与消息兼容

- 生产受管消费者为 `im.message.receive_v1` 和 `card.action.trigger`。启动前遇到已有总线拒绝接管；记录消费者集合、PID 与丢弃计数，退出通过 stdin EOF 回收自有消费者，只对已确认自有且无消费者的总线请求正常停止，不使用 force/all。
- 冻结的 26 个事件名继续用于历史记录查询/回放。非核心事件本轮不自动订阅，状态明确为 `not_enabled/explicit_subscription_required`；不是把未运行的消费者标成已连接。旧 Mail 事件在固定版 CLI 中不存在，明确 `unsupported_by_pinned_cli`。
- 消息正文、卡片 action/form、回复关系分别归一化；附件/复杂消息通过 CLI 读取原消息补齐并检查 ID、chat、sender，不猜绑定。不持久化卡片延迟更新 Token。
- 固定版移除了 typed `im messages create/reply/get`；发送、回复和归属核验使用官方 CLI 的固定 API 路径。更新卡片与上传附件使用仍存在的 typed 命令。不向调用方开放任意 API。
- 只认明确成功且 bot 身份的 CLI 信封。退出码 10 不自动加 `--yes`；发送超时不代表失败已确定，不盲目重发。传输 ACK 不代表 Desktop 已接受答案或 Plan。
- 原消息 ID、链接 ID、未决业务队列、去重和未知字段不重建、不复制。旧卡片 ownership marker 需用 CLI 重新核验；旧队列继续由唯一消费者处理。
- 通用自有飞书客户端保持冻结兼容目录（生成基线为 1.0.92），实际执行固定版为 1.0.93。新的通用业务优先官方 Skills，不扩展自有能力目录。

## 任务运行记录

协议、CLI 输入输出、状态边界、隐私和存储限制的实现契约见 [taskruntime README](../../Core/internal/taskruntime/README.md)。数据位于明确工作区的 `.agents/runtime-data/ksfassistant/tasks-v1/` 并由工作区忽略 Git。

稳定任务 ID 为带命名空间的 HMAC，原始宿主身份不写进 KSF 文件。显式 scope 区分 KSF、单项目和未确定；项目资料引用不是绑定。任务级操作使用文件锁、修订冲突、事件幂等及一个原子 JSON 同批保存快照/历史。CLI 不依赖 Core、Ruby 或常驻服务；Core 重启后按已观测任务补读，报告过期/路由过期不改写业务事实。

## 受管 Skills 与回退

管理器将真实 Skills 文件安装到用户 `~/.agents/skills`，保存原始哈希及所有权清单。同名异源、增删文件、用户编辑或悬而未决事务都停止覆盖。受管目录提供 `ksfas-lark`、`lark-cli` 和 `ksf-assistant-task` 入口，不覆盖全机其它 CLI，也不自动改 PATH；`ksfas` 接入 Skill 说明入口使用方式。

安装应用与安装 Skills 是两个显式动作。更新后用新 Codex 任务验证真实发现结果与实际 CLI 版本，不承诺运行中任务热切换。没有在线更新服务，不伪造“已是最新版本”。

`scripts/package-preview.sh` 冻结执行时源码/文档并复核哈希，拒绝构建中来源变化。安装脚本暂存、验签、识别并受控停止产品旧进程、原子切换并保留上一应用；脚本健康检查仅覆盖产物，运行健康与真实交互另验。回退应用不恢复旧队列、不回放已发送消息、不覆盖切换后新业务事实。

## 验收门槛

自动化包括 Go 全量/Race/vet、Swift XCTest/专项、Windows Node、冻结兼容、四目标 Go 与 universal2/验签、KSF status/verify-runtime 及隔离任务记录测试。测试不读取生产秘密。

实机需在授权单聊和专用新任务验证：受管 CLI 最小调用；原 Desktop 任务绑定、续聊、输入选择、Plan、中断和解除；三轮真实输入解除等待；重复/旧卡片不重执行；重启恢复及退出回收；Core 关闭时任务 CLI 独立工作。未通过的项目保持未验收，不以交叉编译替代 Windows 实机。

## 保留项

- 旧 SDK 凭据文件/Keychain 服务：兼容保留，不由新生产链读取或重建。
- 冻结 Node 源码及其旧依赖版本：仅离线回放，非生产回退。
- 历史产品名、旧应用 ID：仅原有迁移与安装识别位置；不改真实外部地址和 Git 历史。
- KSF 现有目录桥及 Ruby 历史解释机制：不在本轮重写 KSF 内核。
- 已安装旧应用、已保存的 Codex 项目路径及 KSF 通用飞书 Skill 的已有用户修改：不会因为构建源码而被自动改写。
