mod access;
use access::{Device, Registry};
use nexus_protocol::{read_open, Flow, DENIED, INVALID, MAX_PACKET, OK, UNREACHABLE};
use nexus_transport::{Channel, ServerTls, LIFETIME, SETUP};

// Every Android UDP association and every TLS/TCP fallback flow is its own connection, so
// connection slots must cover several devices that share one node.
const MAX_SESSIONS: usize = 512;
// Multiplexed TCP flows and DNS streams across all devices.
const MAX_FLOWS: usize = 4096;
use std::{
    collections::HashSet,
    env, io,
    net::{IpAddr, SocketAddr},
    sync::Arc,
    time::Duration,
};
use tokio::{
    io::{copy_bidirectional, AsyncWriteExt},
    net::{TcpListener, TcpStream, UdpSocket},
    sync::Semaphore,
    time::timeout,
};

struct Policy {
    exact: HashSet<SocketAddr>,
    internet: bool,
}

impl Policy {
    fn permits(&self, addr: SocketAddr) -> bool {
        self.exact.contains(&addr)
            || self.internet
                && match addr.ip() {
                    IpAddr::V4(ip) => !matches!(
                        ip.octets(),
                        [0 | 10 | 127 | 224..=255, ..]
                            | [100, 64..=127, ..]
                            | [169, 254, ..]
                            | [172, 16..=31, ..]
                            | [192, 168, ..]
                            | [192, 0, 0 | 2, _]
                            | [192, 88, 99, _]
                            | [198, 18 | 19, ..]
                            | [198, 51, 100, _]
                            | [203, 0, 113, _]
                            | [168, 63, 129, 16]
                    ),
                    IpAddr::V6(ip) => {
                        let [a, b, ..] = ip.segments();
                        (0x2000..0x4000).contains(&a)
                            && a != 0x2002
                            && !(a == 0x2001 && (b < 0x200 || b == 0xdb8))
                            && !(a == 0x3fff && b < 0x1000)
                    }
                }
    }
}

async fn relay(mut ch: Channel, allowed: &Policy, device: Option<&Device>) -> io::Result<()> {
    let stream = &mut ch.stream;
    let (flow, addr) = match timeout(SETUP, read_open(stream)).await? {
        Ok(v) => v,
        Err(_) => {
            stream.write_u8(INVALID).await?;
            stream.shutdown().await?;
            ch.drain().await;
            return Ok(());
        }
    };
    if !allowed.permits(addr) {
        stream.write_u8(DENIED).await?;
    } else {
        match flow {
            Flow::Tcp => match timeout(SETUP, TcpStream::connect(addr)).await {
                Ok(Ok(mut target)) => {
                    // Interactive protocols (SSH, TLS handshakes) should not wait on Nagle.
                    let _ = target.set_nodelay(true);
                    stream.write_u8(OK).await?;
                    stream.flush().await?;
                    if let Some(device) = device {
                        let (r, w) = tokio::io::split(stream);
                        let (tr, tw) = target.split();
                        tokio::try_join!(device.copy(r, tw), device.copy(tr, w))?;
                    } else {
                        copy_bidirectional(stream, &mut target).await?;
                    }
                }
                _ => stream.write_u8(UNREACHABLE).await?,
            },
            Flow::Udp => {
                let socket = UdpSocket::bind(if addr.is_ipv4() {
                    "0.0.0.0:0"
                } else {
                    "[::]:0"
                })
                .await?;
                socket.connect(addr).await?;
                stream.write_u8(OK).await?;
                stream.flush().await?;
                let (mut r, mut w) = ch.packets();
                let outbound = async {
                    loop {
                        let packet = r.recv().await?;
                        // An empty datagram costs nothing; charge(0) would query the registry.
                        if let Some(device) = device.filter(|_| !packet.is_empty()) {
                            device.charge(packet.len()).await?;
                        }
                        socket.send(&packet).await?;
                    }
                    #[allow(unreachable_code)]
                    Ok::<(), io::Error>(())
                };
                let inbound = async {
                    let mut buf = [0; MAX_PACKET + 1];
                    loop {
                        let n = socket.recv(&mut buf).await?;
                        if n <= MAX_PACKET {
                            if let Some(device) = device.filter(|_| n > 0) {
                                device.charge(n).await?;
                            }
                            w.send(&buf[..n]).await?;
                        }
                    }
                    #[allow(unreachable_code)]
                    Ok::<(), io::Error>(())
                };
                return tokio::select! {res=outbound=>res,res=inbound=>res};
            }
        }
    }
    ch.stream.shutdown().await?;
    ch.drain().await;
    Ok(())
}

async fn session(ch: Channel, allowed: &Policy, registry: Option<&Registry>) -> io::Result<()> {
    if let Some(registry) = registry {
        let cert = ch
            .peer_certificate
            .as_ref()
            .ok_or(io::ErrorKind::PermissionDenied)?;
        let device = registry.device(cert.as_ref());
        device.charge(0).await?;
        let relayed = relay(ch, allowed, Some(&device));
        tokio::pin!(relayed);
        let result = tokio::select! { r = device.until_revoked() => r, r = &mut relayed => r };
        // Return the unused byte lease before the channel closes, so the owner sees settled
        // usage as soon as the peer observes the end of the session.
        device.settle().await;
        result
    } else {
        relay(ch, allowed, None).await
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = env::args().skip(1).collect();
    if args.as_slice() == ["--version"] {
        println!("nexus-server {}", env!("CARGO_PKG_VERSION"));
        return Ok(());
    }
    if args.len() < 6 {
        return Err("usage: nexus-server LISTEN CA.der SERVER.der SERVER.key.der DEVICE.der ALLOWED_IP:PORT [...]".into());
    }
    let addr: SocketAddr = args[0].parse()?;
    let (tls, registry) = match args[4].strip_prefix("registry:") {
        Some(path) => (
            ServerTls::for_device_registry(&args[1], &args[2], &args[3])?,
            Some(Registry::open(path.as_ref())?),
        ),
        None => (
            ServerTls::load(&args[1], &args[2], &args[3], &args[4])?,
            None,
        ),
    };
    let allowed = Arc::new(Policy {
        internet: args[5..].iter().any(|s| s == "--internet"),
        exact: args[5..]
            .iter()
            .filter(|s| *s != "--internet")
            .map(|s| s.parse())
            .collect::<Result<HashSet<SocketAddr>, _>>()?,
    });
    let tcp = TcpListener::bind(addr).await?;
    let endpoint = tls.endpoint(tcp.local_addr()?)?;
    let slots = Arc::new(Semaphore::new(MAX_SESSIONS));
    let flows = Arc::new(Semaphore::new(MAX_FLOWS));
    println!("READY {}", tcp.local_addr()?);
    loop {
        let permit = slots.clone().acquire_owned().await?;
        let tls = tls.clone();
        let allowed = allowed.clone();
        let registry = registry.clone();
        let flows = flows.clone();
        // One admission per tick bounds failed handshakes as well as successful ones.
        tokio::time::sleep(Duration::from_millis(10)).await;
        tokio::select! {
            res=tcp.accept()=> {
                // Aborted handshakes and descriptor exhaustion are transient. Returning here
                // would end every active session on the node.
                let socket = match res {
                    Ok((socket, _)) => socket,
                    Err(error) => {
                        eprintln!("accept failed: {error}");
                        tokio::time::sleep(Duration::from_millis(100)).await;
                        continue;
                    }
                };
                let _ = socket.set_nodelay(true);
                tokio::spawn(async move {let _permit=permit;
                    if let Ok(Ok(ch))=timeout(SETUP,tls.tcp(socket)).await {
                        if let Ok(_flow) = flows.try_acquire() { let _=timeout(LIFETIME,session(ch,&allowed,registry.as_ref())).await; }
                    }
                });
            }
            Some(incoming)=endpoint.accept()=> {
                if !incoming.remote_address_validated() && incoming.may_retry() {let _=incoming.retry(); continue;}
                tokio::spawn(async move {let _permit=permit;
                    if let Ok(Ok(conn))=timeout(SETUP,tls.quic(incoming)).await {
                        let serve = async {
                            let mut active = tokio::task::JoinSet::new();
                            let mut idle = SETUP;
                            loop {
                                tokio::select! {
                                    accepted = conn.accept() => {
                                        let Ok(ch) = accepted else { break };
                                        idle = Duration::from_secs(60);
                                        let Ok(flow) = flows.clone().try_acquire_owned() else { continue };
                                        let allowed = allowed.clone();
                                        let registry = registry.clone();
                                        active.spawn(async move { let _flow = flow; let _ = timeout(LIFETIME, session(ch, &allowed, registry.as_ref())).await; });
                                        if !conn.multiplex { let _ = active.join_next().await; break }
                                    }
                                    Some(_) = active.join_next() => {}
                                    _ = tokio::time::sleep(idle), if active.is_empty() => break,
                                }
                            }
                        };
                        let _ = timeout(LIFETIME, serve).await;
                    }
                });
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn internet_is_opt_in_and_keeps_internal_services_private() {
        let mut policy = Policy {
            exact: HashSet::from(["10.0.0.1:53".parse().unwrap()]),
            internet: false,
        };
        let public = "1.1.1.1:443".parse().unwrap();
        assert!(!policy.permits(public));
        policy.internet = true;
        assert!(policy.permits(public));
        assert!(policy.permits("[2606:4700:4700::1111]:443".parse().unwrap()));
        assert!(policy.permits("10.0.0.1:53".parse().unwrap()));
        for addr in [
            "10.0.0.1:80",
            "127.0.0.1:22",
            "169.254.169.254:80",
            "100.100.100.200:80",
            "[::ffff:127.0.0.1]:80",
            "[::1]:22",
            "[64:ff9b::7f00:1]:22",
            "[fd00::1]:80",
            "[2002:7f00:1::]:80",
        ] {
            assert!(!policy.permits(addr.parse().unwrap()), "{addr}");
        }
    }
}
