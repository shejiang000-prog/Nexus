# Android 节点真实连通测试

基线 `3e4da20`，本地分支 `android/node-real-test`。原 TCP 端口检测无法验证节点认证、协议转发、目标证书或 HTTP 响应，格式可转换的节点也可能无法使用。

## 实现

- 首页用「测试筛选结果的真实连通」替换 TCP 端口检测。测试启动时固定筛选结果，跳过信息节点和转换失败的节点，显示进度并允许取消。节点行给出结果，详情列出测试时间、各目标的状态和成功耗时。
- 每批最多启动一个 libXray 和一个 shoes 实例。每节点使用独立回环端口，每批生成临时 SOCKS 凭据；Xray 按 inboundTag 分流，shoes 的 mixed 监听分别使用自己的 client_chain。使用物理网络解析节点域名、绑定出站套接字；域名验证保留 TLS 主机名。真连与 VPN 共用引擎锁，网络改变或进入 VPN 时中止批次。
- 同时最多 8 个请求。主目标为 Cloudflare generate_204，期望 204；失败时重试 example.com，期望 200。每次尝试有独立的 8 秒截止时间，从本地 SOCKS 连接至收齐 HTTPS 响应头，包含认证、TLS 握手和请求传输。配置节点和启动引擎不计入节点延迟，计入批次总耗时。备用目标成功时保留两次尝试的详情。
- NodeQuality 以当前物理网络类型和 nodeKey 存储最后一次真连时间、结果、说明，并更新原有成功率和延迟评分。真连延迟排序将失败及未测节点排在成功节点后。自动选择排除最近一小时内最后一次真连失败的节点。总览和合计分别显示支持数量和最近 24 小时真连通过数量；无该时段记录显示「未测试」。
- 实验室原有 HTTP 回执模式继续保留。新增 `--probe` 模式使用同一个回环 HTTPS 服务、临时 CA 和测试主机名。测试先得到 404，再经同一个节点取得 200。临时 CA 只用于 instrumentation。

## 雷电验收

设备仅 `emulator-5554`，覆盖安装保留数据。网络测试前核对 Windows 默认路由，仅有物理网关。

| 项目 | 实际输出或观察 |
|---|---|
| 三个 Debug 包 | 指定 Gradle 命令最后输出 `BUILD SUCCESSFUL in 4s`，103 项任务，4 执行、99 已最新；三个 APK 覆盖安装均为 `Success` |
| 默认 CatalogCheck | `3 sources, isolated update, encrypted persistence, native outbound validation` |
| formats | `25 Clash, sing-box and link cases converted or refused as expected`；`3 metadata formats, strict negatives and header precedence passed` |
| refresh | `failure preserved snapshot; backoff, id, nodeKey, VPN primary/backup skip and disconnect catch-up passed` |
| 旧版 WSL 互通 | 原 7 种协议以及两种 REALITY、WS TLS、VMess gRPC/TCP 伪装、Trojan HTTPUpgrade、VLESS XHTTP、SS-2022、Hysteria2 salamander，16 项均输出 `node-side HTTP receipt` |
| 批量 HTTPS 实验室 | `16 / 16 passed, peak 8, 2160 ms`。此用例的总时间含一次取消测试及重新启动；每个节点均校验了回退前的 404 和回退后的 200 |
| 资源回收 | `normal and cancelled batches released all listeners; shoes stopped`。正常结束和请求执行中取消后逐一确认端口拒绝新连接，shoes 报告停止，随后可再次启动 libXray |
| 真实机场 Airport-Test | 两次分别为 `37 / 41 passed, peak 8, 21310 ms` 和 `37 / 41 passed, peak 8, 24640 ms`。新加坡-流媒体01、02、03 的最后一次尝试均为握手失败；台湾-住宅2 超时。失败名单与上一轮 probe-source 一致 |
| 活动 VPN | `active VPN: real-test button disabled, disconnect hint visible`。实际启动订阅 VPN，读取无障碍树核对禁用状态与提示，再断开 |
| 界面 | 实屏显示「已测 0 / 41」，点击取消恢复测试按钮。失败详情同时包含主站和备用站尝试。已目视检查浅色、深色总览和含两种结果的列表，文字可读，主题恢复浅色。四张截图位于 E:/temp/nexus-real-test-ui |

上述 instrumentation 及两种 WSL 验收的最终返回值均为 `INSTRUMENTATION_CODE: -1`。VPN 界面检查首次使用无障碍整页滚动时未找到按钮，改用半屏滑动后通过。

## 复验入口与边界

按前一篇交接的环境运行三个 Debug 构建目标和默认、formats、refresh 回归。CatalogCheck 新增 `probe-batch`（使用 `-e source` 选择已导入来源）、`probe-vpn` 和 `probe-lab`。分别运行原 `tools/verify_compat_interop.py` 命令及其 `--probe` 变体；脚本创建一次性对端，结束后删除 fixture。

未等待一小时或 24 小时自然过期。VPN 按钮实屏检查使用订阅 VPN，未单独启动私人 VPN 或第三方 VPN 验收。未操作真机、发布环境或远端分支。Rust、协议、TunnelService、依赖和 Manifest 均无改动。Codex Security 差异扫描预检未识别该工作树，返回 `Review changes requires a non-bare Git worktree with a resolvable HEAD.`，未生成扫描报告。
