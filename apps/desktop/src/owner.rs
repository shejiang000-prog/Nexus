use serde_json::{json, Value};
use std::{
    io::{BufRead, BufReader, Read, Write},
    process::{Child, ChildStdin},
    sync::{Condvar, Mutex},
    thread,
    time::Duration,
};
use tauri::ipc::{InvokeBody, Request};

const FAILED: &str = "owner_failed";
const INVALID: &str = "invalid_request";
type Reply = Result<Value, &'static str>;

struct Job {
    id: u64,
    phase: &'static str,
    error: Option<&'static str>,
    result: Option<Value>,
    child: Option<Child>,
    input: Option<ChildStdin>,
}

impl Job {
    fn active(&self) -> bool {
        self.id != 0 && !matches!(self.phase, "done" | "failed" | "cancelled")
    }

    fn check_id(&self, id: u64) -> Result<(), &'static str> {
        (id != 0 && self.id == id)
            .then_some(())
            .ok_or("owner_operation_changed")
    }
}

static JOB: Mutex<Job> = Mutex::new(Job {
    id: 0,
    phase: "idle",
    error: None,
    result: None,
    child: None,
    input: None,
});
static FINISHED: Condvar = Condvar::new();

fn request_line(body: &Value) -> Result<(String, Vec<u8>), &'static str> {
    let fields = body.as_object().ok_or(INVALID)?;
    let op = body["op"].as_str().ok_or(INVALID)?;
    let keys: &[&str] = match op {
        "list" => &[],
        "discover" => &["address"],
        "bind" | "deploy" => &["address", "pin", "password"],
        "devices" => &["serverId"],
        "invite" => &["serverId", "days"],
        "renew" => &["serverId", "accountId", "days"],
        "revoke" => &["serverId", "deviceId"],
        _ => return Err(INVALID),
    };
    if fields.len() != keys.len() + 1 {
        return Err(INVALID);
    }
    for &key in keys {
        let v = fields.get(key).ok_or(INVALID)?;
        let s = v.as_str().unwrap_or("");
        let valid = match key {
            "days" => v.is_null() || v.as_u64().is_some_and(|n| (1..=3650).contains(&n)),
            "password" => !s.is_empty() && s.len() <= 4096,
            "address" => !s.is_empty() && s.len() <= 128 && !s.chars().any(char::is_control),
            "pin" => pin(s),
            _ => super::subscriptions::valid_id(s),
        };
        if !valid {
            return Err(if key == "address" {
                "invalid_endpoint"
            } else {
                INVALID
            });
        }
    }
    // Serialize the password once directly into the buffer written to stdin.
    let mut line = serde_json::to_vec(body).map_err(|_| INVALID)?;
    line.pop();
    line.extend_from_slice(b",\"action\":\"owner\"}\n");
    Ok((op.into(), line))
}

fn pin(s: &str) -> bool {
    s.strip_prefix("SHA256:").is_some_and(|b| {
        b.len() == 43
            && b.bytes()
                .all(|c| c.is_ascii_alphanumeric() || b"+/".contains(&c))
    })
}

fn public_error(code: &str) -> &'static str {
    const CODES: &[&str] = &[
        "invalid_endpoint",
        "host_key_changed",
        "ssh_auth",
        "ssh_refused",
        "ssh_timeout",
        "ssh_failed",
        "already_installed",
        "not_installed",
        "unsupported_arch",
        "node_download",
        "node_checksum",
        "install_port_occupied",
        "install_failed",
        "cancelled",
        INVALID,
        "owner_storage",
        "owner_busy",
        "server_not_found",
        "ledger_failed",
        "enrollment_failed",
        "key_missing",
        "identity_busy",
        "invite_rejected",
        "node_unreachable",
    ];
    CODES
        .iter()
        .copied()
        .find(|&item| item == code)
        .unwrap_or(FAILED)
}

fn reply(op: &str, mut output: impl BufRead, mut progress: impl FnMut(&'static str)) -> Reply {
    // Go already limits ledger output to 128 KiB. Allow its JSON projection and framing.
    let mut buffer = String::new();
    loop {
        buffer.clear();
        let n = output
            .by_ref()
            .take(1024 * 1024)
            .read_line(&mut buffer)
            .map_err(|_| FAILED)?;
        if n == 0 || !buffer.ends_with('\n') {
            return Err(FAILED);
        }
        let line = buffer.trim_end_matches(['\r', '\n']);
        match line {
            "progress:connecting" => progress("connecting"),
            "progress:installing" => progress("installing"),
            "progress:binding" => progress("binding"),
            "progress:enrolling" => progress("enrolling"),
            _ => {
                if let Some(code) = line.strip_prefix("error:") {
                    return Err(public_error(code));
                }
                return public_result(op, line.strip_prefix("result:").ok_or(FAILED)?);
            }
        }
    }
}

fn public_result(op: &str, raw: &str) -> Reply {
    let value: Value = serde_json::from_str(raw).map_err(|_| FAILED)?;
    let id = |v: &Value| v.as_str().is_some_and(super::subscriptions::valid_id);
    let valid = match op {
        "list" => value.as_array().is_some_and(|rows| {
            rows.iter().all(|r| {
                exact(r, 2)
                    && id(&r["serverId"])
                    && r["label"].as_str().is_some_and(|s| {
                        s.len() <= 64
                            && s.contains('*')
                            && s.bytes()
                                .all(|c| c.is_ascii_hexdigit() || b".:*[]".contains(&c))
                    })
            })
        }),
        "devices" => value.as_array().is_some_and(|rows| {
            rows.iter().all(|r| {
                exact(r, 6)
                    && id(&r["deviceId"])
                    && id(&r["accountId"])
                    && r["name"].is_string()
                    && r.get("expiresAt")
                        .is_some_and(|e| e.is_null() || e.is_u64())
                    && r["expiryKnown"].is_boolean()
                    && r["revoked"].is_boolean()
            })
        }),
        "discover" => exact(&value, 1) && value["pin"].as_str().is_some_and(pin),
        "deploy" | "bind" => exact(&value, 1) && id(&value["serverId"]),
        "invite" => {
            exact(&value, 1)
                && value["text"].as_str().is_some_and(|s| {
                    s.strip_prefix("复制这条消息后打开 Nexus\nnexus://invite/v2#")
                        .is_some_and(|b| {
                            !b.is_empty()
                                && b.len() <= 16384
                                && b.bytes()
                                    .all(|c| c.is_ascii_alphanumeric() || b"-_".contains(&c))
                        })
                })
        }
        "renew" | "revoke" => value.is_null(),
        _ => false,
    };
    if valid {
        Ok(value)
    } else {
        Err(FAILED)
    }
}

fn exact(v: &Value, n: usize) -> bool {
    v.as_object().is_some_and(|fields| fields.len() == n)
}

fn run(op: &str, mut line: Vec<u8>) -> Reply {
    let _update = super::updates::REQUESTS
        .try_read()
        .map_err(|_| "update_busy")?;
    let mut job = JOB.lock().unwrap();
    if job.phase == "cancelling" {
        return Err("cancelled");
    }
    let mut child = super::spawn_core()?;
    let mut input = child.stdin.take().unwrap();
    let output = child.stdout.take().unwrap();
    job.child = Some(child);
    drop(job);

    let sent = input.write_all(&line);
    line.fill(0);
    drop(line);
    let mut job = JOB.lock().unwrap();
    if job.phase != "cancelling" && sent.is_ok() {
        job.input = Some(input);
    } else {
        drop(input);
    }
    drop(job);
    let outcome = if sent.is_ok() {
        reply(op, BufReader::new(output), |phase| {
            let mut job = JOB.lock().unwrap();
            if job.phase != "cancelling" {
                job.phase = phase;
            }
        })
    } else {
        Err(FAILED)
    };

    let mut job = JOB.lock().unwrap();
    job.input.take();
    let mut child = job.child.take().unwrap();
    drop(job);
    if outcome == Err(FAILED) {
        let _ = child.kill();
    }
    let status = child.wait().map_err(|_| FAILED)?;
    if outcome.is_ok() && !status.success() {
        return Err(FAILED);
    }
    outcome
}

#[tauri::command]
pub(super) fn owner_start(request: Request<'_>) -> Result<u64, &'static str> {
    let InvokeBody::Json(body) = request.body() else {
        return Err(INVALID);
    };
    let mut job = JOB.lock().unwrap();
    if job.active() {
        return Err("owner_busy");
    }
    let (op, line) = request_line(body)?;
    let id = job.id.checked_add(1).ok_or(FAILED)?;
    // A completed result is retained until collected or a new request replaces it.
    job.id = id;
    job.phase = "queued";
    job.error = None;
    job.result = None;
    thread::spawn(move || {
        let outcome = run(&op, line);
        let mut job = JOB.lock().unwrap();
        match outcome {
            Ok(value) => {
                job.phase = "done";
                job.result = Some(value);
            }
            Err(_) if job.phase == "cancelling" => {
                job.phase = "cancelled";
                job.error = Some("cancelled");
            }
            Err(code) => {
                job.phase = if code == "cancelled" {
                    "cancelled"
                } else {
                    "failed"
                };
                job.error = Some(code);
            }
        }
        FINISHED.notify_all();
    });
    Ok(id)
}

fn operation_id(request: Request<'_>) -> Result<u64, &'static str> {
    let InvokeBody::Json(value) = request.body() else {
        return Err(INVALID);
    };
    if !exact(value, 1) {
        return Err(INVALID);
    }
    value["operationId"]
        .as_u64()
        .filter(|id| *id != 0)
        .ok_or(INVALID)
}

#[tauri::command]
pub(super) fn owner_status(request: Request<'_>) -> Reply {
    let operation_id = operation_id(request)?;
    let job = JOB.lock().unwrap();
    job.check_id(operation_id)?;
    Ok(json!({"phase":job.phase,"error":job.error}))
}

#[tauri::command]
pub(super) fn owner_result(request: Request<'_>) -> Reply {
    let operation_id = operation_id(request)?;
    let mut job = JOB.lock().unwrap();
    job.check_id(operation_id)?;
    if let Some(error) = job.error {
        return Err(error);
    }
    if job.active() {
        return Err("owner_pending");
    }
    job.result.take().ok_or("owner_result_taken")
}

#[tauri::command(async)]
pub(super) fn owner_cancel(request: Request<'_>) -> Result<(), &'static str> {
    cancel(operation_id(request)?)
}

fn cancel(operation_id: u64) -> Result<(), &'static str> {
    let mut job = JOB.lock().unwrap();
    job.check_id(operation_id)?;
    if !job.active() {
        return Ok(());
    }
    job.phase = "cancelling";
    job.input.take(); // EOF cancels the SSH context and lets the core persist partial progress.
    let (mut job, _) = FINISHED
        .wait_timeout_while(job, Duration::from_secs(2), |j| {
            j.id == operation_id && j.active()
        })
        .unwrap();
    if job.id == operation_id && job.active() {
        if let Some(child) = &mut job.child {
            let _ = child.kill();
        }
    }
    Ok(())
}

pub(super) fn shutdown() {
    let id = JOB.lock().unwrap().id;
    if id != 0 {
        let _ = cancel(id);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn owner_pipe_contract() {
        let input = json!({"op":"deploy","address":"127.0.0.1:2222","pin":format!("SHA256:{}", "A".repeat(43)), "password":"synthetic\nroot"});
        let (op, line) = request_line(&input).unwrap();
        assert_eq!(line.iter().filter(|&&b| b == b'\n').count(), 1);
        assert_eq!(
            serde_json::from_slice::<Value>(&line).unwrap()["action"],
            "owner"
        );
        assert!(request_line(&json!({"op":"list","password":"extra"})).is_err());
        let wire = format!(
            "progress:connecting\nresult:{{\"serverId\":\"{}\"}}\n",
            "a".repeat(32)
        );
        let mut seen = vec![];
        assert!(reply(&op, wire.as_bytes(), |s| seen.push(s)).is_ok());
        assert_eq!(seen, ["connecting"]);
        assert_eq!(
            reply(&op, b"error:private detail\n".as_slice(), |_| {}),
            Err(FAILED)
        );
        assert_eq!(
            reply(
                "renew",
                b"result:{\"password\":\"private\"}\n".as_slice(),
                |_| {}
            ),
            Err(FAILED)
        );
    }
}
