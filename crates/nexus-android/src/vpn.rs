use super::{
    forwarding::{Backend, Forwarding},
    split::domestic_question,
};
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
    time::{Duration, Instant},
};
use tokio::{
    io::{unix::AsyncFd, Interest},
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

// Native DNS handling and learned-address routes share the same cache as the private
// client. All other packets retain the Xray TUN path (including sniffing and CIDRs).
fn native_packet(
    packet: &[u8],
    routing: &Forwarding,
    active: impl Fn(TcpFlow) -> bool,
    udp: &mut HashMap<UdpFlow, (bool, Instant)>,
) -> bool {
    let Ok(p) = SlicedPacket::from_ip(packet) else {
        return false;
    };
    let (src, dst) = match p.net {
        Some(NetSlice::Ipv4(ip)) => (
            IpAddr::V4(ip.header().source_addr()),
            IpAddr::V4(ip.header().destination_addr()),
        ),
        Some(NetSlice::Ipv6(ip)) => (
            IpAddr::V6(ip.header().source_addr()),
            IpAddr::V6(ip.header().destination_addr()),
        ),
        _ => return false,
    };
    let Some(rules) = routing.rules.as_ref() else {
        return false;
    };
    let now = Instant::now();
    match p.transport {
        Some(TransportSlice::Tcp(t)) => {
            let key = (
                SocketAddr::new(src, t.source_port()),
                SocketAddr::new(dst, t.destination_port()),
            );
            active(key)
                || t.syn()
                    && !t.ack()
                    && (key.1.port() == 53 || routing.refused(key.1) || rules.learned(dst, now))
        }
        Some(TransportSlice::Udp(u)) => {
            if u.destination_port() == 53 {
                return domestic_question(rules, u.payload());
            }
            let key = (
                SocketAddr::new(src, u.source_port()),
                SocketAddr::new(dst, u.destination_port()),
            );
            if let Some((owned, end)) = udp.get_mut(&key) {
                if *end > now {
                    *end = now + UDP_IDLE;
                    return *owned;
                }
            }
            let owned = rules.learned(dst, now);
            if udp.len() >= 4096 {
                udp.retain(|_, (_, end)| *end > now);
            }
            if udp.len() < 4096 {
                udp.insert(key, (owned, now + UDP_IDLE));
            }
            owned
        }
        _ => false,
    }
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
    routing: Arc<Forwarding>,
    (local, remote): UdpFlow,
    mut outgoing: mpsc::Receiver<Vec<u8>>,
    replies: mpsc::Sender<netstack_smoltcp::udp::UdpMsg>,
) -> io::Result<()> {
    if let Some(rules) = &routing.rules {
        if remote.port() == 53 {
            while let Ok(Some(query)) = timeout(DNS_IDLE, outgoing.recv()).await {
                if let Ok(Ok(reply)) = timeout(SETUP, routing.dns(remote, &query, false)).await {
                    replies
                        .send((reply, remote, local))
                        .await
                        .map_err(|_| io::ErrorKind::BrokenPipe)?;
                }
            }
            return Ok(());
        }
        if matches!(routing.backend, Backend::Xray { .. })
            || rules.direct(remote.ip(), Instant::now())
        {
            let socket = routing.udp(remote).await?;
            let mut buf = vec![0; 65535];
            loop {
                tokio::select! {
                    n = socket.recv(&mut buf) => {
                        replies.send((buf[..n?].to_vec(), remote, local)).await.map_err(|_| io::ErrorKind::BrokenPipe)?;
                    }
                    packet = outgoing.recv() => {
                        let Some(packet) = packet else {return Ok(())};
                        socket.send(&packet).await?;
                    }
                    _ = tokio::time::sleep(UDP_IDLE) => return Ok(()),
                }
            }
        }
    }
    let Backend::Nexus(connector) = &routing.backend else {
        return Err(io::ErrorKind::InvalidInput.into());
    };
    let channel = nexus_client::open(connector, &Mode::Auto, Flow::Udp, remote).await?;
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
    routing: Arc<Forwarding>,
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
                    let routing = routing.clone();
                    let replies = replies.clone();
                    tasks.spawn(async move { let _ = udp_flow(routing, key, rx, replies).await; key });
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
    routing: Arc<Forwarding>,
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
        let mut udp_paths = HashMap::new();
        let mut sessions =
            HashMap::<TcpFlow, Option<oneshot::Sender<netstack_smoltcp::TcpStream>>>::new();
        loop {
            tokio::select! {
                read = tun.async_io(Interest::READABLE, |mut file| file.read(&mut buf)) => {
                    let size = read?;
                    if size == 0 { return Err(io::ErrorKind::UnexpectedEof.into()); }
                    if let Backend::Xray { packets, .. } = &routing.backend {
                        if !native_packet(&buf[..size],&routing,|k| sessions.contains_key(&k),&mut udp_paths) {
                            packets.send(&buf[..size]).await?;
                            continue;
                        }
                    }
                    let packet = buf[..size].to_vec();
                    let Some((key, ack)) = initial_syn(&packet) else { sink.send(packet).await?; continue };
                    if sessions.contains_key(&key) || sessions.len() >= MAX_TCP_FLOWS { continue; }
                    let destination = if let Some(alias) = route {
                        if key.1 != SocketAddr::new(alias, target.port()) { continue; }
                        target
                    } else { key.1 };
                    sessions.insert(key,None);
                    let routing = routing.clone();
                    // smoltcp acknowledges a SYN immediately. Only give it the SYN after the
                    // node connects, so a browser can race IPv4 against unreachable IPv6.
                    opening.spawn(async move {
                        let channel = routing.open_tcp(destination).await;
                        (key,ack,packet,channel)
                    });
                }
                Some(done) = opening.join_next() => {
                    let (key,ack,packet,channel) = done?;
                    let Ok(channel) = channel else {
                        sessions.remove(&key);
                        refuse_syn(&tun,key,ack).await?;
                        continue;
                    };
                    let (sender,receiver) = oneshot::channel::<netstack_smoltcp::TcpStream>();
                    sessions.insert(key,Some(sender));
                    let routing=routing.clone();
                    relays.spawn(async move {
                        if let Ok(Ok(mut stream)) = timeout(SETUP,receiver).await {
                            let _=timeout(LIFETIME,routing.relay(channel,key.1,&mut stream)).await;
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
    let xray_replies = async {
        let Backend::Xray { packets, .. } = &routing.backend else {
            return std::future::pending().await;
        };
        let mut packet = vec![0; 65535];
        loop {
            let n = packets.recv(&mut packet).await?;
            if n == 0 {
                return Err(io::ErrorKind::UnexpectedEof.into());
            }
            write_tun(&tun, &packet[..n]).await?;
        }
    };
    routing.device.ready(0)?;
    let datagrams = async {
        match udp {
            Some(socket) => udp_flows(socket, routing.clone()).await,
            None => std::future::pending().await,
        }
    };
    tokio::select! {r=runner=>r, r=ingress=>r, r=egress=>r, r=datagrams=>r,r=xray_replies=>r}
}
