# 网络基准

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
