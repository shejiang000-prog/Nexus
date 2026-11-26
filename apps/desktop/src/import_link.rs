//! One-click subscription import: `nexus://install-config?url=<https URL>&name=<label>`.
//!
//! Windows starts a new process for every link click. If a Nexus window is
//! already open, the new process hands the link over through a file plus a
//! named event and exits, so the user keeps a single window.

use std::{
    env,
    ffi::c_void,
    fs,
    os::windows::ffi::OsStrExt,
    path::PathBuf,
    sync::Mutex,
    thread,
};

use tauri::{AppHandle, Emitter, Manager};

static PENDING: Mutex<Option<(String, String)>> = Mutex::new(None);
const EVENT_NAME: &str = "Local\\NexusDesktopImport";
const EVENT_MODIFY_STATE: u32 = 0x0002;
const INFINITE: u32 = 0xFFFF_FFFF;

#[link(name = "kernel32")]
extern "system" {
    fn CreateEventW(attributes: *const c_void, manual_reset: i32, initial: i32, name: *const u16) -> isize;
    fn OpenEventW(access: u32, inherit: i32, name: *const u16) -> isize;
    fn SetEvent(event: isize) -> i32;
    fn WaitForSingleObject(handle: isize, millis: u32) -> u32;
    fn CloseHandle(handle: isize) -> i32;
}

fn wide(s: &str) -> Vec<u16> {
    std::ffi::OsStr::new(s).encode_wide().chain(Some(0)).collect()
}

fn decode(s: &str) -> Option<String> {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'%' => {
                let hex = std::str::from_utf8(b.get(i + 1..i + 3)?).ok()?;
                out.push(u8::from_str_radix(hex, 16).ok()?);
                i += 3;
            }
            b'+' => {
                out.push(b' ');
                i += 1;
            }
            c => {
                out.push(c);
                i += 1;
            }
        }
    }
    String::from_utf8(out).ok()
}

/// Returns (subscription URL, display name) for a well-formed import link.
pub(super) fn parse(link: &str) -> Option<(String, String)> {
    if link.len() > 4096 {
        return None;
    }
    let (scheme, rest) = link.split_once("://")?;
    if !scheme.eq_ignore_ascii_case("nexus") {
        return None;
    }
    let (target, query) = rest.split_once('?')?;
    if !target.trim_end_matches('/').eq_ignore_ascii_case("install-config") {
        return None;
    }
    let (mut url, mut name) = (None, String::new());
    for pair in query.split('&') {
        let (k, v) = pair.split_once('=').unwrap_or((pair, ""));
        match k {
            "url" => url = Some(decode(v)?),
            "name" => name = decode(v)?,
            _ => {}
        }
    }
    let url = url?;
    let host = url.strip_prefix("https://")?.split(['/', '?', '#']).next()?;
    if url.len() > 2048 || host.is_empty() || url.chars().any(|c| c.is_control() || c.is_whitespace()) {
        return None;
    }
    let name: String = name.chars().filter(|c| !c.is_control()).take(40).collect();
    Some((url, name.trim().to_owned()))
}

fn pending_file() -> Option<PathBuf> {
    let dir = PathBuf::from(env::var_os("LOCALAPPDATA")?);
    dir.is_absolute().then(|| dir.join("Nexus").join("desktop").join("pending-import.txt"))
}

/// Called with the process's only argument when it starts with `nexus://`.
/// Returns true when a running window took the link and this process should exit.
pub(super) fn hand_over(link: &str) -> bool {
    let name = wide(EVENT_NAME);
    let event = unsafe { OpenEventW(EVENT_MODIFY_STATE, 0, name.as_ptr()) };
    if event == 0 {
        // Nobody is listening: this process becomes the window and shows the prompt itself.
        *PENDING.lock().unwrap() = parse(link);
        return false;
    }
    let delivered = pending_file()
        .and_then(|path| {
            fs::create_dir_all(path.parent()?).ok()?;
            fs::write(path, link).ok()
        })
        .is_some()
        && unsafe { SetEvent(event) } != 0;
    unsafe { CloseHandle(event) };
    delivered
}

/// Waits for links handed over by later processes and brings the window forward.
pub(super) fn listen(app: AppHandle) {
    let name = wide(EVENT_NAME);
    let event = unsafe { CreateEventW(std::ptr::null(), 0, 0, name.as_ptr()) };
    if event == 0 {
        return;
    }
    thread::spawn(move || loop {
        if unsafe { WaitForSingleObject(event, INFINITE) } != 0 {
            return;
        }
        let link = pending_file().and_then(|path| {
            let text = fs::read_to_string(&path).ok();
            let _ = fs::remove_file(&path);
            text
        });
        if let Some(found) = link.as_deref().and_then(parse) {
            *PENDING.lock().unwrap() = Some(found);
            let _ = app.emit("nexus-import", ());
        }
        if let Some(window) = app.get_webview_window("main") {
            let _ = window.unminimize();
            let _ = window.show();
            let _ = window.set_focus();
        }
    });
}

#[tauri::command]
pub(super) fn pending_import() -> Option<(String, String)> {
    PENDING.lock().unwrap().take()
}

#[cfg(test)]
mod tests {
    use super::parse;

    #[test]
    fn import_links() {
        assert_eq!(
            parse("nexus://install-config?url=https%3A%2F%2Fpanel.example.com%2Fs%2Fabc123&name=%E6%B5%81%E5%85%89"),
            Some(("https://panel.example.com/s/abc123".into(), "流光".into()))
        );
        assert_eq!(parse("NEXUS://install-config/?url=https://a.example/s/x"), Some(("https://a.example/s/x".into(), "".into())));
        for bad in [
            "nexus://install-config?url=http%3A%2F%2Fa.example%2Fs",
            "nexus://install-config?url=https%3A%2F%2F",
            "nexus://install-config?url=https%3A%2F%2Fa.example%2Fs%20x",
            "nexus://install-config?url=https%3A%2F%2Fa.example%2F%0A",
            "nexus://install-config?name=x",
            "nexus://invite/v2#abc",
            "clash://install-config?url=https%3A%2F%2Fa.example%2Fs",
            "nexus://install-config?url=%ZZ",
        ] {
            assert_eq!(parse(bad), None, "{bad}");
        }
        let long = format!("nexus://install-config?url=https://a.example/s&name={}", "名".repeat(60));
        assert_eq!(parse(&long).unwrap().1.chars().count(), 40);
    }
}
