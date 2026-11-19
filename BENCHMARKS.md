# 网络基准

## 2026-11-22 UDP 关联复用

同一 NexusLab（Debian 13、WSL2）上的两个独立 Linux 网络命名空间，通过 veth 和双向 netem 相连。每格重新启动 release 服务端与客户端驱动，硬件、密钥类型、目标回显服务和驱动源码相同。baseline 为 `a14ec74` 的客户端库和服务端；mux2 为本分支。驱动直接调用 Android 同样使用的 `nexus_client::open`，每格 32 个并发关联共享一个冷启动 Connector。这里使用文件设备密钥，未测 Android Keystore 签名耗时。

每关联发送 21 个 64 字节带序号的报文，逐字节验证回显。首回显时间从应用提交首个报文（调用 OPEN 前）至实际收到第一个正确回显；首包丢失时包括 2 秒等待及后续尝试。持续阶段为序号 1 至 20，每关联至多一个未完成请求，2 秒未收到记丢失。每格有 32 个首回显和 640 个持续样本，每个条件各一批。分位数为最近秩，延迟只统计收到的回显。[全部 5376 行原始样本](docs/benchmarks/2026-11-22-udp-mux.csv)

| RTT / 每方向丢包 | 版本 | 首回显 P50 / P95（ms） | 持续丢失 | 持续回显 P50 / P95（ms） |
|---|---|---:|---:|---:|
| 80 ms / 0% | baseline | 843.5 / 1010.6 | 0/640（0.00%） | 80.3 / 80.4 |
| 80 ms / 0% | mux2 | 323.9 / 324.9 | 0/640（0.00%） | 80.4 / 80.7 |
| 80 ms / 3% | baseline | 766.8 / 2882.7 | 41/640（6.41%） | 80.3 / 80.5 |
| 80 ms / 3% | mux2 | 324.1 / 325.2 | 31/640（4.84%） | 80.4 / 80.7 |
| 180 ms / 0% | baseline | 1066.6 / 1232.1 | 0/640（0.00%） | 180.3 / 180.4 |
| 180 ms / 0% | mux2 | 724.0 / 724.9 | 0/640（0.00%） | 180.4 / 180.7 |
| 180 ms / 3% | baseline | 1131.0 / 3202.4 | 37/640（5.78%） | 180.3 / 180.5 |
| 180 ms / 3% | mux2 | 724.7 / 928.4 | 39/640（6.09%） | 180.4 / 180.7 |

此次四个条件下，复用连接均降低了首回显中位数。持续阶段的成功回显接近链路 RTT；有损条件下 DATAGRAM 仍可丢失。每格只有一次冷启动批次，不能从尾部分位数或小幅丢包率差异推断公网表现。

复验时将 `crates/nexus-client/examples/udp_associations.rs` 复制到基线源码的相同位置，两边分别执行 `cargo build --release --bins --examples`，然后运行：

```sh
unshare -n -- python3 tools/benchmark_link.py --udp --bin-dir /path/to/new/release \
  --baseline-dir /path/to/baseline/release --parallel 32 --count 21 \
  --rtt 80 180 --loss 0 3 --output /tmp/udp-mux.csv
```

## 2026-11-19 传输调优前后（BBR + 大窗口）

同一 Debian 13 WSL2、两个独立网络命名空间、netem 双向对称时延与随机丢包，release 构建。“旧版”为调优前提交，“新版”为本次改动。每笔 HTTP POST 512 KiB 并逐字节核对 512 KiB 回显，并发 2，每格 6 笔（另有 1 笔预热），全部成功。Hysteria2 / TUIC 为 shoes v0.3.8 默认配置。中位耗时如下（毫秒，越小越好）。

| 往返时延 / 每方向丢包 | 旧版 Nexus QUIC | 新版 Nexus QUIC | Hysteria2 | TUIC | Shadowsocks |
|---|---:|---:|---:|---:|---:|
| 80 ms / 0% | 899 | **363** | 414 | 408 | 1042 |
| 80 ms / 3% | 2869 | **420** | 2556 | 631 | 4834 |
| 180 ms / 0% | 2045 | **459** | 922 | 915 | 2523 |
| 180 ms / 3% | 6334 | **854** | 1755 | 1578 | 6309 |

8 KiB 短请求（并发 4，每格 12 笔）的中位数基本不变：80 ms 两种丢包均为 161 ms；180 ms 无丢包为 361 ms，3% 丢包由 541 ms 降至 362 ms。TLS/TCP 回退仍受内核 TCP 拥塞控制限制，与调优前相当。原始数据：[大流量旧版](docs/benchmarks/2026-11-19-bulk-old.csv)、[大流量新版](docs/benchmarks/2026-11-19-bulk-new.csv)、[短请求旧版](docs/benchmarks/2026-11-19-short-old.csv)、[短请求新版](docs/benchmarks/2026-11-19-short-new.csv)。样本小、在单机虚拟网络中测得，真实跨境 VPS 和运营商网络需另测。

```sh
sudo unshare -n -- python3 tools/benchmark_link.py --bin-dir /abs/release --shoes /abs/shoes \
  --output /tmp/bulk.csv --rtt 80 180 --loss 0 3 --size 524288 --count 6 --parallel 2
```

## 2026-11-18 短请求基线

2026-11-18 在同一 Debian 13 WSL2 的两个独立网络命名空间内，用 netem 配置 20、80、180 ms 往返时延和每方向 0%、1%、3%、5% 随机丢包。Rust release 版本测试 Nexus QUIC、Nexus TLS/TCP，shoes v0.3.8 测试 Hysteria2、TUIC v5、Shadowsocks AES-128-GCM。五个客户端进程持续运行，每个条件先预热一笔，再以并发 4 发送 12 笔 8192 字节 HTTP POST，逐字节核对回显。720 笔请求均成功。[逐次原始数据](docs/benchmarks/2026-11-18-netem.csv)

80 ms、每方向 3% 丢包时的应用请求耗时如下。

| 载体 | P50（ms） | P95（ms） |
|---|---:|---:|
| Nexus QUIC | 182.75 | 241.64 |
| Nexus TLS/TCP | 535.21 | 1692.35 |
| Hysteria2 | 108.58 | 241.38 |
| TUIC | 82.19 | 161.79 |
| Shadowsocks | 162.08 | 719.80 |

每格 12 个样本，最近秩法的 P95、P99 均为该格最大值。本实验测量短请求，结果受入口适配、加密方式、机器负载和随机丢包影响。真机电耗、实际运营商网络、持续吞吐和长连接需要另测。

```sh
sudo unshare -n -- python3 tools/benchmark_link.py --bin-dir /absolute/path/to/release --shoes /absolute/path/to/shoes --output /tmp/nexus-results.csv
```

后续在同一授权 VPS 和同一设备复测，增加 VLESS + REALITY、1 / 8 / 32 并发、持续上传下载、连接恢复、CPU/RSS 与长连接。保留失败分母，分别报告 UDP 封锁与网络切换。

## UDP 不通时的连续连接

2026-11-19，Debian 13 WSL，release 构建。客户端连接同一台只转发 TCP 的测试服务器，UDP 套接字持续丢弃 QUIC 包。一个客户端进程连续发起两笔 512 KiB POST，逐字节检查回显。改动前耗时分别为 8.05 秒、8.09 秒，改动后为 8.05 秒、0.09 秒。数据来自这一组回环实验。

自动模式在一次 QUIC 建连超时且 TLS 身份验证成功后，保留 30 秒的 TCP 优先期。后续流量不会延长该期限。期限结束后重新探测 QUIC，Android 切换底层网络后会创建新连接器。显式 QUIC 模式、证书错误处理和设备授权沿用原有逻辑。复验使用 `tools/test_tunnel.py` 的 “UDP blocked, two authenticated requests” 用例。
