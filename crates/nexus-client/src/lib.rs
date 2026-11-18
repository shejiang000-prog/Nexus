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
    let mut ch = connector.connect(mode, flow == Flow::Udp).await?;
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
