# 自研协议性能与容量优化

2026-11-19，分支 `android/subscription-vpn-acceptance`，改动尚未提交。

## 发现的问题与改动

| 问题 | 影响 | 改动入口 |
|---|---|---|
| 服务端 QUIC 单流接收窗口 64 KiB、单连接 1 MiB | 上行单流上限约 64 KiB / RTT，180 ms 时约 2.8 Mbit/s | `nexus-transport`：`tuned_transport()`，单流 2 MiB、单连接 16 MiB，两端共用 |
| 默认 Cubic 拥塞控制 | 随机丢包时速率塌缩 | 两端改用 Quinn BBR |
| 每次读取 16 KiB 或每个 UDP 包都执行一次 SQLite UPDATE 并 fsync | 邀请分享（注册表模式）吞吐受磁盘限制 | `access.rs`：每会话从剩余额度预留至多 1 MiB，本地扣减，结束或释放时退回；synchronous=NORMAL；复制缓冲 64 KiB |
| 服务端全局 64 个连接、64 个流 | 一台手机可占满一半，多人共享时拒绝新流 | 512 个连接、4096 个流 |
| 每连接 32 个流，Android 32 个 TCP 流 | 浏览器空闲长连接占满后新 SYN 被静默丢弃 | 每连接 256 个流；Android 192 个 TCP、64 个 UDP |
| 每次 DNS 查询新建 QUIC 连接并调用 Keystore 签名 | 每次解析多一次完整握手 | `nexus-client::open`：端口 53 走已认证 mux1 连接上的独立流；DNS 关联空闲 10 秒释放 |
| `tcp.accept()` 出错时退出 main | 瞬时错误断开节点上全部会话 | 记录错误后继续 |
| VPN 健康探测一次失败即重建会话 | 弱网下一次丢包就重置全部流 | 连续两次失败才重建 |
| 订阅下载不跟随重定向 | 302 到 CDN 的订阅地址导入失败 | `Subscriptions.fetch()`：最多 3 跳，仅限 HTTPS |
| 服务端到目标 TCP 未关闭 Nagle | 交互式流量额外等待 | `set_nodelay(true)` |

线路格式不变。DNS 走 mux1 流时由服务端已有的流上 UDP 帧路径处理，旧服务端同样支持（下方真机节点实测）。

## 已完成的验证

| 项目 | 结果 |
|---|---|
| Windows：`cargo fmt --check`、`clippy -D warnings`、构建、单元测试 | 通过。新增 5 个额度租约测试：精确扣满、超额整笔拒绝、退款（显式与释放时）、两会话并发不超额、撤销后拒绝 |
| Android 原生库 clippy（x86_64、aarch64） | 通过 |
| Linux（NexusLab Debian 13）：clippy、单元测试、`test_tunnel.py` 14 项、`test_node_registry.py` | 全部通过 |
| Linux 节点 release（x86_64、aarch64） | 最高依赖 GLIBC_2.34；已放入 `app/build/linuxNodes` 并打入 APK，哈希已核对 |
| Debug APK、AndroidTest APK | 构建成功。shoes 跳过重编译（固定版本未改动，沿用已有产物） |
| 雷电 emulator-5554，新客户端 + 现有私人 VPS 节点（旧版服务端） | 覆盖安装保留数据；全设备 VPN 连接；独立探针 UID 默认网络为 VPN、DNS 正确、example.com/org HTTPS 200 并核对正文、保留地址被拒绝；`verify_android_vpn_recovery.py` 断网等待与自动恢复通过，恢复后探针再次通过；断开后 TUN 消失、直连通过 |
| 弱网基准 | 见 [BENCHMARKS.md](../../BENCHMARKS.md) |

验收包：`验收包/Nexus-Android-0.1.0-Perf-QA.apk`（Debug，含新版 Linux 节点）。

## 环境注意

- Windows 版 `test_node_registry.py` 原先只接受 `ConnectionResetError`，Windows 报 `ConnectionAbortedError`，已改为两者均接受。Windows 上该测试在全部断言通过后，删除临时目录时数据库仍被占用，属清理问题；另外本机大量 UDP 端口被 Hyper-V 保留，随机端口偶发 10013。在 Linux 上运行无此问题。
- 仓库位于中文路径时，NDK 链接器在 Windows 上无法打开对象文件，shoes 重编译失败。需要重编译时设置 `NEXUS_SHOES_TARGET` 为纯 ASCII 路径。
- 真机 R5GL14PWDNA 未动。未做：非 DNS UDP 在设备上的回显（需 lab 回显服务）、新版服务端部署到真实 VPS、真实跨境线路测速。
