use super::{
    split::{answers, directives, domestic_question, Rules},
    Device, SETUP,
};
use std::{
    io,
    net::{IpAddr, SocketAddr},
    os::fd::AsRawFd,
    path::PathBuf,
    sync::Arc,
    time::{Duration, Instant},
};
use tokio::{
    io::{copy_bidirectional, AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt},
    net::{TcpSocket, TcpStream, UdpSocket, UnixDatagram, UnixStream},
    time::timeout,
};

pub(super) struct Forwarding {
    pub packets: UnixDatagram,
    dns_socket: PathBuf,
    pub rules: Rules,
    pub device: Arc<Device>,
    pub resolvers: Vec<SocketAddr>,
    /// VPN DNS addresses whose DoT port is refused, so opportunistic Private DNS
    /// falls back to port 53 where domestic names can be classified.
    pub refuse_dot: Vec<IpAddr>,
}

pub(super) const DOT: u16 = 853;

pub(super) enum Tcp {
    Direct(TcpStream),
    Resolver,
}

async fn framed_query(
    stream: &mut (impl AsyncRead + AsyncWrite + Unpin),
    query: &[u8],
) -> io::Result<Vec<u8>> {
    stream
        .write_u16(super::split::dns_frame_length(query.len())?)
        .await?;
    stream.write_all(query).await?;
    stream.flush().await?;
    let mut reply = vec![0; usize::from(stream.read_u16().await?)];
    stream.read_exact(&mut reply).await?;
    Ok(reply)
}

impl Forwarding {
    pub fn new(
        packets: UnixDatagram,
        dns_socket: PathBuf,
        device: Arc<Device>,
        text: &str,
    ) -> io::Result<Self> {
        let (resolvers, refuse_dot, text) = directives(text).ok_or(io::ErrorKind::InvalidData)?;
        if text.is_empty() {
            return Err(io::ErrorKind::InvalidData.into());
        }
        let rules = Rules::parse(text).ok_or(io::ErrorKind::InvalidData)?;
        Ok(Self {
            packets,
            dns_socket,
            device,
            rules,
            resolvers,
            refuse_dot,
        })
    }

    pub fn refused(&self, remote: SocketAddr) -> bool {
        remote.port() == DOT && self.refuse_dot.contains(&remote.ip())
    }

    fn bind(&self, socket: &impl AsRawFd) -> io::Result<()> {
        self.device.protect(socket.as_raw_fd())
    }
    pub async fn udp(&self, addr: SocketAddr) -> io::Result<UdpSocket> {
        let socket = UdpSocket::bind(if addr.is_ipv4() {
            "0.0.0.0:0"
        } else {
            "[::]:0"
        })
        .await?;
        self.bind(&socket)?;
        socket.connect(addr).await?;
        Ok(socket)
    }
    async fn tcp(&self, addr: SocketAddr) -> io::Result<TcpStream> {
        let socket = if addr.is_ipv4() {
            TcpSocket::new_v4()?
        } else {
            TcpSocket::new_v6()?
        };
        self.bind(&socket)?;
        let stream = timeout(SETUP, socket.connect(addr)).await??;
        stream.set_nodelay(true)?;
        Ok(stream)
    }
    pub async fn open_tcp(&self, remote: SocketAddr) -> io::Result<Tcp> {
        if self.refused(remote) {
            return Err(io::ErrorKind::ConnectionRefused.into());
        }
        if remote.port() == 53 {
            return Ok(Tcp::Resolver);
        }
        self.tcp(remote).await.map(Tcp::Direct)
    }
    async fn domestic(&self, query: &[u8], tcp: bool) -> io::Result<Vec<u8>> {
        for &addr in &self.resolvers {
            let attempt = async {
                let reply = if tcp {
                    framed_query(&mut self.tcp(addr).await?, query).await?
                } else {
                    let socket = self.udp(addr).await?;
                    socket.send(query).await?;
                    let mut reply = vec![0; 65535];
                    let n = socket.recv(&mut reply).await?;
                    reply.truncate(n);
                    reply
                };
                let records = answers(query, &reply).ok_or(io::ErrorKind::InvalidData)?;
                if reply[3] & 15 == 2 || reply[3] & 15 == 5 {
                    return Err(io::ErrorKind::Other.into());
                }
                if !self.rules.remember(records, Instant::now()) {
                    // Never release an answer whose direct route cannot be retained.
                    return Err(io::ErrorKind::WouldBlock.into());
                }
                Ok::<_, io::Error>(reply)
            };
            if let Ok(Ok(reply)) = timeout(Duration::from_secs(2), attempt).await {
                return Ok(reply);
            }
        }
        Err(io::ErrorKind::TimedOut.into())
    }
    pub async fn dns(&self, remote: SocketAddr, query: &[u8], tcp: bool) -> io::Result<Vec<u8>> {
        if domestic_question(&self.rules, query) {
            return self.domestic(query, tcp).await;
        }
        // The Xray inbound lives in the app-private directory and routes only
        // DNS through the selected node. Each query retains its original resolver.
        let mut stream = UnixStream::connect(&self.dns_socket).await?;
        stream
            .write_all(format!("CONNECT {remote} HTTP/1.1\r\nHost: {remote}\r\n\r\n").as_bytes())
            .await?;
        // This is a private, pinned in-process Xray HTTP inbound.
        const RESPONSE: &[u8] = b"HTTP/1.1 200 Connection established\r\n\r\n";
        let mut header = [0; RESPONSE.len()];
        stream.read_exact(&mut header).await?;
        if header.as_slice() != RESPONSE {
            return Err(io::ErrorKind::ConnectionRefused.into());
        }
        framed_query(&mut stream, query).await
    }
    pub async fn relay(
        &self,
        channel: Tcp,
        remote: SocketAddr,
        stream: &mut (impl AsyncRead + AsyncWrite + Unpin),
    ) -> io::Result<()> {
        match channel {
            Tcp::Direct(mut peer) => {
                copy_bidirectional(stream, &mut peer).await?;
            }
            Tcp::Resolver => loop {
                let n = timeout(Duration::from_secs(10), stream.read_u16()).await??;
                let mut query = vec![0; usize::from(n)];
                timeout(SETUP, stream.read_exact(&mut query)).await??;
                let reply = timeout(SETUP, self.dns(remote, &query, true)).await??;
                stream
                    .write_u16(super::split::dns_frame_length(reply.len())?)
                    .await?;
                stream.write_all(&reply).await?;
                stream.flush().await?;
            },
        }
        Ok(())
    }
}
