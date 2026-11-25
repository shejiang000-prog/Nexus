#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[cfg(windows)]
use std::os::windows::process::CommandExt;
use std::{
    io::{BufRead, BufReader, Read, Write},
    process::{Child, ChildStdin, Command, Stdio},
    sync::Mutex,
    thread,
};
use tauri::ipc::{InvokeBody, Request};

struct Core {
    status: &'static str,
    input: Option<ChildStdin>,
}

static CORE: Mutex<Core> = Mutex::new(Core {
    status: "stopped",
    input: None,
});

fn monitor(mut child: Child) {
    let mut output = BufReader::new(child.stdout.take().unwrap());
    let mut line = Vec::with_capacity(32);
    loop {
        line.clear();
        if !matches!((&mut output).take(32).read_until(b'\n', &mut line), Ok(1..)) {
            break;
        }
        let status = match line.as_slice() {
            b"connecting\n" => "connecting",
            b"ready:h3\n" => "ready:h3",
            b"ready:h2\n" => "ready:h2",
            b"error\n" => break,
            _ => {
                let _ = child.kill();
                break;
            }
        };
        let mut core = CORE.lock().unwrap();
        if core.input.is_some() {
            core.status = status;
        }
    }
    let _ = child.wait();
    let mut core = CORE.lock().unwrap();
    core.status = if core.status == "stopping" {
        "stopped"
    } else {
        "error"
    };
    core.input = None;
}

#[tauri::command]
fn start_core(request: Request<'_>) -> Result<&'static str, &'static str> {
    let InvokeBody::Json(body) = request.body() else {
        return Err("invalid");
    };
    let invite = body
        .get("invite")
        .filter(|value| {
            value
                .as_str()
                .is_some_and(|text| text.len() <= 16 * 1024 && !text.contains(['\r', '\n']))
        })
        .ok_or("invalid")?;
    let mut core = CORE.lock().unwrap();
    if core.input.is_some() || core.status == "stopping" {
        return Ok(core.status);
    }

    let executable = std::env::current_exe()
        .map_err(|_| "error")?
        .with_file_name("nexus-core.exe");
    let mut command = Command::new(&executable);
    #[cfg(windows)]
    command.creation_flags(0x08000000); // CREATE_NO_WINDOW
    let child = command
        .current_dir(executable.parent().unwrap())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn();
    let mut child = match child {
        Ok(child) => child,
        Err(err) => {
            core.status = if err.kind() == std::io::ErrorKind::NotFound {
                "missing"
            } else {
                "error"
            };
            return Ok(core.status);
        }
    };
    let mut input = child.stdin.take().unwrap();
    if writeln!(input, "{{\"invite\":{invite}}}").is_err() {
        drop(input);
        let _ = child.kill();
        let _ = child.wait();
        core.status = "error";
        return Ok(core.status);
    }
    core.status = "connecting";
    core.input = Some(input);
    thread::spawn(move || monitor(child));
    Ok(core.status)
}

#[tauri::command]
fn stop_core() -> &'static str {
    let mut core = CORE.lock().unwrap();
    if core.input.take().is_some() {
        core.status = "stopping";
    }
    core.status
}

#[tauri::command]
fn core_status() -> &'static str {
    CORE.lock().unwrap().status
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![start_core, stop_core, core_status])
        .run(tauri::generate_context!())
        .expect("desktop initialization failed");
}
