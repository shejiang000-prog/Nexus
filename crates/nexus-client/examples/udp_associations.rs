//! Test and benchmark driver. Build this same file against both revisions.
use nexus_client::open;
use nexus_protocol::{Flow, MAX_PACKET};
use nexus_transport::{Connector, Mode};
use std::{
    env, io,
    sync::Arc,
    time::{Duration, Instant},
};
use tokio::{sync::Barrier, task::JoinSet, time::timeout};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = env::args().skip(1).collect();
    let [server, target, flows, rounds, mode, extra @ ..] = args.as_slice() else {
        return Err("SERVER TARGET FLOWS ROUNDS check|bench|revoke".into());
    };
    let flows: usize = flows.parse()?;
    let rounds: usize = rounds.parse()?;
    let target = target.parse()?;
    let connector = Arc::new(Connector::new(
        server.parse()?,
        "nexus.test",
        "ca.der",
        "device.der",
        "device.key.der",
    )?);
    let barrier = Arc::new(Barrier::new(flows));
    let mut tasks = JoinSet::new();
    if mode == "revoke" {
        use tokio::io::AsyncReadExt;
        let tcp = extra
            .first()
            .ok_or("missing TCP target for revoke test")?
            .parse()?;
        let mut stream = open(&connector, &Mode::Quic, Flow::Tcp, tcp).await?.stream;
        tasks.spawn(async move {
            println!("ACTIVE tcp");
            if timeout(Duration::from_secs(12), stream.read_u8())
                .await?
                .is_ok()
            {
                return Err(io::Error::other("expected closed TCP stream"));
            }
            Ok(())
        });
    }
    for id in 0..flows {
        let connector = connector.clone();
        let barrier = barrier.clone();
        let mode = mode.clone();
        tasks.spawn(async move {
            barrier.wait().await;
            let start = Instant::now();
            let channel = open(&connector, &Mode::Quic, Flow::Udp, target).await?;
            let (mut rx, mut tx) = channel.packets();
            if mode != "bench" {
                timeout(Duration::from_secs(10), barrier.wait()).await?;
            }
            for seq in 0..rounds {
                let size = if mode == "check" && seq == 1 {
                    MAX_PACKET
                } else if mode == "check" && seq == 2 {
                    0
                } else {
                    64
                };
                let mut data = vec![id as u8; size];
                if size > 0 {
                    data[0] = seq as u8;
                }
                let sent = Instant::now();
                tx.send(&data).await?;
                let reply = timeout(Duration::from_secs(2), rx.recv()).await;
                let ms = sent.elapsed().as_secs_f64() * 1000.;
                let elapsed = start.elapsed().as_secs_f64() * 1000.;
                let outcome = match reply {
                    Ok(Ok(actual)) if actual == data => "ok",
                    Err(_) if mode == "bench" => "loss",
                    _ => return Err(io::Error::other("incorrect or missing echo")),
                };
                println!("{id},{seq},{ms:.3},{elapsed:.3},{outcome}");
                if mode == "check" && seq == 2 {
                    barrier.wait().await;
                    if id == 0 {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(50)).await;
                }
            }
            if mode == "revoke" {
                println!("ACTIVE {id}");
                if timeout(Duration::from_secs(8), rx.recv()).await?.is_ok() {
                    return Err(io::Error::other("expected closed association"));
                }
            }
            Ok::<_, io::Error>(())
        });
    }
    while let Some(done) = tasks.join_next().await {
        done??;
    }
    Ok(())
}
