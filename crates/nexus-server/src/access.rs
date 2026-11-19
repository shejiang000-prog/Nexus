use rusqlite::{
    params, Connection, OpenFlags, OptionalExtension, Transaction, TransactionBehavior,
};
use std::{
    io,
    path::Path,
    sync::{Arc, Mutex, MutexGuard},
    time::Duration,
};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

/// Bytes reserved from a device quota per database write. Relays spend the local lease and
/// touch SQLite about once per LEASE bytes instead of once per read or datagram. A lease never
/// exceeds the remaining quota, so concurrent sessions cannot overspend it.
const LEASE: u64 = 1024 * 1024;

fn denied() -> io::Error {
    io::ErrorKind::PermissionDenied.into()
}
fn unavailable() -> io::Error {
    io::Error::other("registry unavailable")
}

#[derive(Clone)]
pub struct Registry(Arc<Mutex<Connection>>);

impl Registry {
    pub fn open(path: &Path) -> io::Result<Self> {
        let conn = Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_WRITE)
            .map_err(io::Error::other)?;
        conn.busy_timeout(Duration::from_millis(500))
            .map_err(io::Error::other)?;
        // The owner tool creates the database in WAL mode. NORMAL skips the fsync per commit;
        // a power loss can only forget the last usage increments, never authorization changes
        // made by the owner tool.
        conn.execute_batch("PRAGMA synchronous=NORMAL")
            .map_err(io::Error::other)?;
        conn.prepare("SELECT cert, expires, quota, used, revoked FROM devices LIMIT 0")
            .map_err(io::Error::other)?;
        Ok(Self(Arc::new(Mutex::new(conn))))
    }
    fn lock(&self) -> io::Result<MutexGuard<'_, Connection>> {
        self.0.lock().map_err(|_| unavailable())
    }
    pub fn device(&self, cert: &[u8]) -> Device {
        Device {
            lease: Arc::new(Lease {
                registry: self.clone(),
                cert: Arc::from(cert),
                available: Mutex::new(0),
            }),
        }
    }
}

struct Lease {
    registry: Registry,
    cert: Arc<[u8]>,
    /// Bytes already counted in the database but not yet relayed.
    available: Mutex<u64>,
}

impl Lease {
    fn authorized(&self) -> io::Result<bool> {
        let held = self.available.lock().map_err(|_| unavailable())?;
        self.registry
            .lock()?
            .query_row(
                "SELECT EXISTS(SELECT 1 FROM devices WHERE cert=?1 AND revoked=0 \
                 AND expires>unixepoch() AND (quota IS NULL OR used-?2<quota))",
                params![&*self.cert, *held as i64],
                |r| r.get(0),
            )
            .map_err(io::Error::other)
    }
    /// Counts at least `need` and at most `want` bytes, bounded by the remaining quota.
    fn reserve(&self, need: u64, want: u64) -> io::Result<u64> {
        let conn = self.registry.lock()?;
        let tx = Transaction::new_unchecked(&conn, TransactionBehavior::Immediate)
            .map_err(io::Error::other)?;
        let row = tx
            .query_row(
                "SELECT quota, used FROM devices \
                 WHERE cert=?1 AND revoked=0 AND expires>unixepoch()",
                [&*self.cert],
                |r| Ok((r.get::<_, Option<i64>>(0)?, r.get::<_, i64>(1)?)),
            )
            .optional()
            .map_err(io::Error::other)?;
        let Some((quota, used)) = row else {
            return Err(denied());
        };
        let room = quota.map(|quota| u64::try_from(quota.saturating_sub(used)).unwrap_or(0));
        if room.is_some_and(|room| room < need) {
            return Err(denied());
        }
        let grant = room.map_or(want, |room| room.min(want));
        tx.execute(
            "UPDATE devices SET used=used+?2 WHERE cert=?1",
            params![&*self.cert, grant as i64],
        )
        .map_err(io::Error::other)?;
        tx.commit().map_err(io::Error::other)?;
        Ok(grant)
    }
    fn refund(&self, bytes: u64) -> io::Result<()> {
        self.registry
            .lock()?
            .execute(
                "UPDATE devices SET used=MAX(used-?2,0) WHERE cert=?1",
                params![&*self.cert, bytes as i64],
            )
            .map_err(io::Error::other)?;
        Ok(())
    }
}

impl Drop for Lease {
    // A session cut off by its lifetime limit skips `settle`; return the lease anyway.
    fn drop(&mut self) {
        let unused = self.available.get_mut().map(std::mem::take).unwrap_or(0);
        if unused == 0 {
            return;
        }
        let refund = Lease {
            registry: self.registry.clone(),
            cert: self.cert.clone(),
            available: Mutex::new(0),
        };
        match tokio::runtime::Handle::try_current() {
            Ok(handle) => {
                handle.spawn_blocking(move || refund.refund(unused));
            }
            Err(_) => {
                let _ = refund.refund(unused);
            }
        }
    }
}

#[derive(Clone)]
pub struct Device {
    lease: Arc<Lease>,
}

impl Device {
    /// `bytes == 0` only checks that the device is still authorized and has quota left.
    pub async fn charge(&self, bytes: usize) -> io::Result<()> {
        let lease = self.lease.clone();
        if bytes == 0 {
            let allowed = tokio::task::spawn_blocking(move || lease.authorized())
                .await
                .map_err(io::Error::other)??;
            return if allowed { Ok(()) } else { Err(denied()) };
        }
        let bytes = bytes as u64;
        loop {
            if let Ok(mut available) = lease.available.try_lock() {
                if *available >= bytes {
                    *available -= bytes;
                    return Ok(());
                }
            }
            // A cancelled relay can abandon this database task. Reserve into the shared
            // refundable balance here; only the live caller above can spend that balance.
            let refill = lease.clone();
            tokio::task::spawn_blocking(move || {
                let mut available = refill.available.lock().map_err(|_| unavailable())?;
                if *available < bytes {
                    let need = bytes - *available;
                    *available += refill.reserve(need, need.max(LEASE))?;
                }
                Ok::<_, io::Error>(())
            })
            .await
            .map_err(io::Error::other)??;
        }
    }
    /// Returns the unused part of the lease to the device quota.
    pub async fn settle(&self) {
        let unused = self
            .lease
            .available
            .lock()
            .map(|mut available| std::mem::take(&mut *available))
            .unwrap_or(0);
        if unused > 0 {
            let lease = self.lease.clone();
            let _ = tokio::task::spawn_blocking(move || lease.refund(unused)).await;
        }
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
        let mut buf = vec![0u8; 64 * 1024];
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

#[cfg(test)]
mod tests {
    use super::*;

    struct Db(std::path::PathBuf);
    impl Drop for Db {
        fn drop(&mut self) {
            for suffix in ["", "-wal", "-shm"] {
                let _ = std::fs::remove_file(format!("{}{suffix}", self.0.display()));
            }
        }
    }

    fn registry(quota: Option<i64>) -> (Db, Registry) {
        static NEXT: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
        let path = std::env::temp_dir().join(format!(
            "nexus-access-{}-{}.db",
            std::process::id(),
            NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        ));
        let conn = Connection::open(&path).unwrap();
        conn.execute_batch(
            "PRAGMA journal_mode=WAL; CREATE TABLE devices(cert BLOB PRIMARY KEY, \
             expires INTEGER NOT NULL, quota INTEGER, used INTEGER NOT NULL DEFAULT 0, \
             revoked INTEGER NOT NULL DEFAULT 0);",
        )
        .unwrap();
        conn.execute(
            "INSERT INTO devices(cert,expires,quota) VALUES (x'01',unixepoch()+600,?1)",
            [quota],
        )
        .unwrap();
        let registry = Registry::open(&path).unwrap();
        (Db(path), registry)
    }

    fn used(registry: &Registry) -> i64 {
        registry
            .lock()
            .unwrap()
            .query_row("SELECT used FROM devices", [], |r| r.get(0))
            .unwrap()
    }

    #[tokio::test]
    async fn exact_quota_is_spent_and_then_denied() {
        let (_db, registry) = registry(Some(128));
        let device = registry.device(&[1]);
        device.charge(0).await.unwrap();
        device.charge(64).await.unwrap();
        device.charge(64).await.unwrap();
        assert!(device.charge(1).await.is_err());
        device.settle().await;
        assert_eq!(used(&registry), 128);
        assert!(registry.device(&[1]).charge(0).await.is_err());
    }

    #[tokio::test]
    async fn a_read_larger_than_the_remaining_quota_is_refused_whole() {
        let (_db, registry) = registry(Some(100));
        let device = registry.device(&[1]);
        assert!(device.charge(101).await.is_err());
        device.charge(100).await.unwrap();
        device.settle().await;
        assert_eq!(used(&registry), 100);
    }

    #[tokio::test]
    async fn unused_lease_is_refunded_on_settle_and_drop() {
        let (_db, registry) = registry(None);
        let device = registry.device(&[1]);
        device.charge(10).await.unwrap();
        assert_eq!(used(&registry), LEASE as i64);
        device.settle().await;
        assert_eq!(used(&registry), 10);
        let dropped = registry.device(&[1]);
        dropped.charge(5).await.unwrap();
        drop(dropped);
        for _ in 0..100 {
            if used(&registry) == 15 {
                return;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        panic!("lease was not refunded on drop: {}", used(&registry));
    }

    #[tokio::test]
    async fn shared_associations_charge_once_and_refund_on_close() {
        let (_db, registry) = registry(Some(8192));
        let d = registry.device(&[1]);
        let mut tasks = tokio::task::JoinSet::new();
        for _ in 0..64 {
            let d = d.clone();
            tasks.spawn(async move {
                d.charge(21).await.unwrap();
                d.charge(21).await.unwrap();
            });
        }
        while let Some(r) = tasks.join_next().await {
            r.unwrap();
        }
        d.charge(0).await.unwrap();
        d.settle().await;
        assert_eq!(used(&registry), 64 * 42);
    }

    #[tokio::test]
    async fn concurrent_sessions_cannot_overspend() {
        let quota = 3 * LEASE as i64 / 2;
        let (_db, registry) = registry(Some(quota));
        let a = registry.device(&[1]);
        let b = registry.device(&[1]);
        let mut spent = 0;
        for _ in 0..2048 {
            for device in [&a, &b] {
                if device.charge(1024).await.is_ok() {
                    spent += 1024;
                }
            }
        }
        assert_eq!(spent, quota);
        a.settle().await;
        b.settle().await;
        assert_eq!(used(&registry), quota);
    }

    #[tokio::test]
    async fn revocation_and_expiry_deny_new_leases() {
        let (_db, registry) = registry(None);
        let device = registry.device(&[1]);
        device.charge(1).await.unwrap();
        registry
            .lock()
            .unwrap()
            .execute("UPDATE devices SET revoked=1", [])
            .unwrap();
        assert!(device.charge(0).await.is_err());
        assert!(device.charge(LEASE as usize).await.is_err());
        device.settle().await;
        assert_eq!(used(&registry), 1);
    }
}
