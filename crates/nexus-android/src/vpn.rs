use super::{Connector, Device};
use etherparse::{NetSlice, PacketBuilder, SlicedPacket, TransportSlice};
use futures_util::{SinkExt, StreamExt};
use netstack_smoltcp::StackBuilder;
use nexus_protocol::{Flow, MAX_PACKET};
use nexus_transport::{Mode, LIFETIME, SETUP};
use std::{
    collections::HashMap,
    fs::File,
    io::{self, Read, Write},
    net::{IpAddr, SocketAddr},
    os::fd::OwnedFd,
    sync::Arc,
    time::Duration,
};
use tokio::{
    io::{copy_bidirectional, unix::AsyncFd, Interest},
    sync::{mpsc, oneshot},
    task::JoinSet,
    time::timeout,
};

type UdpFlow = (SocketAddr, SocketAddr);

type TcpFlow = (SocketAddr, SocketAddr);

// Browsers keep many idle keep-alive connections open. A low cap silently drops new SYNs
// and shows up as pages that never load. Both caps stay below the server's per-connection
// stream limit (nexus_transport::MAX_STREAMS).
const MAX_TCP_FLOWS: usize = 192;
const MAX_UDP_FLOWS: usize = 64;
// DNS uses a fresh source port per query; release those associations quickly.
const DNS_IDLE: Duration = Duration::from_secs(10);
const UDP_IDLE: Duration = Duration::from_secs(60);

fn initial_syn(bytes: &[u8]) -> Option<(TcpFlow, u32)> {
    let packet = SlicedPacket::from_ip(bytes).ok()?;
    let Some(TransportSlice::Tcp(tcp)) = packet.transport else {
        return None;
    };
    if !tcp.syn() || tcp.ack() || tcp.rst() {
        return None;
    }
    let (src, dst) = match packet.net? {
        NetSlice::Ipv4(ip) => (
            IpAddr::V4(ip.header().source_addr()),
            IpAddr::V4(ip.header().destination_addr()),
        ),
        NetSlice::Ipv6(ip) => (
            IpAddr::V6(ip.header().source_addr()),
            IpAddr::V6(ip.header().destination_addr()),
        ),
    };
    let key = (
        SocketAddr::new(src, tcp.source_port()),
        SocketAddr::new(dst, tcp.destination_port()),
    );
    Some((
        key,
        tcp.sequence_number()
            .wrapping_add(1 + tcp.payload().len() as u32),
    ))
}

async fn write_tun(tun: &AsyncFd<File>, packet: &[u8]) -> io::Result<()> {
    let written = tun
        .async_io(Interest::WRITABLE, |mut file| file.write(packet))
        .await?;
    if written != packet.len() {
        return Err(io::ErrorKind::WriteZero.into());
    }
    Ok(())
}

async fn refuse_syn(tun: &AsyncFd<File>, (src, dst): TcpFlow, ack: u32) -> io::Result<()> {
    let ip = match (dst.ip(), src.ip()) {
        (IpAddr::V4(a), IpAddr::V4(b)) => PacketBuilder::ipv4(a.octets(), b.octets(), 64),
        (IpAddr::V6(a), IpAddr::V6(b)) => PacketBuilder::ipv6(a.octets(), b.octets(), 64),
        _ => return Err(io::ErrorKind::InvalidData.into()),
    };
    let mut packet = Vec::with_capacity(60);
    ip.tcp(dst.port(), src.port(), 0, 0)
        .rst()
        .ack(ack)
        .write(&mut packet, &[])
        .map_err(io::Error::other)?;
    write_tun(tun, &packet).await
}

async fn udp_flow(
    connector: Arc<Connector>,
    (local, remote): UdpFlow,
    mut outgoing: mpsc::Receiver<Vec<u8>>,
    replies: mpsc::Sender<netstack_smoltcp::udp::UdpMsg>,
) -> io::Result<()> {
    let channel = nexus_client::open(&connector, &Mode::Auto, Flow::Udp, remote).await?;
    let (mut reader, mut writer) = channel.packets();
    let idle = if remote.port() == 53 {
        DNS_IDLE
    } else {
        UDP_IDLE
    };
    loop {
        tokio::select! {
            reply = reader.recv() => {
                replies.send((reply?, remote, local)).await.map_err(|_| io::ErrorKind::BrokenPipe)?;
            }
            payload = outgoing.recv() => {
                let Some(payload) = payload else { return Ok(()) };
                timeout(SETUP, writer.send(&payload)).await??;
            }
            _ = tokio::time::sleep(idle) => return Ok(()),
        }
    }
}

async fn udp_flows(
    socket: netstack_smoltcp::UdpSocket,
    connector: Arc<Connector>,
) -> io::Result<()> {
    let (mut reader, mut writer) = socket.split();
    let (replies, mut received) = mpsc::channel(64);
    let mut sessions = HashMap::<UdpFlow, mpsc::Sender<Vec<u8>>>::new();
    let mut tasks = JoinSet::new();
    loop {
        tokio::select! {
            packet = reader.next() => {
                let (payload, local, remote) = packet.ok_or(io::ErrorKind::BrokenPipe)?;
                if payload.len() > MAX_PACKET { continue }
                let key = (local, remote);
                if !sessions.contains_key(&key) && sessions.len() < MAX_UDP_FLOWS {
                    let (tx, rx) = mpsc::channel(64);
                    sessions.insert(key, tx);
                    let connector = connector.clone();
                    let replies = replies.clone();
                    tasks.spawn(async move { let _ = udp_flow(connector, key, rx, replies).await; key });
                }
                if let Some(session) = sessions.get(&key) { let _ = session.try_send(payload); }
            }
            Some(reply) = received.recv() => writer.send(reply).await?,
            Some(done) = tasks.join_next() => { sessions.remove(&done?); }
        }
    }
}

pub(super) async fn forward(
    fd: OwnedFd,
    route: Option<IpAddr>,
    target: SocketAddr,
    connector: Arc<Connector>,
    device: Arc<Device>,
) -> io::Result<()> {
    // The file owns this nonblocking TUN descriptor exclusively until AsyncFd drops it.
    let tun = unsafe { AsyncFd::register(File::from(fd)) }.map_err(io::Error::from)?;
    let (stack, runner, udp, listener) = StackBuilder::default()
        .enable_udp(route.is_none())
        .enable_tcp(true)
        .mtu(1500)
        .stack_buffer_size(64)
        .tcp_buffer_size(64)
        .tcp_recv_buffer_size(65536)
        .tcp_send_buffer_size(65536)
        .add_ip_filter_fn(move |_, destination| route.is_none_or(|ip| ip == *destination))
        .build()?;
    let mut listener = listener.ok_or_else(|| io::Error::other("missing TCP listener"))?;
    let runner = runner.ok_or_else(|| io::Error::other("missing TCP runner"))?;
    let (mut sink, mut packets) = stack.split();
    let ingress = async {
        let mut buf = [0u8; 1500];
        let mut opening = JoinSet::new();
        let mut relays = JoinSet::new();
        let mut sessions =
            HashMap::<TcpFlow, Option<oneshot::Sender<netstack_smoltcp::TcpStream>>>::new();
        loop {
            tokio::select! {
                read = tun.async_io(Interest::READABLE, |mut file| file.read(&mut buf)) => {
                    let size = read?;
                    if size == 0 { return Err(io::ErrorKind::UnexpectedEof.into()); }
                    let packet = buf[..size].to_vec();
                    let Some((key, ack)) = initial_syn(&packet) else { sink.send(packet).await?; continue };
                    if sessions.contains_key(&key) || sessions.len() >= MAX_TCP_FLOWS { continue; }
                    let destination = if let Some(alias) = route {
                        if key.1 != SocketAddr::new(alias, target.port()) { continue; }
                        target
                    } else { key.1 };
                    sessions.insert(key,None);
                    let connector = connector.clone();
                    // smoltcp acknowledges a SYN immediately. Only give it the SYN after the
                    // node connects, so a browser can race IPv4 against unreachable IPv6.
                    opening.spawn(async move {
                        let channel = nexus_client::open(&connector,&Mode::Auto,Flow::Tcp,destination).await;
                        (key,ack,packet,channel)
                    });
                }
                Some(done) = opening.join_next() => {
                    let (key,ack,packet,channel) = done?;
                    let Ok(mut channel) = channel else {
                        sessions.remove(&key);
                        refuse_syn(&tun,key,ack).await?;
                        continue;
                    };
                    let (sender,receiver) = oneshot::channel::<netstack_smoltcp::TcpStream>();
                    sessions.insert(key,Some(sender));
                    relays.spawn(async move {
                        if let Ok(Ok(mut stream)) = timeout(SETUP,receiver).await {
                            if let Ok(Ok(_)) = timeout(LIFETIME,copy_bidirectional(&mut stream,&mut channel.stream)).await {
                                channel.drain().await;
                            }
                        }
                        key
                    });
                    sink.send(packet).await?;
                }
                stream = listener.next() => {
                    let (stream,local,remote) = stream.ok_or(io::ErrorKind::BrokenPipe)?;
                    if let Some(sender) = sessions.get_mut(&(local,remote)).and_then(Option::take) {
                        let _ = sender.send(stream);
                    }
                }
                Some(done) = relays.join_next() => { sessions.remove(&done?); }
            }
        }
    };
    let egress = async {
        while let Some(packet) = packets.next().await {
            let packet = packet?;
            write_tun(&tun, &packet).await?;
        }
        Err(io::Error::from(io::ErrorKind::BrokenPipe))
    };
    device.ready(0)?;
    let datagrams = async {
        match udp {
            Some(socket) => udp_flows(socket, connector.clone()).await,
            None => std::future::pending().await,
        }
    };
    tokio::select! {r=runner=>r, r=ingress=>r, r=egress=>r, r=datagrams=>r}
}
