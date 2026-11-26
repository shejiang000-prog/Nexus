#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod import_link;
mod owner;
mod probes;
mod subscriptions;
mod system_proxy;
mod updates;

use std::{
    io::{BufRead, BufReader, Read, Write},
    os::windows::process::CommandExt,
    process::{Child, ChildStdin, Command, Stdio},
    sync::{Condvar, Mutex},
    thread,
    time::Duration,
};
use tauri::ipc::{InvokeBody, Request};

struct Core {
    status: &'static str,
    input: Option<ChildStdin>,
    socks: u16,
    http: u16,
    proxy: bool,
    tun: bool,
    tun_supported: Option<bool>,
    notice: &'static str,
    traffic: [u64; 2],
}

static CORE: Mutex<Core> = Mutex::new(Core {
    status: "stopped",
    input: None,
    socks: 0,
    http: 0,
    proxy: false,
    tun: false,
    tun_supported: None,
    notice: "",
    traffic: [0, 0],
});
static STOPPED: Condvar = Condvar::new();

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
        "error:tun_admin" => "tun_admin",
        "error:tun_driver" => "tun_driver",
        "error:tun_session" => "tun_session",
        "error:tun_conflict" => "tun_conflict",
        "error:tun_recovery" => "tun_recovery",
        "error:local_port" => "local_port",
        "error:no_identity" => "no_identity",
        "error:routing" => "routing",
        "error:subscription_changed" => "subscription_changed",
        "error:subscription_download" => "subscription_download",
        "error:subscription_format" => "subscription_format",
        "error:subscription_storage" => "subscription_storage",
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
        "subscription" => "subscription",
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
        if let Some((up, down)) = line
            .strip_prefix("traffic:")
            .and_then(|s| s.split_once(':'))
        {
            if let (Ok(up), Ok(down)) = (up.parse::<u64>(), down.parse::<u64>()) {
                let mut core = CORE.lock().unwrap();
                core.traffic = [core.traffic[0].max(up), core.traffic[1].max(down)];
                continue;
            }
        }
        if line == "connecting" {
            let mut core = CORE.lock().unwrap();
            if core.input.is_some() && core.status != "stopping" {
                core.status = "connecting";
            }
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
        if core.http == 0 && core.tun && core.notice.starts_with("tun_") {
            core.notice = "";
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
        if terminal == "tun_recovery" {
            core.notice = terminal;
        }
        "stopped"
    } else {
        terminal
    };
    core.input = None;
    core.http = 0;
    core.socks = 0;
    STOPPED.notify_all();
}

#[tauri::command(async)]
fn start_core(request: Request<'_>) -> Result<(), &'static str> {
    let _guard = updates::REQUESTS.try_read().map_err(|_| "update_busy")?;
    let InvokeBody::Json(body) = request.body() else {
        return Err("invite_invalid");
    };
    let tun = match body.get("tun") {
        Some(value) => value.as_bool().ok_or("generic")?,
        None => false,
    };
    let mode = match body.get("mode").and_then(|v| v.as_str()) {
        Some("split") => "split",
        Some("global") => "global",
        _ => return Err("routing"),
    };
    let line = match body.get("source").and_then(|v| v.as_str()).unwrap_or("v2") {
        "v2" => {
            let invite = body
                .get("invite")
                .filter(|v| {
                    v.as_str()
                        .is_some_and(|s| s.len() <= 16384 && !s.contains(['\r', '\n']))
                })
                .ok_or("invite_invalid")?;
            format!(r#"{{"invite":{invite},"mode":"{mode}","tun":{tun},"trafficStats":true}}"#)
        }
        "subscription" => {
            let id = body
                .get("subscriptionId")
                .and_then(|v| v.as_str())
                .filter(|id| subscriptions::valid_id(id))
                .ok_or("subscription_changed")?;
            let node = body
                .get("nodeId")
                .and_then(|v| v.as_str())
                .filter(|id| subscriptions::valid_id(id))
                .ok_or("subscription_changed")?;
            format!(
                r#"{{"action":"start","source":"subscription","subscriptionId":"{id}","nodeId":"{node}","mode":"{mode}","tun":{tun},"trafficStats":true}}"#
            )
        }
        _ => return Err("subscription_changed"),
    };
    let proxy = body
        .get("proxy")
        .and_then(|v| v.as_bool())
        .ok_or("generic")?;
    if tun {
        let probe_again = {
            let mut core = CORE.lock().unwrap();
            match core.tun_supported {
                None => return Err("tun_recovering"),
                Some(true) => false,
                Some(false) => {
                    core.tun_supported = None;
                    true
                }
            }
        };
        if probe_again {
            check_tun_support()?;
        }
    }
    let mut core = CORE.lock().unwrap();
    if core.input.is_some() || core.status == "stopping" {
        return Err("busy");
    }
    let mut child = spawn_core()?;
    let mut input = child.stdin.take().unwrap();
    if writeln!(input, "{line}").is_err() {
        drop(input);
        let _ = child.kill();
        let _ = child.wait();
        return Err("generic");
    }
    core.status = "connecting";
    core.traffic = [0, 0];
    core.proxy = proxy;
    core.tun = tun;
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

fn stop_and_wait() -> Result<(), &'static str> {
    stop_core();
    let core = CORE.lock().unwrap();
    let (core, _) = STOPPED
        .wait_timeout_while(core, Duration::from_secs(30), |core| {
            core.status == "stopping" || core.input.is_some()
        })
        .map_err(|_| "update_cleanup")?;
    if core.status == "stopping" || core.input.is_some() {
        return Err("update_cleanup");
    }
    Ok(())
}

#[tauri::command]
fn traffic_stats() -> Result<[u64; 2], &'static str> {
    let core = CORE.lock().unwrap();
    if matches!(core.status, "h3" | "h2" | "subscription") {
        Ok(core.traffic)
    } else {
        Err("not_connected")
    }
}

#[tauri::command]
fn core_status() -> (&'static str, u16, u16, bool, &'static str, bool) {
    let core = CORE.lock().unwrap();
    (
        core.status,
        core.socks,
        core.http,
        core.proxy,
        core.notice,
        core.tun,
    )
}

#[tauri::command]
fn set_proxy(enabled: bool) -> Result<(), &'static str> {
    let _guard = updates::REQUESTS.try_read().map_err(|_| "update_busy")?;
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
    let _guard = updates::REQUESTS.try_read().map_err(|_| "update_busy")?;
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

// This fixed command never starts a tunnel. Recovery can require a UAC
// response, so keep the WebView and ordinary proxy connections available.
fn recover_tun() -> Result<(), &'static str> {
    let mut child = spawn_core()?;
    let mut reply = String::new();
    let read =
        writeln!(child.stdin.take().unwrap(), r#"{{"action":"recover_tun"}}"#).and_then(|_| {
            BufReader::new(child.stdout.take().unwrap())
                .take(128)
                .read_line(&mut reply)
        });
    if read.is_err() || reply.len() == 128 {
        let _ = child.kill();
    }
    let status = child.wait().map_err(|_| "tun_version")?;
    match (status.success(), reply.trim_end()) {
        (true, "recovered") => Ok(()),
        (false, "error:tun_admin") => Err("tun_admin"),
        (false, "error:tun_recovery") => Err("tun_recovery"),
        _ => Err("tun_version"),
    }
}

// Older sidecars ignore extra start fields. A recognized recovery response
// proves TUN support before we may pass tun=true to the bundled core.
fn check_tun_support() -> Result<(), &'static str> {
    let result = recover_tun();
    let mut core = CORE.lock().unwrap();
    core.tun_supported = Some(matches!(result, Ok(()) | Err("tun_admin" | "tun_recovery")));
    if let Err(code) = result {
        core.notice = code;
    } else if core.notice.starts_with("tun_") || core.notice == "missing" {
        core.notice = "";
    }
    result
}

fn main() {
    let mut args = std::env::args_os().skip(1);
    let cleanup = match args.next() {
        None => false,
        Some(arg) if arg == "--cleanup" && args.next().is_none() => true,
        // Windows passes a clicked nexus:// link as the only argument.
        Some(arg) if args.next().is_none() && arg.to_str().is_some_and(|s| s.get(..8).is_some_and(|p| p.eq_ignore_ascii_case("nexus://"))) => {
            if import_link::hand_over(arg.to_str().unwrap_or_default()) {
                std::process::exit(0);
            }
            false
        }
        Some(_) => std::process::exit(2),
    };
    let _install = updates::install_lock().unwrap_or_else(|code| std::process::exit(code));
    let _lifetime = updates::lifetime_lock(cleanup).unwrap_or_else(|code| std::process::exit(code));
    if cleanup {
        std::process::exit(if updates::recover().is_ok() { 0 } else { 3 });
    }
    if let Err(code) = system_proxy::recover() {
        CORE.lock().unwrap().notice = code;
    }
    thread::spawn(|| {
        let _guard = updates::REQUESTS.read().unwrap();
        check_tun_support()
    });
    tauri::Builder::default()
        .plugin(tauri_plugin_updater::Builder::new().build())
        .setup(|app| {
            import_link::listen(app.handle().clone());
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            start_core,
            stop_core,
            core_status,
            traffic_stats,
            probes::probe_nodes,
            owner::owner_start,
            owner::owner_status,
            owner::owner_result,
            owner::owner_cancel,
            set_proxy,
            device_identity,
            subscriptions::subscriptions,
            import_link::pending_import,
            updates::check_update,
            updates::install_update
        ])
        .build(tauri::generate_context!())
        .expect("desktop initialization failed")
        .run(|_, event| {
            if matches!(event, tauri::RunEvent::Exit) {
                owner::shutdown();
                let _ = stop_and_wait();
            }
        });
}
