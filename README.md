<div align="center">

<img src="docs/brand/nexus-logo.svg" width="96" height="96" alt="Nexus">

# Nexus

**把自己的服务器一键变成私人节点，再一键分享给家人朋友。**

Android 代理客户端，无需注册，内置 Nexus V2 协议，也能导入第三方订阅。

[![Release](https://img.shields.io/github/v/release/shejiang000-prog/Nexus?style=flat-square&label=%E6%9C%80%E6%96%B0%E7%89%88&color=2F6BFF)](https://github.com/shejiang000-prog/Nexus/releases/latest)
[![Android](https://img.shields.io/badge/Android-8.0%2B-1AA7EC?style=flat-square&logo=android&logoColor=white)](#下载与安装)
[![License](https://img.shields.io/badge/License-GPL--3.0-4A5A70?style=flat-square)](LICENSE)

[下载](#下载与安装) · [快速上手](#快速上手) · [Nexus V2](#nexus-v2) · [常见问题](#常见问题) · [隐私](PRIVACY.md)

</div>

---

## 为什么选 Nexus

<table>
<tr>
<td width="50%" valign="top">

### 🚀 一键自建
填上服务器 IP 和密码，一两分钟就能部署好，**不需要域名**。SSH 端口不是 22 的话，写成 `IP:端口` 即可。支持 Debian 12/13 和 Ubuntu 22.04、24.04、26.04、26.10。

</td>
<td width="50%" valign="top">

### 🔗 邀请即用
生成一条邀请发给对方，扫码或粘贴就能连上。有效期可以填天数，也可以设为永久；之后可以续期，也可以单独撤销某台设备。

</td>
</tr>
<tr>
<td valign="top">

### 🛰️ UDP 被封时自动切换
默认使用 HTTP/3（UDP）。如果网络封锁了 UDP，几秒内会自动换成 HTTP/2（TCP）；UDP 恢复后再自动换回来，不用手动操作。

</td>
<td valign="top">

### 🔋 常开也省电
没有连接在用时，不发任何包。熄屏或深度省电之后，亮屏马上能用；Wi-Fi 和流量互相切换时，连接也会自动接上。

</td>
</tr>
<tr>
<td valign="top">

### 🧩 兼容其他客户端
同一台服务器可以同时部署 Reality，有域名时还能加上 Hysteria2。这两种都能导出分享链接，给 v2rayN、小火箭等客户端使用。原有的机场订阅照常导入。

</td>
<td valign="top">

### 🔒 设备绑定，数据只在本机
每台设备领取邀请时会生成自己的密钥，V2 连接靠这把密钥认证，每条邀请只能领取一次。Nexus 没有账号，不收集数据，也不上传日志。

</td>
</tr>
</table>

## Nexus V2

V2 是 Nexus 自研的传输协议。它走的是标准 HTTPS（HTTP/3 和 HTTP/2），每台设备单独认证，账号可以设置到期时间。

一条公网线路上的实测数据：

| 场景 | 实测 |
|:--|:--|
| 正在使用时 UDP 被封 | 3.6–4.4 秒恢复新连接，5.0–6.3 秒恢复网页 |
| 在 UDP 已被封的网络里启动 | 约 2.5 秒打开网页 |
| Wi-Fi 和 5G 来回切换 | 0.5–1.5 秒自动恢复 |
| 熄屏、深度省电 15 分钟后 | 亮屏 1.4 秒恢复 |
| 同一台服务器的下载速度 | V2（HTTP/3）10.7 Mbps，Reality 4.1 Mbps |

<sub>以上数据来自单条线路、不同时段的少量样本，实际效果取决于你的网络和服务器线路。测量方法和原始记录见 [公网与真机实测记录](docs/handoff/2026-11-25-m4-2-public.md)。</sub>

## 快速上手

| | 有机场订阅 | 有自己的服务器 | 收到了邀请 |
|:--:|:--|:--|:--|
| **1** | 点「添加机场订阅」 | 打开「我的服务器」 | 复制邀请消息 |
| **2** | 粘贴链接或扫码 | 填 IP 和 root 密码，域名可以不填 | 打开 Nexus，确认领取 |
| **3** | 选线路，点圆环连接 | 部署好后，这台手机自动成为第一台设备 | 点圆环连接 |

> 「绕过大陆」模式下，国内网站直接访问，其余流量走节点；「全局代理」模式下，所有流量都走节点。你也可以只让选中的应用走代理。

## 下载与安装

到 **[Releases](https://github.com/shejiang000-prog/Nexus/releases/latest)** 下载：

| 文件 | 适用于 |
|:--|:--|
| `Nexus-x.y.z-arm64.apk` | 绝大多数手机（推荐） |
| `Nexus-x.y.z-universal.apk` | 模拟器，或不确定手机 CPU 架构时 |

安装时，系统会要求允许浏览器或文件管理器「安装未知应用」，装完可以关掉这项授权。以后有新版本，App 会提示更新，直接覆盖安装就行。

<details>
<summary><b>校验安装包（可选）</b></summary>

<br>

1. 下载同一版本的 `SHA256SUMS.txt`，核对 APK 的摘要：
   - Windows：`Get-FileHash .\Nexus-x.y.z-arm64.apk -Algorithm SHA256`
   - Linux / macOS：`sha256sum Nexus-x.y.z-arm64.apk`
2. 用 Android SDK 核对签名证书：`apksigner verify --print-certs Nexus-x.y.z-arm64.apk`。证书 SHA-256 应为：

```
D6:9F:CF:1C:0B:B7:60:DF:D7:EA:DD:21:D8:61:2C:30:B0:56:46:FD:0B:1D:A0:56:95:50:24:59:A4:02:83:B3
```

</details>

<details>
<summary><b>从 1.x 升级</b></summary>

<br>

可以直接覆盖安装。升级后，旧版的 V1 私人节点、邀请和绑定会被清除，第三方订阅保留。自建服务器的用户需要重新部署或绑定 V2 节点，再让成员重新领取邀请。

</details>

## 常见问题

<details>
<summary><b>手机清理后台后断线了怎么办？</b></summary>

<br>

在 Nexus 设置的「断线自动恢复」里打开系统 VPN 设置，启用「始终开启的 VPN」。想在重启手机后自动连上，再打开「开机后自动连接」。部分机型（包括部分三星机型）会拦截开机广播；遇到这种情况，开机首次解锁后手动打开一次 Nexus 即可。

</details>

<details>
<summary><b>部署时提示连不上服务器？</b></summary>

<br>

- **提示这个 IP 可能在国内无法访问**：从国内连不上这个 IP，请在服务商那里更换 IP。
- **提示检查 SSH 端口**：服务商给的 SSH 端口不是 22，请填写 `IP:端口`。
- **提示 443 端口被占用**：服务器上已经有 nginx 等程序占用了 443，先停用它，或者换一台干净的服务器。

</details>

<details>
<summary><b>节点没有 IPv6 出口，会有影响吗？</b></summary>

<br>

大多数应用不受影响，因为 Nexus 会按域名把流量交给节点。少数游戏或 P2P 下载工具会直接连接 IPv6 地址，这时可能连不上，换一个支持 IPv6 的节点就行。

</details>

<details>
<summary><b>国内访问 GitHub 很慢怎么办？</b></summary>

<br>

如果已经有能用的节点，先连上再检查更新。也可以用另一台能访问 GitHub 的设备下载 APK，校验后传到手机上安装。

</details>

<details>
<summary><b>遇到连接问题，怎么反馈？</b></summary>

<br>

在设置里点「导出诊断信息」，Nexus 会生成一份文字摘要，由你决定发给谁。摘要只包含版本、机型、网络类型、切换记录和错误码，不含任何地址、密钥或链接。

</details>

## 隐私与许可证

Nexus 没有账号，不接入任何统计、广告或崩溃上报 SDK。订阅、节点和密钥都只保存在你的手机上。详见 [隐私说明](PRIVACY.md)。

Nexus 以 [GPL-3.0](LICENSE) 发布。随包分发的第三方代码、规则和字体保留各自的许可证，详见 [第三方声明](THIRD_PARTY_LICENSES.md)。

## 开发者资料

- [Android 构建与使用](docs/ANDROID.md)
- [V2 客户端传输接口](docs/protocol/v2-transport-api.md) · [节点账本](docs/protocol/v2-node-ledger.md)
- [依赖清单](DEPENDENCIES.md) · [更新记录](CHANGELOG.md)
