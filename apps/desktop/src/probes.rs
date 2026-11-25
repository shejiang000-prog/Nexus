use std::{
    io::{Read, Write},
    sync::{mpsc, Mutex},
    thread,
    time::{Duration, Instant},
};
use tauri::ipc::{InvokeBody, Request};

static PROBING: Mutex<()> = Mutex::new(());
type Measurements = Vec<(String, Option<u32>)>;

fn run(line: String, v2: bool, deadline: Instant) -> Result<Measurements, &'static str> {
    let _probe = PROBING.try_lock().map_err(|_| "probe_busy")?;
    let _guard = super::updates::REQUESTS
        .try_read()
        .map_err(|_| "update_busy")?;
    let mut child = super::spawn_core()?;
    let mut output = child.stdout.take().unwrap();
    let (send, recv) = mpsc::channel();
    // Drain while the sidecar runs. A full subscription can exceed the pipe buffer.
    thread::spawn(move || {
        let mut bytes = Vec::new();
        let result = output.by_ref().take(128 * 1024 + 1).read_to_end(&mut bytes);
        let _ = send.send(result.map(|_| bytes));
    });
    let reply = writeln!(child.stdin.take().unwrap(), "{line}")
        .ok()
        .and_then(|_| {
            recv.recv_timeout(deadline.saturating_duration_since(Instant::now()))
                .ok()
        })
        .and_then(Result::ok)
        .filter(|bytes| bytes.len() <= 128 * 1024);
    if reply.is_none() {
        let _ = child.kill();
    }
    let status = child.wait().map_err(|_| "probe_failed")?;
    let reply = reply.ok_or("probe_failed")?;
    if !status.success() {
        return Err(super::error_label(
            std::str::from_utf8(&reply).unwrap_or("").trim_end(),
        ));
    }
    let rows: Measurements = serde_json::from_slice(&reply).map_err(|_| "probe_failed")?;
    if rows.len() > 2048
        || (v2 && rows.len() != 1)
        || !rows.iter().all(|(id, ms)| {
            (if v2 {
                id == "v2"
            } else {
                super::subscriptions::valid_id(id)
            }) && ms.is_none_or(|n| n <= 3000)
        })
    {
        return Err("probe_failed");
    }
    Ok(rows)
}

#[tauri::command]
pub(super) async fn probe_nodes(request: Request<'_>) -> Result<Measurements, &'static str> {
    let deadline = Instant::now() + Duration::from_secs(10);
    let InvokeBody::Json(body) = request.body() else {
        return Err("invalid_request");
    };
    let fields = body
        .as_object()
        .filter(|f| f.len() == 1)
        .ok_or("invalid_request")?;
    let v2 = fields.get("v2").and_then(|v| v.as_bool()) == Some(true);
    let line = if v2 {
        r#"{"action":"probe_nodes","v2":true}"#.to_owned()
    } else {
        let id = fields
            .get("subscriptionId")
            .and_then(|v| v.as_str())
            .filter(|id| super::subscriptions::valid_id(id))
            .ok_or("invalid_request")?;
        format!(r#"{{"action":"probe_nodes","subscriptionId":"{id}"}}"#)
    };
    tauri::async_runtime::spawn_blocking(move || run(line, v2, deadline))
        .await
        .map_err(|_| "probe_failed")?
}
