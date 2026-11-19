# Android 订阅刷新与首页总览

本地分支 `android/subscription-refresh-overview`，基线 `6ebfb42`。

## 改动

- HTTPS 来源默认 12 小时，来源管理支持关闭、6、12、24 小时。无 URL 的来源仅支持本地导入。删除来源同时清理刷新周期、尝试时间、错误和退避计数。
- WorkManager 每小时检查一次到期来源（审查后由 15 分钟改为 1 小时，减少唤醒），系统可延后执行。回到前台和订阅 VPN 断开时也登记一次相同的联网检查。每个来源独立退避，首次失败后等待 15 分钟，随后翻倍，最长 16 小时。手动更新可提前重试。
- 继续使用原导入、加密存储与 nodeKey。下载和解析均成功后才写入，写回前再次检查版本和 VPN 占用。当前与备用来源在整个订阅 VPN 会话内受保护。失败提示仅存中文分类。
- 首页按来源展示可用性、流量、到期和刷新结果。流量合计按字段统计已知来源，并注明覆盖范围。低于 10% 或距到期不足七天的项目使用主题警示色。响应头逐字段优先，头里缺少的字段（常见为到期时间）再由信息节点补充；仅识别整条匹配的余量、到期和重置提示，保留信息节点并从自动选择中排除。未知字段显示「未提供」。
- 应用 Manifest 沿用原权限。WorkManager 合并 WAKE_LOCK 和 RECEIVE_BOOT_COMPLETED，并使用已有联网及前台服务权限。

## 实际验证

仅使用雷电 `emulator-5554`，覆盖安装保留数据。以下为本次实际输出。最终复测前曾因 singbox_tun 默认路由暂停。用户关闭 TUN 后，确认只剩物理默认路由，重新运行下列回归。

| 项目 | 结果 |
|---|---|
| 三个 Debug APK | 最终构建 `BUILD SUCCESSFUL in 6s`，103 项任务，6 执行、97 已最新。三个 APK 首次安装均为 Success，最后改动后的 app 和 androidTest 再次覆盖安装 Success |
| 默认 CatalogCheck | `3 sources, isolated update, encrypted persistence, native outbound validation`，返回 -1 |
| formats | `25 Clash, sing-box and link cases converted or refused as expected`；`3 metadata formats, strict negatives and header precedence passed`，返回 -1 |
| refresh | `failure preserved snapshot; backoff, id, nodeKey, VPN primary/backup skip and disconnect catch-up passed`，返回 -1。以合成下载响应和推进的检查时间驱动，VPN 通过实际 TunnelService 启停 |
| Airport-Test | 真实刷新返回 `41 nodes; 已用 83.9 GB / 总量 1200 GB · 剩余 1116 GB`，返回 -1。响应未提供到期时间 |
| WSL 互通 | 16 项均返回 `node-side HTTP receipt`，返回 -1。涵盖原七种协议及 SS-2022、两种 REALITY、WS TLS、XHTTP、VMess gRPC/TCP 伪装、HTTPUpgrade、Hysteria2 salamander |
| 系统调度 | dumpsys jobscheduler 显示 Nexus 的 SystemJobService，约束 `TIMING_DELAY CONNECTIVITY`，已运行并登记下次调度 |
| UI | 浅色和深色均显示 41 / 41、流量、更新时间及合计，截图内文字完整；两张实屏裁至总览区域，位于 E:/temp/nexus-refresh-ui。已恢复浅色。管理菜单显示四档，从 12 小时改为 6 小时，保存重开确认后恢复 12 小时 |
| 差异安全扫描 | Codex Security 预检返回 `Review changes requires a non-bare Git worktree with a resolvable HEAD.`，未生成扫描报告 |

## 验证边界

上述设备回归和截图均来自最终功能构建。真实来源没有临期或不足 10% 的余量，本次截图只覆盖正常状态。没有等待 6/12/24 小时的真实墙钟周期。未测真机，未修改 Rust、协议或发布环境。

## 审查与修正（Claude）

在雷电上重跑：三个 APK 构建；默认 CatalogCheck、formats（含 usage）、refresh、refresh-source（Airport-Test：41 个节点，已用 84.2 GB / 总量 1200 GB）均返回 -1；WSL 互通 16 项全部取得回执；probe-source 37 / 41（失败的 4 个节点用官方 Xray CLI 同样失败；马来西亚节点一次超时，复测通过）。

修正两处：

- 周期任务由 15 分钟改为 1 小时，并用 `ExistingPeriodicWorkPolicy.UPDATE` 替换已登记的旧周期。从应用的 WorkManager 数据库确认 `subscription-periodic` 的间隔为 60 分钟。
- 订阅响应头有流量但没有到期时间时（实测机场即如此），原逻辑完全忽略信息节点。现在头部字段优先，缺少的到期时间和余量由信息节点补充。formats 新增两项断言。
