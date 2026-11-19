use nexus_protocol::{write_open, Flow, DENIED, INVALID, OK, UNREACHABLE};
use nexus_transport::{Channel, Connector, Mode, SETUP};
use std::{io, net::SocketAddr};
use tokio::{io::AsyncReadExt, time::timeout};

pub async fn open(
    connector: &Connector,
    mode: &Mode,
    flow: Flow,
    target: SocketAddr,
) -> io::Result<Channel> {
    // DNS is a short request/response exchange. Carrying it as a framed stream on the pooled,
    // already authenticated connection avoids a full QUIC handshake and device signature per
    // lookup. Other UDP keeps its own connection so QUIC DATAGRAM avoids head-of-line delay.
    let dedicated = flow == Flow::Udp && target.port() != 53;
    let mut ch = connector.connect(mode, dedicated).await?;
    timeout(SETUP, async {
        write_open(&mut ch.stream, flow, target).await?;
        match ch.stream.read_u8().await? {
            OK => Ok(()),
            DENIED => Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "destination denied",
            )),
            UNREACHABLE => Err(io::Error::new(
                io::ErrorKind::ConnectionRefused,
                "destination unreachable",
            )),
            INVALID => Err(io::Error::new(io::ErrorKind::InvalidData, "open rejected")),
            _ => Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "unknown response",
            )),
        }
    })
    .await??;
    Ok(ch)
}
