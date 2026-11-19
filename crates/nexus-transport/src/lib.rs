use quinn::{
    crypto::rustls::{QuicClientConfig, QuicServerConfig},
    Connection, Endpoint,
};
use rustls::{
    pki_types::{CertificateDer, PrivateKeyDer, ServerName},
    server::WebPkiClientVerifier,
    ClientConfig, RootCertStore, ServerConfig,
};
use std::{fs, io, net::SocketAddr, sync::Arc, time::Duration};
use tokio::{
    io::{AsyncRead, AsyncWrite},
    net::TcpStream,
    time::timeout,
};
use tokio_rustls::{TlsAcceptor, TlsConnector};

const ALPN: &[u8] = b"nexus/1";
const MULTIPLEX: &[u8] = b"nexus/mux1";
const MUX2: &[u8] = b"nexus/mux2";
const DATAGRAM: &[u8] = b"nexus/udp1";
pub const SETUP: Duration = Duration::from_secs(8);
pub const LIFETIME: Duration = Duration::from_secs(3600);
pub const MAX_CONNECTIONS: usize = 64;
/// Concurrent multiplexed flows a peer may open on one QUIC connection.
pub const MAX_STREAMS: u32 = 256;
// A stream window bounds one flow to window/RTT. 2 MiB keeps a single download above
// 80 Mbit/s at 200 ms RTT; the connection window caps the aggregate buffered per peer.
const STREAM_WINDOW: u32 = 2 * 1024 * 1024;
const CONNECTION_WINDOW: u32 = 16 * 1024 * 1024;

/// Shared QUIC tuning. BBR keeps throughput on lossy long-distance paths where loss-based
/// Cubic collapses, which is the main weak-network case for a private node.
fn tuned_transport() -> quinn::TransportConfig {
    let mut transport = quinn::TransportConfig::default();
    transport
        .stream_receive_window(STREAM_WINDOW.into())
        .receive_window(CONNECTION_WINDOW.into())
        .send_window(u64::from(CONNECTION_WINDOW))
        .datagram_receive_buffer_size(Some(512 * 1024))
        .datagram_send_buffer_size(512 * 1024)
        .congestion_controller_factory(Arc::new(quinn::congestion::BbrConfig::default()));
    transport
}

pub trait Duplex: AsyncRead + AsyncWrite + Unpin + Send {}
impl<T: AsyncRead + AsyncWrite + Unpin + Send> Duplex for T {}

mod mux;
use mux::{Association, Router};

#[derive(Clone)]
struct Peer {
    conn: Connection,
    mux: Option<Arc<Router>>,
    pooled: bool,
}
impl Peer {
    fn new(conn: Connection) -> Self {
        let mux = negotiated(&conn, MUX2).then(|| Router::new(conn.clone()));
        let pooled = mux.is_some() || negotiated(&conn, MULTIPLEX);
        Self { conn, mux, pooled }
    }
    fn channel(
        &self,
        send: quinn::SendStream,
        recv: quinn::RecvStream,
        cert: Option<CertificateDer<'static>>,
    ) -> Channel {
        Channel {
            peer_certificate: cert,
            mux: self.mux.clone().map(|router| (router, send.id().index())),
            association: None,
            stream: Box::new(tokio::io::join(recv, send)),
            connection: (!self.pooled || self.mux.is_some()).then(|| self.conn.clone()),
        }
    }
}

pub struct Channel {
    mux: Option<(Arc<Router>, u64)>,
    association: Option<Association>,
    pub stream: Box<dyn Duplex>,
    pub peer_certificate: Option<CertificateDer<'static>>,
    // Keep the connection alive until the peer drains the final stream bytes.
    connection: Option<Connection>,
}
fn negotiated(conn: &Connection, protocol: &[u8]) -> bool {
    conn.handshake_data()
        .and_then(|h| h.downcast::<quinn::crypto::rustls::HandshakeData>().ok())
        .is_some_and(|h| h.protocol.as_deref() == Some(protocol))
}

pub struct PacketReader {
    input: tokio::sync::mpsc::Receiver<io::Result<Vec<u8>>>,
    datagrams: Option<tokio::sync::mpsc::Receiver<mux::Queued>>,
    _association: Option<Association>,
    _readers: tokio::task::JoinSet<()>,
}
impl PacketReader {
    pub async fn recv(&mut self) -> io::Result<Vec<u8>> {
        let packet = match self.datagrams.as_mut() {
            // Datagrams and stream frames are not mutually ordered. Dropping the permit returns
            // this payload's share of the connection budget.
            Some(datagrams) => tokio::select! {
                Some((data, _permit)) = datagrams.recv() => return Ok(data),
                packet = self.input.recv() => packet,
            },
            None => self.input.recv().await,
        };
        packet.ok_or(io::ErrorKind::UnexpectedEof)?
    }
}
pub struct PacketWriter {
    stream: tokio::io::WriteHalf<Box<dyn Duplex>>,
    connection: Option<Connection>,
    native: bool,
    id: Option<u32>,
}
impl PacketWriter {
    pub async fn send(&mut self, payload: &[u8]) -> io::Result<()> {
        if payload.len() > nexus_protocol::MAX_PACKET {
            return Err(io::ErrorKind::InvalidData.into());
        }
        if let Some(conn) = self
            .connection
            .as_ref()
            .filter(|_| self.native || self.id.is_some() && !payload.is_empty())
        {
            if conn
                .max_datagram_size()
                .is_some_and(|max| payload.len() + if self.id.is_some() { 4 } else { 0 } <= max)
            {
                let data = match self.id {
                    Some(id) => mux::encode(id, payload)?,
                    None => payload.to_vec(),
                };
                match conn.send_datagram(data.into()) {
                    Ok(()) => return Ok(()),
                    Err(quinn::SendDatagramError::TooLarge) => {}
                    Err(e) => return Err(err(e)),
                }
            }
        }
        nexus_protocol::write_packet(&mut self.stream, payload).await
    }
}
impl Channel {
    pub fn mux2(&self) -> bool {
        self.mux.is_some()
    }

    /// Stream indices are unique for the lifetime of a connection. Reject exhaustion instead of wrapping.
    pub fn register_udp(&mut self) -> io::Result<u32> {
        let (router, index) = self.mux.as_ref().ok_or(io::ErrorKind::InvalidInput)?;
        let id = u32::try_from(*index).map_err(err)?;
        self.association = Some(router.register(id)?);
        Ok(id)
    }
    pub fn packets(mut self) -> (PacketReader, PacketWriter) {
        let (mut reader, stream) = tokio::io::split(self.stream);
        let id = self.association.as_ref().map(|a| a.id);
        let datagrams = self.association.as_mut().and_then(|a| a.queue.take());
        let (tx, input) = tokio::sync::mpsc::channel(16);
        let mut readers = tokio::task::JoinSet::new();
        let native = self
            .connection
            .as_ref()
            .is_some_and(|c| negotiated(c, DATAGRAM));
        if let Some(conn) = self.connection.as_ref().filter(|_| native).cloned() {
            let native_tx = tx.clone();
            readers.spawn(async move {
                while let Ok(data) = conn.read_datagram().await {
                    if data.len() <= nexus_protocol::MAX_PACKET
                        && native_tx.send(Ok(data.to_vec())).await.is_err()
                    {
                        break;
                    }
                }
            });
        }
        readers.spawn(async move {
            let mut buf = [0; nexus_protocol::MAX_PACKET];
            loop {
                match nexus_protocol::read_packet(&mut reader, &mut buf).await {
                    Ok(size) => {
                        if tx.send(Ok(buf[..size].to_vec())).await.is_err() {
                            break;
                        }
                    }
                    Err(e) => {
                        let _ = tx.send(Err(e)).await;
                        break;
                    }
                }
            }
        });
        (
            PacketReader {
                input,
                datagrams,
                _association: self.association,
                _readers: readers,
            },
            PacketWriter {
                stream,
                connection: self.connection,
                native,
                id,
            },
        )
    }
    pub async fn drain(self) {
        if self.mux2() {
            return;
        }
        if let Some(conn) = self.connection {
            let _ = timeout(Duration::from_secs(3), conn.closed()).await;
        }
    }
}

fn err(e: impl std::fmt::Display) -> io::Error {
    io::Error::other(e.to_string())
}
fn denied() -> io::Error {
    io::Error::new(
        io::ErrorKind::PermissionDenied,
        "peer identity or ALPN denied",
    )
}
fn cert(path: &str) -> io::Result<CertificateDer<'static>> {
    Ok(fs::read(path)?.into())
}
fn roots(path: &str) -> io::Result<RootCertStore> {
    let mut store = RootCertStore::empty();
    store.add(cert(path)?).map_err(err)?;
    Ok(store)
}
fn key(path: &str) -> io::Result<PrivateKeyDer<'static>> {
    PrivateKeyDer::try_from(fs::read(path)?).map_err(err)
}

#[derive(Clone)]
pub struct ServerTls {
    tls: Arc<ServerConfig>,
    device: Option<CertificateDer<'static>>,
}
impl ServerTls {
    pub fn load(ca: &str, identity: &str, secret: &str, device: &str) -> io::Result<Self> {
        let mut tls = Self::for_device_registry(ca, identity, secret)?;
        tls.device = Some(cert(device)?);
        Ok(tls)
    }
    pub fn for_device_registry(ca: &str, identity: &str, secret: &str) -> io::Result<Self> {
        let verifier = WebPkiClientVerifier::builder(Arc::new(roots(ca)?))
            .build()
            .map_err(err)?;
        let mut tls = ServerConfig::builder_with_protocol_versions(&[&rustls::version::TLS13])
            .with_client_cert_verifier(verifier)
            .with_single_cert(vec![cert(identity)?], key(secret)?)
            .map_err(err)?;
        tls.alpn_protocols = vec![ALPN.to_vec()];
        tls.send_tls13_tickets = 0;
        Ok(Self {
            tls: Arc::new(tls),
            device: None,
        })
    }
    pub fn endpoint(&self, addr: SocketAddr) -> io::Result<Endpoint> {
        let mut tls = self.tls.as_ref().clone();
        tls.alpn_protocols = vec![
            MUX2.to_vec(),
            MULTIPLEX.to_vec(),
            DATAGRAM.to_vec(),
            ALPN.to_vec(),
        ];
        let crypto = QuicServerConfig::try_from(tls).map_err(err)?;
        let mut config = quinn::ServerConfig::with_crypto(Arc::new(crypto));
        let mut limits = tuned_transport();
        limits
            .max_concurrent_bidi_streams(MAX_STREAMS.into())
            .max_concurrent_uni_streams(0u32.into());
        config.transport_config(Arc::new(limits));
        Endpoint::server(config, addr)
    }
    pub async fn tcp(&self, socket: TcpStream) -> io::Result<Channel> {
        let stream = TlsAcceptor::from(self.tls.clone()).accept(socket).await?;
        let peer = stream.get_ref().1;
        let leaf = peer
            .peer_certificates()
            .and_then(|c| c.first())
            .cloned()
            .ok_or_else(denied)?;
        if peer.alpn_protocol() != Some(ALPN)
            || self.device.as_ref().is_some_and(|device| device != &leaf)
        {
            return Err(denied());
        }
        Ok(Channel {
            peer_certificate: Some(leaf),
            stream: Box::new(stream),
            connection: None,
            mux: None,
            association: None,
        })
    }
    pub async fn quic(&self, incoming: quinn::Incoming) -> io::Result<QuicSession> {
        let conn = incoming.await.map_err(err)?;
        let chain = conn
            .peer_identity()
            .and_then(|p| p.downcast::<Vec<CertificateDer<'static>>>().ok());
        let leaf = chain
            .as_ref()
            .and_then(|c| c.first())
            .cloned()
            .ok_or_else(denied)?;
        if self.device.as_ref().is_some_and(|device| device != &leaf) {
            return Err(denied());
        }
        Ok(QuicSession {
            peer: Peer::new(conn),
            leaf,
        })
    }
}
pub struct QuicSession {
    peer: Peer,
    leaf: CertificateDer<'static>,
}
impl QuicSession {
    pub fn multiplex(&self) -> bool {
        self.peer.pooled
    }
    pub fn mux2(&self) -> bool {
        self.peer.mux.is_some()
    }
    pub fn certificate(&self) -> &[u8] {
        self.leaf.as_ref()
    }
    pub fn close(&self) {
        self.peer.conn.close(0u32.into(), b"session ended");
    }
    pub async fn accept(&self) -> io::Result<Channel> {
        let (send, recv) = self.peer.conn.accept_bi().await.map_err(err)?;
        Ok(self.peer.channel(send, recv, Some(self.leaf.clone())))
    }
}

pub enum Mode {
    Quic,
    Tls,
    Auto,
}
impl std::str::FromStr for Mode {
    type Err = io::Error;
    fn from_str(s: &str) -> io::Result<Self> {
        match s {
            "quic" => Ok(Self::Quic),
            "tls" => Ok(Self::Tls),
            "auto" => Ok(Self::Auto),
            _ => Err(err("mode must be quic, tls or auto")),
        }
    }
}

pub struct Connector {
    #[cfg(target_os = "android")]
    protect: Option<Arc<dyn Fn(i32) -> io::Result<()> + Send + Sync>>,
    #[cfg(target_os = "android")]
    network_handle: u64,
    tls: Arc<ClientConfig>,
    quic: tokio::sync::OnceCell<Endpoint>,
    pooled: tokio::sync::Mutex<Option<Peer>>,
    legacy_udp: std::sync::atomic::AtomicBool,
    retry_quic: std::sync::Mutex<Option<std::time::Instant>>,
    server: SocketAddr,
    name: ServerName<'static>,
}
impl Connector {
    #[cfg(target_os = "android")]
    pub fn active_path_stats(&self) -> Option<quinn::PathStats> {
        let pool = self.pooled.try_lock().ok()?;
        pool.as_ref()
            .filter(|c| c.conn.close_reason().is_none())
            .map(|c| c.conn.stats().path)
    }

    pub fn new(
        server: SocketAddr,
        name: &str,
        ca: &str,
        identity: &str,
        secret: &str,
    ) -> io::Result<Self> {
        let tls = ClientConfig::builder_with_protocol_versions(&[&rustls::version::TLS13])
            .with_root_certificates(roots(ca)?)
            .with_client_auth_cert(vec![cert(identity)?], key(secret)?)
            .map_err(err)?;
        Self::from_config(server, name, tls)
    }
    pub fn from_config(server: SocketAddr, name: &str, mut tls: ClientConfig) -> io::Result<Self> {
        tls.alpn_protocols = vec![ALPN.to_vec()];
        tls.resumption = rustls::client::Resumption::disabled();
        Ok(Self {
            tls: Arc::new(tls),
            quic: tokio::sync::OnceCell::new(),
            legacy_udp: false.into(),
            pooled: tokio::sync::Mutex::new(None),
            retry_quic: std::sync::Mutex::new(None),
            #[cfg(target_os = "android")]
            network_handle: 0,
            #[cfg(target_os = "android")]
            protect: None,
            server,
            name: ServerName::try_from(name.to_owned()).map_err(err)?,
        })
    }
    #[cfg(target_os = "android")]
    pub fn with_android_network(
        mut self,
        handle: u64,
        protect: impl Fn(i32) -> io::Result<()> + Send + Sync + 'static,
    ) -> Self {
        self.network_handle = handle;
        self.protect = Some(Arc::new(protect));
        self
    }
    #[cfg(target_os = "android")]
    fn bind_android_network(&self, socket: &impl std::os::fd::AsRawFd) -> io::Result<()> {
        #[link(name = "android")]
        extern "C" {
            fn android_setsocknetwork(network: u64, fd: i32) -> i32;
        }
        // NDK API 23+, socket owns the descriptor throughout this synchronous call.
        if self.network_handle != 0 {
            if let Some(protect) = &self.protect {
                protect(socket.as_raw_fd())?;
            }
        }
        if self.network_handle != 0
            && unsafe { android_setsocknetwork(self.network_handle, socket.as_raw_fd()) } != 0
        {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    }
    fn endpoint(&self) -> io::Result<Endpoint> {
        let local = if self.server.is_ipv4() {
            "0.0.0.0:0"
        } else {
            "[::]:0"
        }
        .parse::<SocketAddr>()
        .unwrap();
        #[cfg(target_os = "android")]
        let endpoint = (|| {
            let udp = std::net::UdpSocket::bind(local)?;
            self.bind_android_network(&udp)?;
            Endpoint::new(
                quinn::EndpointConfig::default(),
                None,
                udp,
                Arc::new(quinn::TokioRuntime),
            )
        })();
        #[cfg(not(target_os = "android"))]
        let endpoint = Endpoint::client(local);
        endpoint.map_err(|e| io::Error::new(io::ErrorKind::AddrNotAvailable, e))
    }
    async fn new_connection(&self, protocol: &[u8]) -> io::Result<Connection> {
        let endpoint = self
            .quic
            .get_or_try_init(|| async { self.endpoint() })
            .await?;
        let mut tls = self.tls.as_ref().clone();
        tls.alpn_protocols = vec![MUX2.to_vec(), protocol.to_vec(), ALPN.to_vec()];
        let mut cfg =
            quinn::ClientConfig::new(Arc::new(QuicClientConfig::try_from(tls).map_err(err)?));
        let mut transport = tuned_transport();
        transport
            .keep_alive_interval(Some(Duration::from_secs(10)))
            // The server never opens streams towards the client.
            .max_concurrent_bidi_streams(0u32.into())
            .max_concurrent_uni_streams(0u32.into());
        cfg.transport_config(Arc::new(transport));
        endpoint
            .connect_with(cfg, self.server, &self.name.to_str())
            .map_err(err)?
            .await
            .map_err(err)
    }
    async fn quic(&self, udp: bool) -> io::Result<Channel> {
        use std::sync::atomic::Ordering::Relaxed;
        let mut cache = self.pooled.lock().await;
        let peer = if let Some(peer) = cache
            .as_ref()
            .filter(|p| p.conn.close_reason().is_none() && (!udp || p.mux.is_some()))
        {
            peer.clone()
        } else if udp && self.legacy_udp.load(Relaxed) {
            // Old servers require separate handshakes. Do not serialize those behind the pool lock.
            drop(cache);
            let peer = Peer::new(self.new_connection(DATAGRAM).await?);
            if peer.pooled {
                *self.pooled.lock().await = Some(peer.clone());
                self.legacy_udp.store(false, Relaxed);
            }
            let (send, recv) = peer.conn.open_bi().await.map_err(err)?;
            return Ok(peer.channel(send, recv, None));
        } else {
            let peer = Peer::new(
                self.new_connection(if udp { DATAGRAM } else { MULTIPLEX })
                    .await?,
            );
            self.legacy_udp.store(peer.mux.is_none(), Relaxed);
            if peer.pooled {
                *cache = Some(peer.clone());
            }
            peer
        };
        drop(cache);
        let (send, recv) = peer.conn.open_bi().await.map_err(err)?;
        Ok(peer.channel(send, recv, None))
    }
    pub async fn connect(&self, mode: &Mode, udp: bool) -> io::Result<Channel> {
        let cooling_down = matches!(mode, Mode::Auto)
            && self
                .retry_quic
                .lock()
                .map_err(err)?
                .is_some_and(|until| std::time::Instant::now() < until);
        let attempt_quic = !matches!(mode, Mode::Tls) && !cooling_down;
        if attempt_quic {
            match timeout(SETUP, self.quic(udp)).await {
                Ok(Err(e))
                    if matches!(mode, Mode::Auto)
                        && e.kind() == io::ErrorKind::AddrNotAvailable => {}
                Ok(Ok(channel)) => {
                    *self.retry_quic.lock().map_err(err)? = None;
                    return Ok(channel);
                }
                Ok(Err(error)) => return Err(error),
                Err(e) if matches!(mode, Mode::Quic) => return Err(e.into()),
                Err(_) => {}
            }
        }
        let channel = timeout(SETUP, async {
            #[cfg(target_os = "android")]
            let socket = {
                use tokio::net::TcpSocket;
                let tcp = if self.server.is_ipv4() {
                    TcpSocket::new_v4()?
                } else {
                    TcpSocket::new_v6()?
                };
                self.bind_android_network(&tcp)?;
                tcp.connect(self.server).await?
            };
            #[cfg(not(target_os = "android"))]
            let socket = TcpStream::connect(self.server).await?;
            let stream = TlsConnector::from(self.tls.clone())
                .connect(self.name.clone(), socket)
                .await?;
            if stream.get_ref().1.alpn_protocol() != Some(ALPN) {
                return Err(denied());
            }
            Ok(Channel {
                peer_certificate: None,
                stream: Box::new(stream),
                connection: None,
                mux: None,
                association: None,
            })
        })
        .await??;
        if attempt_quic {
            *self.retry_quic.lock().map_err(err)? =
                Some(std::time::Instant::now() + Duration::from_secs(30));
        }
        Ok(channel)
    }
}
