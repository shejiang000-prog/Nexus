use std::{
    env,
    ffi::OsString,
    fs,
    fs::{File, OpenOptions},
    os::windows::{ffi::OsStringExt, fs::OpenOptionsExt},
    path::{Path, PathBuf},
    sync::RwLock,
    time::{Duration, Instant},
};
use tauri_plugin_updater::{Error, Update, UpdaterExt};

const RELEASES: &str = "https://github.com/shejiang000-prog/Nexus/releases";
static PENDING: tauri::async_runtime::Mutex<Option<Update>> =
    tauri::async_runtime::Mutex::const_new(None);
// Short requests hold a shared guard until their sidecar has exited. Installation
// waits for those requests, then keeps new network sessions out through handoff.
pub(super) static REQUESTS: RwLock<()> = RwLock::new(());

fn update_error(error: Error) -> &'static str {
    match error {
        Error::Minisign(_)
        | Error::SignatureUtf8(_)
        | Error::Base64(_)
        | Error::MissingSignedVersion
        | Error::SignedVersionMismatch { .. } => "update_signature",
        _ => "update_download",
    }
}

#[tauri::command]
pub(super) async fn check_update(app: tauri::AppHandle) -> Result<Option<String>, &'static str> {
    let mut pending = PENDING.try_lock().map_err(|_| "update_busy")?;
    *pending = None;
    let updater = app
        .updater_builder()
        .configure_client(|client| {
            client
                .https_only(true)
                .connect_timeout(Duration::from_secs(10))
                .read_timeout(Duration::from_secs(30))
        })
        .restart_after_install(false)
        // Tauri's default callback hides all windows before ShellExecuteW can
        // fail. Our network cleanup is complete before install, and a launch
        // failure must leave the window available for feedback and retry.
        .on_before_exit(|| {})
        .build()
        .map_err(|_| "update_check")?;
    let Some(update) = updater.check().await.map_err(|_| "update_check")? else {
        return Ok(None);
    };
    let version = &update.version;
    if update.download_url.as_str()
        != format!("{RELEASES}/download/v{version}/Nexus-{version}-windows-x64-setup.exe")
    {
        return Err("update_check");
    }
    let version = version.clone();
    *pending = Some(update);
    Ok(Some(version))
}

#[tauri::command]
pub(super) async fn install_update() -> Result<(), &'static str> {
    let pending = PENDING.try_lock().map_err(|_| "update_busy")?;
    let update = pending.as_ref().ok_or("update_check")?.clone();
    // Download verifies the artifact and its signed version before touching a
    // running connection. install() independently verifies the same bytes.
    let bytes = update
        .download(|_, _| {}, || {})
        .await
        .map_err(update_error)?;
    tauri::async_runtime::spawn_blocking(move || {
        let _guard = REQUESTS.write().map_err(|_| "update_cleanup")?;
        super::stop_and_wait()?;
        recover()?;
        update.install(bytes).map_err(|_| "update_install")
    })
    .await
    .map_err(|_| "update_install")?
}

pub(super) fn recover() -> Result<(), &'static str> {
    // Another program's replacement is already acknowledged by restore/recover.
    for result in [
        super::system_proxy::restore(),
        super::system_proxy::recover(),
    ] {
        match result {
            Ok(()) | Err("proxy-changed") => {}
            Err(_) => return Err("update_cleanup"),
        }
    }
    super::recover_tun().map_err(|_| "update_cleanup")
}

#[link(name = "shell32")]
extern "system" {
    fn SHGetFolderPathW(hwnd: isize, folder: i32, token: isize, flags: u32, path: *mut u16) -> i32;
}

fn shared_install_lock(path: &Path) -> std::io::Result<File> {
    OpenOptions::new().read(true).share_mode(1).open(path)
}

pub(super) fn install_lock() -> Result<Option<File>, i32> {
    // This is a native x64 binary. CSIDL_PROGRAM_FILES resolves via the
    // system's known-folder API, independently of the caller's environment.
    let mut path = [0u16; 260];
    if unsafe { SHGetFolderPathW(0, 0x26, 0, 0, path.as_mut_ptr()) } != 0 {
        return Err(3);
    }
    let end = path.iter().position(|c| *c == 0).ok_or(3)?;
    let program_files = fs::canonicalize(OsString::from_wide(&path[..end])).map_err(|_| 3)?;
    let install = program_files.join("Nexus");
    let exe = fs::canonicalize(env::current_exe().map_err(|_| 3)?).map_err(|_| 3)?;
    if !exe
        .parent()
        .ok_or(3)?
        .as_os_str()
        .eq_ignore_ascii_case(install.join("app").as_os_str())
    {
        return Ok(None);
    }
    shared_install_lock(&install.join("maintenance.lock"))
        .map(Some)
        .map_err(|_| 2)
}

// All shells in this user's profile share the file. The installer calls
// --cleanup as the original user and can open it exclusively only after exit.
pub(super) fn lifetime_lock(exclusive: bool) -> Result<File, i32> {
    let dir = PathBuf::from(env::var_os("LOCALAPPDATA").ok_or(3)?);
    if !dir.is_absolute() {
        return Err(3);
    }
    let dir = dir.join("Nexus").join("desktop");
    fs::create_dir_all(&dir).map_err(|_| 3)?;
    let path = dir.join("desktop.lock");
    let deadline = Instant::now() + Duration::from_secs(4);
    loop {
        match OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .share_mode(if exclusive { 0 } else { 3 })
            .open(&path)
        {
            Ok(file) => return Ok(file),
            Err(e) if e.raw_os_error() == Some(32) => {
                if !exclusive || Instant::now() >= deadline {
                    return Err(2);
                }
                std::thread::sleep(Duration::from_millis(100));
            }
            Err(_) => return Err(3),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        io::{BufRead, BufReader, Write},
        net::TcpListener,
    };

    // Public test vector from tauri-plugin-updater 2.14.1's offline_tests.
    const KEY: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IEE0OUVGNDc0NkZBNkU2RDcKUldUWDVxWnZkUFNlcERHV2cyYno4bjZBclFuQk13Z1FLMWdlVm9zb3JjYkx6Y3VuRCtLSitjWWU=";
    const SIG: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVUWDVxWnZkUFNlcFBGMnpzanZQL0wvVFF0QkxSbldSdmFxKzRvbUsxVUV1Z3RRcC9VU3JHaUkxdjc2MnNEeTNpZDNhb3I3YlJDOUVUeWVvdDZsTUhIcldvWjVoalVNMVFjPQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkxNjQ5MDUxCWZpbGU6cGF5bG9hZAl2ZXJzaW9uOjIuMC4wCkREcTJDTE9FbDVjUW1DMmVKUkhsblpQVlZpT1pzdTFDanBQS1FvZWk0QS9YQmZrRHFHTzBCK1FNcmNySGdFUTVxVjNtbmU3NXRkRldoSnBxVVhoOUFBPT0=";

    #[test]
    fn maintenance_excludes_running_shells() {
        let path = env::temp_dir().join(format!("nexus-maintenance-{}.test", std::process::id()));
        let first = File::create_new(&path).unwrap();
        drop(first);
        let readers = [
            shared_install_lock(&path).unwrap(),
            shared_install_lock(&path).unwrap(),
        ];
        let exclusive = || OpenOptions::new().write(true).share_mode(0).open(&path);
        assert_eq!(exclusive().unwrap_err().raw_os_error(), Some(32));
        drop(readers);
        let installer = exclusive().unwrap();
        assert_eq!(
            shared_install_lock(&path).unwrap_err().raw_os_error(),
            Some(32)
        );
        drop(installer);
        fs::remove_file(&path).unwrap();
    }

    #[test]
    fn loopback_update_rejects_tampered_package() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let origin = format!("http://{}", listener.local_addr().unwrap());
        let manifest = format!(
            r#"{{"version":"2.0.0","platforms":{{"windows-x86_64":{{"url":"{origin}/setup.exe","signature":"{SIG}"}}}}}}"#
        );
        let server = std::thread::spawn(move || {
            for (path, body) in [
                ("/latest.json", manifest.as_str()),
                ("/setup.exe", "test"),
                ("/setup.exe", "tampered"),
            ] {
                let (mut conn, _) = listener.accept().unwrap();
                let mut lines = BufReader::new(&conn).lines().map(Result::unwrap);
                assert!(lines.next().unwrap().starts_with(&format!("GET {path} ")));
                for header in lines {
                    if header.is_empty() {
                        break;
                    }
                }
                write!(
                    conn,
                    "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                )
                .unwrap();
            }
        });
        let mut context = tauri::test::mock_context(tauri::test::noop_assets());
        let config = format!(
            r#"{{"pubkey":"{KEY}","requireSignedVersion":true,"dangerousInsecureTransportProtocol":true}}"#
        );
        context
            .config_mut()
            .plugins
            .0
            .insert("updater".into(), config.parse().unwrap());
        let app = tauri::test::mock_builder()
            .plugin(tauri_plugin_updater::Builder::new().build())
            .build(context)
            .unwrap();
        let updater = app
            .updater_builder()
            .no_proxy()
            .timeout(Duration::from_secs(5))
            .endpoints(vec![format!("{origin}/latest.json").parse().unwrap()])
            .unwrap()
            .build()
            .unwrap();
        tauri::async_runtime::block_on(async {
            let update = updater.check().await.unwrap().unwrap();
            assert_eq!(update.download(|_, _| {}, || {}).await.unwrap(), b"test");
            assert_eq!(
                update_error(update.download(|_, _| {}, || {}).await.unwrap_err()),
                "update_signature"
            );
        });
        server.join().unwrap();
    }
}
