# M2 第 2 部分 · Linux 安装器验收

2026-11-23，NexusLab，Debian 13、x86_64、systemd PID 1。先在规范分支确定节点级 Reality 密钥、域名规则、Caddy 证书归属和防火墙规则所有权，再修改安装器。客户端传输 API 沿用 `607a3f8`。操作步骤见[私人节点安装与运维](2026-11-23-m2-linux-operations.md)。

## 实际安装与恢复

|NexusLab 场景|结果|
|---|---|
|无域名，仅 Reality|安装、启动并独占 443/TCP。导出的地址是配置的 IP，SNI 是单独指定的目标名称。未安装 Caddy 服务或节点证书。相同参数再装，服务 PID 保持一致。清除后再开始三协议安装。|
|域名，三协议，自动证书|Caddy 通过现有 Caddy 自带的 ACME server 向独立私有 CA 完成验证，签发带测试域名的短期证书。V2 使用 443/TCP+UDP，Reality 使用 8443/TCP，Hysteria2 使用 9443/UDP。相同参数重复安装，三个服务 PID 保持一致。|
|自动续期与 Hysteria2|在专用测试域名上缩短 Caddy 维护间隔，建立已认证 QUIC 连接后等待自动续期。两端的新握手与磁盘上的新叶证书完全一致。最终全量测试在 30.333 s 时观测到新证书。固定在原 QUIC 连接上的认证在等待期间的 14 次检查和换证后的检查均返回 233。测量窗口内没有发起重载或重启。|
|Hysteria2 普通站点|两端对首页、缺失页面和错误认证分别返回 200、404、404。逐项比较响应体及与身份、内容相关的响应头。Hysteria2 通过仅提供普通站点的 Unix 套接字向本机 Caddy 转发。|
|升级与回退|在线备份后令试运行版本在服务入口启动失败。安装器恢复旧程序并启动原来的三项服务，回退前后的节点二进制及账本审计逐字节一致。随后用正式候选升级成功。|
|保留卸载、单选 Hysteria2、清除卸载|默认卸载保留用户列表和已有备份。用保留数据重装仅 Hysteria2，Caddy 提供 TCP 网站和验证端口，Hysteria2 监听 UDP；Caddy 配置没有 V2 handler。NexusLab 内 HTTPS 入口与 Unix 套接字返回相同首页。最后执行 `uninstall --purge`，复核服务、账号、数据、配置及本安装的防火墙资源。|

本地 CA 与 Reality 目标由 `v2/integration/testdata/local_acme.py` 放在私有挂载命名空间。测试仅使用 NexusLab 已有的 Caddy、OpenSSL 和 Python，无须向系统导入测试根证书。安装器的公开默认目标也从 NexusLab 完成了一次 TLS 1.3、X25519 及名称验证。自动化还覆盖名称不匹配、TLS 版本或密钥交换组不符合、地址解析与监听冲突及手工证书模式。

## 两用户兼容验证

隔离网络内运行 `tools/verify_v2_compat.py NODE ASSET_DIR`。使用已批准并经 SHA-256 核对的 Xray-core v26.11.9 和 Hysteria app/v2.14.0 官方客户端。Hysteria 直接读取分享 URI；Xray 由测试把 VLESS 链接字段映射为官方客户端配置。两个用户均通过 TCP 和 UDP。逐次验证新旧凭据、另一用户的已建连接、离线轮换后重启及同代重放。节点换钥后两位用户重新导出的链接均可用，旧链接均被拒，Hysteria2 原会话可用。mihomo 导出通过字段核对。

下表自管理命令发起计时，以旧 TCP 流上的实际回声读写判断。最后一次成功只给出连接存活下限，首次观测到关闭给出本次关闭上限。

|操作|用户 A 已建立的 TCP 流|用户 B|
|---|---|---|
|Reality 轮换 UUID|4.189 s 时仍有回声|原连接可用|
|Reality 禁用 UUID|4.182 s 时仍有回声|原连接可用|
|Reality 节点整体换钥|1.215 s 时已关闭|1.216 s 时已关闭|
|Hysteria2 轮换密码|1.224 s 时已关闭|原连接可用|
|Hysteria2 禁用|1.137 s 时已关闭|原连接可用|

单用户禁用阻止后续认证。上游 Xray 允许已认证的流继续运行，不能将单用户 Reality 禁用表述为即时踢下线。旧 v5 实验布局须用原版工具备份后重建；原 v4 账本可原地事务升级。[节点账本](../protocol/v2-node-ledger.md)

## 测试与后续实机项目

- 离线构建 Linux amd64 节点（启用 race），并交叉构建 Linux arm64 ELF。运行 `go vet ./...`、设置独立节点路径和 `NEXUS_TEST_INSTALLED=1 NEXUS_TEST_ACME_RENEWAL=1` 的 `go test -race -count=1 -v ./...`。vet 通过，10 个有 Go 测试的包全部通过，零跳过。compat 包由上述官方客户端脚本覆盖。实装 V2 用例覆盖一次性邀请、H2/H3 上的 TCP/UDP、活动流重载、撤销及备份。
- NexusLab 使用真实 nftables，另外以隔离网络命名空间核对停用 ufw 可能留下的 ACCEPT 空链。活动 ufw、legacy 表读取和 firewalld 使用命令模拟覆盖。测试包括 IPv4/IPv6、本安装随机命名的应用规则、管理员同端口规则、删除前并发插入、失败回滚、停用 ufw 放行以及 firewalld 输出可复制命令。实验室未安装 ufw/firewalld。
- M4 验证公网签发、支持矩阵内的真实发行版、防火墙实机以及 Shadowrocket、v2rayN/v2rayNG、mihomo 的实际导入。当前交付为分享链接与本地 mihomo 文件。在线订阅地址列入后续。

## Claude 评审

评审对象：开发分支 `edb27bb`…`dbbf608`（共 13 个提交），规范分支 `8a39b3e`、`628db3b`。

结论：上轮两个设计问题都已按要求改正，工程质量继续保持。本轮发现**一个重要缺陷**（云服务器上无法安装）和一个中等问题（Reality 禁用后旧连接不断），在 M2 第 3 部分修复。

**独立复现（NexusLab，从 `dbbf608` 的 git archive 构建，GOPROXY=off）**

- prepare、节点构建、`go vet ./...` 通过。带独立节点的 `go test -race -count=1 -v ./...`：10 个包全部通过。两个实装用例（`TestInstalledPrivateNode`、`TestInstalledACMERenewal`）需要预先安装，这次跳过，ACME 续期以 Codex 的记录为准。
- `tools/verify_v2_compat.py` 独立重跑（62 s，退出码 0），两个上游资产的 SHA-256 与批准清单一致。
  - Hysteria2：轮换后旧连接 0.041 s 关闭，禁用后 0.049 s 关闭。
  - Reality：轮换和禁用 3.1 s 后旧连接仍在回显；节点整体换钥后两名用户旧连接 2.04 s 关闭。
  - 两用户 TCP/UDP、回环目的地拒绝、离线轮换后重启、mihomo 字段核对，均通过。
- **评审实装**：`install.sh install --non-interactive --protocols reality --public-host 192.0.2.10`（无域名，默认目标 www.microsoft.com:443）。
  - 安装成功，xray 独占 443/TCP；目录 0700、账本 0600，属主为服务账户。
  - 建用户、开通 Reality 后状态为 applied；导出链接 `sni=www.microsoft.com`、`fp=chrome`、`flow=xtls-rprx-vision`，地址为 public-host。
  - 相同参数重装，服务 PID 不变。
  - `uninstall --purge` 后账户、目录、单元全部移除，nft 规则集与安装前逐字一致。
  - 实验室里另有的 `nexus-node`、`nexus-claim` 服务是 11-21 的 V1 实验节点，与本安装器无关。
- 本地 ACME 用 Caddy 自带的 acme_server 搭私有 CA，没有引入新工具，做法好。

**缺陷 1（重要）：ACME 预检要求域名解析到本机网卡地址，NAT 云主机必然失败**

`host_linux.go` 中的 `localDNS` 把 A/AAAA 记录与 `net.InterfaceAddrs()` 比对，不在本机网卡上就拒绝安装。AWS、GCP、Oracle Cloud、阿里云、腾讯云等的公网 IP 是 1:1 NAT，并不出现在网卡上，这类主机会被直接挡掉。

修复方向：不要求解析地址必须在网卡上。可以改为在 80 端口开临时监听，带随机令牌，然后经域名回访自己；回环 NAT 不可用时退化为警告。交互模式让用户确认继续，无交互模式提供显式参数跳过。证书最终能否签发以 ACME 实际结果为准，失败时回滚并给出明确原因（解析错误、80/443 被安全组挡住等）。不得为探测公网 IP 而访问第三方服务。

**问题 2（中）：Reality 禁用或轮换不会断开该用户已有的连接**

上游 Xray 的 RemoveUser 只阻止新认证，已认证的流会一直保持。对“撤销朋友访问”这种场景，这不够。整节点换钥约 2 s 能断开所有人，说明重建入站是可行的。

建议：禁用、`revoke-user`、轮换（应对链接泄露）默认重建 Reality 入站，强制所有 Reality 用户重连，其他用户的客户端会自动重连。界面和文档写明这个代价，并提供不断流的选项。

**观察（低）**

- 默认目标 www.microsoft.com 被大量 Reality 节点使用，且其 IP 与 VPS 所在网络不一致（SNI 与 IP 归属不符是已知的可检测特征）。建议安装器提示用户优先选择与 VPS 同网络或同地区、支持 TLS 1.3 的站点，M4 实网评估默认值。
