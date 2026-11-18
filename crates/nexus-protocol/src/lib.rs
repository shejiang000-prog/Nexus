use std::{
    io,
    net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr},
};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAGIC: &[u8; 4] = b"NXUS";
const VERSION: u8 = 1;
pub const OK: u8 = 0;
pub const INVALID: u8 = 1;
pub const DENIED: u8 = 2;
pub const UNREACHABLE: u8 = 3;
pub const MAX_PACKET: usize = 1472; // Largest IPv4 UDP payload at the Android TUN MTU of 1500.

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Flow {
    Tcp = 1,
    Udp = 2,
}

pub fn invalid(msg: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, msg)
}

pub async fn write_open(
    w: &mut (impl AsyncWrite + Unpin),
    flow: Flow,
    target: SocketAddr,
) -> io::Result<()> {
    w.write_all(MAGIC).await?;
    w.write_all(&[VERSION, flow as u8]).await?;
    match target.ip() {
        IpAddr::V4(ip) => {
            w.write_u8(4).await?;
            w.write_all(&ip.octets()).await?;
        }
        IpAddr::V6(ip) => {
            w.write_u8(6).await?;
            w.write_all(&ip.octets()).await?;
        }
    }
    w.write_u16(target.port()).await?;
    w.flush().await
}

pub async fn read_open(r: &mut (impl AsyncRead + Unpin)) -> io::Result<(Flow, SocketAddr)> {
    let mut magic = [0; 4];
    r.read_exact(&mut magic).await?;
    if &magic != MAGIC || r.read_u8().await? != VERSION {
        return Err(invalid("unsupported Nexus preface"));
    }
    let flow = match r.read_u8().await? {
        1 => Flow::Tcp,
        2 => Flow::Udp,
        _ => return Err(invalid("unknown flow")),
    };
    let ip = match r.read_u8().await? {
        4 => {
            let mut bytes = [0; 4];
            r.read_exact(&mut bytes).await?;
            IpAddr::V4(Ipv4Addr::from(bytes))
        }
        6 => {
            let mut bytes = [0; 16];
            r.read_exact(&mut bytes).await?;
            IpAddr::V6(Ipv6Addr::from(bytes))
        }
        _ => return Err(invalid("unknown address family")),
    };
    let port = r.read_u16().await?;
    if port == 0 {
        return Err(invalid("invalid port"));
    }
    Ok((flow, SocketAddr::new(ip, port)))
}

pub async fn read_packet(
    r: &mut (impl AsyncRead + Unpin),
    buf: &mut [u8; MAX_PACKET],
) -> io::Result<usize> {
    let n = usize::from(r.read_u16().await?);
    if n > MAX_PACKET {
        return Err(invalid("datagram exceeds limit"));
    }
    r.read_exact(&mut buf[..n]).await?;
    Ok(n)
}

pub async fn write_packet(w: &mut (impl AsyncWrite + Unpin), payload: &[u8]) -> io::Result<()> {
    if payload.len() > MAX_PACKET {
        return Err(invalid("datagram exceeds limit"));
    }
    w.write_u16(payload.len() as u16).await?;
    w.write_all(payload).await?;
    w.flush().await
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn open_vectors() {
        for flow in [Flow::Tcp, Flow::Udp] {
            for addr in ["127.0.0.1:443", "[::1]:53"] {
                let addr = addr.parse().unwrap();
                let mut bytes = Vec::new();
                write_open(&mut bytes, flow, addr).await.unwrap();
                assert_eq!(read_open(&mut &bytes[..]).await.unwrap(), (flow, addr));
                for i in 0..bytes.len() {
                    assert!(read_open(&mut &bytes[..i]).await.is_err());
                }
            }
        }
    }
    #[tokio::test]
    async fn rejects_invalid_frames() {
        for i in [0, 4, 5, 6] {
            let mut bytes = *b"NXUS\x01\x01\x04\x7f\x00\x00\x01\x00\x50";
            bytes[i] = 0xff;
            assert!(read_open(&mut &bytes[..]).await.is_err());
        }
        assert!(read_packet(&mut &[0xff, 0xff][..], &mut [0; MAX_PACKET])
            .await
            .is_err());
    }
}
