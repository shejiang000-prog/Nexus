# Nexus Windows 客户端设计 · PC-M0

Windows x64 桌面用户可把一个支持代理的应用接入自己的 V2 节点。首次在首页粘贴邀请，之后留空即可使用上次设备身份。首页显示连接状态、实际线路和两个回环入口。M0 的全部流量均由使用者显式送入这些端口。

## 进程、通信与鉴权

- 普通用户运行 Tauri 2。WebView 只加载随程序分发的 HTML、CSS、JS，本地主窗口只需启动、停止和读状态三个命令。Rust 按自身可执行文件的目录定位 `nexus-core.exe`，便于独立构建和回收 Go/Xray 运行时。每个壳同时只持有一个核心子进程。
- 控制权来自 Windows 传给这个子进程的匿名管道句柄。启动时 stdin 写一行 JSON，唯一业务字段是 `invite`。stdout 只接受 `connecting`、`ready:h3`、`ready:h2`、`error`。邀请不进入命令行、日志或设备记录。停止时关闭写端；父进程意外退出也产生 EOF，操作系统回收子进程的监听和流。不需要本机控制端口或长期共享口令。
- 核心用固定类型配置创建 Xray。SOCKS5 在 `127.0.0.1:21808` 提供 TCP 和 UDP ASSOCIATE，HTTP 在 `127.0.0.1:21809` 提供代理。UDP 客户端须采用 ASSOCIATE 回复中的回环中继端口。监听失败即启动失败。两个数据入口免口令，开启期间本机其他进程也能使用，适用范围为可信的单用户桌面。
- 核心用同一套 V2 选择器自动协商 H3/H2。设备证明确认且本机监听就绪后才报告可用，线路变化后重新确认。界面显示最近一次经核心确认的线路。新请求可走备用线路，已经断开的业务流由应用决定是否重试。

## 设备密钥

使用当前用户 CNG 的命名 ECDSA P-256 密钥，先尝试 Microsoft Platform Crypto Provider（TPM），新注册时若无法建立该密钥则选 Microsoft Software Key Storage Provider。已登记的身份始终重新打开原提供程序；丢失密钥即报错。仅导出压缩公钥，通过 `NCryptSignHash` 为协议消息的 SHA-256 签名，私钥留在提供程序内。[CNG 持久化密钥](https://learn.microsoft.com/en-us/windows/win32/api/ncrypt/nf-ncrypt-ncryptcreatepersistedkey) · [CNG 签名](https://learn.microsoft.com/en-us/windows/win32/api/ncrypt/nf-ncrypt-ncryptsignhash)

每份邀请派生独立别名。首次兑换前在 `%LOCALAPPDATA%\Nexus\desktop` 保存提供程序、authority、pin 和随机请求 ID，成功后原子替换为设备收据并切换当前身份。兑换串行执行，响应丢失可用原邀请、原密钥和原请求 ID 重试。域名按证书信任链验证，IP 邀请必须有 SPKI pin，回退时同样校验。

## 系统代理与崩溃恢复（后续）

用户主动开启后，才为当前用户、当前 WinINet 连接设置 HTTP/HTTPS 代理；指向已就绪的回环 HTTP 入口。通过 WinINet per-connection options 读取和写入，正常结束前恢复并广播设置变化。WinHTTP 和其他代理客户端由各自所有者管理。[WinINet 选项](https://learn.microsoft.com/en-us/windows/win32/wininet/option-flags)

写入前原子保存原值、预期写入值、连接标识和会话标识，保留代理启用位、地址、绕过表、PAC、自动发现及缺省值。由仍存活的监管进程在壳消失时复原，双进程同时退出或断电时由下次启动恢复。恢复前逐项核对所有权：仅当当前配置仍等于本次写入值时恢复快照。若 v2rayN 或用户已更改设置，保留现值并提示。写入、通知、清理日志分别可重试。

## TUN（后续，须批准）

采用固定版 Xray 的 Windows TUN 入站及 Wintun，继续走同一个 `selected` 出站。仅用户授权开启整机接管时提权；高权限进程只接受固定的网卡、路由和 DNS 操作，通信须限制当前登录用户并绑定本次会话。记录本次创建的接口及路由，节点连接保留物理出口，停止、崩溃恢复和网络切换都按接口标识及所有权撤销。这样既能支持不使用系统代理的应用，又能沿用现有传输。

## 绕过大陆（后续）

共用 `tools/generate_split_rules.py` 的锁定输入和产物 `split-rules.txt`，沿用 Android 的大陆域名、国内 IPv4/IPv6 CIDR 和本地地址规则。把命中的域名或字面 IP 交给 Xray `freedom/direct`，余下流量交给 V2。SOCKS 和 HTTP 保留应用交来的目标域名，让 Xray 能在解析前分流。

DNS 单独判定查询名：命中国内规则的请求送物理接口的解析器，其余 DNS 先于普通 CIDR 规则进入隧道，避免因解析器地址落入直连段而泄漏。TUN 阶段对国内 DNS 应答按 TTL 记录地址映射，供后续只有 IP 的连接使用，并随网络切换清空。沿用 Android 分流样例检验相同输入的决策。M0 尚无直连出站。

## 订阅导入（后续）

复用固定版 libXray 的 `share.ConvertShareLinksToXrayJson` 转换支持的分享链接，只取经校验的单一出站。将 Android `Subscriptions.kt`、`NodeFormats.kt`、`CompatEngine.kt` 的纯解析及字段白名单逐步提到共享 Go 层，覆盖明文链接、Base64 链接组及支持的 Clash YAML 子集；以同一批输入验证两端结果。Kotlin UI、SnakeYAML 和 Android Keystore 留在平台层。保留 Android 的 512 KiB 上限、结构深度、重复键、协议和证书校验约束，拒绝远端提供入站、路由或控制接口。订阅凭据以后以当前用户 DPAPI 加密。M0 只接受 V2 设备邀请。

## 安装与自动更新（后续）

同一版本的壳和核心随 Tauri 2 的 x64 NSIS 安装包一起交付。默认按用户安装；正式包及两个程序使用 Authenticode 签名。更新采用 Tauri 官方 updater，HTTPS 获取清单，验证内置更新公钥和签名后才退出旧版本并整包替换，保留设备记录。更新失败保留或恢复上一对可执行文件，防止新壳启动旧协议核心。签名密钥由发布端保管。M0 交付可并排运行的两个开发程序。[Tauri updater](https://v2.tauri.app/plugin/updater/)

## 共用代码与待批准依赖

| 当前共享代码 | 用途 |
| --- | --- |
| `v2/enrollment`、`v2/proof` | 邀请解析、兑换、TLS 绑定及设备证明 |
| `v2/transport`、`v2/udp`、`v2/internal/congestion`、`v2/mobile` | H3/H2 协商、线路选择、TCP、UDP 关联和签名接口 |
| `apps/android/engine/v2vpn`、同一目录树的 DNS 辅助代码 | 以 V2 替换默认 Xray 出站、UDP 分发和首包 DNS |
| `tools/prepare_v2.py` | 从固定 Git 对象、锁定模块及补丁组装两端引擎。Android 接收原有 JSON 配置，Windows 只注册回环入站需要的组件 |

M0 没有新增第三方依赖。以下项目按功能单独审批后再引入：

| 功能 | 待批准项 | 审批范围 |
| --- | --- | --- |
| 整机 TUN | Wintun x64 DLL/驱动分发；启用固定版 Xray 所引用的 `golang.zx2c4.com/wintun`、`wireguard/windows/tunnel/winipcfg` | 锁版本及摘要、签名、许可证、提权和卸载恢复验收 |
| 自动更新 | `tauri-plugin-updater` | 版本、锁文件变化、签名和回滚验证 |
| 安装包 | NSIS 打包工具（构建端） | 锁定构建工具和 WebView2 部署策略，验证整包签名 |
