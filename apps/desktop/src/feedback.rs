use std::io::{Read, Write};
use tauri::ipc::{InvokeBody, Request};

const INVALID: &str = "invalid_request";

fn text(value: &serde_json::Value, max: usize, multiline: bool) -> Option<String> {
    let s = value.as_str().unwrap_or("").trim();
    (s.chars().count() <= max
        && !s
            .chars()
            .any(|c| c.is_control() && !(multiline && matches!(c, '\n' | '\r' | '\t'))))
    .then(|| s.to_owned())
}

fn send(line: String) -> Result<(), &'static str> {
    let _guard = super::updates::REQUESTS
        .try_read()
        .map_err(|_| "update_busy")?;
    let mut child = super::spawn_core()?;
    let mut reply = String::new();
    let io = writeln!(child.stdin.take().unwrap(), "{line}").and_then(|()| {
        child
            .stdout
            .take()
            .unwrap()
            .take(4096)
            .read_to_string(&mut reply)
    });
    let status = child.wait().map_err(|_| "feedback_failed")?;
    match reply.trim_end() {
        "sent" if io.is_ok() && status.success() => Ok(()),
        "error:feedback_busy" => Err("feedback_busy"),
        _ => Err("feedback_failed"),
    }
}

/// Sends what the user typed, with the app version, to the feedback inbox.
#[tauri::command]
pub(super) async fn send_feedback(request: Request<'_>) -> Result<(), &'static str> {
    let InvokeBody::Json(body) = request.body() else {
        return Err(INVALID);
    };
    let message = text(&body["body"], 2000, true)
        .filter(|s| !s.is_empty())
        .ok_or(INVALID)?;
    let contact = text(&body["contact"], 100, false).ok_or(INVALID)?;
    let line = serde_json::json!({
        "action": "feedback",
        "version": env!("CARGO_PKG_VERSION"),
        "contact": contact,
        "body": message,
    })
    .to_string();
    tauri::async_runtime::spawn_blocking(move || send(line))
        .await
        .map_err(|_| "feedback_failed")?
}
