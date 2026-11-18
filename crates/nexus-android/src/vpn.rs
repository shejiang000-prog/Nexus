use super::{transfer, Connector, Device};
use futures_util::{SinkExt, StreamExt};
use netstack_smoltcp::StackBuilder;
use std::{
    fs::File,
    io::{self, Read, Write},
    net::{IpAddr, SocketAddr},
    os::fd::OwnedFd,
    sync::Arc,
};
use tokio::{
    io::{unix::AsyncFd, Interest},
    task::JoinSet,
};

pub(super) async fn forward(
    fd: OwnedFd,
    route: IpAddr,
    target: SocketAddr,
    connector: Arc<Connector>,
    device: Arc<Device>,
) -> io::Result<()> {
    // The file owns this nonblocking TUN descriptor exclusively until AsyncFd drops it.
    let tun = unsafe { AsyncFd::register(File::from(fd)) }.map_err(io::Error::from)?;
    let (stack, runner, _, listener) = StackBuilder::default()
        .enable_tcp(true)
        .mtu(1500)
        .stack_buffer_size(64)
        .tcp_buffer_size(64)
        .tcp_recv_buffer_size(65536)
        .tcp_send_buffer_size(65536)
        .add_ip_filter_fn(move |_, destination| *destination == route)
        .build()?;
    let mut listener = listener.ok_or_else(|| io::Error::other("missing TCP listener"))?;
    let runner = runner.ok_or_else(|| io::Error::other("missing TCP runner"))?;
    let (mut sink, mut packets) = stack.split();
    let ingress = async {
        let mut buf = [0u8; 1500];
        loop {
            let size = tun
                .async_io(Interest::READABLE, |mut file| file.read(&mut buf))
                .await?;
            if size == 0 {
                return Err(io::Error::from(io::ErrorKind::UnexpectedEof));
            }
            sink.send(buf[..size].to_vec()).await?;
        }
    };
    let egress = async {
        while let Some(packet) = packets.next().await {
            let packet = packet?;
            let written = tun
                .async_io(Interest::WRITABLE, |mut file| file.write(&packet))
                .await?;
            if written != packet.len() {
                return Err(io::Error::from(io::ErrorKind::WriteZero));
            }
        }
        Err(io::Error::from(io::ErrorKind::BrokenPipe))
    };
    let flows = async {
        let mut active = JoinSet::new();
        loop {
            tokio::select! {
                stream = listener.next() => {
                    let (stream, _, destination) = stream.ok_or_else(|| io::Error::from(io::ErrorKind::BrokenPipe))?;
                    if destination == SocketAddr::new(route, target.port()) && active.len() < 16 {
                        active.spawn(transfer(stream,connector.clone(),target));
                    }
                }
                Some(_) = active.join_next(), if !active.is_empty() => {}
            }
        }
    };
    device.ready(0)?;
    tokio::select! {r=runner=>r, r=ingress=>r, r=egress=>r, r=flows=>r}
}
