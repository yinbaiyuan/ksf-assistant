# 兼容性

| 组件 | 团队版要求 | 失败边界 |
| --- | --- | --- |
| macOS | 13 或更高；arm64 / x86_64 | 更低版本不支持 |
| Codex CLI | 支持 `account/rateLimits/read` 与 `account/usage/read` | 额度与账号 Token 独立降级 |
| Codex Desktop | 支持当前本机 IPC 协议 | 任务计数、打开任务和前台首轮提交可能不可用 |
| KSF | 根目录含 `AGENTS.md`、标准面板桥及 v1 Catalog/Projection 协议 | 项目工作台与长期 Token 历史不可用 |
| 飞书桥 | 本机 Node.js、运行中的单聊入站和卡片回调、Codex daemon、授权人员别名；任务控制要求 `codex-feishu-task-link-v1` response v2 | 飞书状态、测试发送与任务连接独立降级 |

Codex Desktop IPC 不是公开稳定接口。升级 Codex 后应运行 `scripts/live-smoke-test.sh`，再判断是否继续兼容。
