# 订阅兼容性

2026-11-19，分支 `android/subscription-vpn-acceptance`，改动尚未提交。只在雷电 `emulator-5554` 上验证，真机未动。

## 发现的问题与改动

| 问题 | 影响 | 改动入口 |
|---|---|---|
| Clash/sing-box 结构化节点带 `reality-opts`、`tls.reality` 即拒绝 | 机场 Clash 订阅里常见的 VLESS + REALITY 全部不可用 | 新增 `NodeFormats.kt`，转为 libXray 分享链接（`security=reality`、`pbk`、`sid`、`fp`） |
| 结构化节点只接受 tcp、ws、grpc | HTTPUpgrade、XHTTP、TCP HTTP 伪装不可用 | 支持 `ws-opts.v2ray-http-upgrade`、sing-box `httpupgrade`、`xhttp-opts`、Clash `network: http`；WebSocket 早期数据转为 `?ed=` |
| Hysteria2 的 `obfs`、端口跳跃被拒绝 | salamander 混淆节点不可用 | Clash `obfs`/`ports`/`hop-interval` 与 sing-box `obfs`/`server_ports`/`hop_interval` 转为 libXray 参数 |
| Clash `client-fingerprint`、`fingerprint` 被拒绝 | 带 uTLS 指纹或固定证书的节点不可用 | 前者转 `fp`；后者是叶证书 SHA-256，转 Xray `pcs`（同等校验） |
| sing-box 的 `network` 被误当作传输方式 | sing-box 节点写 `network: udp` 时被拒绝 | sing-box 只读 `transport` |
| v2rayN VMess JSON 的 `"sni": ""` 被当作有效值 | CDN 节点 SNI 为空，TLS 失败 | 空值回退到 Host；同时支持 httpupgrade、xhttp、TCP HTTP 伪装、`scy`、`fp`、`alpn` |
| 分享链接 TLS 未写 `sni` 时 libXray 用服务器地址 | 地址为 IP 或公共 CDN 名的 WebSocket 节点握手失败 | `NodeFormats.hostAsSni`：按 v2rayN 行为用 `host` 补 `sni` |
| YAML 1.1 把 `short-id: 0123` 读成八进制 83，`password: 1_000` 读成 1000 | REALITY 认证失败、密码错误 | `TextResolver`：YAML 标量除 true/false、null 外一律保留文本 |
| TUIC 链接带 `congestion_control`、`peer` 等常见参数即拒绝 | 面板导出的 TUIC 链接基本不可用 | `ShoesEngine` 忽略不影响连接的参数，`peer` 作为 SNI 别名；Clash `fingerprint` 固定证书；`disable-sni: false` 不再拒绝 |
| 订阅首行不是已知协议（如 `ssr://`）时整份按 YAML 解析 | 混有不支持协议的 Base64 订阅导入失败 | 任一行是已知协议即按链接列表解析 |
| 不支持的节点只在点连接时报“节点配置无法使用” | 用户不知道原因，自动选择会选到无法使用的节点 | `UnsupportedNode` 带中文原因；列表红字显示、连接按钮禁用、来源卡片显示数量、自动选择排除；连接失败提示具体原因 |
| 下载订阅使用 Java 默认 User-Agent | 部分面板拒绝或返回非预期格式 | `Nexus/0.1.0 (compatible; v2rayN)`，面板据此返回 Base64 链接 |

仍拒绝：跳过证书验证、自定义 CA、ECH、Shadowsocks 插件、HTTP/2 与 QUIC 传输（Xray 已移除）、VMess alterId 非 0、旧式流加密、TUIC v4、链式代理。Hysteria2 的 `up`/`down` 不转换，使用 BBR。

## 已完成的验证

| 项目 | 结果 |
|---|---|
| `CatalogCheck` 新模式 `formats`（雷电，真实 libXray） | 25 项通过：Clash 15 项、sing-box 5 项（另有 selector、direct 未导入）、Base64 链接 5 项（含 `ssr://` 首行、v2rayN VMess JSON）。逐项核对转换结果中的关键字段或拒绝原因 |
| `tools/verify_compat_interop.py`（雷电 → WSL 一次性对端） | 16 个节点全部取得节点侧 HTTP 回执：原有 7 种协议，加 Clash/sing-box 两种 VLESS REALITY + Vision、WebSocket TLS（Host 作 SNI、证书指纹）、VMess gRPC TLS、Trojan HTTPUpgrade、VLESS XHTTP、VMess TCP HTTP 伪装、sing-box SS-2022、Hysteria2 salamander |
| `CatalogCheck` 默认模式 | 通过 |
| `verify_subscription_vpn.py --links --protocol vless --protocol tuic --ld` | 全设备 VPN：VLESS → Trojan、TUIC → Shadowsocks 首选与停机切换，浏览器与独立应用 DNS/TCP/UDP 均通过；双网关闭后自动恢复，测速下行 3.58 Mbps、上行 5.28 Mbps；断开后直连通过 |
| 界面 | 来源卡片显示“15 个节点（5 个暂不支持）”，节点行红字显示原因、按钮为“仅导入” |

## 真实机场订阅

用户提供的一个机场订阅（地址未写入仓库），HTTPS 导入得到 41 个节点：29 个 VLESS + REALITY + Vision（uTLS `ios`），12 个 AnyTLS。全部可转换，流量信息显示为“已用 80.3 GB / 1200 GB · 剩余 1120 GB”。

| 项目 | 结果 |
|---|---|
| `CatalogCheck -e mode probe-source -e source <名称>` | 37 / 41 个节点经应用自身转换与引擎取得 https://example.com（证书正常校验）。失败的 3 个新加坡节点和 1 个台湾节点，在 WSL 用官方 Xray CLI 同样失败，属节点侧问题 |
| 全设备 VPN | VLESS REALITY 与 AnyTLS 各一个节点：DNS、example.com/org HTTPS 通过；应用内测速分别为下行 4.68 / 上行 18.00 Mbps、下行 6.64 / 上行 8.34 Mbps |

发现并修复的问题：Xray 只用物理网络的第一个 DNS 服务器解析节点域名。雷电沿用了主机 v2rayN TUN 的 DNS（172.18.0.2），TUN 关闭后该地址失效，所有以域名为地址的 VLESS 节点无法连接；真机的第一个 DNS 不可达时同样会发生。`CompatEngine.resolved` 改为先用物理网络的系统解析器（会尝试全部 DNS 并遵循私人 DNS）解析，再交给 Xray；TLS 未写 SNI 时保留域名作 SNI。shoes（TUIC、AnyTLS）原本就这样解析。

测试注意：主机 v2rayN 开启 TUN 时，WSL 与雷电的流量都会先经过它的路由。该订阅 AnyTLS 节点的 SNI 在那条路径上被中断，在 TUN 开启时得出的结论不可靠。测试前确认 `route print` 中没有 `singbox_tun` 默认路由。

未做：REALITY 节点经 `TunnelService` 的全设备 VPN 已用真实节点验证，但未覆盖切换与断网恢复。

## 复验

```powershell
$labHost = ((wsl -d NexusLab -- hostname -I).Trim() -split '\s+')[0]
adb -s emulator-5554 shell am instrument -w -e mode formats dev.nexus.localclient.test/dev.nexus.localclient.CatalogCheck
python -u tools/verify_compat_interop.py --serial emulator-5554 --host $labHost --xray /opt/nexus-lab/xray --shoes /opt/nexus-lab/shoes
```

## 环境注意

- NexusLab 的 `/tmp` 是 tmpfs，WSL 空闲关机后清空。Xray 26.11.9 与 shoes 0.3.8 已放到 `/opt/nexus-lab`，来源是 `E:/temp/nexus-bootstrap-tools` 下的压缩包。
- REALITY 的目标站必须把 EncryptedExtensions、Certificate、CertificateVerify、Finished 分别放在独立记录里（Go 与多数线上站点如此）。OpenSSL 会合并成一条，服务端判为无效连接。`compat_lab.py --variants` 因此用同一 Xray 进程里的 Go TLS 入站作诱饵站。
- 从 Git Bash 调用时设置 `MSYS_NO_PATHCONV=1`，否则 `/opt/...` 参数会被改写成 Windows 路径。
