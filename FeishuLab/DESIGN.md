---
version: alpha
name: KSFAssistant Feishu Lab
colors:
  primary: "#155BC8"
  success: "#18734B"
  warning: "#955600"
  error: "#B83139"
  ink: "#202B3D"
  muted: "#5D697A"
  surface: "#F4F6F9"
  paper: "#FFFFFF"
  line: "#D9DFE8"
typography:
  title:
    fontFamily: system-ui
    fontSize: 26px
    fontWeight: 650
    lineHeight: 1.3
  body:
    fontFamily: system-ui
    fontSize: 14px
    lineHeight: 1.55
  code:
    fontFamily: ui-monospace
    fontSize: 12px
    lineHeight: 1.6
spacing:
  xs: 4px
  sm: 8px
  md: 16px
  lg: 24px
  xl: 32px
rounded:
  sm: 6px
  md: 12px
components:
  navigation:
    width: 204px
  inspector:
    width: 420px
---

# 飞书实验台

## Overview

面向开发者和产品验收人员的本机操作工具，参考 API 调试器的目录—请求—结果层级，延续 KSFAssistant 的安静、精确、系统工具气质。它不是菜单栏弹窗，不继承 336px 宽度和禁侧栏规则。以工作空间、表格、分隔线组织高密度信息，不以大数字或装饰卡片填充页面。

## Colors

白色工作区、冷灰页面底、蓝色当前选中和主要动作。绿色表示直接放行，棕色表示逐次确认，红色表示禁止；所有颜色都有文字说明。未知使用灰色，不能冒充通过。

## Typography

中文系统字体承载标题和操作，等宽字体只用于能力 ID、JSON、策略版本和测量值。目录以短名称为主、完整 ID 为辅。避免全大写标签。

## Layout

桌面固定窄侧栏；能力页为可筛选目录和请求检查器双栏。结果紧跟当前请求，审批信息贴近确认动作。窄屏侧栏改为顶部可换行导航，目录与检查器上下排列，不隐藏核心操作。页面只有正常文档滚动和明确的目录/代码阅读区。

## Elevation & Depth

使用细分隔线，不叠加重阴影。危险操作使用原生确认对话框保护注意力，日常详情就地展开。

## Shapes

输入框和按钮使用小圆角，区域边缘使用中圆角。状态仅为紧凑文字标记，不把整行染色。

## Components

所有输入有可见标签。按钮具备忙碌、禁用、键盘焦点和失败反馈；错误保留请求内容。空状态说明如何继续。刷新明确表明刷新对象和时间，网络失败不清空用户草稿。JSON 不通过 HTML 渲染，私有结果不自动持久化。支持深色和减少动画偏好。

## Do's and Don'ts

- 先呈现能否执行及原因，再呈现具体参数。
- 区分本机治理、飞书授权和运行开关，不能用一个绿灯替代三者。
- 审批按钮必须指向明确 Operation，结果未知不显示自动重试。
- 不使用营销插画、渐变背景、大数字仪表盘或无意义动画。
