# Android 分流 · 绕过大陆

基线为 `protocol/udp-mux` 的 `3eff9fd`，工作分支为 `android/split-routing`。私人 Nexus 与 Xray 节点已在雷电验证国内直连、其余走节点。TUIC / AnyTLS 保持全局代理，首页和节点行会提示实际行为。

## 用户可见行为

- 新装和升级时，只要 `local/bypassMainland` 尚无值，就采用「绕过大陆」。在「出口与诊断」选择模式，或点首页连接卡片上的当前模式。已连接时在现有 TUN 上重启转发引擎，原有连接需要重新建立。等待物理网络恢复时使用最后保存的模式。
- 按应用名单仍由 Android VPN Builder 执行。未选中的应用完全走物理网络。全局代理沿用各引擎原来的转发入口。私人配置的单目标别名及本地端口转发继续保留原语义。
- 选择绕过时，TUIC / AnyTLS 的节点行和已连接卡片均显示「此节点不支持绕过大陆，将全局代理」。浅色和深色模式都在雷电上实际点击并检查过。

## 转发与 DNS

| 接管方 | 实现 |
| --- | --- |
| 私人 Nexus | smoltcp 按普通 DNS 的问题名选上游。新流依据 DNS 学得的直连地址、私有或保留段及大陆 CIDR 开物理 TCP/UDP 套接字，其余继续调用 `nexus_client::open`。直连 TCP 等待目标连接成功才确认 SYN，连接失败时回 RST。服务器传输套接字继续使用原有物理网络绑定。 |
| Xray | 配置增加 `freedom/direct` 出站及域名优先、CIDR 其次的内联规则，开启 HTTP/TLS/QUIC 的 `routeOnly` 嗅探。Rust 与 Xray 之间通过保留 IP 包边界的 Unix socketpair 交接 TUN 流量。Rust 共用私人节点的 DNS 分类和 TTL 表，拦截国内 DNS 与已学习地址。其余 TCP DNS 通过应用私有目录的 Unix HTTP CONNECT 入站送到原解析器，UDP DNS 交给 Xray。端口 53 到节点的规则优先于静态 IP 规则，避免把未知域名送入国内解析器。全局模式直接给 Xray 系统 TUN fd。 |
| shoes | 固定版本 0.3.8（`7680938b96a245a21d9e481ad009205b956e4436`）可按地址和域名配置直连，上游 DNS 按监听器配置。它无法按每个查询名选上游并将应答按 TTL 路由。本版对两种设置均采用已有全局 TUN 配置。 |

国内域名的标准 TCP/UDP 53 查询先发往 223.5.5.5，失败再试 119.29.29.29。直连套接字先保护并绑定当前底层网络，再向目标发送。域名按标签边界匹配；不可可靠分类的请求走节点。学习前校验事务 ID、回显问题、记录所属名字和 CNAME 链，按链上最小 TTL 限制在 30–600 秒。学习表随会话释放，最多 8192 个地址，满表时先清过期项；不能容纳新地址时不返回未经路由的国内应答。

私人引擎可以从普通 DNS 获得域名，裸 IP 连接使用 IP 规则。App 自带 DoH/DoT 的查询内容不参与本机 DNS 分类。应用从 VPN 建立前的缓存直接使用地址时，私人引擎也只能按 IP 判断。Xray 还可利用明文 Host 或可嗅探的握手域名。

## 随包数据与体积

Gradle 的 `generateSplitRules` 在 `preBuild` 生成一份 UTF-8 asset。Rust 读取该文件，Xray 从相同字节生成内联规则。这样 APK 只存一份规则，两个引擎的输入版本一致。升级规则需要更新固定版本及摘要、运行生成测试、重新发布 App。Debug 可注入应用私有目录的测试规则和实验室解析器；发布配置只读 asset。

| 输入 | 固定版本 | SHA-256 |
| --- | --- | --- |
| [domain-list-community](https://github.com/v2fly/domain-list-community/releases/tag/20261118141523) 的 `dlc.dat_plain.yml`，已展开的 `cn` 类别 | `20261118141523` | `20352b9263f6e3db420465efb565a7a0345ddde535afedf92515a7bb0ffa4dd8` |
| [china-operator-ip](https://github.com/gaoyifan/china-operator-ip/tree/be3de75a9698c50193c35c1215509e89d5d5cf6f) 的 `china.txt` | `be3de75a9698c50193c35c1215509e89d5d5cf6f` | `e2c280f8adfd3a5f16e0cb5e518193b7f4487f010b5e92ac7b6513317904bb8f` |
| 同一版本 `china6.txt` | 同上 | `225a126c2b779d3962861558b6b1435d667da189b036bbdfb31f544974356c59` |

两个数据项目的 MIT 声明见根目录 `THIRD_PARTY_LICENSES.md` 和 APK 内 `assets/licenses/split-routing/`。生成器另外并入 Nexus 服务端 Policy 的保留段。最终有 6713 条后缀、44 条完整域名、3 条正则、6242 个 IPv4 和 3453 个 IPv6 前缀。Rust 只识别当前固定版本的三条正则，更新上游时应同步核对。

| 可比 Debug APK | 字节 |
| --- | ---: |
| 基线，两个 ABI | 155444959 |
| 本次，两个 ABI | 155869265 |
| 增量 | **424306（414.4 KiB）** |
| 其中规则文件原始 / APK 压缩后 | 320677 / 78485 |

体积取自两个独立空 Gradle 构建目录，使用各自版本的 Rust JNI 和一致的 shoes、libXray、Linux 节点及签名材料。工作目录增量打包可能保留 ZIP 空隙，不宜用于计算增量。

## 构建与回归

环境为 Windows、WSL NexusLab（Linux target 在 Linux 文件系统）及雷电 `emulator-5554`。网络测前用 `route print` 和活动默认路由接口核对，未出现 singbox_tun 默认路由。Android 构建设置 `NEXUS_SHOES_TARGET=E:/temp/nexus-shoes-target`，复用既有 shoes 产物，安装使用 `-r`。

| 检查 | 实际输出或计数 |
| --- | --- |
| Windows / Linux `cargo fmt --check`，workspace 全目标 Clippy，`-D warnings` | 两个平台均退出 0 |
| Windows / Linux `cargo test --workspace` | 各 14 passed，0 failed；分流的 3 组分别为 `dns_and_expiry`、`ipv6_answer_follows_the_question_chain`、`domains_and_cidrs`，覆盖正常、截断、压缩指针环、超长名、地址边界与 TTL |
| Windows / Linux `tools/test_tunnel.py`，带 `a14ec74` 旧版二进制 | 各 `18 integration checks passed`，含 `udp1 interoperability with baseline server` 及 `baseline client` |
| Android NDK release Clippy | `x86_64-linux-android`、`aarch64-linux-android` 均 `Finished release profile` |
| Android App、AndroidTest、vpnprobe Debug | 三个 assemble 目标 `BUILD SUCCESSFUL` |
| `tools/test_split_rules.py`，固定源冷缓存、重复生成与损坏输入 | `PASS reproducible snapshot (320677 bytes)`；`PASS checksum mismatch rejected` |
| CatalogCheck 默认 | `3 sources, isolated update, encrypted persistence, native outbound validation` |
| formats | `25 Clash, sing-box and link cases converted or refused as expected`；`3 metadata formats, strict negatives and header precedence passed` |
| refresh | `failure preserved snapshot; backoff, id, nodeKey, VPN primary/backup skip and disconnect catch-up passed` |
| interop / probe-lab | 16/16 均有 `node-side HTTP receipt`；`16 / 16 passed, peak 8, 1520 ms`；`normal and cancelled batches released all listeners; shoes stopped` |
| 私人节点全设备 vpnprobe，两模式各断网恢复一次 | 各在断网前和恢复后返回 `INSTRUMENTATION_CODE: -1`，`default=VPN`、两个 HTTPS 200、`32,1200,1472 byte echoes`、IPv4/IPv6 的 `TCP connection refused before handshake`。两次均观测到等待网络、保留 VPN 路由、重新认证并就绪 |
| 按应用名单 | 只选 Nexus 后，独立 vpnprobe 报 `default=ordinary network`，两个 HTTPS 200，返回 -1 |
| UI | 实际点击首页模式开关后 `home toggle reconnected; card, settings and node warning captured`。首页、设置和节点行的浅色/深色 PNG 在工作区外 `E:/temp/nexus-split-data/` |

探针的负例此前只等 6 秒，早于私人引擎已有的 8 秒建连截止，绕过模式初测出现探针自身超时。负例观察窗口改为 12 秒后，实测能收到 RST。

## 双路径实验与公网出口

`tools/compat_lab.py --split` 提供两个 TCP/UDP 目标和两个 DNS 解析器。测试使用 `local.split.test` 和 `remote.split.test`。前者的应答地址刻意排除在静态 IPv4 列表之外，能直连即实际用到了学习表。目标按所见源地址返回 `direct` 或 `node`；节点侧另用 Linux `ss` 核对发送套接字所属进程，只记录引擎和协议。国内实验解析器遇到非大陆查询会拒绝回答。

| 引擎 | 全局 TCP / UDP | 绕过 TCP / UDP | 节点侧证据 |
| --- | --- | --- | --- |
| 私人 | 两个查询及两个目标均经节点 | 本地域名和目标直连，其余经节点 | `NODE nexus-server TCP`、`NODE nexus-server UDP` |
| Xray | 同上 | 同上 | `NODE xray TCP`、`NODE xray UDP` |
| shoes | 两个查询及两个目标均经节点 | 两个查询及两个目标仍经节点 | `NODE shoes TCP`、`NODE shoes UDP` |

三个引擎各四项均输出 `both DNS queries and both source receipts matched`，各返回 -1。私人及 Xray 绕过模式下实际出现 `DNS domestic local direct`、`DNS node-resolver remote node`，以及各自的 TCP/UDP 直连与节点回执。复跑入口为 CatalogCheck 的 `split-lab` 或 `split-public` 模式，`-e engine private|xray|shoes`。前者从 Debug 应用私有的 `no_backup/split-nodes.json` 读取临时夹具；私人节点测试需要先装入 `android_lab.py` 签发的实验室配置。

公网使用雷电现有 Airport-Test 的 Xray、shoes 节点及一个临时 WSL 私人节点。设备内比较国内查询站点和 Cloudflare trace，只输出比较结果。

| 节点 | 全局国内 vs 全局境外 | 绕过国内 vs 物理 | 绕过国内 vs 全局 | 绕过境外 vs 全局 |
| --- | --- | --- | --- | --- |
| Airport-Test / Xray | 一致 | 一致 | 不一致 | 一致 |
| Airport-Test / shoes | 一致 | 不一致 | 一致 | 一致 |
| WSL 私人 | 一致 | 一致 | 一致 | 一致 |

私人节点和模拟器共享公网 NAT，其公网比较不能单独区分路径。上面的实验室进程与目标回执能区分二者。测试结束后已在 App 停止状态恢复原偏好目录，删除测试注入，系统恢复浅色，最终设备输出 `Device "tun0" does not exist.`。

## 1 MiB 测速与已知边界

使用现有 `NodeQuality.measure` 对真实 Xray 节点测代理 HTTPS。下表每格为下载 / 上传，单位 Mbps；每轮同一节点先全局、后绕过。

| 轮次 | 全局 | 绕过 |
| --- | ---: | ---: |
| 首次 | 3.70 / 8.95 | 6.69 / 1.17 |
| 复测 1 | 5.07 / 2.57 | 4.73 / 6.05 |
| 复测 2 | 3.01 / 10.94 | 3.91 / 12.39 |
| 复测 3 | 3.13 / 15.45 | 5.33 / 5.33 |
| 最终 | 3.25 / 12.04 | 8.04 / 9.29 |

同一 WSL Xray 节点、完整 HTTP 请求、每方向五次中位数：全局 419.43 / 359.51，绕过 629.15 / 314.57 Mbps；最终带节点回执复测为全局 503.32 / 251.66，绕过 838.86 / 503.32 Mbps。实验室 HTTP 和公网下载均未显示持续降速，公网短上传波动很大，不能证明性能相同。

另一个非 HTTP 实验只先发送五字节指令，计时包含等首包；全局下载 629.15、绕过 38.13 Mbps。Xray 对无法立即判断的协议有约 200 ms 的嗅探等待，短指令后等待服务端响应的应用可能看到这个额外时延。试验性改为阻塞写入后仍只有 36.47 Mbps，该改动已撤回。

未验证真实公网 IPv6 分流、应用自带加密 DNS 的域名识别或真实 VPS。IPv6 已验证报文解析、前缀边界及 TCP 否定用例。正式 Codex Security 差异扫描未出报告：预检可识别本地 HEAD，启动扫描返回 `Review changes requires a non-bare Git worktree with a resolvable HEAD`。上述功能测试及本地代码审查独立完成。

## 审查（Claude）

**修复：Android「私人 DNS · 自动」会让国内域名分流失效。** Android 9 起默认开启自动模式，会对 VPN 的 DNS 地址尝试 853 端口 DoT。校验通过后，所有查询都走 TLS，原生层只拦截 53 端口，看不到查询名。结果是国内域名不再走国内 DNS，学习表一直为空，只剩 CIDR 判断；Xray 还能靠嗅探判断去向，但域名仍由节点侧解析。用户实际配置（私人节点，VPN DNS 为 1.1.1.1，服务端开公网出口）会触发这个问题。

复现：雷电 `emulator-5554`，临时 NexusLab 私人节点，VPN DNS 设为国内可达的 DoT 服务器 223.5.5.5。

| 条件 | `dumpsys dnsresolver` 中 223.5.5.5:853 | 设备上 TCP 到 223.5.5.5:853 |
|---|---|---|
| 修复前，全局 | `state=success` | 连通 |
| 修复前，绕过 | `state=success`（第一次连接出现过一次 fail，重试成功） | 连通，53 ms |
| 修复后，全局 | `state=success` | 连通，49 ms |
| 修复后，绕过，私人节点（3 次） | `state=fail` | 6–13 ms 内被拒 |
| 修复后，绕过，Airport-Test Xray 节点 | `state=fail` | 20 ms 内被拒 |

修复后两条路径的 53 端口解析、国内 IP 直连均正常。经机场节点到 1.1.1.1:853 的 DoT 在两种模式下本来就失败，所以真实机场节点无法直接演示这个问题。

改法：
- **TunnelService / SplitRouting：** 绕过模式在规则字节前加一行 `refuse-dot:<VPN DNS>`。如果物理网络的 `LinkProperties.privateDnsServerName` 非空（用户手动指定了私人 DNS 主机名，即严格模式），就不加这一行，以免断网。Xray 配置生成时跳过这一行。
- **Rust：** 前导指令行（`resolvers:`、`refuse-dot:`）的解析移到 `split::directives`，顺序任意。`Forwarding::open_tcp` 对「VPN DNS:853」直接回 RST。Xray 模式下，这类 SYN 交给原生栈处理，以便回 RST。
- **新单测：** `directive_lines`。

**其他核查（无需改动）：**
- **国内域名列表：** `cn` 类别 6760 条中没有 `@!cn` 条目。带属性的 550 条是 `@ads`、`@cn` 等，去掉属性后合并，结果正确。
- **并发 DNS：** Xray 绕过模式下 100 个不同国内域名并发解析，全部在 183 ms 内返回，没有触发 64 个 UDP 会话上限造成丢查询。
- **强杀后重连：** Xray 绕过运行中 `am force-stop` 后，`cache/split-dns.sock` 和 `.lock` 会残留。重新连接时 Xray 自行清理，VPN 正常，探测全部连通。
- **嗅探等待：** 交接文档记录的 Xray 嗅探等待确实存在：只发 1 字节、未被学习的国内 IP，直连约慢 1 秒；被学习表命中的地址走原生直连，不受影响。TLS/HTTP 首包能立即嗅探。这属于 Xray 机制，保留为已知限制。

**修复后复验：**
- **Windows：** fmt 无差异；workspace 全目标 clippy 无警告；单测 split 4、protocol 2、server 7、transport 2，均通过。x86_64 / aarch64 Android release clippy（`-D warnings`）均通过。
- **Linux（NexusLab，rustc 1.100.0）：** 单测同上；`test_tunnel.py` 带 a14ec74 基线 `18 integration checks passed`；`test_split_rules.py` 两项 PASS。
- **雷电 split-lab：** private、xray、shoes 三个引擎均为 `INSTRUMENTATION_CODE: -1`，共 14 项结果。私人节点和 Xray 在绕过时为「本地域名与目标直连，其余经节点」，shoes 全部经节点。Xray 实验室 1 MiB：两种模式均为下行 503.32 / 上行 251.66 Mbps。
- **雷电 split-public（Airport-Test Xray）：**
  - 绕过时，国内站点出口与物理网络一致，与全局不一致。
  - 境外站点出口与全局一致。
  - `-1`。
- **收尾：** 每次测试都在 App 停止后恢复原偏好，确认 tun0 消失，并删除设备上的临时脚本。

**仍未覆盖：**
- 真实 VPS 上经 1.1.1.1 的 DoT 拦截。按约定没有连用户的 VPS。
- 严格模式私人 DNS 下仍无法按域名分流。这是用户的主动选择，界面暂无提示。
