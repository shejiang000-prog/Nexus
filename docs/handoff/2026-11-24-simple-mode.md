# Android 简易连接与服务器管理

## Nexus Signal 视觉与动效

本节记录在 `android/simple-mode` 未提交工作树上的 Signal 改版。下文的蓝灰、淡粉蓝与圆体评审记录均为历史版本。当前实现以 `docs/design/signal/` 的七份 HTML 为准，保留原生 Compose 和原有数据、授权、部署与连接路径。

| 页面 | 实现与逐屏差异 |
| --- | --- |
| 首次打开 Welcome | 176 dp 轨道、品牌标志、30 sp 标题、订阅和服务器双入口。邀请捷径仅在空状态显示。延续应用导航和设置入口。按绿色使用约束，欢迎图使用中性墨色；横屏并排显示图案与操作。 |
| 连接 Main | Canvas 三轨和品牌圆心按 idle、connecting、connected 切换；琥珀/绿交错旋转、呼吸和双波纹。原生按压弹簧与状态触感。卡片包含当前节点、分流、计时、上传与下载的最近 24 秒采样。首页继续提供全部订阅的额度、到期、支持和真连记录。宽屏右侧显示操作和总览，手机纵向滚动。无计数样本显示破折号。 |
| 节点 Nodes | 保留测速、筛选、收藏、订阅管理与私人节点。自动选最快开关使用现有实测排序；节点展示地区、名称、协议、信号格、最近实测延迟及选择标记。无测量的节点没有虚构延迟。S24 的节点页截图保留其真实空状态，雷电截图使用现有订阅。原型固定七行，实际列表按数据滚动。 |
| 我的服务器 Server | 绑定提示、邀请主操作、设备和可用额度进度、撤销确认、管理和高级入口。线上接口提供设备授权和累计用量；界面使用这两项，最近使用时间缺失时明确提示。截图采用生产组件和本地合成的单设备状态。 |
| 邀请 Invite | 原生底部弹窗、居中二维码、距离错峰显现、有效期/设备/流量、分享、敏感剪贴板复制与确认条。使用真实链接生成纠错级别 H 的码、四模块静区及 28 dp 中央标志，替代 HTML 演示图的 46 px 遮盖。宽屏分为二维码与操作两栏。额度在生成前设置，已签发的邀请只展示实际参数。 |
| 收到邀请 Receive | 设备间虚线、成功实线和勾、按钮光圈与粒子；手机型号可改，领取成功先保存身份再展示成功反馈，之后请求 VPN 授权。现有邀请载荷缺少有效期和额度，显示「邀请未提供」。使用 Android 系统输入框、返回和键盘适配。 |

**公共实现。** `ui/Theme.kt` 映射 Tokens 明暗配色、系统中文字体与 Space Grotesk 等宽数字。字体和完整 OFL 随 APK 分发，来源记入 DEPENDENCIES.md 及 THIRD_PARTY_LICENSES.md。`ui/Components.kt` 提供按钮、分段选择、节点行、提示、导航用标志与列表错峰。`ui/Orbit.kt` 绘制三轨。`ui/InvitationArt.kt` 绘制二维码、领取反馈。`SimpleDesign.kt`、`ConnectionUi.kt`、`MainActivity.kt`、`SubscriptionScreen.kt`、`SubscriptionOverview.kt` 及 `SimpleServer.kt` 使用这些组件。卡片 20 dp，内容边距 20 dp，主要按钮 56 dp，导航标签 13 sp。Android 依赖、服务端、Rust 与协议均沿用既有实现。

**动效和数据。** 连接中三轨周期 1.1/1.6/2.3 秒，连接后 14/20/28 秒；呼吸 3.2 秒，波纹 2.8 秒、错开 1.4 秒。切页退出 200 ms、进入 450 ms。读取系统动画设置并监听前后台和熄屏变化。关闭动画时圆环静止，切页保留 150 ms 淡化。当前订阅节点优先以活动连接的 nodeKey 查找对应条目，使备用节点的地区、协议和延迟跟随实际连接。速率从当前 Nexus VPN 接口的字节增量和实际采样时间计算；设备看不到计数时不补零。速率曲线沿用真实数据，静态截图测试未启动 VPN 时不生成演示流量。

**截图与对照。** `E:/temp/nexus-signal-shots/` 下每台设备各 14 张。文件名为 `{emulator-5554 或 R5GL14PWDNA}-{light 或 dark}-{first、connect、connecting、nodes、server、invite 或 receive}.png`。参考 HTML 的浅深两套静帧在同目录 `reference/`；本机按模板的默认状态展开，关闭 CSS 动效以便比对静态布局。模拟器另试了横屏，二维码在两栏布局中可以完整扫码。真机仅展示界面，连接及邀请数据为合成样例，未开启 VPN、未连接用户的 VPS、未操作 v2rayNG。

### 本轮验证

测试使用 Windows Debug APK、雷电 `emulator-5554`、WSL NexusLab 临时服务端及 S24 Ultra。开测前 `route print` 对应的默认路由为以太网。构建设置 `NEXUS_SHOES_TARGET=E:/temp/nexus-shoes-target`，沿用现有 shoes 产物。设备均先核验签名，再用 `adb install -r` 覆盖。

| 项目 | 实际观测 |
| --- | --- |
| 构建及本地单测 | App、androidTest、vpnprobe 的 Debug APK 构建成功。最后按活动节点的 nodeKey 对齐名称、地区和延迟后，构建输出 `BUILD SUCCESSFUL in 5s`，106 tasks，7 executed、99 up-to-date。修正横屏测试定位后最终构建为 `BUILD SUCCESSFUL in 3s`（4 executed、102 up-to-date）。Gradle `:app:testDebugUnitTest NO-SOURCE`，没有本地 JVM 单元测试可计为通过。逻辑验证由下列 instrumentation 执行。 |
| 字体与许可证 | APK 包含 `res/font/space_grotesk.ttf`（136676 字节）及 `assets/licenses/space-grotesk/OFL.txt`（4495 字节）。中文随系统字体和字号设置。 |
| 六屏及二维码 | 雷电与 S24 各两种主题，四次均返回 `screens=6 screens and connecting; synthetic connection state; no VPN requested`、`INSTRUMENTATION_CODE: -1`。每次在截图中定位二维码，识别结果与原始合成邀请完全一致。首轮横屏全图扫码曾报 NotFoundException，改为横屏双栏、完整静区和整像素网格后通过。过长的邀请先降纠错并取消中央标志，超出二维码容量时保留系统分享和复制。 |
| 首页切换、订阅总览 | `idle, starting, connected, disconnected; simulated state, no VPN requested`；合成总览 `support 4/5; 24-hour passes 2; stale result excluded; total visible`。实际来源 `Airport-Test visible on launch; add opened the importer; source opened its management dialog`。手机纵屏需滚动到总览。 |
| S24 帧耗时与停绘 | HWUI `dumpsys gfxinfo ... framestats`，合成已连接状态，预热后按 IntendedVsync 去重。进入后台前 606 帧，p50 **6.28 ms**、p90 **7.91 ms**，2 帧超过 Android FrameDeadline。熄屏前 604 帧，p50 **6.13 ms**、p90 **7.66 ms**，7 帧超时。后台、熄屏稳定后的两个 1 秒采样窗口内新增绘制帧分别为 **0、0**。设备生命周期均已退出 RESUMED。以上样本不能证明全程零掉帧。 |
| 订阅回归 | 默认模式返回 `3 sources, isolated update, encrypted persistence, native outbound validation`；formats 为 25 个转换用例、3 种元数据格式；refresh 验证失败保留快照、退避、id/nodeKey、VPN 主备跳过与断开补更。probe-vpn 检查测试按钮禁用与断开提示。 |
| 三引擎实验室 | 私人、Xray、shoes 的全局/绕过 × TCP/UDP 均返回 `both DNS queries and both source receipts matched`。首轮实测 TUN 上传/下载分别增加 1696/1264、2610/1540、2570/1780 字节。shoes 检查了切换导致重连以及卡片、设置、节点的提示。 |
| 协议互通、批量真连 | `verify_compat_interop.py` 常规与 `--probe` 均 exit 0；16/16，峰值 8，首轮 3710 ms。最终套件同项 16/16、peak 8、2063 ms。正常结束和取消均释放监听，shoes 已停止。 |
| 服务器与邀请 | 临时 Debian 部署后 `deployed, invited, reopened with local key`。绑定已有部署保持服务程序、配置、单元文件哈希不变，只增加一把授权公钥。主机密钥替换后界面返回「服务器身份已变化，操作已停止」。第二身份 `claimed, saved, connected, then lost authenticated connection after revoke`。 |
| 旧管理功能 | `extended.py` exit 0。临时 Debian 的部署、DNS 包、认证和 HTTPS 200、原密钥配置续用、备用节点、固定出口、重复领取、加密迁移与撤销状态检查均完成。 |
| 订阅全设备 VPN | `verify_subscription_vpn.py` 以实验室 VLESS 运行，浏览器取得节点侧回执，独立进程验证公网 HTTPS、DNS/TCP/UDP；`matrix exit 0`。其余协议的互通和批量测试覆盖见上行。 |
| 横屏提示定位复测 | 最新活动节点卡片下，旧测试只往下滚，超时截图显示它已越过提示并停在订阅总览底部，网络回执仍全部正确。SplitCheck 改为在实际滚动容器内查找、到边界后反向。复测 shoes 用时 6.7 s，依次取得 card/settings/row，返回 `home toggle reconnected; card, settings and node warning captured` 与 -1。随后重测双来源总览和四态连接动效。 |
| 系统移除动画 | 雷电将 ANIMATOR_DURATION_SCALE 设为 0 后，连接、开始、成功、断开四态均通过。已连接界面 4.2 秒只呈现 4 帧，符合每秒时钟刷新；恢复了原系统动画设置。 |
| 邀请逻辑与超长二维码 | simple 返回 `standard branded, long unbranded and oversize share-only checked`。剪贴板接受两种合法形式，拒绝五种无效输入；默认 7 天、1 台、不限流量，MB/GB 换算通过。 |
| 实际订阅 | 雷电运行 refresh-source 得到 41 个节点、96.1 GB / 1200 GB、剩余 1104 GB。probe-batch 为 40/41、peak 8、18080 ms；单节点路径 probe-source 同为 40/41。该次有一个节点未通过真连。 |
| 公网分流回归 | Xray 的国内查询与物理出口一致，境外查询和全局的节点出口一致；shoes 的国内和境外均走节点，符合既有提示。私人实验室节点与物理网络共用公网出口，两者一致，路径区分由 split-lab 的服务端回执验证。三次 instrumentation 均为 -1。 |
| 全设备 VPN 断网恢复 | 全局代理、绕过大陆各在断网前及恢复后运行独立 vpnprobe，四次均得到默认网络 VPN、两个 HTTPS 200、32/1200/1472 字节 UDP 回显及 instrumentation -1。适配了 `tools/verify_android_vpn_recovery.py` 的已连接标签。测试结束恢复原偏好与网络。临时 WSL 环境输出 `Stopped 4 isolated guest init processes; deleted disposable guest files and credentials`。 |




基线 `android/split-routing@d7ba46b`，工作分支 `android/simple-mode`。所有改动保留在本地工作树，供检查。

雷电和 S24 的本轮 crash 缓冲区均未发现 Nexus 进程崩溃记录。两台设备的截图已按浅、深两套逐屏与 HTML 静帧核对，原尺寸 PNG 保留系统栏与真实字体缩放。

相同连接动画另一次独立采集得到 450 帧，p50 **6.33 ms**、p90 **7.79 ms**。两帧分别为 8.86 ms 和 19.84 ms，对应系统截止时间 8.33 ms 和 16.67 ms；其中进入绘制之前分别耗时 5.78 ms 和 13.38 ms。切到后台后的采样窗口同样新增 0 帧。逐帧原始数据在 `E:/temp/nexus-signal-shots/s24-frame-times.csv`。锁屏验证使用前一轮相同的圆环实现。第二次解锁后已补拍最终版六屏和两种连接中画面。相机对外部屏幕的识别、微信实际收件、人工判断触感强弱，以及真机上的 VPN 连通性未列入本轮实测。

## 审查（Claude）

逐屏截图与设计稿一致。服务器密钥存储、主机身份固定、`restrict` 授权和邀请格式校验没有发现问题。本轮修正了以下几处：

- **重装服务器后无路可走。** 主机身份固定后，用户重装 VPS 会永远停在「服务器身份已变化」。现在出现该错误时，提供「我重装过这台服务器」入口。二次确认会提示可能有人冒充，确认后由 `OwnerAccess.forget` 只清除该主机的固定身份、管理密钥和就绪标记，再输入一次密码即可重新绑定。`simple` 用例新增对 forget 的检查：只删除目标主机，其他主机保留。
- **数字字体假粗体。** `space_grotesk.ttf` 是可变字体，默认实例为 Light。原先只注册了一个 Font，粗体时长靠系统合成。现在按 400/500/600/700 四档分别注册 `FontVariation.weight`。
- **剪贴板提示过频。** 每次回到前台都会读取任何新的文本剪贴板，Android 12+ 每次都会弹出「已粘贴」提示。现在只读取 10 分钟内复制的文本。
- **设备列表不自动加载。** 进入已绑定的服务器页时，自动拉取设备列表，不再需要先点「已授权设备」。

复测：雷电 `simple`、`simple-shots`、`simple-motion` 均返回 -1。连接页截图确认时长显示为真实粗体。重装后重新绑定的完整界面流程没有在 WSL 实验服务器上走过，只验证了 `forget` 的存储层行为。

## 四条常用路径

计数从已打开的首页起，到完成指定动作止。输入列按必填字段计，扫码对准和网络等待不算点击。下表单列首次系统 VPN 授权、相机授权、系统分享选人；这些系统页面由 Android 控制。旧版步数按基线可见按钮重建。

| 任务 | 改造前 | 当前路径与点击数 | 必填输入（前 → 后） |
| --- | --- | --- | --- |
| A 连接 | 私人节点点「连接 VPN」后还需确认，2 次；订阅节点要从列表选择再确认 | 已有节点时点「连接」，1 次。首次另有系统 VPN 授权 | 0 → 0 |
| B 添加机场并测试、参与自动选点 | 添加、导入、测试，再进来源管理开启「参与自动选择」并保存，至少 6 次 | 首次页「添加机场订阅」→「粘贴并添加」，2 次；有其他节点时从「节点」→「添加节点」→「粘贴并添加」，3 次。扫码入口直接导入 | 来源名和链接 2 项 → 已复制链接或相机扫码时 0 个表单字段 |
| C 部署或绑定 | 「部署与分享」→「拥有者管理」→「预览部署」→确认，4 次；已有部署每次重新管理都需登录，没有单独绑定入口 | 「我的服务器」或首次页添加入口 →「部署新服务器」或「绑定已部署的服务器」→ 确认，3 次 | 首次名称、IP、密码、主机指纹及 DNS → IP、root 密码 2 项；后续免输入 |
| D 邀请 | 首页进入管理、填登录信息、创建邀请后再点显示二维码；要发送还需关闭二维码并选分享，约 4–6 次 | 「我的服务器」→「邀请」即显示二维码，2 次；点「发送邀请」打开系统分享面板，第 3 次 | 登录资料和邀请名称 → 0 |
| D 领取 | 深链接进入后填写设备名，依次核对、领取、确认导入、发起连接、确认连接，约 5 次 | 明确打开链接或扫到码后点「接受并连接」，1 次；从前台剪贴板发现邀请则先点「查看邀请」，共 2 次 | 设备名 1 项 → 0 |

首页缺少任何节点时仅显示添加机场订阅、添加私人服务器两项。手动导入邀请进入「节点 → 添加节点 → 导入设备邀请」；打开邀请链接或扫描邀请二维码直接进入领取页。连接状态、断开和网络等待状态在同一位置。原来的公钥导出、配置导入、密钥轮换和本地端口转发在「设置 → 高级」；固定出口、按应用代理及诊断在设置中。节点页保留来源管理、导入导出、收藏、刷新、质量排序和手动测试。服务器迁移、服务状态、更新、回滚与备份恢复归入服务器高级管理。

## 实现与安全

- 首页、导航与视觉改动位于 `MainActivity.kt`、`SimpleDesign.kt`、`ConnectionUi.kt`、`network_globe.xml` 和 `NetworkScreen.kt`。保留 Compose Material 3，底部仅有连接、节点、我的服务器。首次添加、日常连接、节点管理共用导航。窄屏二维码上下排列，横屏二维码和分享操作并排。
- （历史，已被 Signal 取代）上一版曾用低饱和粉蓝配色。现行配色和对比度以 `docs/design/signal/Tokens.dc.html` 与 `ui/Theme.kt` 为准。
- `TunnelService.kt` 提供本次 TUN 接口名，速率从该接口实际收发字节按单调时钟计算。优先读取内核接口计数，Android 12 起可回退到系统接口统计。无法读取时对应速率显示「不可用」。上传和下载含 VPN 接管的流量，按应用排除的流量不计入。
- `Subscriptions.kt`、`SubscriptionScreen.kt`、`SubscriptionRefresh.kt` 复用既有订阅加密存储、刷新和真连引擎。粘贴或扫码时获取来源后，以已有 WorkManager 排队测试，离开节点页也能运行。VPN 占用引擎或没有物理网络时等待重试。自动选择在符合来源额度和近期失败规则的节点中，优先采用当前物理网络下 24 小时内成功且耗时最短的一项。
- `NodeManager.kt`、`OwnerAccess.kt`、`SimpleServer.kt`、`ServerScreen.kt` 处理首次信任、绑定、部署、管理和分享。先探测服务器公钥，此连接拒绝认证；用户确认后，在新的严格固定主机密钥的会话里使用一次 root 密码。再次连接或绑定时主机身份变化会报警并停止。高级界面对已记录的默认端口主机也校验已有固定值。首次信任依赖首次连接的网络和用户输入的地址。
- 每台服务器分别生成本机管理密钥。优先尝试 Ed25519；雷电实测走兼容分支 **ECDSA P-256**。私钥用 Android Keystore 的 AES-GCM 密钥加密，主机和固定指纹作为附加认证数据。SSH 库在使用时需要短暂得到解密后的私钥，因此它与直接在 Keystore 内签名的 VPN 设备身份是两种凭据。App 禁止备份。root 密码仅保存在本次操作内存中。
- 绑定检查已有部署，幂等追加带 `restrict` 的公钥，并以新密钥读取设备列表。绑定不签发新的 VPN 设备身份。管理公钥有 root 管理能力；删除手机上的 App 后，服务器上的公钥仍需由拥有者自行从 `authorized_keys` 移除。解绑和密钥跨手机迁移没有作为本版快捷操作。
- 新部署的默认连接端口和邀请端口沿用 8443、8444，默认解析器采用 1.1.1.1，可在首次表单「高级」修改。邀请默认链接和设备有效期均为 7 天、1 台、不限流量。设备期限可配置至 365 天，现有服务端接受的链接领取窗口上限为 7 天。流量输入和设备累计量使用十进制 MB/GB。
- 分享含二维码、系统发送和复制。App 只在前台窗口获得焦点后检查文本剪贴板，去重同一次内容，校验完整 `nexus://invite` 格式。复制邀约时设置 Android 13+ 敏感剪贴板标志。发现邀请须由用户确认；扫码、文件、深链接走相同的邀请验证和证书校验。设备名默认手机型号。
- **最近使用时间受旧服务端接口限制。** 设备列表只有名称、期限、额度、已用量和撤销状态，UI 明示「最近使用：服务器未提供」。没有把查询时间、签发时间或额度变化当成设备使用时间。服务端、协议、Android 依赖和 Rust 依赖均未改动。

## 验证

环境为 Windows、NexusLab Debian 13、雷电 `emulator-5554`。NexusLab 已有一套节点，本次使用其内部独立的临时 Debian 根文件系统和 systemd/SSH 目标，以隔离新部署、绑定和迁移测试。测前检查 `route print` 和默认路由接口。Android 构建指定 `NEXUS_SHOES_TARGET=E:/temp/nexus-shoes-target`，复用原 shoes 产物。两台设备安装前核对原有包签名，使用 `adb install -r`。

| 项目 | 实际输出或观察 |
| --- | --- |
| Windows / Linux | 两端 fmt、workspace 全目标 Clippy 均退出 0；各 15 个单测通过，0 失败。带 `a14ec74` 的 `test_tunnel.py` 两端均为 `18 integration checks passed`，退出 0。Windows UDP 回显线程在关闭时打印一条异常提示 |
| Android Debug | App、AndroidTest、vpnprobe 三个 assemble 目标均 `BUILD SUCCESSFUL` |
| 输入逻辑单测（Android 运行时） | `clipboard accepts 2 forms, rejects 5; 7 days / 1 device / unlimited; decimal MB and GB` |
| 新部署与重进管理 | `deployed, invited, reopened with local key`，返回 -1 |
| 绑定已有部署 | `bound existing installation and listed devices using local key`；`existing deployment/config/unit hashes identical; original authorized keys retained, exactly one new public key` |
| 主机密钥变化 | 在临时目标更换 SSH 主机密钥，原管理操作报「服务器身份已变化，操作已停止。请先向服务器提供商核实。」恢复实验密钥后可继续 |
| 第二个身份与撤销 | 实际点击领取页按钮，`second identity claimed, saved, connected, then lost authenticated connection after revoke`。撤销后认证连接终止；系统 VPN 保留原来的失败保护行为 |
| CatalogCheck 默认 / formats / refresh / probe-vpn | 默认 `3 sources, isolated update, encrypted persistence, native outbound validation`；formats 25 项，含 3 种元信息；refresh 的失败保留、退避、id、nodeKey、VPN 主备跳过和补更通过；`active VPN: real-test button disabled, disconnect hint visible` |
| interop / probe-lab | 16/16 节点侧 HTTP 回执；`16 / 16 passed, peak 8, 2104 ms`；正常和取消后 listener 释放、shoes 停止 |
| split-lab | 私人、Xray、shoes 各四项 TCP/UDP 与 DNS 回执匹配，均返回 -1。TUN 计数同时增长，上传/下载增量分别为 **1636/1224**、**2706/1540**、**2666/1780** 字节。实际点首页开关验证重连及 shoes 提示 |
| vpnprobe 与断网恢复 | 全局和绕过模式各测断网前、恢复后。每次均为 `default=VPN`、两个 HTTPS 200、32/1200/1472 字节 UDP 回显。两轮均输出 `Offline: waiting state reached, VPN routes retained` 和 `Recovered: private node authenticated and VPN ready` |
| 旧版拥有者功能 | key、deploy、claim、private、packets、rotate、failover、migration-crypto、migration-ssh、cleanup 已执行。已收到认证 HTTPS 200、DNS transaction/answers verified、旧端口拒绝新连接；固定故障出口三次未切走，解除后验证备用节点；两个隔离系统间迁移后 CA、撤销状态、额度和活动服务均通过，出口变化时 VPN 暂停 |
| 真实订阅刷新与真连 | 刷新得到 41 个节点，已用 96.0 GB／总量 1200 GB，剩余 1104 GB。批量实测 30/41、并发峰值 8、35.763 秒；随后逐节点访问回执 40/41。两次实网结果有差异，未独立定位差异原因 |
| split-public | Xray 绕过模式下国内出口与直连一致，海外与全局一致；shoes 两个目标均与全局一致。私人节点与物理网络共用出口，无法由公网结果区分路径，分流证据见 split-lab。Xray 1 MiB 下载/上传：绕过 6.69/6.17 Mbps，全局 4.98/6.12 Mbps，各一次 |
| 订阅全设备 VPN | Android 文档选择器导入成功。以 VLESS 建连，通过浏览器回执、公共 HTTPS 和独立 DNS/TCP/UDP；关闭 VLESS 测试服务后切到 Trojan，重复检查通过。断开后普通网络与两个公共 HTTPS 页面验证通过，驱动退出 0。本轮该驱动选取了一个起始协议 |
| 正式安全扫描 | Codex Security 预检确认本地工作树。启动正式差异扫描返回 `Review changes requires a non-bare Git worktree with a resolvable HEAD`，未取得正式报告 |

前一轮浅色文字调浅后的调试包构建输出 `BUILD SUCCESSFUL in 5s`。两台设备均同签名覆盖安装。调浅前在模拟器复测 default、simple 均返回 -1；该调试包再次复测 simple，返回 -1。模拟器在 App 停止状态下还原原始偏好目录，验证 tun0 已消失。四个临时 WSL 客体和本轮产生的服务器测试凭据已清理。

测试代码在 `SimpleCheck.kt`、`SplitCheck.kt`、`CatalogCheck.kt`。断网恢复和订阅矩阵驱动同步更新了页面导航或文字。

## 首页布局、动效与字体复审

有订阅时，竖屏上方紧凑排列连接、节点和分流，随后直接列出全部订阅；横屏左侧放连接控制，右侧独立滚动总览。只有私人节点时沿用居中的电源键。无节点时仅展示添加订阅、私人服务器两个入口。已连接时显示时长、上传、下载三个读数。节点长名称有独立空间，收藏移入详情；添加节点的面板集中粘贴、扫描、文件、手动输入与设备邀请。扫码到合规邀请时仍需在领取页确认。

连接键按下/松开约 80/160 ms；已认证后路径在 480 ms 内依次点亮，按钮底色和前景同时切换。页面进入约 200 ms，高级表单使用 Compose 的展开/收起。空闲不循环播放动效。主界面的“连接中”转圈遵循系统动画设置，按钮文字和状态独立表达结果。

界面使用 400W、500W 两套简体圆体子集，覆盖本版静态文字、GB 2312 常用区与 ASCII，未包含的动态字符由 Android 回退。子集保留字体版权并更名为 Nexus UI Rounded，随包附 OFL 原文。两个 TTF 各含 3,870 个 Unicode 映射，原始大小共 3,092,168 字节，APK 内两个字体条目的压缩数据共 1,892,399 字节。一次性制作使用工作区外的 FontTools，不需要 Android 运行时或构建依赖。原始文件 SHA-256 分别为 `d14f5a453a221525f0db4887412712cbc03a84d0dc44ce2d22d51d1c136ed9a1` 和 `f2ab724d799497b9eafed501528fbc15ba6987769b767ab450ee28f7b81bb4d8`。[字体发布版](https://github.com/lxgw/975Yuan/releases/tag/26.07.26)

| 本轮布局与动效验证 | 实际结果 |
| --- | --- |
| Debug 构建 | 最后一轮 App、AndroidTest、vpnprobe 三目标输出 `BUILD SUCCESSFUL in 5s`，已核对 APK 内两份字体和 OFL 文件 |
| 页面与邀请入口 | 雷电横屏与 S24 Ultra 竖屏分别检查明暗主题，`6 screens; sample invite; no VPN requested`。通过新增的「节点 → 添加节点 → 导入设备邀请」入口实际到达粘贴页并返回。相机硬件未参与 |
| 动效及减少动态效果 | 模拟器默认设置与动画时长系数 0 下两轮均得到 `idle, starting, connected, disconnected; simulated state, no VPN requested`。测试结束恢复系统设置。录屏使用本地合成状态 |
| 控件与分流 | probe-vpn 返回 `active VPN: real-test button disabled, disconnect hint visible`。三个 split-lab 引擎各自在全局和绕过下得到 TCP、UDP 及 DNS 回执；私人、Xray、shoes 上传/下载计数增量为 1616/1224、2706/1540、2666/1780 字节。实际点开关得到 `home toggle reconnected; card, settings and node warning captured` |
| 新导入界面 | VLESS 测试订阅经 Android 文件选择器导入；正常连接及切到 Trojan 备用节点后，均验证浏览器回执、公共 HTTPS、独立 DNS/TCP/UDP。记录 `Disconnected: ordinary network and both public HTTPS pages verified`，驱动退出 0 |
| 交付包实连 | 调整按钮文字变色与速率排版后，交付包在 shoes 实验节点复测。全局与绕过两种模式的 TCP/UDP 及 DNS 回执匹配；TUN 上传/下载增量 2886/1780 字节，返回 -1。模拟器原配置已在停 App 状态下还原，tun0 已消失 |
| 测试范围 | 布局和字体迭代期间未重复执行前表中的 Windows/Linux Rust、旧服务器部署、迁移和撤销全套回归。前表对应简单模式开发阶段的实测记录 |

## 恢复首页订阅总览

上一轮把总览放到了「节点 → 订阅管理」，因此启动 App 后看不到订阅。现在首页直接使用同一个 `SubscriptionOverview`。按现有来源顺序全部显示，来源多时连续滚动，不设预览条数上限。支持数按转换结果统计，真连通过数只取当前物理网络最近 24 小时记录。流量、有效期、上次成功更新及失败原因均取自该来源。合计标注提供流量的来源数。低于 10% 的余量和不足 7 天的有效期分别标红。总览旁可直接添加订阅，点击来源进入其管理弹窗。

`MainActivity.kt` 在 IO 线程装载来源、节点、支持情况和测速记录；`SubscriptionScreen.kt` 共用相同行构造、支持判定和底层网络观察；`SimpleDesign.kt` 保留连接与分流，并给总览留出首屏空间。变更测试在 `SimpleCheck.kt` 和 `CatalogCheck.kt`。

- 构建 App、AndroidTest、vpnprobe 三目标，`BUILD SUCCESSFUL in 10s`。S24 Ultra 同签名覆盖安装。
- S24 Ultra 浅色和深色分别运行合成双来源用例，均返回 `two sources; support 4/5; 24-hour passes 2; stale result excluded; total visible`。两个来源及合计同屏可见，另一个来源未提供流量或到期时间。截图用演示数据，未改动真机订阅。
- 雷电浅/深色从实际启动入口读取已保存的 Airport-Test，两次均返回 `Airport-Test visible on launch; add opened the importer; source opened its management dialog`。画面为支持 41/41、24 小时内真连通过 36、已用 84.2 GB／总量 1200 GB／剩余 1116 GB、到期未提供。这是当时的本地统计，不代表本轮重新跑过 41 个节点真连。
- refresh 复测返回 `failure preserved snapshot; backoff, id, nodeKey, VPN primary/backup skip and disconnect catch-up passed`，simple 输入检查通过。
- 本次总览截图目录为 `E:/temp/nexus-simple-mode/home-overview/`，真机原图为 `R5GL14PWDNA-light.png`、`R5GL14PWDNA-dark.png`，对照图为 `s24-overview.jpg`。雷电同目录 `emulator-5554-light.png`、`emulator-5554-dark.png`，仅含订阅区的截图为 `real-subscription.png`。
- 默认 CatalogCheck 返回 `3 sources, isolated update, encrypted persistence, native outbound validation`。在雷电和 WSL 的 shoes 实验节点从新首页切换分流，得到 `home toggle reconnected; card, settings and node warning captured`，双模式 TCP/UDP/DNS 回执通过，TUN 计数增量 2886/1780 字节。检查后已停止 App、还原模拟器配置，确认 tun0 消失。
- 总览这次修改验证 Android 页面与数据统计；此前 Windows/Linux 和服务器回归对应前文记录。

## 截图与验收范围

最终淡粉蓝圆体版截图在工作区外 `E:/temp/nexus-simple-mode/layout-motion/`。每台设备各有浅色、深色 6 张 PNG，共 24 张。文件名为下表前缀加后缀。真机仅进行界面检查；首次和待连接状态展示生产组件和合成数据，邀请二维码和领取页使用不可兑换的测试邀请。服务器和节点页为实际页面。真机未发起 VPN 授权、部署、绑定或领取，没有操作 v2rayNG。

| 设备及主题前缀 | 六个文件后缀 |
| --- | --- |
| `R5GL14PWDNA-light-` | `first.png`、`connect.png`、`nodes.png`、`server.png`、`invite.png`、`receive.png` |
| `R5GL14PWDNA-dark-` | 同上 |
| `emulator-5554-light-` | 同上 |
| `emulator-5554-dark-` | 同上 |
| `connection-review.jpg` | S24 Ultra 浅色、深色连接页 |
| `emulator-live-connection.png` | 雷电连接 WSL 实验节点时截取的实际首页 |
| `connection-motion.mp4`、`connection-motion.gif` | 模拟器屏幕录制，使用合成状态检查连接、断开过渡 |

新部署和绑定验收调用了 Android 正式拥有者接口；没有录制从填写完整表单开始的逐次点击。粘贴后的后台测速、实机相机扫码未做端到端计时。

未在真实 VPS 操作。用户用自己 VPS 在真机完成最终端到端分享验收。实际相机识别、微信内部的分享选人和系统分享接收端未作为自动化通过项。

## 真机验收与界面二次优化（Claude，2026-11-20）

### 真机实测（S24 Ultra，Airport-Test 订阅，台湾/韩国住宅节点）

订阅链接由 `source-export` / `source-import` 两个 QA 模式在两台设备的 App 私有目录间直接传递，没有输出，也没有在电脑上落盘，导入后两端都已删除。下表的 IP 只做「与直连出口是否相同」的比较，不记录具体地址。

| 项目 | 结果 |
| --- | --- |
| 连接 | 首次点圆环后接受系统 VPN 授权，连接成功 |
| 绕过大陆 | 国内回显 = 直连出口，境外回显 = 节点出口；google 204 / baidu 200 |
| 全局代理 | 境外走节点；国内回显站（ipip.net）经节点访问失败，baidu 200。判断是回显站拒绝境外来源，未深查 |
| Wi-Fi → 5G | 关闭 Wi-Fi 后 1.5 秒恢复访问，VPN 底层网络切到蜂窝 |
| 5G 下绕过大陆 | **国内回显失败**，境外和 baidu 正常。原因未定位，见下方待办 |
| 5G → Wi-Fi | 2.5 秒切回，底层网络更新，分流正常 |
| 后台 60 秒 | 仍在保护中，分流正常 |
| 进程被杀（kill -9） | **VPN 随进程消失，60 秒内没有重启**，流量回落为直连，境外不可达。公开发布前需要处理，见下方待办 |

另一个发现：已连接页面一直在动画，Android 的 uiautomator 始终拿不到「空闲」状态，无法读取界面。真机脚本因此改用连接状态加真实请求来判断。用户使用不受影响，但自动化测试和部分无障碍工具会受影响。

### 界面二次优化

- 节点地区优先识别名称里的中文地名（台湾→TW、香港→HK 等），再看旗帜表情。机场常把港台节点标成 🇨🇳 旗，之前会显示成 CN。显示名去掉了开头的旗帜。
- 节点列表改为按订阅分组的整卡列表（`GroupPlace`），组内用分隔线。信号格按延迟着色，未测的节点只显示「未测」。右侧「›」改成选择圆圈（已选为绿勾），语义仍是「使用此节点」。
- 节点页头部缩为一行「N 个节点 · M 个订阅」，加小号「添加节点」按钮。
- 连接页当前节点改成整卡可点的 `CurrentNode`。速率卡只在已连接时出现。订阅总览每个来源一张卡：剩余量大号数字、进度条，单来源时不再显示合计。
- 我的服务器：已绑定时依次是状态卡、邀请按钮、三格限制摘要、设备分组列表（带刷新）、维护分组列表。未绑定时表单放进一张卡，并说明密码不会保存。
- 回归（雷电）：`simple`、`simple-shots`、`simple-overview`、`simple-motion`、默认 CatalogCheck 均返回 -1。split-lab 与 probe-vpn 需要实验室 VPN，本轮未重跑；节点行仍保留 shoes 提示文字。
