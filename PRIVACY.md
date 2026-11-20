# Nexus 隐私说明

Nexus 不设账号，不收集或向开发者上传个人数据、活动记录、统计信息或崩溃报告。应用未接入统计、广告或第三方崩溃上报 SDK。

订阅、节点配置和本机私钥保存在应用私有存储或 Android Keystore，应用禁止系统备份。你导入订阅时会联系所填来源，刷新时也会联系该来源。连接节点会与所选服务通信。私人服务器管理通过 SSH 直连你指定的服务器，首次绑定密码仅用于本次登录。创建或领取邀请会联系指定服务器。你主动分享的邀请、导出的配置或高级备份由你保管。

检查更新仅请求 https://api.github.com/repos/shejiang000-prog/Nexus/releases/latest，使用 Nexus/版本号 作为 User-Agent。GitHub 能看到正常网络请求的来源地址。可在设置中关闭自动检查。「去下载」会打开系统浏览器。

节点真连测试首先访问 https://cp.cloudflare.com/generate_204 ，失败后访问 https://example.com/ 。网页诊断和自动备用节点验证也会访问 https://example.com/ 。手动 1 MiB 下载测速访问 https://speed.cloudflare.com/__down?bytes=1048576 ，上传测速访问 https://speed.cloudflare.com/__up 。主动查询出口或开启出口监测后访问 https://ipinfo.io/json 。服务提供方会看到请求所使用的出口地址。DNS 会按所选分流和节点配置发往相应解析器。

最多保留最近五份崩溃记录，每份最多 64 KiB，仅含经筛选的调用堆栈、应用版本和 Android 系统版本。你可以在「关于」里查看并选择是否复制。反馈须经确认后才会打开 GitHub，预填标题、App 版本、系统版本和机型；是否提交由你决定，崩溃记录不会自动附加。

应用回到前台时会在本机识别最近复制的纯文本邀请或疑似 HTTPS 订阅链接，需要你确认才会进入领取或导入流程。剪贴板内容不会发给 Nexus 或 GitHub。
