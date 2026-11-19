use nexus_protocol::MAX_PACKET;
use std::{
    collections::HashMap,
    io,
    sync::{Arc, Mutex},
};
use tokio::sync::{mpsc, OwnedSemaphorePermit, Semaphore};

/// Datagrams one association may queue. A brief consumer stall (an Android TUN write, a quota
/// refill) must not turn into loss for bulk UDP such as HTTP/3; legacy udp1 had quinn's whole
/// receive buffer for one association.
const QUEUE: usize = 512;
/// Payload bytes queued across all associations of one connection, released as they are read.
const BUDGET: usize = 1024 * 1024;

/// A routed payload holds its share of the connection budget until the reader takes it.
pub(super) type Queued = (Vec<u8>, OwnedSemaphorePermit);
type Routes = Mutex<HashMap<u32, mpsc::Sender<Queued>>>;

pub(super) fn encode(id: u32, payload: &[u8]) -> io::Result<Vec<u8>> {
    if !(1..=MAX_PACKET).contains(&payload.len()) {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    let mut packet = Vec::with_capacity(4 + payload.len());
    packet.extend_from_slice(&id.to_be_bytes());
    packet.extend_from_slice(payload);
    Ok(packet)
}

fn dispatch(routes: &Routes, budget: &Arc<Semaphore>, data: &[u8]) {
    if !(5..=4 + MAX_PACKET).contains(&data.len()) {
        return;
    }
    let id = u32::from_be_bytes(data[..4].try_into().unwrap());
    let Some(tx) = routes.lock().unwrap().get(&id).cloned() else {
        return;
    };
    // Over budget or a full queue drops this datagram only; the connection reader never waits.
    if let Ok(permit) = budget
        .clone()
        .try_acquire_many_owned((data.len() - 4) as u32)
    {
        let _ = tx.try_send((data[4..].to_vec(), permit));
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
        let budget = Arc::new(Semaphore::new(BUDGET));
        let reader = tokio::spawn(async move {
            while let Ok(data) = conn.read_datagram().await {
                dispatch(&table, &budget, &data);
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
        let (tx, rx) = mpsc::channel(QUEUE);
        table.insert(id, tx);
        Ok(Association {
            router: self.clone(),
            id,
            queue: Some(rx),
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
    pub queue: Option<mpsc::Receiver<Queued>>,
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
        let budget = Arc::new(Semaphore::new(BUDGET));
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
            dispatch(&routes, &budget, bad);
        }
        assert!(rx.try_recv().is_err());
        let frame = encode(0x01020304, &[7; MAX_PACKET]).unwrap();
        assert_eq!(&frame[..4], &[1, 2, 3, 4]);
        dispatch(&routes, &budget, &frame);
        assert_eq!(rx.try_recv().unwrap().0, vec![7; MAX_PACKET]);
        routes.lock().unwrap().clear();
        dispatch(&routes, &budget, &frame);
        assert!(rx.try_recv().is_err());
    }

    #[test]
    fn budget_bounds_queued_bytes_and_is_released_by_the_reader() {
        let routes = Routes::default();
        let budget = Arc::new(Semaphore::new(3 * MAX_PACKET));
        let (tx, mut rx) = mpsc::channel(QUEUE);
        routes.lock().unwrap().insert(9, tx);
        let frame = encode(9, &[1; MAX_PACKET]).unwrap();
        for _ in 0..5 {
            dispatch(&routes, &budget, &frame);
        }
        let held: Vec<_> = std::iter::from_fn(|| rx.try_recv().ok()).collect();
        assert_eq!(held.len(), 3);
        dispatch(&routes, &budget, &frame);
        assert!(rx.try_recv().is_err());
        drop(held);
        dispatch(&routes, &budget, &frame);
        assert_eq!(rx.try_recv().unwrap().0.len(), MAX_PACKET);
    }
}
