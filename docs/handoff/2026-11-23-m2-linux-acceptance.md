# M2 第 1 部分 · 私人节点 Linux 安装器交接

2026-11-23。规范分支先提交安装设计 `bf1c9fa`，再在开发分支实现。范围为 Linux 本地部署、v5 账本及兼容凭据管理。管理员命令见[安装与运维](2026-11-23-m2-linux-operations.md)。客户端传输、移动端接口及 Go 依赖清单均与 M1 收尾版本一致。

## NexusLab 实测

环境为 Debian 13 x86_64 / WSL2、systemd PID 1、Go 1.27.4。验收使用实验室专用名称、临时证书及账号。三个 systemd 单元实际启动，V2 占 443/TCP+UDP，Reality 占 8443/TCP，Hysteria2 占 9443/UDP。

|项目|观察结果|
|---|---|
|来源与字节|直接获取 Xray v26.11.9 ZIP、Hysteria app/v2.14.0 可执行文件。amd64/arm64 共四个实际 SHA-256 均与批准值匹配。两个 arm64 ELF 头及项目 arm64 交叉构建通过静态架构核对。|
|引导与预检|从独立提供的摘要校验节点后启动；错误摘要拒绝执行。归档内的引导脚本固定 LF，可由 dash 运行。覆盖交互及无交互输入、证书域名/密钥异常、下载摘要不符、端口冲突和预检失败。|
|安装事务与防火墙|故障注入覆盖账户、账本、systemd、nft 和健康检查各阶段。在独立网络命名空间实际创建、重复应用、回滚和删除 nft 规则，保留既有规则。正式安装重复执行时三个服务主 PID 相同。已核对账本与私钥 0600、受控目录 0700。|
|升级与还原|实际升级先做在线备份，停旧服务后再取得切换点。让候选程序启动失败，三个 systemd 服务均恢复；原二进制和回退后审计输出分别与升级前逐字节一致。正常升级也通过。|
|保留、重装、清理|停止 Hysteria2 后登记禁用，CLI 返回失败并保留期望状态。默认卸载保留账本与备份。重装重放该代次，账本确认禁用已应用。最终对实验部署执行 purge，确认专用目录、账户、组、四个单元文件和 nft 表均已移除。|
|V2|实际安装服务上完成 H2/H3 邀请兑换、TCP/UDP 回显、带流重载、在线备份及撤销断流。独立节点套件的恢复演练、审计/邀请检查和换库重载同样通过。|
|最终验证|离线准备 Go 模块成功。`go vet ./...` 和 `go test -race -count=1 -v ./...` 均通过。使用已安装的独立节点二进制，并启用实际安装服务用例；测试日志没有跳过项。|

## 官方客户端与凭据

两种协议各使用两个用户。客户端与对应服务端为相同的批准版本。验收脚本 `tools/verify_v2_compat.py` 在独立 mount/net 命名空间执行，从本地管理 CLI 导出各用户分享链接，建立 TCP 及 UDP 回显。Hysteria 客户端原生读取 URI；Xray Core 的命令行使用 JSON，脚本只从导出的 VLESS URI 转出其官方客户端所需字段。

|场景|VLESS + Reality|Hysteria2|
|---|---|---|
|轮换|新凭据的 TCP/UDP 成功，使用旧链接的新客户端两种请求均失败|同左|
|同协议的另一用户|既有 TCP 持续回显，UDP 继续收发|同左|
|旧流首次无法回显|1.200 s|1.157 s|
|禁用和重启|禁用后的链接无法新建 TCP/UDP。服务离线期间轮换，重启时忽略旧缓存；显式重放前旧凭据已失效，同代重放后新凭据可用|同左|

时延从调用轮换 CLI 前开始，包含 CLI 执行和另一用户的成功回显。每协议观察一次，数值为此次旧 TCP 流终止的观测上界。两个上游服务均拒绝访问回环字面地址及解析到回环的域名，对 TCP 和 UDP 分别进行了反向检查。审计和交接不包含分享链接、凭据或原始数据库。

mihomo 订阅按导出链接逐项检查两项代理的 name、type、server、port、udp，以及 Reality 的 uuid、flow、network、tls、servername、client-fingerprint、public-key、short-id 和 Hysteria2 的 password、sni、证书验证字段。字段说明见 [VLESS](https://wiki.metacubex.one/config/proxies/vless/)、[Hysteria2](https://wiki.metacubex.one/config/proxies/hysteria2/) 和 [TLS](https://wiki.metacubex.one/config/proxies/tls/)。

## 交给 M4

- 沿批准矩阵在 Debian 12/13、Ubuntu 22.04/24.04 的 x86_64/arm64 真机验证开机启动、实际防火墙和公网证书。
- Shadowrocket、v2rayN / v2rayNG、mihomo 类客户端分别实测导入及连通。mihomo 官方已注明对 Xray v26.7.11+ 的 Reality 兼容性不作保证，本次锁定的 v26.11.9 在该范围内，放行须保留具体核心版本和实际连通结果。[mihomo Reality 说明](https://wiki.metacubex.one/config/proxies/tls/#reality-opts)
- 仅 IP 部署仍待产品决定。IP SAN 公有证书和可信邀请固定公钥的前提及站点外观差异已记入 Linux 安装器规范。公开发行时还需提供独立可验证的 Nexus 发行摘要和签名材料。
