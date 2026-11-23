# V2 客户端传输接口 · M0 冻结版

对应实现提交 `607a3f8`。

## 变更记录

- 2026-11-23 · M3（`2a566ea`、`56bf5b9`）。Android 增加独立邀请兑换绑定和 TCP 半关闭。调用方可选用 `Stream.closeWrite()`。Go 的 `DialTCP` 保持 `io.ReadWriteCloser` 返回类型，需要半关闭的调用方可断言 `interface{ CloseWrite() error }`。现有调用方可沿用原代码。
- 2026-11-23 · M0。固定默认 H2 握手配置，Go 与移动端增加可选节点解析回调。原有移动端调用在 Protector 与 Observer 之间补入 Resolver，可传 null。此后接口变更须在本节注明提交、调用方影响和迁移方式。

桌面 Go 客户端和 Android 共用仓库根目录下的 `v2/`。模块路径为 `github.com/shejiang000-prog/Nexus/v2`，便于导入两个平台共用的实现，并让 Rust workspace 和 V1 独立构建。Android 产品 VPN 通过合编引擎使用 gomobile 绑定。

## 生命周期和拨号

|Go 接口|调用约定|
|---|---|
|`transport.Start(parent context.Context, c transport.Config) (*transport.Client, error)`|等待第一个通过证书校验和签名的通道就绪。连接持有期间保持 parent 有效。|
|`c.State() string`、`c.Stop()`|状态为 h3、h2 或 stopped。Stop 可重复调用，取消使用中的通道。调用方仍应 Close 自己的业务句柄。|
|`c.DialTCP(ctx, target, initial) (io.ReadWriteCloser, error)`|target 为带显式端口的主机名或 IP。最多可附带 16 KiB 乐观数据。收到 2xx 及认证确认头后才返回流。ctx 管理流的整个生命周期。|
|`c.OpenUDP(ctx, target) (*transport.Association, error)`|在成功响应及 capsule 协商后返回。ReadPacket、WritePacket 各处理一个报文，允许空报文，最大载荷 65,507 字节。调用 Close 结束关联。|

`Authority` 是验证证书与生成 exporter context 的入口，始终包含端口。`Endpoint` 是实际拨号地址，默认等于 Authority。Roots 为空时用系统信任库。ClientHello 可选，接受调用方提供的受信 H2 配置样本。缺省使用内置 Chrome 157 配置。固定版本的 uTLS 生成各连接独有的随机数、会话 ID、密钥份额和 SNI，生成文件仅保存签名算法列表及公开的信任锚标识。Device 为 16 字节 ID。Sign 接受完整待签字节，须用 ECDSA P-256/SHA-256 签名并返回 64 字节大端 r||s。连接竞速可能并行向签名器提出请求，每条成功建立的传输连接各有自己的证明。

`Mode` 支持 auto（默认）、h3、h2。HealthPath 默认是同源的 `/health.bin`，部署应在此提供可读取的 32,768 字节静态对象。OnState 收到初始通道和切换成功后的通道名，由单独的 goroutine 串行回调；回调应迅速返回。Stop 后可通过 State 查询终止状态。每个物理网络或入口配置使用独立 Client，变更时调用 Stop 并重建。VPN 层负责传入 Protector。节点返回多个地址时依次尝试，每次在剩余截止时间内给下一地址留出机会。服务端核验全部 DNS 结果后才拨号。UDP 的 connect 只能反映本地路由及套接字错误，无响应时不会迁移旧关联。

首次竞速给 H3 250 ms 先发时间。已有业务的 H3 首个静默窗口预建 H2（相邻预建至少间隔 10 秒），两个 500 ms 弱进展窗口后比较两条连接的同源健康对象。H2 可用且 H3 无响应或超过 H2 用时的四倍时，新流转到 H2。H3 仍有收包时旧流排空，持续静默再主动关闭。H2 至少驻留 30 秒才探测回切。旧流上的数据不会自动转发到新连接。H3 路径 MTU 放不下的报文退回关联自己的 capsule。

## 错误和重试

用 `errors.As(err, &transportError)` 检查启动和打开业务流返回的 `*transport.Error`。连接已打开后的 Read/Write 返回底层 I/O 错误或 EOF。严格区分拨号失败和应用已经写出的数据。

|Code|意义及调用方动作|
|---:|---|
|1|配置或输入不合法，修正配置或报文大小。|
|2|证书验证失败，检查入口、信任锚和时间。|
|3|设备签名或认证确认失败，检查该设备的绑定。|
|4|并发或资源不足，遵从调用方退避策略。|
|5|目标被策略拒绝、解析或拨号失败，检查目标。|
|6|传输中断。重新打开业务前确认其重试语义。|
|7|乐观数据可能已经发出且没有最终响应，结果未知。非 2xx 按具体状态码返回，2xx 缺少确认标记返回 3。有副作用的请求交给业务层核对。|

## Android

gomobile 包 `mobile` 的 `Mobile.start(authority, endpoint, mode, ca, id, hello, signer, protector, resolver, observer)` 返回 Client。可提供 PEM 信任锚或使用系统信任；id 必须为 16 字节。Java 实现 `Signer.sign(byte[] message): byte[]`（可抛异常），调用 Android Keystore 的 `SHA256withECDSA` 签原消息。返回 ASN.1 DER，Go 验证整数范围并转换为 r||s。私钥留在 Keystore 中。Observer.stateChanged(String) 接收通道名。Stream.read(max) 限 1–65,536 字节，write 返回写入长度。Association 按包读写；绑定层可能把空字节数组映射为 null，成功返回时两者都代表空报文。捕获原始 Exception 后可调用 `Mobile.errorCode(exception)` 直接取得 Go 错误码；传入 null 返回 0，普通 I/O 异常返回 6。

普通 CONNECT 的 H2 UDP 使用目标 authority 与单个 `Nexus-Flow: udp`，默认 Go 标签即可运行。H3 仍使用 RFC 9298。可选的 `Config.Protect(fd int) bool` 与 Java `Protector.protect(int fd)` 在各候选 TCP 套接字 connect、UDP 套接字 bind 前同步执行。VPN 调用方应调用 VpnService.protect(fd)，失败返回 false 并以错误码 1 停止。回调不得缓存或关闭 fd。可选的 `Config.Resolve(ctx, host) ([]netip.Addr, error)` 为 H2、H3 及后续预建和探测解析节点域名。提供回调后，所有拨号只使用它返回的字面地址，按原顺序分配剩余时限逐一尝试。空列表、非法地址或回调出错返回码 6。IP 字面量直接拨号。未提供回调时由系统解析。证书仍验证 Authority。

Android 用同一底层网络创建解析器，返回以逗号分隔的 IPv4/IPv6 字面地址，禁止返回主机名。回调可并发，避免在 UI 线程启动客户端。网络发生变化后重建 Client。同步 Java 回调须及时返回，Go 取消上下文无法抢占正在执行的 Java 查询。

```kotlin
val resolver = mobile.Resolver { host ->
    underlyingNetwork.getAllByName(host).joinToString(",") { it.hostAddress!! }
}
```

Android 的邀请兑换使用独立绑定 `enrollment.Enrollment.parse` 和 `redeem`。先解析并展示域名，确认后用 Keystore 签署本次兑换握手的证明。回执中的设备 ID 为 16 字节标识；`expiresAt` 是账户到期的 Unix 秒，`expiryKnown` 用来区分长期有效和旧节点未给出信息。本轮导入器拒绝含 SPKI pin 的邀请。业务方法沿用原签名；新增 `Stream.closeWrite()` 仅发送上行结束，调用方仍可读取目标在 EOF 后返回的数据。

## 浏览器参考配置维护

Chrome 稳定版主版本变化后，在回环上用临时用户配置采样。将规范工作树的 `tools/` 加入 PYTHONPATH，运行 `python tools/generate_v2_chrome_profile.py --chrome <本机浏览器路径>`。生成器只提取两个审核过的字段，遇到新的锚摘要或签名列表会停止。审核新版 uTLS 预设、新字段和上游 H3 ChromeParrot 分支后更新生成器，重新生成 Go 表。当前固定的 H3 分支提交为 `2895f039137a`。

运行默认 H2 ClientHello 单测，在不传 ClientHello 的情况下对本机 Chrome、产品 H2 和 H3 各采 50 次。逐项比较 JA4、密码套件顺序、扩展集合、组、签名算法、锚摘要及 H3 传输参数和值。随机量分别检查类型、长度和允许的取值；记录双方版本和样本数。全部通过后才能替换参考版本，并在变更记录中登记。

## 可复现构建

使用 Go 1.27.4、Python 3.11+，以及 git 或 patch。在仓库根运行下列命令，OUT 指向仓库外**尚不存在**的目录。prepare_v2 同时校验固定 go.mod 和模块 zip 的 h1，生成的本地 replace 仅写入 OUT。AAR 构建还需要同版 gomobile/gobind、已有 Android SDK/NDK 和指定提交的本地 libXray。

```sh
python3 tools/prepare_v2.py "$OUT" --android
cd v2
export GOFLAGS="-modfile=$OUT/v2.mod -mod=readonly"
go build -o "$OUT/nexus-node" ./cmd/nexus-node
go vet ./...
NEXUS_NODE_TEST_BINARY="$OUT/nexus-node" go test -race ./...
```

节点读取 Caddy JSON。服务器的 `enable_datagrams` 控制监听上的原生 H3 DATAGRAM。nexus handler 接收 authority、只含公钥的 device_keys，可选 allow_exact 放行精确 IP:port。文件站点作为后续 handler。默认策略检查本次解析的所有结果，并只拨已经核验的字面地址。公开文件目录与身份和证书目录必须独立。systemd 示例位于 `v2/deploy/`。

互通字节见 `v2/proof/testdata/connection.json`，包含普通 exporter 标签、主机和 IPv6 context、设备签名、邀请签名和拒绝样例。exporter 字节为确定性输入。对真实握手的验证在 integration 包中进行。邀请事务、授权账本和活动撤销归 M1。[TLS exporter](https://www.rfc-editor.org/rfc/rfc8446#section-7.5) · [CONNECT-UDP](https://www.rfc-editor.org/rfc/rfc9298)
