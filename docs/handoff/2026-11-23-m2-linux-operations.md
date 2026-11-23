# M2-1 私人节点安装与运维

## 安装和升级

准备首批支持的 Linux/systemd 主机、管理员控制的域名、PEM 证书链及私钥。开启 Reality 时准备可接受 `<用户标识>.<节点域名>` SNI 的 TLS 目标。可以用自己具有通配证书的站点。未知 SNI 在 Reality 入口关闭连接。V2 与 Hysteria2 客户端照常验证节点域名及证书。

从独立可信渠道取得本架构 Nexus SHA-256。设定 `NEXUS_RELEASE_BASE` 为受信 HTTPS 发布目录，按架构设 `NEXUS_SHA256_AMD64` 或 `NEXUS_SHA256_ARM64`。引导脚本取 `nexus-node-linux-amd64` 或 `nexus-node-linux-arm64`。实验室可以用 `NEXUS_NODE_FILE` 指向已构建文件，也必须提供匹配摘要。本轮没有对外发布地址或签名清单。

```sh
# 在目标机管理员 shell 中，先设定上述发布目录和摘要变量。
sh install.sh install --interactive
# 无交互，仅部署 V2。NODE_DOMAIN、TLS_CHAIN、TLS_KEY 由操作者提供。
sh install.sh install --non-interactive --domain "$NODE_DOMAIN" --cert "$TLS_CHAIN" --key "$TLS_KEY"
# 添加 --protocols v2,reality,hysteria2 和 --reality-target "$TLS_TARGET" 可选三项。
/opt/nexus-v2/bin/nexus-node status
```

单选默认 443，多选为 V2 保留 443/TCP+UDP，另两项分别默认 8443/TCP、9443/UDP；使用 `--v2-port`、`--reality-port`、`--hysteria2-port` 指定允许的端口组合。现有监听冲突、时钟未同步、空间不足或现存防火墙拓扑无法安全识别时，按错误排除原因后重跑。支持空 nftables 与可识别的 inet filter/input，不接管 ufw、firewalld 或未知拓扑。放行安装器列出的云安全组端口。根证书信任与 DNS 解析仍由运营者维护。

使用新版本引导脚本及独立验证的新摘要执行 `sh install.sh upgrade`。升级保留在线备份和停止旧服务后的切换点，失败恢复上一版程序及账本。升级窗口暂停管理写入。切换失败后在 `/var/lib/nexus-v2/backups/` 查看受限诊断；保留的试运行副本可供核对试运行期间新增的记录。默认卸载用 `sh install.sh uninstall`，数据和管理身份保留以便重装。明确要清除本次安装的数据、备份和账号才使用 `sh install.sh uninstall --purge`。真实部署更换证书时，先将新链和配对私钥按服务账号、0600 安全替换到 `/etc/nexus-v2/`，再重载 V2 配置并重启选中的 Hysteria2 服务，核验域名和新证书；Hysteria2 重启会中断现有会话。

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
ledger compat disable "$user_id" hysteria2
ledger compat apply reality
```

`PRIVATE_DIR` 应是本机管理员拥有的 0700 目录。导出文件和备份均含凭据；分享链接通过受控渠道交付。节点不提供订阅 HTTP 路径。轮换成功后重新导出。服务应用失败时 CLI 返回非零，`status` 显示待处理代次或失败；修复服务后用 `apply PROTOCOL` 重放。旧服务可能仍接受之前的凭据，直至更新成功。用户其他设备如果复制了旧链接也需更新。

`revoke-user` 原子撤销现有 V2 设备并登记两种兼容凭据的禁用，再向服务送达。V2 的设备撤销仍可独立使用。兼容账号到期停止接收新认证。审计记录用户、协议、代次及结果，省略 UUID、私钥和密码。

## 备份和恢复

服务账号运行 `ledger backup ABSOLUTE_NEW_PATH`，生成经过完整性检查的 0600 副本。恢复时记录备份时点并暂停管理 CLI，停止本部署的 V2、Reality、Hysteria2 单元。保管旧库和 WAL/SHM。以服务账户从副本恢复 `ledger.db`，权限 0600。启动选中单元，对每个已安装的兼容协议执行 `ledger compat apply PROTOCOL`，包括空表，再检查用户、设备、审计、代次及实际接入。每个兼容服务都从安装账本读取期望修订；未读出有效快照时拒绝接入。旧实验快照不会成为授权来源。更早备份之后发生的撤销、轮换或兑换需要单独核对。
