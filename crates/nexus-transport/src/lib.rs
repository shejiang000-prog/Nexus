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
pub const SETUP: Duration = Duration::from_secs(8);
pub const LIFETIME: Duration = Duration::from_secs(3600);
pub const MAX_CONNECTIONS: usize = 64;

pub trait Duplex: AsyncRead + AsyncWrite + Unpin + Send {}
impl<T: AsyncRead + AsyncWrite + Unpin + Send> Duplex for T {}

pub struct Channel {
    pub stream: Box<dyn Duplex>,
    // Keep the connection alive until the peer drains the final stream bytes.
    connection: Option<Connection>,
}
impl Channel {
    pub async fn drain(self) {
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
    device: CertificateDer<'static>,
}
impl ServerTls {
    pub fn load(ca: &str, identity: &str, secret: &str, device: &str) -> io::Result<Self> {
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
            device: cert(device)?,
        })
    }
    pub fn endpoint(&self, addr: SocketAddr) -> io::Result<Endpoint> {
        let crypto = QuicServerConfig::try_from(self.tls.as_ref().clone()).map_err(err)?;
        let mut config = quinn::ServerConfig::with_crypto(Arc::new(crypto));
        let mut limits = quinn::TransportConfig::default();
        limits
            .max_concurrent_bidi_streams(1u32.into())
            .max_concurrent_uni_streams(0u32.into())
            .stream_receive_window(65536u32.into())
            .receive_window(131072u32.into())
            .datagram_receive_buffer_size(None);
        config.transport_config(Arc::new(limits));
        Endpoint::server(config, addr)
    }
    pub async fn tcp(&self, socket: TcpStream) -> io::Result<Channel> {
        let stream = TlsAcceptor::from(self.tls.clone()).accept(socket).await?;
        let peer = stream.get_ref().1;
        if peer.alpn_protocol() != Some(ALPN)
            || peer.peer_certificates().and_then(|c| c.first()) != Some(&self.device)
        {
            return Err(denied());
        }
        Ok(Channel {
            stream: Box::new(stream),
            connection: None,
        })
    }
    pub async fn quic(&self, incoming: quinn::Incoming) -> io::Result<Channel> {
        let conn = incoming.await.map_err(err)?;
        let chain = conn
            .peer_identity()
            .and_then(|p| p.downcast::<Vec<CertificateDer<'static>>>().ok());
        if chain.as_ref().and_then(|c| c.first()) != Some(&self.device) {
            return Err(denied());
        }
        let (send, recv) = conn.accept_bi().await.map_err(err)?;
        Ok(Channel {
            stream: Box::new(tokio::io::join(recv, send)),
            connection: Some(conn),
        })
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
    tls: Arc<ClientConfig>,
    quic: tokio::sync::OnceCell<Endpoint>,
    server: SocketAddr,
    name: ServerName<'static>,
}
impl Connector {
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
            server,
            name: ServerName::try_from(name.to_owned()).map_err(err)?,
        })
    }
    fn endpoint(&self) -> io::Result<Endpoint> {
        let mut quic = Endpoint::client(
            if self.server.is_ipv4() {
                "0.0.0.0:0"
            } else {
                "[::]:0"
            }
            .parse()
            .unwrap(),
        )
        .map_err(|e| io::Error::new(io::ErrorKind::AddrNotAvailable, e))?;
        quic.set_default_client_config(quinn::ClientConfig::new(Arc::new(
            QuicClientConfig::try_from(self.tls.as_ref().clone()).map_err(err)?,
        )));
        Ok(quic)
    }
    async fn quic(&self) -> io::Result<Channel> {
        let conn = self
            .quic
            .get_or_try_init(|| async { self.endpoint() })
            .await?
            .connect(self.server, &self.name.to_str())
            .map_err(err)?
            .await
            .map_err(err)?;
        let (send, recv) = conn.open_bi().await.map_err(err)?;
        Ok(Channel {
            stream: Box::new(tokio::io::join(recv, send)),
            connection: Some(conn),
        })
    }
    pub async fn connect(&self, mode: &Mode) -> io::Result<Channel> {
        if !matches!(mode, Mode::Tls) {
            match timeout(SETUP, self.quic()).await {
                Ok(Err(e))
                    if matches!(mode, Mode::Auto)
                        && e.kind() == io::ErrorKind::AddrNotAvailable => {}
                Ok(result) => return result,
                Err(e) if matches!(mode, Mode::Quic) => return Err(e.into()),
                Err(_) => {}
            }
        }
        timeout(SETUP, async {
            let socket = TcpStream::connect(self.server).await?;
            let stream = TlsConnector::from(self.tls.clone())
                .connect(self.name.clone(), socket)
                .await?;
            if stream.get_ref().1.alpn_protocol() != Some(ALPN) {
                return Err(denied());
            }
            Ok(Channel {
                stream: Box::new(stream),
                connection: None,
            })
        })
        .await?
    }
}
