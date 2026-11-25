#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod system_proxy;

use std::{
    io::{BufRead, BufReader, Read, Write},
    os::windows::process::CommandExt,
    process::{Child, ChildStdin, Command, Stdio},
    sync::Mutex,
    thread,
};
use tauri::ipc::{InvokeBody, Request};

struct Core {
    status: &'static str,
    input: Option<ChildStdin>,
    socks: u16,
    http: u16,
    proxy: bool,
    notice: &'static str,
}

static CORE: Mutex<Core> = Mutex::new(Core {
    status: "stopped",
    input: None,
    socks: 0,
    http: 0,
    proxy: false,
    notice: "",
});

fn spawn_core() -> Result<Child, &'static str> {
    let exe = std::env::current_exe()
        .map_err(|_| "generic")?
        .with_file_name("nexus-core.exe");
    Command::new(&exe)
        .current_dir(exe.parent().ok_or("generic")?)
        .creation_flags(0x08000000)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| {
            if e.kind() == std::io::ErrorKind::NotFound {
                "missing"
            } else {
                "generic"
            }
        })
}

fn error_label(line: &str) -> &'static str {
    match line {
        "error:invite_invalid" => "invite_invalid",
        "error:invite_rejected" => "invite_rejected",
        "error:node_unreachable" => "node_unreachable",
        "error:key_missing" => "key_missing",
        "error:identity_changed" => "identity_changed",
        "error:identity_busy" => "identity_busy",
        "error:local_port" => "local_port",
        "error:no_identity" => "no_identity",
        "error:routing" => "routing",
        _ => "generic",
    }
}

fn ready(line: &str) -> Option<(&'static str, u16, u16)> {
    let mut parts = line.split(':');
    if parts.next()? != "ready" {
        return None;
    }
    let lane = match parts.next()? {
        "h3" => "h3",
        "h2" => "h2",
        _ => return None,
    };
    let socks = parts.next()?.parse().ok()?;
    let http = parts.next()?.parse().ok()?;
    (socks != 0 && http != 0 && socks != http && parts.next().is_none())
        .then_some((lane, socks, http))
}

fn restore(core: &mut Core) {
    if let Err(code) = system_proxy::restore() {
        core.notice = code;
    }
}

fn monitor(mut child: Child) {
    let mut output = BufReader::new(child.stdout.take().unwrap());
    let mut terminal = "generic";
    let mut line = String::new();
    loop {
        line.clear();
        if !matches!((&mut output).take(128).read_line(&mut line), Ok(1..)) {
            break;
        }
        let line = line.trim_end_matches(['\r', '\n']);
        if line == "connecting" {
            continue;
        }
        let Some((lane, socks, http)) = ready(line) else {
            terminal = error_label(line);
            if !line.starts_with("error:") {
                let _ = child.kill();
            }
            break;
        };
        let mut core = CORE.lock().unwrap();
        if core.input.is_none() {
            continue;
        }
        if core.http == 0 && core.proxy {
            if let Err(code) = system_proxy::enable(http) {
                core.notice = code;
                core.proxy = false;
            }
        }
        core.status = lane;
        core.socks = socks;
        core.http = http;
    }
    let _ = child.wait();
    let mut core = CORE.lock().unwrap();
    restore(&mut core);
    core.status = if core.status == "stopping" {
        "stopped"
    } else {
        terminal
    };
    core.input = None;
    core.http = 0;
    core.socks = 0;
}

#[tauri::command]
fn start_core(request: Request<'_>) -> Result<(), &'static str> {
    let InvokeBody::Json(body) = request.body() else {
        return Err("invite_invalid");
    };
    let invite = body
        .get("invite")
        .filter(|v| {
            v.as_str()
                .is_some_and(|s| s.len() <= 16384 && !s.contains(['\r', '\n']))
        })
        .ok_or("invite_invalid")?;
    let mode = match body.get("mode").and_then(|v| v.as_str()) {
        Some("split") => "split",
        Some("global") => "global",
        _ => return Err("routing"),
    };
    let proxy = body
        .get("proxy")
        .and_then(|v| v.as_bool())
        .ok_or("generic")?;
    let mut core = CORE.lock().unwrap();
    if core.input.is_some() || core.status == "stopping" {
        return Err("busy");
    }
    let mut child = spawn_core()?;
    let mut input = child.stdin.take().unwrap();
    if writeln!(input, "{{\"invite\":{invite},\"mode\":\"{mode}\"}}").is_err() {
        drop(input);
        let _ = child.kill();
        let _ = child.wait();
        return Err("generic");
    }
    core.status = "connecting";
    core.proxy = proxy;
    core.input = Some(input);
    thread::spawn(move || monitor(child));
    Ok(())
}

#[tauri::command]
fn stop_core() {
    let mut core = CORE.lock().unwrap();
    restore(&mut core);
    if core.input.take().is_some() {
        core.status = "stopping";
    }
}

#[tauri::command]
fn core_status() -> (&'static str, u16, u16, bool, &'static str) {
    let core = CORE.lock().unwrap();
    (core.status, core.socks, core.http, core.proxy, core.notice)
}

#[tauri::command]
fn set_proxy(enabled: bool) -> Result<(), &'static str> {
    let mut core = CORE.lock().unwrap();
    let changed = if !enabled {
        system_proxy::restore()
    } else if core.http != 0 && core.input.is_some() {
        system_proxy::enable(core.http)
    } else {
        Ok(())
    };
    core.notice = changed.err().unwrap_or("");
    if changed.is_ok() || !enabled {
        core.proxy = enabled
    }
    changed
}

#[tauri::command]
fn device_identity(
    remove: bool,
    expected: Option<String>,
) -> Result<(String, String), &'static str> {
    let core = CORE.lock().unwrap();
    if remove && (core.input.is_some() || core.status == "stopping") {
        return Err("busy");
    }
    let valid_alias = |alias: &str| {
        alias.len() == 64
            && alias
                .bytes()
                .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
    };
    let expected = expected.as_deref().unwrap_or("");
    if !(expected.is_empty() && !remove || valid_alias(expected)) {
        return Err("identity_changed");
    }
    let mut child = spawn_core()?;
    let action = if remove { "remove" } else { "identity" };
    if writeln!(
        child.stdin.take().unwrap(),
        "{{\"action\":\"{action}\",\"expected\":\"{expected}\"}}"
    )
    .is_err()
    {
        let _ = child.kill();
        let _ = child.wait();
        return Err("generic");
    }
    let mut line = String::new();
    let read = BufReader::new(child.stdout.take().unwrap())
        .take(128)
        .read_line(&mut line);
    let status = child.wait().map_err(|_| "generic")?;
    read.map_err(|_| "generic")?;
    let line = line.trim_end_matches(['\r', '\n']);
    if !status.success() {
        return Err(error_label(line));
    }
    if remove && line == "removed" {
        return Ok((String::new(), String::new()));
    }
    let (alias, label) = line
        .strip_prefix("identity:")
        .and_then(|s| s.split_once('|'))
        .ok_or("generic")?;
    if !remove
        && label.len() <= 48
        && ((alias.is_empty() && label.is_empty()) || (valid_alias(alias) && label.contains('*')))
        && label
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b".:*[]-".contains(&b))
    {
        return Ok((label.to_owned(), alias.to_owned()));
    }
    Err("generic")
}

fn main() {
    if let Err(code) = system_proxy::recover() {
        CORE.lock().unwrap().notice = code;
    }
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            start_core,
            stop_core,
            core_status,
            set_proxy,
            device_identity
        ])
        .build(tauri::generate_context!())
        .expect("desktop initialization failed")
        .run(|_, event| {
            if matches!(event, tauri::RunEvent::Exit) {
                stop_core()
            }
        });
}
