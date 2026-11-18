use rusqlite::{params, Connection, OpenFlags};
use std::{
    io,
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

#[derive(Clone)]
pub struct Registry(Arc<Mutex<Connection>>);

impl Registry {
    pub fn open(path: &Path) -> io::Result<Self> {
        let conn = Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_WRITE)
            .map_err(io::Error::other)?;
        conn.busy_timeout(Duration::from_millis(500))
            .map_err(io::Error::other)?;
        conn.prepare("SELECT cert, expires, quota, used, revoked FROM devices LIMIT 0")
            .map_err(io::Error::other)?;
        Ok(Self(Arc::new(Mutex::new(conn))))
    }
    pub fn device(&self, cert: &[u8]) -> Device {
        Device {
            registry: self.clone(),
            cert: Arc::from(cert),
        }
    }
}

#[derive(Clone)]
pub struct Device {
    registry: Registry,
    cert: Arc<[u8]>,
}

impl Device {
    pub async fn charge(&self, bytes: usize) -> io::Result<()> {
        let device = self.clone();
        tokio::task::spawn_blocking(move || {
            let conn = device.registry.0.lock().map_err(|_| io::Error::other("registry unavailable"))?;
            let cert: &[u8] = &device.cert;
            let allowed = if bytes == 0 {
                conn.query_row("SELECT EXISTS(SELECT 1 FROM devices WHERE cert=?1 AND revoked=0 AND expires>unixepoch() AND (quota IS NULL OR used<quota))",
                    [cert], |r| r.get(0)).map_err(io::Error::other)?
            } else {
                conn.execute("UPDATE devices SET used=used+?2 WHERE cert=?1 AND revoked=0 AND expires>unixepoch() AND (quota IS NULL OR ?2<=quota-used)",
                    params![cert, bytes as i64]).map_err(io::Error::other)? == 1
            };
            if allowed { Ok(()) } else { Err(io::ErrorKind::PermissionDenied.into()) }
        }).await.map_err(io::Error::other)?
    }
    pub async fn until_revoked(&self) -> io::Result<()> {
        loop {
            tokio::time::sleep(Duration::from_secs(2)).await;
            self.charge(0).await?;
        }
    }
    pub async fn copy(
        &self,
        mut source: impl AsyncRead + Unpin,
        mut dest: impl AsyncWrite + Unpin,
    ) -> io::Result<()> {
        let mut buf = [0u8; 16384];
        loop {
            let n = source.read(&mut buf).await?;
            if n == 0 {
                return dest.shutdown().await;
            }
            self.charge(n).await?;
            dest.write_all(&buf[..n]).await?;
        }
    }
}
