use nexus_protocol::{
    read_open, read_packet, write_packet, Flow, DENIED, INVALID, MAX_PACKET, OK, UNREACHABLE,
};
use nexus_transport::{Channel, ServerTls, LIFETIME, MAX_CONNECTIONS, SETUP};
use std::{collections::HashSet, env, io, net::SocketAddr, sync::Arc, time::Duration};
use tokio::{
    io::{copy_bidirectional, AsyncWriteExt},
    net::{TcpListener, TcpStream, UdpSocket},
    sync::Semaphore,
    time::timeout,
};

async fn relay(mut ch: Channel, allowed: &HashSet<SocketAddr>) -> io::Result<()> {
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
    if !allowed.contains(&addr) {
        stream.write_u8(DENIED).await?;
    } else {
        match flow {
            Flow::Tcp => match timeout(SETUP, TcpStream::connect(addr)).await {
                Ok(Ok(mut target)) => {
                    stream.write_u8(OK).await?;
                    stream.flush().await?;
                    copy_bidirectional(stream, &mut target).await?;
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
                let (mut r, mut w) = tokio::io::split(stream);
                let outbound = async {
                    let mut buf = [0; MAX_PACKET];
                    loop {
                        let n = read_packet(&mut r, &mut buf).await?;
                        socket.send(&buf[..n]).await?;
                    }
                    #[allow(unreachable_code)]
                    Ok::<(), io::Error>(())
                };
                let inbound = async {
                    let mut buf = [0; MAX_PACKET + 1];
                    loop {
                        let n = socket.recv(&mut buf).await?;
                        if n <= MAX_PACKET {
                            write_packet(&mut w, &buf[..n]).await?;
                        }
                    }
                    #[allow(unreachable_code)]
                    Ok::<(), io::Error>(())
                };
                tokio::select! {res=outbound=>res?,res=inbound=>res?}
            }
        }
    }
    ch.stream.shutdown().await?;
    ch.drain().await;
    Ok(())
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = env::args().skip(1).collect();
    if args.len() < 6 {
        return Err("usage: nexus-server LISTEN CA.der SERVER.der SERVER.key.der DEVICE.der ALLOWED_IP:PORT [...]".into());
    }
    let addr: SocketAddr = args[0].parse()?;
    let tls = ServerTls::load(&args[1], &args[2], &args[3], &args[4])?;
    let allowed = Arc::new(
        args[5..]
            .iter()
            .map(|s| s.parse())
            .collect::<Result<HashSet<SocketAddr>, _>>()?,
    );
    let tcp = TcpListener::bind(addr).await?;
    let endpoint = tls.endpoint(tcp.local_addr()?)?;
    let slots = Arc::new(Semaphore::new(MAX_CONNECTIONS));
    println!("READY {}", tcp.local_addr()?);
    loop {
        let permit = slots.clone().acquire_owned().await?;
        let tls = tls.clone();
        let allowed = allowed.clone();
        // One admission per tick bounds failed handshakes as well as successful ones.
        tokio::time::sleep(Duration::from_millis(10)).await;
        tokio::select! {
            res=tcp.accept()=> {
                let (socket,_) = res?;
                tokio::spawn(async move {let _permit=permit;
                    if let Ok(Ok(ch))=timeout(SETUP,tls.tcp(socket)).await { let _=timeout(LIFETIME,relay(ch,&allowed)).await; }
                });
            }
            Some(incoming)=endpoint.accept()=> {
                if !incoming.remote_address_validated() && incoming.may_retry() {let _=incoming.retry(); continue;}
                tokio::spawn(async move {let _permit=permit;
                    if let Ok(Ok(ch))=timeout(SETUP,tls.quic(incoming)).await { let _=timeout(LIFETIME,relay(ch,&allowed)).await; }
                });
            }
        }
    }
}
