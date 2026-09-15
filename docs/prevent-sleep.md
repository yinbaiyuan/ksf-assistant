# 禁止电脑睡眠

设置中的“禁止电脑睡眠”默认关闭。开启后，仅在 KSFAssistant 运行期间阻止系统因空闲自动睡眠；屏幕仍可关闭，锁屏不受影响。关闭面板不释放，关闭开关或退出应用即释放。选择保存在本机，下次启动恢复，不与飞书连接数量绑定。

这不是远程在线保证：合盖、手动睡眠、关机、系统强制电源策略、低电量和网络故障仍可能使远程任务不可达。无人值守时建议接电、保持网络并避免合盖；持续唤醒会增加耗电。不开启显示器常亮，不修改系统电源计划，不阻止用户锁屏。

平台宿主拥有系统请求及生命周期：macOS 使用 IOKit `PreventUserIdleSystemSleep` assertion；Windows 使用 Electron `powerSaveBlocker` 的 `prevent-app-suspension`。不引入辅助常驻进程、PowerShell、管理员权限或 Go Core 电源策略。系统请求失败时显示错误，不把新请求记为成功开启。

验证：Windows 行为与设置持久化由 `Windows/test/sleep-inhibitor.test.cjs` 覆盖；macOS 可运行：

```sh
swiftc Sources/KSFAssistant/SleepInhibitor.swift scripts/tests/sleep-inhibitor.swift -o /tmp/ksfassistant-sleep-tests
/tmp/ksfassistant-sleep-tests --live
```

实机验收还需开启开关后查看 macOS `pmset -g assertions` 或 Windows `powercfg /requests`，确认系统请求存在；关闭、退出后请求消失，重启恢复选择。保持锁屏超过系统睡眠时限后从手机发送消息，确认远程连接。Windows 休眠策略及无人值守长时测试不能由 macOS 单元测试替代。
