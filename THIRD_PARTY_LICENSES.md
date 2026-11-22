# 第三方许可证清单

Android 数字字体 Space Grotesk 依 SIL Open Font License 1.1 分发。版权与完整许可见 `assets/licenses/space-grotesk/OFL.txt`。[官方字体文件](https://github.com/google/fonts/tree/main/ofl/spacegrotesk)

以下为两个 Cargo.lock 解析的注册包，包含构建依赖及非当前目标的条件分支。版本、SPDX 或上游许可文件来自对应 Cargo 包元数据。

核心采用 Quinn（MIT/Apache-2.0）、Tokio（MIT）、rustls（多许可选择）、JNI（MIT/Apache-2.0）。ring 涵盖 ISC、Apache-2.0 与随附源码许可，发行时保留 LICENSE、LICENSE-BoringSSL、LICENSE-other-bits 及嵌入来源声明。PC WebView 使用平台运行时。

AndroidX、AGP、Gradle、Kotlin 使用 Apache-2.0；SDK/NDK 的工具、系统库和平台组件适用其随附许可。桌面依赖包含 MPL-2.0 文件级义务；分发前应准备对应版本的受涵盖源码及获取路径，并随包保留版权、许可证和 NOTICE。Unicode 数据保留随包授权。含多个 OR 选项的库须记录实际选择；多个 AND 选项同时适用。

本地 Android Debug 开发版新增以下依赖。发行前应随二进制核对完整 notices、对应源码及间接依赖。

- [libXray v26.11.9](https://github.com/XTLS/libXray/tree/v26.11.9) 使用 MIT，其 Android 库内记录的 [Xray-core 676eaf66f8f0](https://github.com/XTLS/Xray-core/tree/676eaf66f8f0) 使用 MPL-2.0。APK 的 `assets/licenses/xray/` 包含两份上游许可和对应的源码地址。
- [shoes v0.3.8](https://github.com/cfal/shoes/tree/v0.3.8) 使用 MIT，构建同时打包上游 LICENSE、THIRD_PARTY_NOTICES.md 及 licenses 目录。
- 分流规则使用 v2fly/domain-list-community（20261118141523，MIT）和 gaoyifan/china-operator-ip（be3de75a，MIT）。完整声明随 APK 放在 assets/licenses/split-routing/。
- AndroidX WorkManager（androidx.work:work-runtime-ktx 2.10.1）使用 Apache-2.0。
- JSch 2.28.8 使用 BSD-3-Clause，SnakeYAML 2.6、ZXing Core 3.5.4 和 ZXing Android Embedded 4.3.0 使用 Apache-2.0。rusqlite 0.43 使用 MIT，SQLite 核心为公有领域。

| 包 | 版本 | 上游许可 | 依赖树 |
|---|---|---|---|
| [adler2](https://crates.io/crates/adler2/2.0.1) | 2.0.1 | 0BSD OR MIT OR Apache-2.0 | desktop |
| [aho-corasick](https://crates.io/crates/aho-corasick/1.1.5) | 1.1.5 | Unlicense OR MIT | desktop |
| [alloc-no-stdlib](https://crates.io/crates/alloc-no-stdlib/3.0.0) | 3.0.0 | BSD-3-Clause | desktop |
| [alloc-stdlib](https://crates.io/crates/alloc-stdlib/0.3.0) | 0.3.0 | BSD-3-Clause | desktop |
| [android_system_properties](https://crates.io/crates/android_system_properties/0.1.6) | 0.1.6 | MIT OR Apache-2.0 | desktop |
| [anyhow](https://crates.io/crates/anyhow/1.0.104) | 1.0.104 | MIT OR Apache-2.0 | desktop |
| [arrayvec](https://crates.io/crates/arrayvec/0.7.8) | 0.7.8 | MIT OR Apache-2.0 | core |
| [atk](https://crates.io/crates/atk/0.18.2) | 0.18.2 | MIT | desktop |
| [atk-sys](https://crates.io/crates/atk-sys/0.18.2) | 0.18.2 | MIT | desktop |
| [atomic-waker](https://crates.io/crates/atomic-waker/1.1.2) | 1.1.2 | Apache-2.0 OR MIT | desktop |
| [autocfg](https://crates.io/crates/autocfg/1.5.1) | 1.5.1 | Apache-2.0 OR MIT | desktop |
| [base64](https://crates.io/crates/base64/0.21.7) | 0.21.7 | MIT OR Apache-2.0 | desktop |
| [base64](https://crates.io/crates/base64/0.23.1) | 0.23.1 | MIT OR Apache-2.0 | desktop |
| [bit-set](https://crates.io/crates/bit-set/0.11.1) | 0.11.1 | Apache-2.0 OR MIT | desktop |
| [bit-set](https://crates.io/crates/bit-set/0.8.0) | 0.8.0 | Apache-2.0 OR MIT | desktop |
| [bit-vec](https://crates.io/crates/bit-vec/0.10.2) | 0.10.2 | Apache-2.0 OR MIT | desktop |
| [bit-vec](https://crates.io/crates/bit-vec/0.8.0) | 0.8.0 | Apache-2.0 OR MIT | desktop |
| [bitflags](https://crates.io/crates/bitflags/1.3.2) | 1.3.2 | MIT/Apache-2.0 | core, desktop |
| [bitflags](https://crates.io/crates/bitflags/2.13.2) | 2.13.2 | MIT OR Apache-2.0 | desktop |
| [block-buffer](https://crates.io/crates/block-buffer/0.10.4) | 0.10.4 | MIT OR Apache-2.0 | desktop |
| [block-buffer](https://crates.io/crates/block-buffer/0.12.1) | 0.12.1 | MIT OR Apache-2.0 | desktop |
| [block2](https://crates.io/crates/block2/0.6.2) | 0.6.2 | MIT | desktop |
| [brotli](https://crates.io/crates/brotli/9.1.3) | 9.1.3 | BSD-3-Clause AND MIT | desktop |
| [brotli-decompressor](https://crates.io/crates/brotli-decompressor/6.3.2) | 6.3.2 | BSD-3-Clause/MIT | desktop |
| [bs58](https://crates.io/crates/bs58/0.5.1) | 0.5.1 | MIT/Apache-2.0 | desktop |
| [bumpalo](https://crates.io/crates/bumpalo/3.20.4) | 3.20.4 | MIT OR Apache-2.0 | core, desktop |
| [bytemuck](https://crates.io/crates/bytemuck/1.25.2) | 1.25.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [byteorder](https://crates.io/crates/byteorder/1.5.0) | 1.5.0 | Unlicense OR MIT | core, desktop |
| [bytes](https://crates.io/crates/bytes/1.12.2) | 1.12.2 | MIT | core, desktop |
| [cairo-rs](https://crates.io/crates/cairo-rs/0.18.5) | 0.18.5 | MIT | desktop |
| [cairo-sys-rs](https://crates.io/crates/cairo-sys-rs/0.18.2) | 0.18.2 | MIT | desktop |
| [camino](https://crates.io/crates/camino/1.2.6) | 1.2.6 | MIT OR Apache-2.0 | desktop |
| [cargo-platform](https://crates.io/crates/cargo-platform/0.1.9) | 0.1.9 | MIT OR Apache-2.0 | desktop |
| [cargo_metadata](https://crates.io/crates/cargo_metadata/0.19.2) | 0.19.2 | MIT | desktop |
| [cargo_toml](https://crates.io/crates/cargo_toml/1.0.1) | 1.0.1 | Apache-2.0 OR MIT | desktop |
| [cc](https://crates.io/crates/cc/1.11.2) | 1.11.2 | MIT OR Apache-2.0 | core, desktop |
| [cesu8](https://crates.io/crates/cesu8/1.1.0) | 1.1.0 | Apache-2.0/MIT | core, desktop |
| [cfb](https://crates.io/crates/cfb/0.14.0) | 0.14.0 | MIT | desktop |
| [cfg-expr](https://crates.io/crates/cfg-expr/0.15.8) | 0.15.8 | MIT OR Apache-2.0 | desktop |
| [cfg-if](https://crates.io/crates/cfg-if/1.0.5) | 1.0.5 | MIT OR Apache-2.0 | core, desktop |
| [cfg_aliases](https://crates.io/crates/cfg_aliases/0.2.2) | 0.2.2 | MIT | core |
| [chacha20](https://crates.io/crates/chacha20/0.10.2) | 0.10.2 | MIT OR Apache-2.0 | core |
| [chrono](https://crates.io/crates/chrono/0.4.45) | 0.4.45 | MIT OR Apache-2.0 | desktop |
| [combine](https://crates.io/crates/combine/4.6.9) | 4.6.9 | MIT | core, desktop |
| [const-oid](https://crates.io/crates/const-oid/0.10.3) | 0.10.3 | Apache-2.0 OR MIT | desktop |
| [cookie](https://crates.io/crates/cookie/0.18.2) | 0.18.2 | MIT OR Apache-2.0 | desktop |
| [core-foundation](https://crates.io/crates/core-foundation/0.10.1) | 0.10.1 | MIT OR Apache-2.0 | desktop |
| [core-foundation-sys](https://crates.io/crates/core-foundation-sys/0.8.7) | 0.8.7 | MIT OR Apache-2.0 | desktop |
| [core-graphics](https://crates.io/crates/core-graphics/0.25.0) | 0.25.0 | MIT OR Apache-2.0 | desktop |
| [core-graphics-types](https://crates.io/crates/core-graphics-types/0.2.0) | 0.2.0 | MIT OR Apache-2.0 | desktop |
| [cpufeatures](https://crates.io/crates/cpufeatures/0.2.17) | 0.2.17 | MIT OR Apache-2.0 | desktop |
| [cpufeatures](https://crates.io/crates/cpufeatures/0.3.1) | 0.3.1 | MIT OR Apache-2.0 | core, desktop |
| [crc32fast](https://crates.io/crates/crc32fast/1.5.5) | 1.5.5 | MIT OR Apache-2.0 | desktop |
| [crossbeam-channel](https://crates.io/crates/crossbeam-channel/0.5.17) | 0.5.17 | MIT OR Apache-2.0 | desktop |
| [crossbeam-utils](https://crates.io/crates/crossbeam-utils/0.8.23) | 0.8.23 | MIT OR Apache-2.0 | desktop |
| [crypto-common](https://crates.io/crates/crypto-common/0.1.7) | 0.1.7 | MIT OR Apache-2.0 | desktop |
| [crypto-common](https://crates.io/crates/crypto-common/0.2.2) | 0.2.2 | MIT OR Apache-2.0 | desktop |
| [cssparser](https://crates.io/crates/cssparser/0.37.0) | 0.37.0 | MPL-2.0 | desktop |
| [cssparser](https://crates.io/crates/cssparser/0.38.0) | 0.38.0 | MPL-2.0 | desktop |
| [cssparser-macros](https://crates.io/crates/cssparser-macros/0.7.1) | 0.7.1 | MPL-2.0 | desktop |
| [ctor](https://crates.io/crates/ctor/1.0.14) | 1.0.14 | Apache-2.0 OR MIT | desktop |
| [darling](https://crates.io/crates/darling/0.24.1) | 0.24.1 | MIT | desktop |
| [darling_core](https://crates.io/crates/darling_core/0.24.1) | 0.24.1 | MIT | desktop |
| [darling_macro](https://crates.io/crates/darling_macro/0.24.1) | 0.24.1 | MIT | desktop |
| [dbus](https://crates.io/crates/dbus/0.9.12) | 0.9.12 | Apache-2.0/MIT | desktop |
| [defmt](https://crates.io/crates/defmt/1.2.0) | 1.2.0 | MIT OR Apache-2.0 | core |
| [defmt-macros](https://crates.io/crates/defmt-macros/1.2.0) | 1.2.0 | MIT OR Apache-2.0 | core |
| [defmt-parser](https://crates.io/crates/defmt-parser/1.1.0) | 1.1.0 | MIT OR Apache-2.0 | core |
| [deranged](https://crates.io/crates/deranged/0.5.9) | 0.5.9 | MIT OR Apache-2.0 | desktop |
| [derive_more](https://crates.io/crates/derive_more/2.1.1) | 2.1.1 | MIT | desktop |
| [derive_more-impl](https://crates.io/crates/derive_more-impl/2.1.1) | 2.1.1 | MIT | desktop |
| [digest](https://crates.io/crates/digest/0.10.7) | 0.10.7 | MIT OR Apache-2.0 | desktop |
| [digest](https://crates.io/crates/digest/0.11.3) | 0.11.3 | MIT OR Apache-2.0 | desktop |
| [dirs](https://crates.io/crates/dirs/7.0.0) | 7.0.0 | MIT OR Apache-2.0 | desktop |
| [dirs-sys](https://crates.io/crates/dirs-sys/0.5.0) | 0.5.0 | MIT OR Apache-2.0 | desktop |
| [dispatch2](https://crates.io/crates/dispatch2/0.3.1) | 0.3.1 | Zlib OR Apache-2.0 OR MIT | desktop |
| [displaydoc](https://crates.io/crates/displaydoc/0.2.7) | 0.2.7 | MIT OR Apache-2.0 | desktop |
| [dlopen2](https://crates.io/crates/dlopen2/0.8.2) | 0.8.2 | MIT | desktop |
| [dlopen2_derive](https://crates.io/crates/dlopen2_derive/0.4.3) | 0.4.3 | MIT | desktop |
| [dom_query](https://crates.io/crates/dom_query/0.28.0) | 0.28.0 | MIT | desktop |
| [dom_query](https://crates.io/crates/dom_query/0.29.0) | 0.29.0 | MIT | desktop |
| [dpi](https://crates.io/crates/dpi/0.1.2) | 0.1.2 | Apache-2.0 AND MIT | desktop |
| [dtoa](https://crates.io/crates/dtoa/1.0.11) | 1.0.11 | MIT OR Apache-2.0 | desktop |
| [dtoa-short](https://crates.io/crates/dtoa-short/0.3.5) | 0.3.5 | MPL-2.0 | desktop |
| [dunce](https://crates.io/crates/dunce/1.0.5) | 1.0.5 | CC0-1.0 OR MIT-0 OR Apache-2.0 | desktop |
| [dyn-clone](https://crates.io/crates/dyn-clone/1.0.20) | 1.0.20 | MIT OR Apache-2.0 | desktop |
| [embed-resource](https://crates.io/crates/embed-resource/3.0.12) | 3.0.12 | MIT | desktop |
| [embed_plist](https://crates.io/crates/embed_plist/1.2.2) | 1.2.2 | MIT OR Apache-2.0 | desktop |
| [equivalent](https://crates.io/crates/equivalent/1.0.3) | 1.0.3 | Apache-2.0 OR MIT | desktop |
| [erased-serde](https://crates.io/crates/erased-serde/0.4.11) | 0.4.11 | MIT OR Apache-2.0 | desktop |
| [etherparse](https://crates.io/crates/etherparse/0.16.0) | 0.16.0 | MIT OR Apache-2.0 | core |
| [fastrand](https://crates.io/crates/fastrand/2.5.0) | 2.5.0 | Apache-2.0 OR MIT | desktop |
| [fdeflate](https://crates.io/crates/fdeflate/0.3.7) | 0.3.7 | MIT OR Apache-2.0 | desktop |
| [field-offset](https://crates.io/crates/field-offset/0.3.6) | 0.3.6 | MIT OR Apache-2.0 | desktop |
| [find-msvc-tools](https://crates.io/crates/find-msvc-tools/0.1.15) | 0.1.15 | MIT OR Apache-2.0 | core, desktop |
| [flate2](https://crates.io/crates/flate2/1.1.11) | 1.1.11 | MIT OR Apache-2.0 | desktop |
| [fnv](https://crates.io/crates/fnv/1.0.7) | 1.0.7 | Apache-2.0 / MIT | desktop |
| [foldhash](https://crates.io/crates/foldhash/0.2.0) | 0.2.0 | Zlib | desktop |
| [foreign-types](https://crates.io/crates/foreign-types/0.5.0) | 0.5.0 | MIT/Apache-2.0 | desktop |
| [foreign-types-macros](https://crates.io/crates/foreign-types-macros/0.2.4) | 0.2.4 | MIT/Apache-2.0 | desktop |
| [foreign-types-shared](https://crates.io/crates/foreign-types-shared/0.3.1) | 0.3.1 | MIT/Apache-2.0 | desktop |
| [form_urlencoded](https://crates.io/crates/form_urlencoded/1.2.2) | 1.2.2 | MIT OR Apache-2.0 | desktop |
| [futures](https://crates.io/crates/futures/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core |
| [futures-channel](https://crates.io/crates/futures-channel/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-core](https://crates.io/crates/futures-core/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-executor](https://crates.io/crates/futures-executor/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-io](https://crates.io/crates/futures-io/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-macro](https://crates.io/crates/futures-macro/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-sink](https://crates.io/crates/futures-sink/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-task](https://crates.io/crates/futures-task/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [futures-util](https://crates.io/crates/futures-util/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | core, desktop |
| [gdk](https://crates.io/crates/gdk/0.18.2) | 0.18.2 | MIT | desktop |
| [gdk-pixbuf](https://crates.io/crates/gdk-pixbuf/0.18.5) | 0.18.5 | MIT | desktop |
| [gdk-pixbuf-sys](https://crates.io/crates/gdk-pixbuf-sys/0.18.0) | 0.18.0 | MIT | desktop |
| [gdk-sys](https://crates.io/crates/gdk-sys/0.18.2) | 0.18.2 | MIT | desktop |
| [gdkwayland-sys](https://crates.io/crates/gdkwayland-sys/0.18.2) | 0.18.2 | MIT | desktop |
| [gdkx11](https://crates.io/crates/gdkx11/0.18.2) | 0.18.2 | MIT | desktop |
| [gdkx11-sys](https://crates.io/crates/gdkx11-sys/0.18.2) | 0.18.2 | MIT | desktop |
| [generic-array](https://crates.io/crates/generic-array/0.14.7) | 0.14.7 | MIT | desktop |
| [getrandom](https://crates.io/crates/getrandom/0.2.17) | 0.2.17 | MIT OR Apache-2.0 | core |
| [getrandom](https://crates.io/crates/getrandom/0.3.4) | 0.3.4 | MIT OR Apache-2.0 | desktop |
| [getrandom](https://crates.io/crates/getrandom/0.4.3) | 0.4.3 | MIT OR Apache-2.0 | core, desktop |
| [gio](https://crates.io/crates/gio/0.18.4) | 0.18.4 | MIT | desktop |
| [gio-sys](https://crates.io/crates/gio-sys/0.18.1) | 0.18.1 | MIT | desktop |
| [glib](https://crates.io/crates/glib/0.18.5) | 0.18.5 | MIT | desktop |
| [glib-macros](https://crates.io/crates/glib-macros/0.18.5) | 0.18.5 | MIT | desktop |
| [glib-sys](https://crates.io/crates/glib-sys/0.18.1) | 0.18.1 | MIT | desktop |
| [glob](https://crates.io/crates/glob/0.3.4) | 0.3.4 | MIT OR Apache-2.0 | desktop |
| [gobject-sys](https://crates.io/crates/gobject-sys/0.18.0) | 0.18.0 | MIT | desktop |
| [gtk](https://crates.io/crates/gtk/0.18.2) | 0.18.2 | MIT | desktop |
| [gtk-sys](https://crates.io/crates/gtk-sys/0.18.2) | 0.18.2 | MIT | desktop |
| [gtk3-macros](https://crates.io/crates/gtk3-macros/0.18.2) | 0.18.2 | MIT | desktop |
| [hash32](https://crates.io/crates/hash32/0.3.1) | 0.3.1 | MIT OR Apache-2.0 | core |
| [hashbrown](https://crates.io/crates/hashbrown/0.12.3) | 0.12.3 | MIT OR Apache-2.0 | desktop |
| [hashbrown](https://crates.io/crates/hashbrown/0.17.1) | 0.17.1 | MIT OR Apache-2.0 | desktop |
| [heapless](https://crates.io/crates/heapless/0.9.3) | 0.9.3 | MIT OR Apache-2.0 | core |
| [heck](https://crates.io/crates/heck/0.4.1) | 0.4.1 | MIT OR Apache-2.0 | desktop |
| [heck](https://crates.io/crates/heck/0.5.0) | 0.5.0 | MIT OR Apache-2.0 | desktop |
| [hex](https://crates.io/crates/hex/0.4.3) | 0.4.3 | MIT OR Apache-2.0 | desktop |
| [html5ever](https://crates.io/crates/html5ever/0.39.0) | 0.39.0 | MIT OR Apache-2.0 | desktop |
| [html5ever](https://crates.io/crates/html5ever/0.40.1) | 0.40.1 | MIT OR Apache-2.0 | desktop |
| [http](https://crates.io/crates/http/1.5.0) | 1.5.0 | MIT OR Apache-2.0 | desktop |
| [http-body](https://crates.io/crates/http-body/1.1.0) | 1.1.0 | MIT | desktop |
| [http-body-util](https://crates.io/crates/http-body-util/0.1.5) | 0.1.5 | MIT | desktop |
| [httparse](https://crates.io/crates/httparse/1.10.1) | 1.10.1 | MIT OR Apache-2.0 | desktop |
| [hybrid-array](https://crates.io/crates/hybrid-array/0.4.15) | 0.4.15 | MIT OR Apache-2.0 | desktop |
| [hyper](https://crates.io/crates/hyper/1.12.0) | 1.12.0 | MIT | desktop |
| [hyper-util](https://crates.io/crates/hyper-util/0.1.21) | 0.1.21 | MIT | desktop |
| [iana-time-zone](https://crates.io/crates/iana-time-zone/0.1.65) | 0.1.65 | MIT OR Apache-2.0 | desktop |
| [iana-time-zone-haiku](https://crates.io/crates/iana-time-zone-haiku/0.1.2) | 0.1.2 | MIT OR Apache-2.0 | desktop |
| [ico](https://crates.io/crates/ico/0.6.0) | 0.6.0 | MIT | desktop |
| [icu_collections](https://crates.io/crates/icu_collections/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [icu_locale_core](https://crates.io/crates/icu_locale_core/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [icu_normalizer](https://crates.io/crates/icu_normalizer/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [icu_normalizer_data](https://crates.io/crates/icu_normalizer_data/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [icu_properties](https://crates.io/crates/icu_properties/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [icu_properties_data](https://crates.io/crates/icu_properties_data/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [icu_provider](https://crates.io/crates/icu_provider/2.4.0) | 2.4.0 | Unicode-3.0 | desktop |
| [ident_case](https://crates.io/crates/ident_case/1.0.1) | 1.0.1 | MIT/Apache-2.0 | desktop |
| [idna](https://crates.io/crates/idna/1.1.0) | 1.1.0 | MIT OR Apache-2.0 | desktop |
| [idna_adapter](https://crates.io/crates/idna_adapter/1.2.2) | 1.2.2 | Apache-2.0 OR MIT | desktop |
| [indexmap](https://crates.io/crates/indexmap/1.9.3) | 1.9.3 | Apache-2.0 OR MIT | desktop |
| [indexmap](https://crates.io/crates/indexmap/2.14.2) | 2.14.2 | Apache-2.0 OR MIT | desktop |
| [infer](https://crates.io/crates/infer/0.22.0) | 0.22.0 | MIT | desktop |
| [ipnet](https://crates.io/crates/ipnet/2.12.2) | 2.12.2 | MIT OR Apache-2.0 | desktop |
| [itoa](https://crates.io/crates/itoa/1.0.19) | 1.0.19 | MIT OR Apache-2.0 | desktop |
| [javascriptcore-rs](https://crates.io/crates/javascriptcore-rs/1.1.2) | 1.1.2 | MIT | desktop |
| [javascriptcore-rs-sys](https://crates.io/crates/javascriptcore-rs-sys/1.1.1) | 1.1.1 | MIT | desktop |
| [jiff](https://crates.io/crates/jiff/0.2.46) | 0.2.46 | Unlicense OR MIT | desktop |
| [jiff-core](https://crates.io/crates/jiff-core/0.1.8) | 0.1.8 | Unlicense OR MIT | desktop |
| [jiff-static](https://crates.io/crates/jiff-static/0.2.46) | 0.2.46 | Unlicense OR MIT | desktop |
| [jiff-tzdb](https://crates.io/crates/jiff-tzdb/0.1.10) | 0.1.10 | Unlicense OR MIT | desktop |
| [jiff-tzdb-platform](https://crates.io/crates/jiff-tzdb-platform/0.1.3) | 0.1.3 | Unlicense OR MIT | desktop |
| [jni](https://crates.io/crates/jni/0.21.1) | 0.21.1 | MIT/Apache-2.0 | core, desktop |
| [jni-sys](https://crates.io/crates/jni-sys/0.3.1) | 0.3.1 | MIT OR Apache-2.0 | core, desktop |
| [jni-sys](https://crates.io/crates/jni-sys/0.4.1) | 0.4.1 | MIT OR Apache-2.0 | core, desktop |
| [jni-sys-macros](https://crates.io/crates/jni-sys-macros/0.4.1) | 0.4.1 | MIT OR Apache-2.0 | core, desktop |
| [js-sys](https://crates.io/crates/js-sys/0.3.107) | 0.3.107 | MIT OR Apache-2.0 | core, desktop |
| [json-patch](https://crates.io/crates/json-patch/4.2.0) | 4.2.0 | MIT/Apache-2.0 | desktop |
| [jsonptr](https://crates.io/crates/jsonptr/0.7.1) | 0.7.1 | MIT OR Apache-2.0 | desktop |
| [keyboard-types](https://crates.io/crates/keyboard-types/0.8.3) | 0.8.3 | MIT OR Apache-2.0 | desktop |
| [libappindicator](https://crates.io/crates/libappindicator/0.9.0) | 0.9.0 | Apache-2.0 OR MIT | desktop |
| [libappindicator-sys](https://crates.io/crates/libappindicator-sys/0.9.0) | 0.9.0 | Apache-2.0 OR MIT | desktop |
| [libc](https://crates.io/crates/libc/0.2.191) | 0.2.191 | MIT OR Apache-2.0 | core, desktop |
| [libdbus-sys](https://crates.io/crates/libdbus-sys/0.2.7) | 0.2.7 | Apache-2.0/MIT | desktop |
| [libloading](https://crates.io/crates/libloading/0.7.4) | 0.7.4 | ISC | desktop |
| [libredox](https://crates.io/crates/libredox/0.1.25) | 0.1.25 | MIT | desktop |
| [link-section](https://crates.io/crates/link-section/0.19.4) | 0.19.4 | Apache-2.0 OR MIT | desktop |
| [litemap](https://crates.io/crates/litemap/0.8.3) | 0.8.3 | Unicode-3.0 | desktop |
| [lock_api](https://crates.io/crates/lock_api/0.4.14) | 0.4.14 | MIT OR Apache-2.0 | core, desktop |
| [log](https://crates.io/crates/log/0.4.35) | 0.4.35 | MIT OR Apache-2.0 | core, desktop |
| [lru-slab](https://crates.io/crates/lru-slab/0.1.3) | 0.1.3 | MIT OR Apache-2.0 OR Zlib | core |
| [managed](https://crates.io/crates/managed/0.8.0) | 0.8.0 | 0BSD | core |
| [markup5ever](https://crates.io/crates/markup5ever/0.39.0) | 0.39.0 | MIT OR Apache-2.0 | desktop |
| [markup5ever](https://crates.io/crates/markup5ever/0.40.0) | 0.40.0 | MIT OR Apache-2.0 | desktop |
| [memchr](https://crates.io/crates/memchr/2.8.3) | 2.8.3 | Unlicense OR MIT | core, desktop |
| [memoffset](https://crates.io/crates/memoffset/0.9.1) | 0.9.1 | MIT | desktop |
| [mime](https://crates.io/crates/mime/0.3.18) | 0.3.18 | MIT OR Apache-2.0 | desktop |
| [miniz_oxide](https://crates.io/crates/miniz_oxide/0.8.9) | 0.8.9 | MIT OR Zlib OR Apache-2.0 | desktop |
| [miniz_oxide](https://crates.io/crates/miniz_oxide/0.9.1) | 0.9.1 | MIT OR Zlib OR Apache-2.0 | desktop |
| [mio](https://crates.io/crates/mio/1.2.5) | 1.2.5 | MIT | core, desktop |
| [muda](https://crates.io/crates/muda/0.20.0) | 0.20.0 | Apache-2.0 OR MIT | desktop |
| [ndk](https://crates.io/crates/ndk/0.9.0) | 0.9.0 | MIT OR Apache-2.0 | desktop |
| [ndk-context](https://crates.io/crates/ndk-context/0.1.1) | 0.1.1 | MIT OR Apache-2.0 | desktop |
| [ndk-sys](https://crates.io/crates/ndk-sys/0.6.0+11769913) | 0.6.0+11769913 | MIT OR Apache-2.0 | desktop |
| [netstack-smoltcp](https://crates.io/crates/netstack-smoltcp/0.2.5) | 0.2.5 | MIT OR Apache-2.0 | core |
| [new_debug_unreachable](https://crates.io/crates/new_debug_unreachable/1.0.6) | 1.0.6 | MIT | desktop |
| [num-conv](https://crates.io/crates/num-conv/0.2.2) | 0.2.2 | MIT OR Apache-2.0 | desktop |
| [num-traits](https://crates.io/crates/num-traits/0.2.19) | 0.2.19 | MIT OR Apache-2.0 | desktop |
| [num_enum](https://crates.io/crates/num_enum/0.7.6) | 0.7.6 | BSD-3-Clause OR MIT OR Apache-2.0 | desktop |
| [num_enum_derive](https://crates.io/crates/num_enum_derive/0.7.6) | 0.7.6 | BSD-3-Clause OR MIT OR Apache-2.0 | desktop |
| [objc2](https://crates.io/crates/objc2/0.6.5) | 0.6.5 | MIT | desktop |
| [objc2-app-kit](https://crates.io/crates/objc2-app-kit/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-cloud-kit](https://crates.io/crates/objc2-cloud-kit/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-core-data](https://crates.io/crates/objc2-core-data/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-core-foundation](https://crates.io/crates/objc2-core-foundation/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-core-graphics](https://crates.io/crates/objc2-core-graphics/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-core-image](https://crates.io/crates/objc2-core-image/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-core-location](https://crates.io/crates/objc2-core-location/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-core-text](https://crates.io/crates/objc2-core-text/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-encode](https://crates.io/crates/objc2-encode/4.1.0) | 4.1.0 | MIT | desktop |
| [objc2-exception-helper](https://crates.io/crates/objc2-exception-helper/0.1.1) | 0.1.1 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-foundation](https://crates.io/crates/objc2-foundation/0.3.2) | 0.3.2 | MIT | desktop |
| [objc2-io-surface](https://crates.io/crates/objc2-io-surface/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-quartz-core](https://crates.io/crates/objc2-quartz-core/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-ui-kit](https://crates.io/crates/objc2-ui-kit/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-user-notifications](https://crates.io/crates/objc2-user-notifications/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [objc2-web-kit](https://crates.io/crates/objc2-web-kit/0.3.2) | 0.3.2 | Zlib OR Apache-2.0 OR MIT | desktop |
| [once_cell](https://crates.io/crates/once_cell/1.21.4) | 1.21.4 | MIT OR Apache-2.0 | core, desktop |
| [option-ext](https://crates.io/crates/option-ext/0.2.0) | 0.2.0 | MPL-2.0 | desktop |
| [pango](https://crates.io/crates/pango/0.18.3) | 0.18.3 | MIT | desktop |
| [pango-sys](https://crates.io/crates/pango-sys/0.18.0) | 0.18.0 | MIT | desktop |
| [parking_lot](https://crates.io/crates/parking_lot/0.12.5) | 0.12.5 | MIT OR Apache-2.0 | desktop |
| [parking_lot_core](https://crates.io/crates/parking_lot_core/0.9.12) | 0.9.12 | MIT OR Apache-2.0 | desktop |
| [percent-encoding](https://crates.io/crates/percent-encoding/2.3.2) | 2.3.2 | MIT OR Apache-2.0 | desktop |
| [phf](https://crates.io/crates/phf/0.13.1) | 0.13.1 | MIT | desktop |
| [phf](https://crates.io/crates/phf/0.14.0) | 0.14.0 | MIT | desktop |
| [phf_codegen](https://crates.io/crates/phf_codegen/0.13.1) | 0.13.1 | MIT | desktop |
| [phf_codegen](https://crates.io/crates/phf_codegen/0.14.0) | 0.14.0 | MIT | desktop |
| [phf_generator](https://crates.io/crates/phf_generator/0.13.1) | 0.13.1 | MIT | desktop |
| [phf_generator](https://crates.io/crates/phf_generator/0.14.0) | 0.14.0 | MIT | desktop |
| [phf_macros](https://crates.io/crates/phf_macros/0.13.1) | 0.13.1 | MIT | desktop |
| [phf_macros](https://crates.io/crates/phf_macros/0.14.0) | 0.14.0 | MIT | desktop |
| [phf_shared](https://crates.io/crates/phf_shared/0.13.1) | 0.13.1 | MIT | desktop |
| [phf_shared](https://crates.io/crates/phf_shared/0.14.0) | 0.14.0 | MIT | desktop |
| [pin-project-lite](https://crates.io/crates/pin-project-lite/0.2.17) | 0.2.17 | Apache-2.0 OR MIT | core, desktop |
| [pkg-config](https://crates.io/crates/pkg-config/0.3.34) | 0.3.34 | MIT OR Apache-2.0 | desktop |
| [plist](https://crates.io/crates/plist/1.10.4) | 1.10.4 | MIT | desktop |
| [png](https://crates.io/crates/png/0.18.1) | 0.18.1 | MIT OR Apache-2.0 | desktop |
| [portable-atomic](https://crates.io/crates/portable-atomic/1.15.0) | 1.15.0 | Apache-2.0 OR MIT | desktop |
| [portable-atomic-util](https://crates.io/crates/portable-atomic-util/0.2.8) | 0.2.8 | Apache-2.0 OR MIT | desktop |
| [potential_utf](https://crates.io/crates/potential_utf/0.1.6) | 0.1.6 | Unicode-3.0 | desktop |
| [powerfmt](https://crates.io/crates/powerfmt/0.2.1) | 0.2.1 | MIT OR Apache-2.0 | desktop |
| [ppv-lite86](https://crates.io/crates/ppv-lite86/0.2.21) | 0.2.21 | MIT OR Apache-2.0 | core |
| [precomputed-hash](https://crates.io/crates/precomputed-hash/0.1.1) | 0.1.1 | MIT | desktop |
| [proc-macro-crate](https://crates.io/crates/proc-macro-crate/1.3.1) | 1.3.1 | MIT OR Apache-2.0 | desktop |
| [proc-macro-crate](https://crates.io/crates/proc-macro-crate/2.0.2) | 2.0.2 | MIT OR Apache-2.0 | desktop |
| [proc-macro-crate](https://crates.io/crates/proc-macro-crate/3.5.0) | 3.5.0 | MIT OR Apache-2.0 | desktop |
| [proc-macro-error](https://crates.io/crates/proc-macro-error/1.0.4) | 1.0.4 | MIT OR Apache-2.0 | desktop |
| [proc-macro-error-attr](https://crates.io/crates/proc-macro-error-attr/1.0.4) | 1.0.4 | MIT OR Apache-2.0 | desktop |
| [proc-macro2](https://crates.io/crates/proc-macro2/1.0.107) | 1.0.107 | MIT OR Apache-2.0 | core, desktop |
| [quick-xml](https://crates.io/crates/quick-xml/0.42.0) | 0.42.0 | MIT | desktop |
| [quinn](https://crates.io/crates/quinn/0.11.12) | 0.11.12 | MIT OR Apache-2.0 | core |
| [quinn-proto](https://crates.io/crates/quinn-proto/0.11.19) | 0.11.19 | MIT OR Apache-2.0 | core |
| [quinn-udp](https://crates.io/crates/quinn-udp/0.5.16) | 0.5.16 | MIT OR Apache-2.0 | core |
| [quote](https://crates.io/crates/quote/1.0.47) | 1.0.47 | MIT OR Apache-2.0 | core, desktop |
| [r-efi](https://crates.io/crates/r-efi/5.3.0) | 5.3.0 | MIT OR Apache-2.0 OR LGPL-2.1-or-later | desktop |
| [r-efi](https://crates.io/crates/r-efi/6.0.0) | 6.0.0 | MIT OR Apache-2.0 OR LGPL-2.1-or-later | core, desktop |
| [rand](https://crates.io/crates/rand/0.10.4) | 0.10.4 | MIT OR Apache-2.0 | core |
| [rand](https://crates.io/crates/rand/0.8.8) | 0.8.8 | MIT OR Apache-2.0 | core |
| [rand_chacha](https://crates.io/crates/rand_chacha/0.3.1) | 0.3.1 | MIT OR Apache-2.0 | core |
| [rand_core](https://crates.io/crates/rand_core/0.10.1) | 0.10.1 | MIT OR Apache-2.0 | core |
| [rand_core](https://crates.io/crates/rand_core/0.6.4) | 0.6.4 | MIT OR Apache-2.0 | core |
| [rand_pcg](https://crates.io/crates/rand_pcg/0.10.2) | 0.10.2 | MIT OR Apache-2.0 | core |
| [raw-window-handle](https://crates.io/crates/raw-window-handle/0.6.2) | 0.6.2 | MIT OR Apache-2.0 OR Zlib | desktop |
| [redox_syscall](https://crates.io/crates/redox_syscall/0.5.18) | 0.5.18 | MIT | desktop |
| [redox_users](https://crates.io/crates/redox_users/0.5.3) | 0.5.3 | MIT | desktop |
| [ref-cast](https://crates.io/crates/ref-cast/1.0.27) | 1.0.27 | MIT OR Apache-2.0 | desktop |
| [ref-cast-impl](https://crates.io/crates/ref-cast-impl/1.0.27) | 1.0.27 | MIT OR Apache-2.0 | desktop |
| [regex](https://crates.io/crates/regex/1.13.2) | 1.13.2 | MIT OR Apache-2.0 | desktop |
| [regex-automata](https://crates.io/crates/regex-automata/0.4.19) | 0.4.19 | MIT OR Apache-2.0 | desktop |
| [regex-syntax](https://crates.io/crates/regex-syntax/0.8.12) | 0.8.12 | MIT OR Apache-2.0 | desktop |
| [reqwest](https://crates.io/crates/reqwest/0.13.5) | 0.13.5 | MIT OR Apache-2.0 | desktop |
| [ring](https://crates.io/crates/ring/0.17.14) | 0.17.14 | Apache-2.0 AND ISC | core |
| [rustc-hash](https://crates.io/crates/rustc-hash/2.1.3) | 2.1.3 | Apache-2.0 OR MIT | core, desktop |
| [rustc_version](https://crates.io/crates/rustc_version/0.4.1) | 0.4.1 | MIT OR Apache-2.0 | desktop |
| [rustls](https://crates.io/crates/rustls/0.23.46) | 0.23.46 | Apache-2.0 OR ISC OR MIT | core |
| [rustls-pki-types](https://crates.io/crates/rustls-pki-types/1.16.0) | 1.16.0 | MIT OR Apache-2.0 | core |
| [rustls-webpki](https://crates.io/crates/rustls-webpki/0.103.17) | 0.103.17 | ISC | core |
| [rustversion](https://crates.io/crates/rustversion/1.0.23) | 1.0.23 | MIT OR Apache-2.0 | core, desktop |
| [same-file](https://crates.io/crates/same-file/1.0.6) | 1.0.6 | Unlicense/MIT | core, desktop |
| [schemars](https://crates.io/crates/schemars/0.8.22) | 0.8.22 | MIT | desktop |
| [schemars](https://crates.io/crates/schemars/0.9.0) | 0.9.0 | MIT | desktop |
| [schemars](https://crates.io/crates/schemars/1.2.3) | 1.2.3 | MIT | desktop |
| [schemars_derive](https://crates.io/crates/schemars_derive/0.8.22) | 0.8.22 | MIT | desktop |
| [scopeguard](https://crates.io/crates/scopeguard/1.2.0) | 1.2.0 | MIT OR Apache-2.0 | core, desktop |
| [selectors](https://crates.io/crates/selectors/0.38.0) | 0.38.0 | MPL-2.0 | desktop |
| [selectors](https://crates.io/crates/selectors/0.41.0) | 0.41.0 | MPL-2.0 | desktop |
| [semver](https://crates.io/crates/semver/1.0.28) | 1.0.28 | MIT OR Apache-2.0 | desktop |
| [serde](https://crates.io/crates/serde/1.0.229) | 1.0.229 | MIT OR Apache-2.0 | desktop |
| [serde-untagged](https://crates.io/crates/serde-untagged/0.1.9) | 0.1.9 | MIT OR Apache-2.0 | desktop |
| [serde_core](https://crates.io/crates/serde_core/1.0.229) | 1.0.229 | MIT OR Apache-2.0 | desktop |
| [serde_derive](https://crates.io/crates/serde_derive/1.0.229) | 1.0.229 | MIT OR Apache-2.0 | desktop |
| [serde_derive_internals](https://crates.io/crates/serde_derive_internals/0.29.1) | 0.29.1 | MIT OR Apache-2.0 | desktop |
| [serde_json](https://crates.io/crates/serde_json/1.0.154) | 1.0.154 | MIT OR Apache-2.0 | desktop |
| [serde_repr](https://crates.io/crates/serde_repr/0.1.21) | 0.1.21 | MIT OR Apache-2.0 | desktop |
| [serde_spanned](https://crates.io/crates/serde_spanned/0.6.9) | 0.6.9 | MIT OR Apache-2.0 | desktop |
| [serde_spanned](https://crates.io/crates/serde_spanned/1.1.3) | 1.1.3 | MIT OR Apache-2.0 | desktop |
| [serde_with](https://crates.io/crates/serde_with/3.24.0) | 3.24.0 | MIT OR Apache-2.0 | desktop |
| [serde_with_macros](https://crates.io/crates/serde_with_macros/3.24.0) | 3.24.0 | MIT OR Apache-2.0 | desktop |
| [serialize-to-javascript](https://crates.io/crates/serialize-to-javascript/0.1.2) | 0.1.2 | MIT OR Apache-2.0 | desktop |
| [serialize-to-javascript-impl](https://crates.io/crates/serialize-to-javascript-impl/0.1.2) | 0.1.2 | MIT OR Apache-2.0 | desktop |
| [servo_arc](https://crates.io/crates/servo_arc/0.4.3) | 0.4.3 | MIT OR Apache-2.0 | desktop |
| [servo_arc](https://crates.io/crates/servo_arc/0.5.0) | 0.5.0 | MIT OR Apache-2.0 | desktop |
| [sha2](https://crates.io/crates/sha2/0.10.9) | 0.10.9 | MIT OR Apache-2.0 | desktop |
| [sha2](https://crates.io/crates/sha2/0.11.1) | 0.11.1 | MIT OR Apache-2.0 | desktop |
| [shlex](https://crates.io/crates/shlex/2.0.1) | 2.0.1 | MIT OR Apache-2.0 | core, desktop |
| [simd-adler32](https://crates.io/crates/simd-adler32/0.3.10) | 0.3.10 | MIT | desktop |
| [siphasher](https://crates.io/crates/siphasher/1.0.4) | 1.0.4 | MIT OR Apache-2.0 | desktop |
| [slab](https://crates.io/crates/slab/0.4.12) | 0.4.12 | MIT | core, desktop |
| [smallvec](https://crates.io/crates/smallvec/1.16.6) | 1.16.6 | MIT OR Apache-2.0 | desktop |
| [smoltcp](https://crates.io/crates/smoltcp/0.14.0) | 0.14.0 | 0BSD | core |
| [socket2](https://crates.io/crates/socket2/0.6.5) | 0.6.5 | MIT OR Apache-2.0 | core, desktop |
| [softbuffer](https://crates.io/crates/softbuffer/0.4.8) | 0.4.8 | MIT OR Apache-2.0 | desktop |
| [soup3](https://crates.io/crates/soup3/0.5.0) | 0.5.0 | MIT | desktop |
| [soup3-sys](https://crates.io/crates/soup3-sys/0.5.0) | 0.5.0 | MIT | desktop |
| [spin](https://crates.io/crates/spin/0.9.9) | 0.9.9 | MIT | core |
| [stable_deref_trait](https://crates.io/crates/stable_deref_trait/1.2.1) | 1.2.1 | MIT OR Apache-2.0 | core, desktop |
| [static_vcruntime](https://crates.io/crates/static_vcruntime/3.0.0) | 3.0.0 | MIT OR Apache-2.0 OR Zlib | desktop |
| [string_cache](https://crates.io/crates/string_cache/0.11.0) | 0.11.0 | MIT OR Apache-2.0 | desktop |
| [string_cache](https://crates.io/crates/string_cache/0.9.0) | 0.9.0 | MIT OR Apache-2.0 | desktop |
| [string_cache_codegen](https://crates.io/crates/string_cache_codegen/0.11.2) | 0.11.2 | MIT OR Apache-2.0 | desktop |
| [string_cache_codegen](https://crates.io/crates/string_cache_codegen/0.6.1) | 0.6.1 | MIT OR Apache-2.0 | desktop |
| [strsim](https://crates.io/crates/strsim/0.11.1) | 0.11.1 | MIT | desktop |
| [subtle](https://crates.io/crates/subtle/2.6.1) | 2.6.1 | BSD-3-Clause | core |
| [swift-rs](https://crates.io/crates/swift-rs/1.0.9) | 1.0.9 | MIT OR Apache-2.0 | desktop |
| [syn](https://crates.io/crates/syn/1.0.109) | 1.0.109 | MIT OR Apache-2.0 | desktop |
| [syn](https://crates.io/crates/syn/2.0.119) | 2.0.119 | MIT OR Apache-2.0 | core, desktop |
| [syn](https://crates.io/crates/syn/3.0.10) | 3.0.10 | MIT OR Apache-2.0 | core, desktop |
| [sync_wrapper](https://crates.io/crates/sync_wrapper/1.0.2) | 1.0.2 | Apache-2.0 | desktop |
| [synstructure](https://crates.io/crates/synstructure/0.14.0) | 0.14.0 | MIT | desktop |
| [system-deps](https://crates.io/crates/system-deps/6.2.2) | 6.2.2 | MIT OR Apache-2.0 | desktop |
| [tao](https://crates.io/crates/tao/0.37.2) | 0.37.2 | Apache-2.0 | desktop |
| [tao-macros](https://crates.io/crates/tao-macros/0.1.4) | 0.1.4 | MIT OR Apache-2.0 | desktop |
| [target-lexicon](https://crates.io/crates/target-lexicon/0.12.16) | 0.12.16 | Apache-2.0 WITH LLVM-exception | desktop |
| [tauri](https://crates.io/crates/tauri/2.12.6) | 2.12.6 | Apache-2.0 OR MIT | desktop |
| [tauri-build](https://crates.io/crates/tauri-build/2.7.5) | 2.7.5 | Apache-2.0 OR MIT | desktop |
| [tauri-codegen](https://crates.io/crates/tauri-codegen/2.7.5) | 2.7.5 | Apache-2.0 OR MIT | desktop |
| [tauri-macros](https://crates.io/crates/tauri-macros/2.7.5) | 2.7.5 | Apache-2.0 OR MIT | desktop |
| [tauri-runtime](https://crates.io/crates/tauri-runtime/2.12.3) | 2.12.3 | Apache-2.0 OR MIT | desktop |
| [tauri-runtime-wry](https://crates.io/crates/tauri-runtime-wry/2.12.4) | 2.12.4 | Apache-2.0 OR MIT | desktop |
| [tauri-utils](https://crates.io/crates/tauri-utils/2.10.3) | 2.10.3 | Apache-2.0 OR MIT | desktop |
| [tauri-winres](https://crates.io/crates/tauri-winres/0.3.7) | 0.3.7 | MIT | desktop |
| [tendril](https://crates.io/crates/tendril/0.5.1) | 0.5.1 | MIT OR Apache-2.0 | desktop |
| [thiserror](https://crates.io/crates/thiserror/1.0.69) | 1.0.69 | MIT OR Apache-2.0 | core, desktop |
| [thiserror](https://crates.io/crates/thiserror/2.0.21) | 2.0.21 | MIT OR Apache-2.0 | core, desktop |
| [thiserror-impl](https://crates.io/crates/thiserror-impl/1.0.69) | 1.0.69 | MIT OR Apache-2.0 | core, desktop |
| [thiserror-impl](https://crates.io/crates/thiserror-impl/2.0.21) | 2.0.21 | MIT OR Apache-2.0 | core, desktop |
| [time](https://crates.io/crates/time/0.3.55) | 0.3.55 | MIT OR Apache-2.0 | desktop |
| [time-core](https://crates.io/crates/time-core/0.1.9) | 0.1.9 | MIT OR Apache-2.0 | desktop |
| [time-macros](https://crates.io/crates/time-macros/0.2.32) | 0.2.32 | MIT OR Apache-2.0 | desktop |
| [tinystr](https://crates.io/crates/tinystr/0.8.5) | 0.8.5 | Unicode-3.0 | desktop |
| [tinyvec](https://crates.io/crates/tinyvec/1.13.3) | 1.13.3 | Zlib OR Apache-2.0 OR MIT | core, desktop |
| [tokio](https://crates.io/crates/tokio/1.54.1) | 1.54.1 | MIT | core, desktop |
| [tokio-macros](https://crates.io/crates/tokio-macros/2.7.2) | 2.7.2 | MIT | core |
| [tokio-rustls](https://crates.io/crates/tokio-rustls/0.26.6) | 0.26.6 | MIT OR Apache-2.0 | core |
| [tokio-util](https://crates.io/crates/tokio-util/0.7.20) | 0.7.20 | MIT | core, desktop |
| [toml](https://crates.io/crates/toml/0.8.2) | 0.8.2 | MIT OR Apache-2.0 | desktop |
| [toml](https://crates.io/crates/toml/1.2.1+spec-1.1.0) | 1.2.1+spec-1.1.0 | MIT OR Apache-2.0 | desktop |
| [toml_datetime](https://crates.io/crates/toml_datetime/0.6.3) | 0.6.3 | MIT OR Apache-2.0 | desktop |
| [toml_datetime](https://crates.io/crates/toml_datetime/1.1.3+spec-1.1.0) | 1.1.3+spec-1.1.0 | MIT OR Apache-2.0 | desktop |
| [toml_edit](https://crates.io/crates/toml_edit/0.19.15) | 0.19.15 | MIT OR Apache-2.0 | desktop |
| [toml_edit](https://crates.io/crates/toml_edit/0.20.2) | 0.20.2 | MIT OR Apache-2.0 | desktop |
| [toml_edit](https://crates.io/crates/toml_edit/0.25.19+spec-1.1.0) | 0.25.19+spec-1.1.0 | MIT OR Apache-2.0 | desktop |
| [toml_parser](https://crates.io/crates/toml_parser/1.1.5+spec-1.1.0) | 1.1.5+spec-1.1.0 | MIT OR Apache-2.0 | desktop |
| [toml_writer](https://crates.io/crates/toml_writer/1.2.0+spec-1.1.0) | 1.2.0+spec-1.1.0 | MIT OR Apache-2.0 | desktop |
| [tower](https://crates.io/crates/tower/0.5.3) | 0.5.3 | MIT | desktop |
| [tower-http](https://crates.io/crates/tower-http/0.6.11) | 0.6.11 | MIT | desktop |
| [tower-layer](https://crates.io/crates/tower-layer/0.3.3) | 0.3.3 | MIT | desktop |
| [tower-service](https://crates.io/crates/tower-service/0.3.3) | 0.3.3 | MIT | desktop |
| [tracing](https://crates.io/crates/tracing/0.1.44) | 0.1.44 | MIT | core, desktop |
| [tracing-core](https://crates.io/crates/tracing-core/0.1.36) | 0.1.36 | MIT | core, desktop |
| [tray-icon](https://crates.io/crates/tray-icon/0.25.1) | 0.25.1 | MIT OR Apache-2.0 | desktop |
| [try-lock](https://crates.io/crates/try-lock/0.2.5) | 0.2.5 | MIT | desktop |
| [typeid](https://crates.io/crates/typeid/1.0.3) | 1.0.3 | MIT OR Apache-2.0 | desktop |
| [typenum](https://crates.io/crates/typenum/1.20.1) | 1.20.1 | MIT OR Apache-2.0 | desktop |
| [unicode-ident](https://crates.io/crates/unicode-ident/1.0.27) | 1.0.27 | (MIT OR Apache-2.0) AND Unicode-3.0 | core, desktop |
| [unicode-segmentation](https://crates.io/crates/unicode-segmentation/1.13.3) | 1.13.3 | MIT OR Apache-2.0 | desktop |
| [untrusted](https://crates.io/crates/untrusted/0.9.0) | 0.9.0 | ISC | core |
| [url](https://crates.io/crates/url/2.5.8) | 2.5.8 | MIT OR Apache-2.0 | desktop |
| [urlpattern](https://crates.io/crates/urlpattern/0.6.0) | 0.6.0 | MIT | desktop |
| [utf8_iter](https://crates.io/crates/utf8_iter/1.1.0) | 1.1.0 | Apache-2.0 OR MIT | desktop |
| [utf_types](https://crates.io/crates/utf_types/1.0.0) | 1.0.0 | Apache-2.0 OR MIT | desktop |
| [uuid](https://crates.io/crates/uuid/1.29.3) | 1.29.3 | Apache-2.0 OR MIT | desktop |
| [version-compare](https://crates.io/crates/version-compare/0.2.1) | 0.2.1 | MIT | desktop |
| [version_check](https://crates.io/crates/version_check/0.9.5) | 0.9.5 | MIT/Apache-2.0 | desktop |
| [vswhom](https://crates.io/crates/vswhom/0.1.0) | 0.1.0 | MIT | desktop |
| [vswhom-sys](https://crates.io/crates/vswhom-sys/0.1.3) | 0.1.3 | MIT | desktop |
| [walkdir](https://crates.io/crates/walkdir/2.5.0) | 2.5.0 | Unlicense/MIT | core, desktop |
| [want](https://crates.io/crates/want/0.3.2) | 0.3.2 | MIT | desktop |
| [wasi](https://crates.io/crates/wasi/0.11.1+wasi-snapshot-preview1) | 0.11.1+wasi-snapshot-preview1 | Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT | core, desktop |
| [wasip2](https://crates.io/crates/wasip2/1.0.4+wasi-0.2.12) | 1.0.4+wasi-0.2.12 | Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT | desktop |
| [wasm-bindgen](https://crates.io/crates/wasm-bindgen/0.2.130) | 0.2.130 | MIT OR Apache-2.0 | core, desktop |
| [wasm-bindgen-futures](https://crates.io/crates/wasm-bindgen-futures/0.4.80) | 0.4.80 | MIT OR Apache-2.0 | desktop |
| [wasm-bindgen-macro](https://crates.io/crates/wasm-bindgen-macro/0.2.130) | 0.2.130 | MIT OR Apache-2.0 | core, desktop |
| [wasm-bindgen-macro-support](https://crates.io/crates/wasm-bindgen-macro-support/0.2.130) | 0.2.130 | MIT OR Apache-2.0 | core, desktop |
| [wasm-bindgen-shared](https://crates.io/crates/wasm-bindgen-shared/0.2.130) | 0.2.130 | MIT OR Apache-2.0 | core, desktop |
| [wasm-streams](https://crates.io/crates/wasm-streams/0.5.0) | 0.5.0 | MIT OR Apache-2.0 | desktop |
| [web-sys](https://crates.io/crates/web-sys/0.3.107) | 0.3.107 | MIT OR Apache-2.0 | desktop |
| [web-time](https://crates.io/crates/web-time/1.1.0) | 1.1.0 | MIT OR Apache-2.0 | core, desktop |
| [web_atoms](https://crates.io/crates/web_atoms/0.2.6) | 0.2.6 | MIT OR Apache-2.0 | desktop |
| [web_atoms](https://crates.io/crates/web_atoms/0.3.0) | 0.3.0 | MIT OR Apache-2.0 | desktop |
| [webkit2gtk](https://crates.io/crates/webkit2gtk/2.0.2) | 2.0.2 | MIT | desktop |
| [webkit2gtk-sys](https://crates.io/crates/webkit2gtk-sys/2.0.2) | 2.0.2 | MIT | desktop |
| [webview2-com](https://crates.io/crates/webview2-com/0.39.1) | 0.39.1 | MIT | desktop |
| [webview2-com-macros](https://crates.io/crates/webview2-com-macros/0.8.1) | 0.8.1 | MIT | desktop |
| [webview2-com-sys](https://crates.io/crates/webview2-com-sys/0.39.1) | 0.39.1 | MIT | desktop |
| [winapi](https://crates.io/crates/winapi/0.3.9) | 0.3.9 | MIT/Apache-2.0 | desktop |
| [winapi-i686-pc-windows-gnu](https://crates.io/crates/winapi-i686-pc-windows-gnu/0.4.0) | 0.4.0 | MIT/Apache-2.0 | desktop |
| [winapi-util](https://crates.io/crates/winapi-util/0.1.11) | 0.1.11 | Unlicense OR MIT | core, desktop |
| [winapi-x86_64-pc-windows-gnu](https://crates.io/crates/winapi-x86_64-pc-windows-gnu/0.4.0) | 0.4.0 | MIT/Apache-2.0 | desktop |
| [window-vibrancy](https://crates.io/crates/window-vibrancy/0.8.1) | 0.8.1 | Apache-2.0 OR MIT | desktop |
| [windows](https://crates.io/crates/windows/0.62.2) | 0.62.2 | MIT OR Apache-2.0 | desktop |
| [windows-collections](https://crates.io/crates/windows-collections/0.3.2) | 0.3.2 | MIT OR Apache-2.0 | desktop |
| [windows-core](https://crates.io/crates/windows-core/0.62.2) | 0.62.2 | MIT OR Apache-2.0 | desktop |
| [windows-future](https://crates.io/crates/windows-future/0.3.2) | 0.3.2 | MIT OR Apache-2.0 | desktop |
| [windows-implement](https://crates.io/crates/windows-implement/0.60.2) | 0.60.2 | MIT OR Apache-2.0 | desktop |
| [windows-interface](https://crates.io/crates/windows-interface/0.59.3) | 0.59.3 | MIT OR Apache-2.0 | desktop |
| [windows-link](https://crates.io/crates/windows-link/0.2.1) | 0.2.1 | MIT OR Apache-2.0 | core, desktop |
| [windows-numerics](https://crates.io/crates/windows-numerics/0.3.1) | 0.3.1 | MIT OR Apache-2.0 | desktop |
| [windows-result](https://crates.io/crates/windows-result/0.4.1) | 0.4.1 | MIT OR Apache-2.0 | desktop |
| [windows-strings](https://crates.io/crates/windows-strings/0.5.1) | 0.5.1 | MIT OR Apache-2.0 | desktop |
| [windows-sys](https://crates.io/crates/windows-sys/0.45.0) | 0.45.0 | MIT OR Apache-2.0 | core, desktop |
| [windows-sys](https://crates.io/crates/windows-sys/0.52.0) | 0.52.0 | MIT OR Apache-2.0 | core |
| [windows-sys](https://crates.io/crates/windows-sys/0.61.2) | 0.61.2 | MIT OR Apache-2.0 | core, desktop |
| [windows-targets](https://crates.io/crates/windows-targets/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows-targets](https://crates.io/crates/windows-targets/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows-threading](https://crates.io/crates/windows-threading/0.2.1) | 0.2.1 | MIT OR Apache-2.0 | desktop |
| [windows-version](https://crates.io/crates/windows-version/0.1.7) | 0.1.7 | MIT OR Apache-2.0 | desktop |
| [windows_aarch64_gnullvm](https://crates.io/crates/windows_aarch64_gnullvm/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_aarch64_gnullvm](https://crates.io/crates/windows_aarch64_gnullvm/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_aarch64_msvc](https://crates.io/crates/windows_aarch64_msvc/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_aarch64_msvc](https://crates.io/crates/windows_aarch64_msvc/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_i686_gnu](https://crates.io/crates/windows_i686_gnu/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_i686_gnu](https://crates.io/crates/windows_i686_gnu/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_i686_gnullvm](https://crates.io/crates/windows_i686_gnullvm/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_i686_msvc](https://crates.io/crates/windows_i686_msvc/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_i686_msvc](https://crates.io/crates/windows_i686_msvc/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_x86_64_gnu](https://crates.io/crates/windows_x86_64_gnu/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_x86_64_gnu](https://crates.io/crates/windows_x86_64_gnu/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_x86_64_gnullvm](https://crates.io/crates/windows_x86_64_gnullvm/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_x86_64_gnullvm](https://crates.io/crates/windows_x86_64_gnullvm/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [windows_x86_64_msvc](https://crates.io/crates/windows_x86_64_msvc/0.42.2) | 0.42.2 | MIT OR Apache-2.0 | core, desktop |
| [windows_x86_64_msvc](https://crates.io/crates/windows_x86_64_msvc/0.52.6) | 0.52.6 | MIT OR Apache-2.0 | core |
| [winnow](https://crates.io/crates/winnow/0.5.40) | 0.5.40 | MIT | desktop |
| [winnow](https://crates.io/crates/winnow/1.0.5) | 1.0.5 | MIT | desktop |
| [winreg](https://crates.io/crates/winreg/0.56.0) | 0.56.0 | MIT | desktop |
| [wit-bindgen](https://crates.io/crates/wit-bindgen/0.57.1) | 0.57.1 | Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT | desktop |
| [writeable](https://crates.io/crates/writeable/0.6.4) | 0.6.4 | Unicode-3.0 | desktop |
| [wry](https://crates.io/crates/wry/0.57.2) | 0.57.2 | Apache-2.0 OR MIT | desktop |
| [x11](https://crates.io/crates/x11/2.21.0) | 2.21.0 | MIT | desktop |
| [x11-dl](https://crates.io/crates/x11-dl/2.21.0) | 2.21.0 | MIT | desktop |
| [yoke](https://crates.io/crates/yoke/0.8.4) | 0.8.4 | Unicode-3.0 | desktop |
| [yoke-derive](https://crates.io/crates/yoke-derive/0.8.4) | 0.8.4 | Unicode-3.0 | desktop |
| [zerocopy](https://crates.io/crates/zerocopy/0.8.63) | 0.8.63 | BSD-2-Clause OR Apache-2.0 OR MIT | core |
| [zerocopy-derive](https://crates.io/crates/zerocopy-derive/0.8.63) | 0.8.63 | BSD-2-Clause OR Apache-2.0 OR MIT | core |
| [zerofrom](https://crates.io/crates/zerofrom/0.1.8) | 0.1.8 | Unicode-3.0 | desktop |
| [zerofrom-derive](https://crates.io/crates/zerofrom-derive/0.1.8) | 0.1.8 | Unicode-3.0 | desktop |
| [zeroize](https://crates.io/crates/zeroize/1.9.2) | 1.9.2 | Apache-2.0 OR MIT | core |
| [zerotrie](https://crates.io/crates/zerotrie/0.2.5) | 0.2.5 | Unicode-3.0 | desktop |
| [zerovec](https://crates.io/crates/zerovec/0.11.9) | 0.11.9 | Unicode-3.0 | desktop |
| [zerovec-derive](https://crates.io/crates/zerovec-derive/0.11.7) | 0.11.7 | Unicode-3.0 | desktop |
| [zlib-rs](https://crates.io/crates/zlib-rs/0.6.8) | 0.6.8 | Zlib | desktop |
| [zmij](https://crates.io/crates/zmij/1.0.23) | 1.0.23 | MIT | desktop |

## V2 传输开发分支

|组件和锁定版本|许可与用途|
|---|---|
|[apernet/quic-go 2895f039137a](https://github.com/apernet/quic-go/tree/2895f039137a)；[qpack v0.6.0](https://github.com/quic-go/qpack/tree/v0.6.0)|MIT。H3 和头部编码。Caddy 导入路径的 QUIC 源码亦来自同一分支，加本仓库补丁。|
|[uTLS v1.8.2](https://github.com/refraction-networking/utls/tree/v1.8.2)|BSD-3-Clause。客户端 TLS。|
|[Go x/net v0.60.0](https://github.com/golang/net/tree/v0.60.0)；[Go x/mobile 197770444867](https://github.com/golang/mobile/tree/197770444867)|BSD-3-Clause。前者提供 H2，后者用于 gomobile/gobind 及绑定运行时。|
|[Caddy v2.11.9](https://github.com/caddyserver/caddy/tree/v2.11.9)|Apache-2.0。只链接服务端。再分发保留上游 LICENSE 和适用的 NOTICE。|
|[Hysteria 拥塞控制 c2b181e](https://github.com/apernet/hysteria/tree/c2b181e0b0e72b077e4b0b7297735af14d1f9929/core/internal/congestion)|MIT。复制的代码文件头标注上游路径和提交，授权全文随 v2/internal/congestion/LICENSE.md 分发。|
|[libXray v26.11.9](https://github.com/XTLS/libXray/tree/v26.11.9)；[Xray-core 676eaf66f8f0](https://github.com/XTLS/Xray-core/tree/676eaf66f8f0)|MIT / MPL-2.0。合编 AAR 仍须提供受 MPL 涵盖文件的对应源码及本地 CONNECT-IP 修改。|

节点账本使用 [modernc.org/sqlite v1.58.0](https://pkg.go.dev/modernc.org/sqlite@v1.58.0?tab=licenses)。驱动、modernc.org/libc v1.75.6、mathutil v1.7.1、memory v1.12.1 和 bigfft 24d4a6f8daec 的模块根 LICENSE 均为 BSD 三条款。发布节点时需随产物保留各组件版权、许可及所含第三方源文件的声明。该存储驱动只链接节点。

Go 间接模块及精确版本见 v2/go.mod、v2/go.sum。正式分发需按实际编译目标复核间接组件的版权、许可、NOTICE 和修改源码。本轮未发布构建产物。
