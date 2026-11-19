# Android GitHub 发布准备 · 第一轮

从 `android/simple-mode@a209c51` 开出的本地分支为 `release/prep-1`。公开安装包使用 `dev.nexus.android`，开发版继续使用 `dev.nexus.localclient`。两个包拥有各自的存储、VPN 授权和 Keystore 身份。临时签名包仅供验收，首次对外分发必须使用拥有者控制的正式签名。

## S24 重启验收安排

在动手重启前记录本计划。目标仅为 S24 Ultra（R5GL14PWDNA）。在公开版已有工作连接、系统 VPN 授权和恢复请求后，先开启「开机后自动连接」，再重启一次。首次解锁后核对前台服务、VPN 接口及经节点的请求。结束时关闭开机连接、断开测试 VPN，并恢复测试前的 Wi-Fi 设置。实验室私人节点位于本机 WSL；用户的 VPS 及 v2rayNG 设置不参与测试。此段为操作安排，实测结果另列。

## 构建和签名

`publicVersion` 是版本的单一来源。1.0.0 对应 versionCode 10000，后两段各占两位。公开产物为 `Nexus-1.0.0-arm64.apk` 和 `Nexus-1.0.0-universal.apk`。通用包包含 arm64-v8a、x86_64。release 启用 R8 和资源收缩。

正式密钥由仓库拥有者在工作区外生成。以下命令通过交互提示设置口令，不要把实际口令写在命令行或项目文件中。

```sh
keytool -genkeypair -keystore nexus-release.p12 -storetype PKCS12 -alias nexus-release -keyalg RSA -keysize 4096 -validity 15000
```

离线备份密钥并单独保存口令、别名和证书 SHA-256。GitHub APK 没有应用商店托管的签名密钥，丢失正式 keystore 后就无法给已安装用户发布同签名覆盖更新。不要用本轮测试密钥做首发。安装过临时签名公开包的测试设备要另行处理签名冲突。

本机构建可通过环境变量或用户目录 `~/.gradle/gradle.properties` 提供 `NEXUS_KEYSTORE`（文件路径）、`NEXUS_KEYSTORE_PASSWORD`、`NEXUS_KEY_ALIAS`、`NEXUS_KEY_PASSWORD`。GitHub Secrets 使用同名四项及 `NEXUS_KEYSTORE_B64`，其中 Actions 的 `NEXUS_KEYSTORE` 只填文件名。工作流在 runner 临时目录还原、使用并删除 keystore，只有创建 Release 的 job 获得 contents:write。不要打印或提交密钥材料。

## 品牌与功能

图标选用信号绿背景及墨黑 N，与 Signal 的 Mark 共用路径坐标比例。两色的 WCAG 对比约 12:1；交换前后景的数学对比相同。选择绿色作底便于在深色桌面上识别外轮廓。圆形裁切与 Android 13 主题着色使用同一安全区中的 N。通知小图标是独立透明背景单色矢量。

## 开源许可

Nexus 工程采用 GPL-3.0，根目录 LICENSE 是 GNU 官方原文，35149 字节，SHA-256 为 `3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986`。README 及应用内「关于」提供 GitHub 源码链接。发布 APK 时应同步提供该 tag 的完整对应源码、构建脚本、确切依赖版本和各组件的授权声明。

| 组件 | 组合及分发判断 |
| --- | --- |
| Xray-core | MPL-2.0。在符合第 3.3 条且作品没有额外 Exhibit B 排除声明时，可组合进 GPLv3 较大作品。对 AAR 对应的上游提交 676eaf66f8f0 扫描了 1195 个非许可证源文件、文档等条目，未发现额外的“不兼容 Secondary Licenses”声明。保留 MPL、对应源码路径及修改说明。 |
| libXray、shoes | MIT，可以与 GPLv3 组合；保留原声明。shoes 的传递依赖另有随包许可。 |
| JSch | BSD 风格的宽松许可，与 GPLv3 兼容；保留版权、条件与免责条款。 |
| ZXing、Android Embedded | Apache-2.0 与 GPLv3 兼容，保留许可证及上游归属声明。 |
| Space Grotesk | 字体作为独立 OFL-1.1 资源随 APK 分发，保留原许可、版权及保留字体名限制，不把字体改授 GPL。 |
| v2fly/domain-list-community、china-operator-ip | 本项目采用的两份上游规则数据均为 MIT，允许随 GPLv3 软件分发，保留原作者和许可文件。 |

依据见 [Mozilla MPL FAQ 第 14、25 问](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)、[GNU 许可证兼容资料](https://www.gnu.org/licenses/license-list.html)及 [OFL FAQ](https://openfontlicense.org/ofl-faq/)。上表覆盖本轮点名的组件，不能替代对未来源可追溯的第三方二进制或新增依赖的审查。

## 首发前由拥有者完成

妥善备份正式签名、配置仓库 Secrets、核对正式证书摘要及 tag 对应源码；检查本轮验收结果后再决定是否发布。


## 改动位置

| 部分 | 文件和行为 |
| --- | --- |
| 发行包与签名 | `apps/android/app/build.gradle`、`apps/android/gradle.properties`、`proguard-rules.pro`、`proguard-test-api.pro`。实际发行包启用 R8 和资源收缩。测试 APK 独立编译，首次运行遇到被优化掉的订阅构造函数，报 `NoSuchMethodError`。按测试字节码对发行类和运行时库的直接引用生成保留规则，允许重命名。修正后在同一份发行包上重跑。 |
| 品牌与数据保护 | `AndroidManifest.xml`、`res/mipmap-anydpi-v26/nexus.xml`、`res/mipmap-anydpi-v33/nexus.xml`、`res/drawable/nexus_{mark,notification}.xml`、`res/values/nexus_brand.xml`、`res/xml/no_backup.xml`。分别设置了自适应、圆形、主题及通知图标，关闭备份及设备迁移。 |
| 客户端 | `MainActivity.kt`、`ReleaseInfo.kt`、`ReleaseScreens.kt`、`TunnelService.kt`。设置页加入自动及手动检查更新、开机连接、关于、许可、隐私和反馈。崩溃堆栈保存在应用私有目录，用户可预览、单独复制。反馈需用户确认才打开浏览器。开机恢复沿用保存的连接请求，手动断开会清除它。 |
| 构建流水线 | `.github/workflows/release.yml`、`tools/prepare_android_release.py`。校验 tag 及版本说明、ABI 和三个原生库，签名后生成摘要，发布 job 再验摘要。 |
| 测试 | `CatalogCheck.kt`、`ReleaseCheck.kt`、`vpnprobe/…/Probe.java`。增加更新响应模拟、私有实验室回执和图标检查。vpnprobe 的 HTTPS 模式供非实验室公网测切网使用，默认模式保留原来的 TCP 拒绝和 UDP 回显检查。 |
| 公共说明 | 根目录 `LICENSE`、`README.md`、`CHANGELOG.md`、`PRIVACY.md`，`docs/screenshots/` 内三张裁去状态栏的 S24 截图，`assets/licenses/` 中的 JSch 和两份 ZXing 声明，以及本篇交接和索引。 |

## 实际验收

预检时 Windows 默认路由未经过 singbox_tun。测试密钥和截图在仓库外。本机使用已有 shoes 原生构建和 `NEXUS_SHOES_TARGET` 对应的外部目录；Gradle 构建时传入 `-x buildShoes`。公开包与 Debug 并存，两个测试设备原有的 Airport-Test 通过本机的遮蔽表单导入；链接没有经过主机或剪贴板。私人节点测试只连接了 WSL NexusLab 的临时服务端，使用测试机生成的公钥签发临时证书。

| 项目 | 实际输出、判定 |
| --- | --- |
| Debug、debug androidTest、vpnprobe | `BUILD SUCCESSFUL in 1m`，107 项，33 executed、74 up-to-date。 |
| Release、release androidTest | 保留规则修正后 `BUILD SUCCESSFUL in 1m 33s`，83 项，19 executed、64 up-to-date。后续增加测试用的 HTTPS 和回执检查，测试 APK 及 vpnprobe 构建成功。 |
| 缺少签名配置 | `Release signing requires: NEXUS_KEYSTORE, NEXUS_KEYSTORE_PASSWORD, NEXUS_KEY_ALIAS, NEXUS_KEY_PASSWORD`。直接执行 `:app:assembleRelease` 返回 `BUILD FAILED in 4s`，符合预期。 |
| 签名、ABI、资源 | 两个 APK 均通过 `apksigner verify --verbose --print-certs`，同一临时证书。aapt 读出 `dev.nexus.android`、`1.0.0`、`10000`。arm64 包仅含 arm64-v8a，通用包另含 x86_64。APK 内 19 条许可资源，Nexus 许可和隐私文件逐字节与根目录一致。 |
| 权限 | aapt 列出 INTERNET、ACCESS_NETWORK_STATE、FOREGROUND_SERVICE、FOREGROUND_SERVICE_SPECIAL_USE、POST_NOTIFICATIONS、CAMERA、WAKE_LOCK、RECEIVE_BOOT_COMPLETED 及应用自有的 DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION。没有申请安装 APK 的权限。 |
| 发布流水线 | YAML、v* 触发、两个 job 的权限、4 个 action SHA、6 段 Bash 语法通过。Docker 官方仓库对指定 Rust 镜像返回 HTTP 200。实际 APK 组装出两行 SHA-256，均回读验证。build.yml 无改动。GitHub 托管 runner 和发布动作未执行。 |
| 更新与隐私 | `release-update` 返回 `INSTRUMENTATION_CODE: -1`。新版本的标题、摘要和下载入口可见，当前版本、离线、格式错误三个分支均无升级提示，重复检查未再次发请求。构造含链接的异常，输出堆栈未含该链接。 |
| 雷电 CatalogCheck | 默认模式报告 `3 sources, isolated update, encrypted persistence, native outbound validation`。simple 报告剪贴板识别 2 种、拒绝 5 种；默认 7 天、1 台、不限流量；十进制 MB/GB。两项均返回 -1。 |
| 雷电 Signal 界面 | simple-shots 报告六屏及连接中，simple-overview 报告支持 4/5、24 小时真连通过 2 个，simple-motion 报告 idle、starting、connected、disconnected。三项均返回 -1。画面状态由测试注入。 |
| 雷电真实订阅与恢复 | split-public xray 返回 -1。全局模式国内与境外出口一致；绕过模式国内与物理出口一致、境外与全局出口一致。全局下/上 8.25/15.17 Mbps，绕过 13.98/20.31 Mbps。杀进程后的 resume 输出 `resumed subscription node after subscription request`，返回 -1。此后按用户要求关闭雷电。 |
| S24 真实订阅 | Airport-Test 的 Xray 节点，split-public 返回 -1。全局模式国内与境外出口一致，绕过模式国内与物理出口一致、与全局出口不一致，境外与全局出口一致。全局下/上 3.25/10.07 Mbps，绕过 7.47/13.49 Mbps。 |
| S24 切网 | Wi-Fi → 蜂窝 → Wi-Fi。每一阶段底层网络均为 VALIDATED，蜂窝阶段系统报告 NR。独立 vpnprobe 每一阶段通过默认 VPN 校验，对 example.com 和 example.org 均取得 `HTTPS 200, verified page`，三次均 -1。 |
| S24 私人节点 | 真实 release 包、Android Keystore 和 WSL 临时节点，USB 反向映射只承担实验室 TCP 传输。通过私人 VPN 请求临时回显服务，结果 `private VPN authenticated; HTTP 200 + temporary Debian receipt; resume request saved`，返回 -1。测试结束后恢复私人配置和节点选择。 |
| S24 开机连接 | **失败。** 开关已开启，重启前实际 VPN 对两个 HTTPS 站点均返回 200。重启后用户完成首次解锁，系统显示 RUNNING_UNLOCKED，未打开 Nexus。等待后无 VPN 接口。系统记录 Nexus 的 BootReceiver 为 `SKIPPED`，原因 `mBroadcastConsumerSkip`。结束时开关恢复关闭，Wi-Fi 已恢复，VPN 接口消失。最后清理时，自动化未能定位旋转中的「断开」控件，随后通过测试入口发送同一服务的 stop 操作，等待恢复请求清除后读回 `connected=false; boot=false; saved=false`。 |

Android 14 要求声明前台服务类型及对应权限。Android 15 禁止若干类型从开机广播启动；specialUse 不在该项禁用清单。本次 S24 是 Android 16（API 36），系统在投递给接收器之前跳过了广播，未观察到执行 startForegroundService 时因服务类型被拒绝。Android 14、15 的真机开机恢复均未验证。[Android 前台服务变更](https://developer.android.com/develop/background-work/services/fgs/changes)

## 可复核产物与边界

本轮测试 APK 在 `E:/temp/nexus-release-prep-1/artifacts/`。以下摘要只对应这次临时签名的构建。正式签名后会改变。

| 文件 | 字节 | SHA-256 |
| --- | ---: | --- |
| Nexus-1.0.0-arm64.apk | 73728263 | `e911b914e1127319835f2a11450365c8e02e6ca70fb255153b4698ccf0ce022f` |
| Nexus-1.0.0-universal.apk | 144922728 | `75b5e9a4ff174103b9d9ffe3e642ef8ebc78753ee6d6b6df7fec1fd4902d5446` |

截图目录是 `E:/temp/nexus-release-prep-1/`。S24 的 `R5GL14PWDNA-{light,dark}-{about,privacy,update,boot}.png` 来自应用设置。`R5GL14PWDNA-{light,dark}-launcher.png` 来自 One UI 启动器的应用搜索结果。S24 的 `R5GL14PWDNA-{light,dark}-notification.png` 为系统通知行截图，仅裁出 Nexus。雷电此前也拍到明暗两套启动器和系统通知行。S24 的 `R5GL14PWDNA-{light,dark}-monochrome-framework.png` 直接取安装包经 Android 框架加载的 monochrome 图层，并使用系统颜色渲染，用于检查资源。另有 `R5GL14PWDNA-{light,dark}-themed-launcher.png`，来自 One UI 开启「将调色板应用至应用程序图标」后的真实启动器，每次点选确认打开公开包。浅色为青绿色底、白色 N；深色为更深的蓝绿色底、黑色 N。测后恢复了原有夜间模式，并核实调色板与应用图标调色两个开关均已关闭。

本轮没有发布、打 tag 或提交。真实 VPS 未参与。首发仍需拥有者配置正式签名并决定如何处理 S24 开机广播被拦截的验收项。

## 审查（Claude）

本轮整体通过。签名材料只来自 Secrets，keystore 用完即删；只有发布 job 有写权限，产物两次核对摘要。检查更新不发送设备信息，崩溃记录只保留可解析的类名和方法名。以下几处已修改：

- **APK 体积：** 原生库（libgojni 34 MB、libshoes 28 MB、libnexus_android 4 MB）以不压缩方式存放，arm64 包 73 MB。已设 `useLegacyPackaging true`，压缩后 arm64 为 32.4 MB，通用包从 145 MB 降到 58.7 MB。三份库原本就没有调试段，可省的空间全来自压缩。
- **关闭混淆：** 源码以 GPL-3.0 公开，混淆起不到保护作用，只会让崩溃堆栈看不懂（Release 也没有发布 mapping）。在 `proguard-rules.pro` 加 `-dontobfuscate`，保留 R8 的裁剪和优化。
- **手动检查更新：** 原逻辑里，自动检查过后 24 小时内点「检查更新」不会发出请求。现在手动检查只限制在 1 分钟内不重复。
- **开机连接文案：** 说明三星等手机会拦截开机广播（S24 记录的 `mBroadcastConsumerSkip` 正是这个原因），推荐改用「始终开启的 VPN」。
- 删除构建过程留下的 809 MB 堆转储 `apps/android/java_pid115812.hprof`，`.gitignore` 增加 `*.hprof`。

复测：用临时签名构建 release、release androidTest、debug 均成功。在 S24 上用 release 包（`dev.nexus.android`）跑 `simple`、`simple-shots`、`simple-overview`、`simple-motion`、`release-update`、默认 CatalogCheck、`split-public -e engine xray`，都返回 -1。界面连接、断开正常，速率显示真实数值。

**新发现的发布阻断项（问题早已存在，不是本轮引入）：** 节点本身没有 IPv6 出口时（例如 Airport-Test 的「新加坡-流媒体05」），在 VPN 中 `curl -4` 访问 google、example.com、cloudflare 都正常，`curl -6` 和默认请求全部失败。原因是 Xray 的 TUN 先在本地接受 IPv6 连接，再到节点上失败，应用因此不会回退到 IPv4。debug 和 release 包表现一致。应用自身的 `split-public` 能通过，是因为测试请求走了 IPv4。下一步要处理：对经节点的 DNS 查询不返回 AAAA（或按节点能力决定），并覆盖全局模式。
