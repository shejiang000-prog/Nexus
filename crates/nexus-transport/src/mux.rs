use nexus_protocol::MAX_PACKET;
use std::{
    collections::HashMap,
    io,
    sync::{Arc, Mutex},
};
use tokio::sync::mpsc;

type Sender = mpsc::Sender<io::Result<Vec<u8>>>;
type Routes = Mutex<HashMap<u32, Sender>>;

pub(super) fn encode(id: u32, payload: &[u8]) -> io::Result<Vec<u8>> {
    if !(1..=MAX_PACKET).contains(&payload.len()) {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    let mut packet = Vec::with_capacity(4 + payload.len());
    packet.extend_from_slice(&id.to_be_bytes());
    packet.extend_from_slice(payload);
    Ok(packet)
}

fn dispatch(routes: &Routes, data: &[u8]) {
    if !(5..=4 + MAX_PACKET).contains(&data.len()) {
        return;
    }
    let id = u32::from_be_bytes(data[..4].try_into().unwrap());
    if let Some(tx) = routes.lock().unwrap().get(&id) {
        // A slow association may lose datagrams, but cannot block the connection reader.
        let _ = tx.try_send(Ok(data[4..].to_vec()));
    }
}

pub(super) struct Router {
    routes: Arc<Routes>,
    reader: tokio::task::AbortHandle,
}
impl Router {
    pub fn new(conn: quinn::Connection) -> Arc<Self> {
        let routes = Arc::new(Routes::default());
        let table = routes.clone();
        let reader = tokio::spawn(async move {
            while let Ok(data) = conn.read_datagram().await {
                dispatch(&table, &data);
            }
        })
        .abort_handle();
        Arc::new(Self { routes, reader })
    }
    pub fn register(self: &Arc<Self>, id: u32) -> io::Result<Association> {
        let mut table = self.routes.lock().unwrap();
        if table.len() >= 256 || table.contains_key(&id) {
            return Err(io::ErrorKind::WouldBlock.into());
        }
        let (tx, rx) = mpsc::channel(16);
        table.insert(id, tx.clone());
        Ok(Association {
            router: self.clone(),
            id,
            queue: Some((tx, rx)),
        })
    }
}
impl Drop for Router {
    fn drop(&mut self) {
        self.reader.abort();
    }
}
pub(super) struct Association {
    router: Arc<Router>,
    pub id: u32,
    pub queue: Option<(Sender, mpsc::Receiver<io::Result<Vec<u8>>>)>,
}
impl Drop for Association {
    fn drop(&mut self) {
        self.router.routes.lock().unwrap().remove(&self.id);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn wire_bounds_unknown_and_closed_ids() {
        let routes = Routes::default();
        let (tx, mut rx) = mpsc::channel(1);
        routes.lock().unwrap().insert(0x01020304, tx);
        assert!(encode(1, &[]).is_err());
        assert!(encode(1, &[0; MAX_PACKET + 1]).is_err());
        for bad in [
            &[][..],
            &[1, 2, 3],
            &[1, 2, 3, 4],
            &[1, 2, 3, 5, 9],
            &[0; MAX_PACKET + 5],
        ] {
            dispatch(&routes, bad);
        }
        assert!(rx.try_recv().is_err());
        let frame = encode(0x01020304, &[7; MAX_PACKET]).unwrap();
        assert_eq!(&frame[..4], &[1, 2, 3, 4]);
        dispatch(&routes, &frame);
        assert_eq!(rx.try_recv().unwrap().unwrap(), vec![7; MAX_PACKET]);
        routes.lock().unwrap().clear();
        dispatch(&routes, &frame);
        assert!(rx.try_recv().is_err());
    }
}
