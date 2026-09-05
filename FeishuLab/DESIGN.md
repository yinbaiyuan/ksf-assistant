---
version: "alpha"
name: "KSFAssistant Feishu Lab"
description: "飞书实验台现有实现的视觉记录；仅适用于本目录。"
colors:
  primary: "#155bc8"
  primary-hover: "#104da9"
  dark-primary: "#85b5ff"
  dark-primary-button: "#235cad"
  dark-primary-button-hover: "#2b6cc9"
  success: "#18734b"
  warning: "#955600"
  error: "#b83139"
  dark-success: "#77d3a2"
  dark-warning: "#edbf74"
  dark-error: "#ff9aa4"
  ink: "#202b3d"
  muted: "#5d697a"
  surface: "#f4f6f9"
  paper: "#ffffff"
  line: "#d9dfe8"
  selected: "#edf3fc"
  hover: "#f1f4f8"
  dark-ink: "#e2e7ef"
  dark-muted: "#a4afbf"
  dark-surface: "#171c25"
  dark-paper: "#202733"
  dark-line: "#3c4656"
  dark-selected: "#293b55"
  dark-hover: "#2a3443"
  selection-background: "#c7dbfa"
  selection-ink: "#152b4f"
typography:
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei\", sans-serif"
    fontSize: "26px"
    fontWeight: 650
    lineHeight: 1.3
    letterSpacing: "-0.025em"
  title-narrow:
    fontSize: "24px"
    fontWeight: 650
    lineHeight: 1.3
    letterSpacing: "-0.025em"
  heading:
    fontSize: "21px"
    fontWeight: 650
    lineHeight: 1.35
  subheading:
    fontSize: "15px"
    fontWeight: 650
    lineHeight: 1.55
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei\", sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.55
  button:
    fontSize: "14px"
    fontWeight: 550
    lineHeight: 1.55
  table:
    fontSize: "13px"
    lineHeight: 1.55
  label:
    fontSize: "12px"
    lineHeight: 1.55
  metadata:
    fontSize: "11px"
    lineHeight: 1.55
  required:
    fontSize: "10px"
    fontWeight: 500
    lineHeight: 1.55
  code:
    fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace"
    fontSize: "12px"
    lineHeight: 1.55
  code-input:
    fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace"
    fontSize: "12px"
    lineHeight: 1.6
  code-result:
    fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace"
    fontSize: "12px"
    lineHeight: 1.7
rounded:
  sm: "6px"
  mark: "9px"
  md: "12px"
spacing:
  xxs: "2px"
  xs: "4px"
  compact: "6px"
  sm: "8px"
  control: "10px"
  grid: "12px"
  md: "16px"
  inset: "18px"
  section: "20px"
  lg: "24px"
  xl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.paper}"
    typography: "{typography.button}"
    rounded: "{rounded.sm}"
    padding: "7px 13px"
  button-primary-hover:
    backgroundColor: "{colors.primary-hover}"
  button-primary-dark:
    backgroundColor: "{colors.dark-primary-button}"
    textColor: "{colors.paper}"
  button-primary-dark-hover:
    backgroundColor: "{colors.dark-primary-button-hover}"
  button-secondary:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.button}"
    rounded: "{rounded.sm}"
    padding: "7px 13px"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.primary}"
    rounded: "{rounded.sm}"
    padding: "5px 8px"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.sm}"
    padding: "9px 10px"
  navigation-current:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.primary}"
    rounded: "{rounded.sm}"
    padding: "11px 12px"
  badge-allowed:
    textColor: "{colors.success}"
    typography: "{typography.label}"
  badge-confirm-each:
    textColor: "{colors.warning}"
    typography: "{typography.label}"
  badge-disabled:
    textColor: "{colors.error}"
    typography: "{typography.label}"
  badge-unknown:
    textColor: "{colors.muted}"
    typography: "{typography.label}"
  page-section:
    backgroundColor: "{colors.paper}"
    rounded: "{rounded.md}"
    padding: "26px"
  catalog-selected:
    backgroundColor: "{colors.selected}"
    padding: "15px 18px"
  navigation:
    width: "204px"
---

# 飞书实验台

## Overview

**Creative North Star: "目录—请求—结果的本机工作台"**

面向开发者和人工验收人员，以 API 调试器式目录、参数编辑与原操作核验组织信息；冷灰页面、白色工作区、细分隔线和紧凑文字承担层级。延续既有系统工具风格，不是新视觉身份提案，也不是桌面菜单栏弹窗；父级弹窗宽度和禁侧栏规则不适用于此工作区。

本文件只记录 `src/style.css`、`src/App.vue` 与 `src/components/*.vue` 的当前实现，产品边界见 `PRODUCT.md` 和 `README.md`。未提供用户批准的效果稿或新视觉决策；北极星是对现有结构的归纳，不代表用户批准的新命名。图像策略沿用当前实现：零发布位图、无外部字体或装饰插画，仅使用内联描边 SVG 图标。

**Key Characteristics:**
- 能力目录与检查器并置，执行结果跟随当前请求。
- 状态使用文字和语义色；选中底色与键盘焦点分别表达不同状态。
- 窄屏重排而非隐藏核心操作，页内确认不打断到系统对话框。

### 证据与交接范围

- 本次为既有操作型界面的文档补录；不改变 UI、根级设计系统或产品授权边界。
- 已查看的有效首屏证据为 `output/review/desktop.png`（1440 × 1000）与 `output/review/mobile.png`（390 × 844）。它们只展示浅色能力页首屏，不证明其余页面、完整滚动区域、深色模式或全部交互正确。截图中的旧文字不覆盖当前源码。
- `*-full.png` 截图损坏，不纳入证据。reviewer 的 `qualified ship` 只覆盖列明的源码与测试修复。旧标签页清理后，已补齐新版直通、禁止、确认、取消、跨页状态、权限和用例的实际浏览器操作；具体范围见 `../docs/architecture/feishu-lab-validation.md`，不把这些操作外推为所有租户或完整无障碍验收。
- 下述 token、状态与无障碍属性来自源码核对，不是全产品视觉或可访问性认证。截图是审阅材料，不是发布资源，也不是批准的效果稿。

## Colors

单一操作蓝配冷灰中性色；成功、警告、禁止使用独立语义色，不扩展成额外品牌配色。YAML 中无前缀的是浅色值，`dark-*` 记录 `prefers-color-scheme: dark` 的对应值；界面没有独立主题切换器。

### Primary

- `primary` 对应 `--primary`，用于主按钮、导航选中、安静文字按钮、光标、复选框及焦点。浅色主按钮悬停使用 `primary-hover`。
- 深色 `--primary` 使用 `dark-primary`，但实心主按钮必须另用 `dark-primary-button`，悬停填充用 `dark-primary-button-hover`，文字始终沿用白色 `paper`。不能用浅亮的深色强调色填满白字按钮。
- 深色主按钮的悬停边框仍由已有浅色悬停规则提供 `primary-hover`；当前深色规则只覆盖悬停背景，文档不替实现“修正”边框。
- 文本选择使用 `selection-background` 与 `selection-ink`，深色模式未单独覆盖。

### Semantic States

| 语义 | 浅色 token | 深色 token | 当前呈现 |
| --- | --- | --- | --- |
| 直接放行 / 成功 | `success` | `dark-success` | `allowed`；结构校验通过另有明确文字，不代表授权通过 |
| 逐次确认 / 警告 | `warning` | `dark-warning` | `confirm_each`；确认提示、过期与版本变化提示 |
| 禁止 / 错误 | `error` | `dark-error` | `disabled`；错误提示与必填标记 |
| 尚未读取 / 未知 | `muted` | `dark-muted` | `unknown`；不是成功、也不必然是失败 |

目录摘要以小圆点加文字表达治理；列表和检查器以文字状态标记表达。工作台结果只有 `succeeded` 标绿，其余结果标题使用灰色；`awaiting_confirmation` 与 `outcome_unknown` 另有棕色解释块。诊断组件将 `ready` 标绿、`degraded` 标棕，其余标灰；运行记录表保留原始状态文本，不声称所有终态都有独立配色。

错误与警告提示的浅色背景均为语义色 5% 混合 `--paper`；边框分别为错误色 25%、警告色 22% 混合 `--line`（`color-mix(in srgb, ...)`）。深色改用 `--surface` 背景，保留语义文字和混合边框。成功提示仅改变文字，仍使用普通提示底色和边框。

**The 状态不替代结论 Rule.** 治理文字、身份授权、服务健康分别呈现；尚未读取和结果未知不能改写为通过。

### Neutral

| 用途 / CSS 变量 | 浅色 token | 深色 token |
| --- | --- | --- |
| 正文 `--ink` | `ink` | `dark-ink` |
| 辅助文字 `--muted` | `muted` | `dark-muted` |
| 页面、表头、JSON 摘要 `--surface` | `surface` | `dark-surface` |
| 工作区、导航、输入 `--paper` | `paper` | `dark-paper` |
| 分隔线、控件边框 `--line` | `line` | `dark-line` |
| 选中导航、目录、编辑方式 `--selected` | `selected` | `dark-selected` |
| 普通按钮悬停 `--hover` | `hover` | `dark-hover` |

代码未定义八阶色板。`.impeccable/design.json` 中每色八阶 OKLCH 色带是按规范生成的面板预览元数据，保留原色的色相与色度、递增明度；它们不是发布 CSS token、不是新的可用配色，也不构成用户批准的设计决策。

## Typography

正文沿用 YAML `body` 中的系统字体栈，中文回退为 PingFang SC / Microsoft YaHei；不下载字体。代码使用 `code` 的系统等宽栈。没有营销展示字体或独立 display 层级。

| 层级 | 当前尺寸 / 字重 / 行高 | 应用 |
| --- | --- | --- |
| 页面标题 | 26px / 650 / 1.3 | h1，字距 -0.025em |
| 窄屏页面标题 | 24px / 650 / 1.3 | 仅 ≤620px 的页头 h1 |
| 区域标题 | 21px / 650 / 1.35 | h2，长内容允许任意位置换行 |
| 小节标题 | 15px / 650 / 1.55 | h3；品牌名称同尺寸，字重沿 strong 默认 |
| 正文与主控件 | 14px / 正文 400、按钮 550 / 1.55 | 目录名称另用 600 |
| 表格与身份说明 | 13px / 继承 / 1.55 | 窄屏表格降为 12px |
| 标签与辅助说明 | 12px / 按用途 / 1.55 | 字段标签 600、表头 550、状态标记 600 |
| 次级元数据 | 11px / 按用途 / 1.55 | 目录 ID、字段类型、领域标签、页脚 |
| 必填标记 | 10px / 500 / 1.55 | 仅辅助“必填”文字，不作为正文标准 |
| 等宽内容 | 12px / 继承 | 普通 code 行高 1.55、JSON 输入 1.6、结果 pre 1.7 |

这是当前字号分布，不是等比推导的新标尺；10px / 11px 的辅助小字仅记录现状，不推广为通用可读性标准。普通 textarea 行高 1.6；代码与 ID 允许换行，不靠缩字塞入固定宽度。可点击导航为 500，当前项为 650；≤820px 导航为 12px。没有全大写标签规则。

## Layout

### 工作区与宽度

桌面壳层为 `204px minmax(0, 1fr)` CSS Grid。导航为 sticky（top 0、height 100vh），内边距 27px 16px 22px；主区宽度 100%、最大 1760px、内边距 34px 32px 20px。最大值只约束主区，不是整页居中宽度。

目录 / 检查器宽度来自 grid，**没有固定 420px 检查器**：默认 `minmax(280px, .95fr) minmax(390px, 1.2fr)`。检查器 `min-width: 0`、sticky top 0；工作台内边距 23px 24px。目录、请求与结果共享一个带边框工作区，结果在请求下方，不是右侧弹层。

筛选区默认 `minmax(220px, 2fr) repeat(4, minmax(100px, 1fr))`、间隔 12px。身份信息默认两等列、间隔 24px；scope 列表默认两列。常见动作组间隔 8px、标签组 12px，并允许换行。没有严格的“全局 8px 网格”；7px 按钮图文间隔、15px 字段间距等也属于现有实现。

### 响应式边界

各媒体查询按源码顺序叠加，不能把导航变顶栏与目录变单列合并为同一个断点。

| 条件（CSS px） | 实际变化 |
| --- | --- |
| ≥1150px | 目录独立纵向滚动：max-height `calc(100vh - 290px)`，min-height 540px；目录标题与分页分别 sticky 顶 / 底 |
| ≤1100px | 导航 174px、横向内边距 10px；主区 25px 20px；目录两列改为 `1fr 1.2fr`；搜索占筛选区整行，其余四等列；工作台与工具栏横向内边距 18px；刷新组纵向排列 |
| ≤820px | 壳层 block，导航变顶部、静态定位、高度自适应；导航按钮可换行，页脚连接信息隐藏；主区 24px 18px；目录仍两列：`minmax(230px, .9fr) minmax(300px, 1.1fr)`；scope 单列、身份间隔 16px、页脚纵排 |
| ≤620px | 主区 22px 12px；页头纵排、标题 24px、刷新组横排；筛选两列且搜索跨列；目录 / 检查器变纵向 flex，目录 max-height 330px 且独立滚动；目录行 min-height 99px，检查器静态定位且满宽；身份单列；表格 fixed 布局、12px 字号；导航隐藏图标但保留文字 |

1101–1149px 保留默认 204px 侧栏与默认目录比例，但没有 ≥1150px 的目录滚动规则。621–820px 的顶部导航仍配双栏；单列从 620px 开始。已检查目录在 390、620、621、820、821、1150、1440px 无横向溢出；不是穷举所有视口和文本组合。

### 阅读区

- 页面按正常文档流滚动；目录只在上述宽屏 / 小屏区间建立受限滚动区，不能写成始终无内部滚动。
- JSON 结果最大高度 440px，超出可滚动，使用 `pre-wrap` 和任意位置换行。空状态说明的最大行长 55ch。
- 页内确认块最大宽度 60ch，外层最大宽度 100%，上下外边距 12px；不是固定宽度对话框。
- 普通页面分区内边距 26px；≤820px 为 20px，≤620px 为 17px 14px。表格单元格从 12px 13px 缩为 10px 7px，不能假定所有区域都是相同卡片内距。

## Elevation & Depth

无浮层投影。纸面与页面底色形成轻量层次，1px 边框与分隔线组织目录、表格、表单和结果。选中目录有 `inset 0 0 0 1px var(--primary)`，是唯一的 box-shadow 用法，不是悬浮卡片。键盘焦点使用 outline，不能用选中边框代替。

**The 平面分区 Rule.** 依靠纸面色、细边框和间距分区，不添加浮层投影；目录选中的内描边只是状态反馈。

## Shapes

常规按钮、输入、提示与 JSON 容器为小圆角（`rounded.sm`）；完整工作区和页面分区为中圆角（`rounded.md`）；品牌图标框用 `rounded.mark`。目录行本身为直角、横向满宽，不能把所有列表行做成独立圆角卡片。

状态标记是紧凑文字，不是胶囊；连接点和摘要点分别为 7px / 6px、50% 圆角。图标来自 `Icon.vue`：24 × 24 viewBox、currentColor 描边 1.6、圆端点，常规显示 20px。导航在 ≤820px 显示 16px 图标，≤620px 隐藏图标；36px 品牌图标框仍保留。没有发布位图、纹理、外部图标字体或效果稿切片；审阅 PNG 不计入发布资源。

## Components

### Buttons

默认按钮为纸面底、正文色、1px 分隔线边框，小圆角，最小高度 36px、内边距 7px 13px、图文间隔 7px。普通悬停使用 hover 底色、muted 边框；主按钮与其深色例外见 Colors。安静文字按钮为透明底与边框、主色文字、内边距 5px 8px；悬停仍继承普通按钮规则。

禁用按钮 opacity 0.48、not-allowed 光标，悬停选择器排除 disabled。工作台使用“校验中…”“执行中…”等动作文字与 `aria-busy`，不是所有按钮都有统一旋转图标。

按钮仅有背景 150ms ease 过渡；没有入场、位移或循环动画。`prefers-reduced-motion: reduce` 全局关闭 transition，并将 scroll-behavior 设为 auto。

### Inputs / Fields

文本、选择框与 textarea 满宽，小圆角、1px 边框、内边距 9px 10px、最小高度 39px，光标使用主色；textarea 可纵向调整。复选框单独为 16 × 16px，无普通输入框内距布局承诺。

表单、搜索、附件和查询控件有可见标签；策略表内的选择框由可见风险行 / 列标题提供上下文，同时附带 `aria-label`，并非每个控件都有独立 `label` 元素。占位符不替代名称。参数错误采用就地 `role="alert"` 提示，源码没有通用字段红边或逐字段 `aria-invalid` 机制；不要记录成已实现。

全局 `:focus-visible` 为主色 2px outline、向外偏移 3px。页面提供“跳到工作区”链接，仅获焦时进入视口；JSON pre 可聚焦并有可访问名称。确认区是页内文档流，不实施模态焦点陷阱或自动转移。已实际验证 JSON 模式按钮 → 编辑器 → 校验按钮的键盘顺序；不代表完整无障碍审计。

### Navigation / Catalog / Segmented

主导航有文字、`aria-label` 与当前项 `aria-current="page"`；桌面按钮最小高度 43px，选中背景 selected、文字 primary。选中导航与当前键盘焦点可落在不同项。

目录行为整行按钮：默认最小高度 110px、15px 18px 内边距、4px 行内间距；选中使用淡蓝底与内描边，`aria-pressed` 表示选择状态。分页为 25 项一页，数量来自目录，不固化截图中的总数。

表单 / JSON 切换使用有边框的小圆角分段组，组内距与间隔各 2px；按钮最小高度 28px、3px 10px 内边距、12px 字号。当前项使用 selected 底色和 primary 文字；当前实现是普通按钮组，不宣称完整 ARIA tabs 键盘协议。

### Status / Containers / JsonView

状态文字标记为 12px / 600、禁止换行；普通容器用纸面底、细边框，无装饰阴影。失败提示保留操作上下文；未读取授权时先呈现解释与核验入口，不以空白或绿灯代替结论。

契约与 JSON 用原生 `details/summary` 就地展开，摘要为 muted 色 / 550 字重。JSON 摘要内距 9px 12px、内容 15px；以 Vue 文本插值展示，不使用 HTML 注入。结果区 `aria-live="polite"`、结构校验和限制说明使用状态提示。清空会话记录及清空用例仍为直接按钮，不能声称所有清空行为都有二次确认。

### ConfirmAction

确认执行、取消 Operation 与保存策略共用页内两步组件。第一步按钮通过 `aria-expanded` 展开棕色提示，包含“动作名：二次确认”、对象及后果说明；第二步为“已核对，继续”与“返回核对”。继续先收起提示再发出确认事件；返回只关闭提示。禁用状态变化也会关闭提示。

这是普通文档流内的 `role="group"`，没有遮罩、阻塞窗口、`window.confirm` 或原生对话框。待确认 Operation 的摘要、有效期与查询入口仍在附近；保存策略前显示逐条前后差异，草稿变化会重建确认组件，避免沿用旧确认。

**The 确认留在上下文 Rule.** 确认与取消使用页内两步，Operation 摘要或策略差异留在动作附近；不把系统原生对话框写成现有机制。

## Do's and Don'ts

### Do:
- Do 保留目录—请求—结果的阅读顺序，先呈现治理、身份、风险与执行边界，再呈现参数。
- Do 区分平台授权、本机治理、运行状态及资源可见性；保留尚未读取、等待确认和结果未知的明确文字。
- Do 按实际断点重排，保持字段标签、键盘焦点和原操作核验入口可见。
- Do 保存策略前展示差异及原 revision，确认和取消均说明具体对象。

### Don't:
- Don't 把父级菜单栏弹窗的尺寸和禁侧栏规则套到实验台，也不要把检查器写成固定 420px。
- Don't 把页内两步确认描述成原生 confirm、模态弹窗或已经验证的焦点陷阱。
- Don't 把当前截图中的能力数量、revision 或身份状态固化为产品常量；未知结果不提供自动重试承诺。
- Don't 为这次文档补录添加营销插画、渐变背景、大数字仪表盘、装饰动画或新品牌规则。
- Don't 将静态检测、文档 lint 或限定修复项的 ship 结论替代全浏览器交互、深色实测或真实租户验收。
