use std::io::{Read, Write};
use tauri::ipc::{InvokeBody, InvokeResponseBody, Request};

const INVALID: &str = "subscription_format";
const STORAGE: &str = "subscription_storage";
const LIMIT: usize = 64 * 1024 * 1024;

pub(super) fn valid_id(id: &str) -> bool {
    id.len() == 32
        && id
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

type Node = (String, String, String);
type Subscription = (String, String, bool, Option<i64>, Option<i64>, Vec<Node>);

fn public_label(label: &str) -> bool {
    !label.is_empty()
        && label.len() <= 512
        && !label.contains("://")
        && !label.chars().any(char::is_control)
}

// Project the public DTO onto a fixed response. Extra sidecar fields never reach the WebView.
fn public_subscriptions(body: InvokeBody) -> Result<Vec<Subscription>, &'static str> {
    let InvokeBody::Json(value) = body else {
        return Err(STORAGE);
    };
    let rows = value["subscriptions"]
        .as_array()
        .filter(|a| a.len() <= 32)
        .ok_or(STORAGE)?;
    let mut result = Vec::with_capacity(rows.len());
    for row in rows {
        let id = row["id"].as_str().filter(|v| valid_id(v)).ok_or(STORAGE)?;
        let name = row["name"]
            .as_str()
            .filter(|v| public_label(v))
            .ok_or(STORAGE)?;
        let refreshable = row["refreshable"].as_bool().ok_or(STORAGE)?;
        let items = row["nodes"]
            .as_array()
            .filter(|a| a.len() <= 2048)
            .ok_or(STORAGE)?;
        let mut nodes = Vec::with_capacity(items.len());
        for node in items {
            let key = node["id"].as_str().filter(|v| valid_id(v)).ok_or(STORAGE)?;
            let label = node["name"]
                .as_str()
                .filter(|v| public_label(v))
                .ok_or(STORAGE)?;
            let protocol = node["protocol"]
                .as_str()
                .filter(|s| {
                    !s.is_empty()
                        && s.len() <= 32
                        && s.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
                })
                .ok_or(STORAGE)?;
            nodes.push((key.to_owned(), label.to_owned(), protocol.to_owned()));
        }
        result.push((
            id.to_owned(),
            name.to_owned(),
            refreshable,
            row["remaining"].as_i64(),
            row["expire"].as_i64(),
            nodes,
        ));
    }
    Ok(result)
}

fn request_core(line: String) -> Result<Vec<Subscription>, &'static str> {
    let mut child = super::spawn_core()?;
    let mut response = String::new();
    let read = writeln!(child.stdin.take().unwrap(), "{line}").and_then(|()| {
        child
            .stdout
            .take()
            .unwrap()
            .take((LIMIT + 1) as u64)
            .read_to_string(&mut response)
    });
    if read.is_err() || response.len() > LIMIT {
        let _ = child.kill();
        let _ = child.wait();
        return Err(STORAGE);
    }
    if !child.wait().map_err(|_| STORAGE)?.success() {
        return Err(super::error_label(response.trim_end()));
    }
    let body = InvokeResponseBody::Json(response)
        .deserialize()
        .map_err(|_| STORAGE)?;
    public_subscriptions(InvokeBody::Json(body))
}

#[tauri::command]
pub(super) async fn subscriptions(request: Request<'_>) -> Result<Vec<Subscription>, &'static str> {
    let InvokeBody::Json(body) = request.body() else {
        return Err(INVALID);
    };
    let line = match body["action"].as_str() {
        Some("subscriptions") => r#"{"action":"subscriptions"}"#.to_owned(),
        Some("import_subscription") => {
            let url = body
                .get("url")
                .filter(|v| {
                    v.as_str().is_some_and(|s| {
                        !s.is_empty() && s.len() <= 512 * 1024 && !s.chars().any(char::is_control)
                    })
                })
                .ok_or(INVALID)?;
            format!(r#"{{"action":"import_subscription","url":{url}}}"#)
        }
        Some(action @ ("update_subscription" | "remove_subscription")) => {
            let id = body["id"]
                .as_str()
                .filter(|id| valid_id(id))
                .ok_or("subscription_changed")?;
            format!(r#"{{"action":"{action}","id":"{id}"}}"#)
        }
        _ => return Err(INVALID),
    };
    tauri::async_runtime::spawn_blocking(move || request_core(line))
        .await
        .map_err(|_| STORAGE)?
}
