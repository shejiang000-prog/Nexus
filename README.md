# Nexus

Android 优先的本地网络客户端与私人节点协议。此开发批次可使用设备独立密钥通过自己的 Linux 服务端向单个许可目标转发数据。无需注册。当前手机入口为固定目标的本地 TCP 端口。

- [Android 构建、导入和使用](docs/ANDROID.md)
- [工程架构](ARCHITECTURE.md) · [协议规范](PROTOCOL_SPEC.md) · [威胁模型](SECURITY.md)
- [验收矩阵](TEST_PLAN.md) · [依赖](DEPENDENCIES.md) · [交接与实测记录](docs/handoff/INDEX.md)

## 核心复验

```sh
cargo build --workspace --locked
cargo test --workspace --locked
python3 tools/test_tunnel.py
```

需要 OpenSSL 3 和 Python 3。Windows 可将 OPENSSL 设置为 Git 附带的 openssl.exe 完整路径。测试使用临时身份及临时端口，按字节核对独立 HTTP/UDP 目标。可选的 tools/test_windows_linux.py --public 在指定 WSL2 实例内运行普通权限服务并额外请求 example.com。

## 手工启动 CLI

先用 `python3 tools/lab_identity.py lab-identity` 在私有目录生成短期开发身份，cd 到该目录。假定目标为测试机上的 127.0.0.1:8000，分别运行：

```sh
../target/debug/nexus-server 127.0.0.1:4443 ca.der server.der server.key.der device.der 127.0.0.1:8000
../target/debug/nexus-client auto 127.0.0.1:4443 nexus.test ca.der device.der device.key.der tcp 127.0.0.1:8000 127.0.0.1:18080
```

向 127.0.0.1:18080 发出的字节将发送给授权目标。支持 auto/quic/tls 和 tcp/udp。仅在自己的受控环境使用文件私钥。Windows 外壳可通过 `cargo build --manifest-path apps/desktop/Cargo.toml --locked` 构建。
