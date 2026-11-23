# M2 私人节点安装与运维

## 安装和升级

V2、Hysteria2 需要节点域名。默认由 Caddy 通过 ACME 管理证书；有现成证书时可一起提供绝对路径 `--cert`、`--key`。域名 A/AAAA 记录须指向本机接口，放行验证所需的 80/TCP 和 443/TCP。仅选 Reality 时可用节点 IPv4 或 IPv6 地址，使用 `--public-host`，省略 `--domain`。Reality 目标及 SNI 单独选择，目标证书须对每个名称有效，安装器会验证服务器支持 TLS 1.3 和 X25519。

从独立可信渠道取得本架构 Nexus SHA-256。设定 `NEXUS_RELEASE_BASE` 为受信 HTTPS 发布目录，按架构设 `NEXUS_SHA256_AMD64` 或 `NEXUS_SHA256_ARM64`。引导脚本取 `nexus-node-linux-amd64` 或 `nexus-node-linux-arm64`。实验室可以用 `NEXUS_NODE_FILE` 指向已构建文件，也必须提供匹配摘要。本轮没有对外发布地址或签名清单。

```sh
# 在目标机管理员 shell 中，先设定上述发布目录和摘要变量。
sh install.sh install --interactive
# 无交互，V2 使用自动证书，NODE_DOMAIN 由操作者提供。
sh install.sh install --non-interactive --domain "$NODE_DOMAIN"
# 三协议，可用 --reality-target host:port 和 --reality-server-names name1,name2 选择目标。
sh install.sh install --non-interactive --domain "$NODE_DOMAIN" --protocols v2,reality,hysteria2
# 无域名，仅 Reality，NODE_IP 是交付给用户的节点地址。
sh install.sh install --non-interactive --protocols reality --public-host "$NODE_IP"
/opt/nexus-v2/bin/nexus-node status
```

单选默认 443，多选为 V2 保留 443/TCP+UDP，另两项分别默认 8443/TCP、9443/UDP；使用 `--v2-port`、`--reality-port`、`--hysteria2-port` 指定允许的端口组合。现有监听冲突、时钟未同步、空间不足或现存防火墙拓扑无法安全识别时，按错误排除原因后重跑。支持活动 ufw 和可识别的原生 nftables 拓扑。ufw 未启用时走已有防火墙路径。firewalld 活动时会在变更前中止并输出按实际 TCP/UDP 端口生成的 firewall-cmd 命令，供核对入站区域；执行该命令不改变本阶段对 firewalld 的支持范围。放行安装器列出的云安全组端口。根证书信任与 DNS 解析仍由运营者维护。

使用新版本引导脚本及独立验证的新摘要执行 `sh install.sh upgrade`。升级保留在线备份和停止旧服务后的切换点，失败恢复上一版程序及账本。升级窗口暂停管理写入。切换失败后在 `/var/lib/nexus-v2/backups/` 查看受限诊断；保留的试运行副本可供核对试运行期间新增的记录。默认卸载用 `sh install.sh uninstall`，数据和管理身份保留以便重装。明确要清除本次安装的数据、备份和账号才使用 `sh install.sh uninstall --purge`。自动模式下 Caddy 独占写入托管证书；Hysteria2 在新握手时重读同一对文件，续期保留现有会话。手工更换证书时按服务账号、0600 安全替换配对文件并重载 Caddy。只启用 Hysteria2 时，Caddy 仍通过私有 Unix 套接字提供普通站点；自动模式额外提供公网上的 TCP 验证和 HTTPS 站点。

## 用户管理

在本地管理员 shell 设置私有输出权限。服务账号执行所有账本命令，避免不同账号创建的 WAL 或协议应用锁权限不一致。V2 用户通过 `invite` 创建邀请。兼容服务初装时用户表为空。

```sh
umask 077
ledger() { runuser -u nexus-v2 -- /opt/nexus-v2/bin/nexus-node ledger /var/lib/nexus-v2/ledger.db "$@"; }
user_id=$(ledger create-user)
ledger compat enable "$user_id" reality
ledger compat enable "$user_id" hysteria2
ledger compat status "$user_id"
ledger compat export "$user_id" links > "$PRIVATE_DIR/links.txt"
ledger compat export "$user_id" mihomo > "$PRIVATE_DIR/proxies.yaml"
ledger compat rotate "$user_id" reality
# 影响本节点全部 Reality 用户的独立操作
ledger compat rotate-node-key reality
ledger compat disable "$user_id" hysteria2
ledger compat apply reality
```

`PRIVATE_DIR` 应是本机管理员拥有的 0700 目录。导出文件和备份均含凭据；分享链接通过受控渠道交付。节点不提供订阅 HTTP 路径。单用户 Reality 轮换更换 UUID；禁用或替换 UUID 阻止新认证，已通过认证的连接可能持续到客户端自然关闭。整节点换钥会关闭该 Reality 入站的已有连接，所有 Reality 用户应重新导出。Hysteria2 用户继续使用原凭据。服务应用失败时 CLI 返回非零，`status` 显示待处理代次或失败；修复服务后用 `apply PROTOCOL` 重放。旧服务可能仍接受之前的凭据，直至更新成功。用户其他设备如果复制了旧链接也需更新。

`revoke-user` 原子撤销现有 V2 设备并登记两种兼容凭据的禁用，再向服务送达。V2 的设备撤销仍可独立使用。兼容账号到期停止接收新认证。审计记录用户、协议、代次及结果，省略 UUID、私钥和密码。

## 备份和恢复

服务账号运行 `ledger backup ABSOLUTE_NEW_PATH`，生成经过完整性检查的 0600 副本。恢复时记录备份时点并暂停管理 CLI，停止本部署的 V2、Reality、Hysteria2 单元。保管旧库和 WAL/SHM。以服务账户从副本恢复 `ledger.db`，权限 0600。启动选中单元，对每个已安装的兼容协议执行 `ledger compat apply PROTOCOL`，包括空表，再检查用户、设备、审计、代次及实际接入。每个兼容服务都从安装账本读取期望修订；未读出有效快照时拒绝接入。旧实验快照不会成为授权来源。更早备份之后发生的撤销、轮换或兑换需要单独核对。
