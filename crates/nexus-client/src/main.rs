use nexus_client::open;
use nexus_protocol::{read_packet, write_packet, Flow, MAX_PACKET};
use nexus_transport::{Connector, Mode, LIFETIME, MAX_CONNECTIONS, SETUP};
use std::{env, io, net::SocketAddr, sync::Arc};
use tokio::{
    io::copy_bidirectional,
    net::{TcpListener, UdpSocket},
    sync::Semaphore,
    time::timeout,
};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = env::args().skip(1).collect();
    if args.len() != 9 {
        return Err("usage: nexus-client MODE SERVER:PORT SERVER_NAME CA.der DEVICE.der DEVICE.key.der tcp|udp TARGET:PORT LOCAL:PORT".into());
    }
    let mode = Arc::new(args[0].parse::<Mode>()?);
    let connector = Arc::new(Connector::new(
        args[1].parse()?,
        &args[2],
        &args[3],
        &args[4],
        &args[5],
    )?);
    let target = args[7].parse()?;
    let addr: SocketAddr = args[8].parse()?;
    if !addr.ip().is_loopback() {
        return Err("local listener requires loopback".into());
    }
    match args[6].as_str() {
        "tcp" => {
            let listener = TcpListener::bind(addr).await?;
            println!("READY {}", listener.local_addr()?);
            let slots = Arc::new(Semaphore::new(MAX_CONNECTIONS));
            loop {
                let permit = slots.clone().acquire_owned().await?;
                let (mut socket, _) = listener.accept().await?;
                let connector = connector.clone();
                let mode = mode.clone();
                tokio::spawn(async move {
                    let _permit = permit;
                    let result = timeout(LIFETIME, async {
                        let mut channel = open(&connector, &mode, Flow::Tcp, target).await?;
                        copy_bidirectional(&mut socket, &mut channel.stream).await?;
                        channel.drain().await;
                        Ok::<_, io::Error>(())
                    })
                    .await;
                    if !matches!(result, Ok(Ok(()))) {
                        eprintln!("TCP flow failed");
                    }
                });
            }
        }
        "udp" => {
            let socket = UdpSocket::bind(addr).await?;
            println!("READY {}", socket.local_addr()?);
            let mut buf = [0; MAX_PACKET + 1];
            loop {
                let (n, peer) = socket.recv_from(&mut buf).await?;
                if n > MAX_PACKET {
                    continue;
                }
                let exchange = async {
                    let mut ch = open(&connector, &mode, Flow::Udp, target).await?;
                    write_packet(&mut ch.stream, &buf[..n]).await?;
                    let n =
                        read_packet(&mut ch.stream, (&mut buf[..MAX_PACKET]).try_into().unwrap())
                            .await?;
                    socket.send_to(&buf[..n], peer).await?;
                    Ok::<_, io::Error>(())
                };
                if !matches!(timeout(SETUP * 3, exchange).await, Ok(Ok(()))) {
                    eprintln!("UDP exchange failed");
                }
            }
        }
        _ => Err("flow must be tcp or udp".into()),
    }
}
