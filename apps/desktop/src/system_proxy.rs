//! Current-user WinINet ownership. The lock protects cooperating Nexus processes;
//! WinINet has no compare-and-swap with independent writers.
use std::{
    env,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    os::windows::{ffi::OsStrExt, fs::OpenOptionsExt},
    path::{Path, PathBuf},
    ptr::null,
    sync::Mutex,
    time::{SystemTime, UNIX_EPOCH},
};

type Result<T> = std::result::Result<T, &'static str>;
const ERROR: &str = "proxy-error";
const CHANGED: &str = "proxy-changed";
const BUSY: &str = "proxy-busy";
const JOURNAL: &str = "wininet.journal";
const PENDING: &str = "wininet.pending";
const LOCAL_BYPASS: &str = concat!(
    "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;",
    "172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;",
    "172.28.*;172.29.*;172.30.*;172.31.*;192.168.*;169.254.*;<local>",
);
static OWNER: Mutex<Option<File>> = Mutex::new(None);

// None is a missing WinINet string, distinct from an explicit empty string.
// Preserve UTF-16 code units exactly, including non-Unicode profile names.
type Wide = Option<Vec<u16>>;

#[derive(Clone, PartialEq, Eq)]
struct Settings {
    flags: u32,
    discovery: u32,
    server: Wide,
    bypass: Wide,
    pac: Wide,
}

impl Settings {
    fn at_step(&self, port: u16, step: u8, owned_bypass: &Wide) -> Self {
        let endpoint = format!("http=127.0.0.1:{port};https=127.0.0.1:{port}");
        Self {
            flags: if step >= 3 {
                self.flags & !15 | 3
            } else {
                self.flags
            }, // DIRECT | PROXY, without PAC/auto-detect.
            server: if step >= 1 {
                Some(endpoint.encode_utf16().collect())
            } else {
                self.server.clone()
            },
            bypass: if step >= 2 {
                owned_bypass.clone()
            } else {
                self.bypass.clone()
            },
            ..self.clone()
        }
    }
}

// WinINet may return NULL after being asked to clear a string. Keep the
// original representation in the journal for the eventual restore.
fn same_options(left: &Settings, right: &Settings) -> bool {
    left.flags == right.flags
        && left.discovery == right.discovery
        && left.pac == right.pac
        && left.server.as_deref().unwrap_or_default() == right.server.as_deref().unwrap_or_default()
        && left.bypass.as_deref().unwrap_or_default() == right.bypass.as_deref().unwrap_or_default()
}

trait Store {
    fn connection(&self) -> Result<Wide>;
    fn read(&self, connection: &Wide) -> Result<Settings>;
    fn write_field(&self, connection: &Wide, step: u8, settings: &Settings) -> Result<()>;
    fn notify(&self) -> Result<()>;
}

struct Backup {
    connection: Wide,
    owned_bypass: Wide,
    before: Settings,
    port: u16,
    session: [u8; 20],
    from: u8,
    to: u8,
}

fn take<const N: usize>(input: &mut &[u8]) -> Result<[u8; N]> {
    let (value, rest) = input.split_at_checked(N).ok_or(ERROR)?;
    *input = rest;
    value.try_into().map_err(|_| ERROR)
}

fn read_wide(input: &mut &[u8]) -> Result<Wide> {
    let count = u32::from_le_bytes(take(input)?);
    if count == u32::MAX {
        return Ok(None);
    }
    if count > 65535 {
        return Err(ERROR);
    }
    let mut value = Vec::with_capacity(count as usize);
    for _ in 0..count {
        let unit = u16::from_le_bytes(take(input)?);
        if unit == 0 {
            return Err(ERROR);
        }
        value.push(unit);
    }
    Ok(Some(value))
}

impl Backup {
    fn at_step(&self, step: u8) -> Settings {
        self.before.at_step(self.port, step, &self.owned_bypass)
    }

    fn save(&self, dir: &Path) -> Result<()> {
        // Write and flush the complete record before publishing its name.
        let mut data = b"NXP2".to_vec();
        data.extend(self.session);
        data.extend([self.from, self.to]);
        data.extend(self.port.to_le_bytes());
        data.extend(self.before.flags.to_le_bytes());
        data.extend(self.before.discovery.to_le_bytes());
        for value in [
            &self.connection,
            &self.before.server,
            &self.before.bypass,
            &self.before.pac,
            &self.owned_bypass,
        ] {
            match value {
                Some(value) if value.len() <= 65535 => {
                    data.extend((value.len() as u32).to_le_bytes());
                    for unit in value {
                        data.extend(unit.to_le_bytes());
                    }
                }
                Some(_) => return Err(ERROR),
                None => data.extend(u32::MAX.to_le_bytes()),
            }
        }
        let temp = dir.join(PENDING);
        let mut file = File::create(&temp).map_err(|_| ERROR)?;
        file.write_all(&data)
            .and_then(|_| file.sync_all())
            .map_err(|_| ERROR)?;
        drop(file);
        let path = |p: &Path| p.as_os_str().encode_wide().chain([0]).collect::<Vec<_>>();
        // The exclusive file lock covers journal replacement.
        if unsafe {
            MoveFileExW(
                path(&temp).as_ptr(),
                path(&dir.join(JOURNAL)).as_ptr(),
                1 | 8,
            )
        } == 0
        {
            return Err(ERROR);
        }
        Ok(())
    }

    fn load(dir: &Path) -> Result<Option<Self>> {
        let file = match File::open(dir.join(JOURNAL)) {
            Ok(file) => file,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(_) => return Err(ERROR),
        };
        let mut data = Vec::new();
        file.take(530_000)
            .read_to_end(&mut data)
            .map_err(|_| ERROR)?;
        let mut input = data.as_slice();
        let version = take::<4>(&mut input)?;
        if &version != b"NXP1" && &version != b"NXP2" {
            return Err(ERROR);
        }
        let session = take::<20>(&mut input)?;
        let [from, to] = take::<2>(&mut input)?;
        if from > 3 || to > 3 || from.abs_diff(to) > 1 {
            return Err(ERROR);
        }
        let port = u16::from_le_bytes(take(&mut input)?);
        let flags = u32::from_le_bytes(take(&mut input)?);
        let discovery = u32::from_le_bytes(take(&mut input)?);
        let connection = read_wide(&mut input)?;
        let before = Settings {
            flags,
            discovery,
            server: read_wide(&mut input)?,
            bypass: read_wide(&mut input)?,
            pac: read_wide(&mut input)?,
        };
        // V1 used an empty bypass list for every owned snapshot. V2 records
        // the actual value, including while resuming a partial V1 restore.
        let owned_bypass = if &version == b"NXP1" {
            Some(Vec::new())
        } else {
            read_wide(&mut input)?
        };
        if !input.is_empty() || port == 0 || owned_bypass.is_none() {
            return Err(ERROR);
        }
        Ok(Some(Self {
            connection,
            owned_bypass,
            before,
            port,
            session,
            from,
            to,
        }))
    }
}

// Journal the two possible full snapshots around a single WinINet option
// write. A crash between fields never leaves an unidentifiable partial batch.
fn change(store: &impl Store, dir: &Path, backup: &mut Backup, from: u8, to: u8) -> Result<()> {
    let before = backup.at_step(from);
    let after = backup.at_step(to);
    backup.from = from;
    backup.to = to;
    backup.save(dir)?;
    if !same_options(&store.read(&backup.connection)?, &before) {
        return Err(CHANGED);
    }
    if before != after {
        store.write_field(&backup.connection, from.max(to), &after)?;
        if !same_options(&store.read(&backup.connection)?, &after) {
            return Err(CHANGED);
        }
    }
    Ok(())
}

fn recover_locked(store: &impl Store, dir: &Path) -> Result<()> {
    // No live writer can own the pending file while this process holds the lock.
    match fs::remove_file(dir.join(PENDING)) {
        Ok(()) => (),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
        Err(_) => return Err(ERROR),
    }
    let Some(mut backup) = Backup::load(dir)? else {
        return Ok(());
    };
    let current = store.read(&backup.connection)?;
    let step = [backup.to, backup.from]
        .into_iter()
        .find(|step| same_options(&current, &backup.at_step(*step)));
    let Some(step) = step else {
        fs::remove_file(dir.join(JOURNAL)).map_err(|_| ERROR)?;
        return Err(CHANGED);
    };
    for from in (1..=step).rev() {
        if let Err(error) = change(store, dir, &mut backup, from, from - 1) {
            if error == CHANGED {
                fs::remove_file(dir.join(JOURNAL)).map_err(|_| ERROR)?;
            }
            return Err(error);
        }
    }
    store.notify()?;
    fs::remove_file(dir.join(JOURNAL)).map_err(|_| ERROR)
}

fn apply(store: &impl Store, dir: &Path, port: u16) -> Result<()> {
    if port == 0 {
        return Err(ERROR);
    }
    let connection = store.connection()?;
    let before = store.read(&connection)?;
    let mut session = [0; 20];
    session[..4].copy_from_slice(&std::process::id().to_le_bytes());
    session[4..].copy_from_slice(
        &SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| ERROR)?
            .as_nanos()
            .to_le_bytes(),
    );
    let mut backup = Backup {
        before,
        owned_bypass: Some(LOCAL_BYPASS.encode_utf16().collect()),
        connection,
        port,
        session,
        from: 0,
        to: 0,
    };
    for from in 0..3 {
        change(store, dir, &mut backup, from, from + 1)?;
    }
    // A completed takeover owns only the final snapshot.
    backup.from = 3;
    backup.save(dir)?;
    store.notify()
}

fn directory() -> Result<PathBuf> {
    let base = PathBuf::from(env::var_os("LOCALAPPDATA").ok_or(ERROR)?);
    if !base.is_absolute() {
        return Err(ERROR);
    }
    Ok(base.join("Nexus").join("desktop"))
}

fn lock(dir: &Path) -> Result<File> {
    fs::create_dir_all(dir).map_err(|_| ERROR)?;
    OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .share_mode(0)
        .open(dir.join("wininet.lock"))
        .map_err(|e| {
            if e.raw_os_error() == Some(32) {
                BUSY
            } else {
                ERROR
            }
        })
}

pub fn enable(port: u16) -> Result<()> {
    let mut owner = OWNER.lock().map_err(|_| ERROR)?;
    let dir = directory()?;
    if owner.is_some() {
        return Err(BUSY);
    }
    let file = lock(&dir)?;
    recover_locked(&WinInet, &dir)?;
    let result = apply(&WinInet, &dir, port);
    if result.is_err() {
        let recovery = recover_locked(&WinInet, &dir);
        if recovery == Err(ERROR) {
            // Preserve exclusive ownership while our rollback remains retryable.
            *owner = Some(file);
        }
        recovery?;
        return result;
    }
    *owner = Some(file);
    Ok(())
}

pub fn restore() -> Result<()> {
    let mut owner = OWNER.lock().map_err(|_| ERROR)?;
    if owner.is_none() {
        return Ok(());
    }
    let result = recover_locked(&WinInet, &directory()?);
    if result != Err(ERROR) {
        *owner = None;
    }
    result
}

pub fn recover() -> Result<()> {
    let owner = OWNER.lock().map_err(|_| ERROR)?;
    let dir = directory()?;
    if owner.is_some()
        || (!dir.join(JOURNAL).try_exists().map_err(|_| ERROR)?
            && !dir.join(PENDING).try_exists().map_err(|_| ERROR)?)
    {
        return Ok(());
    }
    let _lock = lock(&dir)?;
    recover_locked(&WinInet, &dir)
}

#[repr(C)]
struct OptionValue {
    id: u32,
    value: usize,
}
#[repr(C)]
struct OptionList {
    size: u32,
    connection: *const u16,
    count: u32,
    error: u32,
    options: *mut OptionValue,
}
#[link(name = "wininet")]
extern "system" {
    fn InternetGetConnectedStateExW(
        flags: *mut u32,
        name: *mut u16,
        len: u32,
        reserved: u32,
    ) -> i32;
    fn InternetQueryOptionW(
        handle: usize,
        option: u32,
        buffer: *mut OptionList,
        size: *mut u32,
    ) -> i32;
    fn InternetSetOptionW(handle: usize, option: u32, buffer: *const OptionList, size: u32) -> i32;
}
#[link(name = "kernel32")]
extern "system" {
    fn GlobalFree(ptr: usize) -> usize;
    fn lstrlenW(string: *const u16) -> i32;
    fn MoveFileExW(from: *const u16, to: *const u16, flags: u32) -> i32;
}

fn terminated(value: &Wide) -> Wide {
    value
        .as_ref()
        .map(|units| units.iter().copied().chain([0]).collect())
}
fn pointer(value: &Wide) -> *const u16 {
    value.as_ref().map_or(null(), |units| units.as_ptr())
}

struct WinInet;
impl Store for WinInet {
    fn connection(&self) -> Result<Wide> {
        let mut flags = 0;
        let mut name = [0u16; 257]; // RAS_MaxEntryName + NUL.
        if unsafe {
            InternetGetConnectedStateExW(&mut flags, name.as_mut_ptr(), name.len() as u32, 0)
        } == 0
        {
            return Err(ERROR);
        }
        if flags & 1 == 0 {
            return Ok(None);
        } // WinINet default/LAN profile.
        let end = name
            .iter()
            .position(|c| *c == 0)
            .filter(|n| *n > 0)
            .ok_or(ERROR)?;
        Ok(Some(name[..end].to_vec()))
    }

    fn read(&self, connection: &Wide) -> Result<Settings> {
        let name = terminated(connection);
        // Query FLAGS_UI as required for the user's configured auto-detect bit.
        let mut options = [10, 5, 2, 3, 4].map(|id| OptionValue { id, value: 0 });
        let mut size = size_of::<OptionList>() as u32;
        let mut list = OptionList {
            size,
            connection: pointer(&name),
            count: options.len() as u32,
            error: 0,
            options: options.as_mut_ptr(),
        };
        let ok = unsafe { InternetQueryOptionW(0, 75, &mut list, &mut size) } != 0;
        let mut strings = [None, None, None];
        for (item, out) in options[2..].iter().zip(&mut strings) {
            if item.value != 0 {
                let ptr = item.value as *const u16;
                if ok {
                    let len = unsafe { lstrlenW(ptr) } as usize;
                    *out = Some(unsafe { std::slice::from_raw_parts(ptr, len) }.to_vec());
                }
                unsafe { GlobalFree(item.value) };
            }
        }
        if !ok {
            return Err(ERROR);
        }
        let [server, bypass, pac] = strings;
        Ok(Settings {
            flags: options[0].value as u32,
            discovery: options[1].value as u32,
            server,
            bypass,
            pac,
        })
    }

    fn write_field(&self, connection: &Wide, step: u8, settings: &Settings) -> Result<()> {
        let name = terminated(connection);
        let server = terminated(&settings.server);
        let bypass = terminated(&settings.bypass);
        // Only these three members change. PAC URL and auto-discovery details
        // stay in the OS and participate in the ownership check on restore.
        let mut option = match step {
            1 => OptionValue {
                id: 2,
                value: pointer(&server) as usize,
            },
            2 => OptionValue {
                id: 3,
                value: pointer(&bypass) as usize,
            },
            3 => OptionValue {
                id: 1,
                value: settings.flags as usize,
            },
            _ => return Err(ERROR),
        };
        let size = size_of::<OptionList>() as u32;
        let list = OptionList {
            size,
            connection: pointer(&name),
            count: 1,
            error: 0,
            options: &mut option,
        };
        if unsafe { InternetSetOptionW(0, 75, &list, size) } == 0 {
            return Err(ERROR);
        }
        Ok(())
    }

    fn notify(&self) -> Result<()> {
        for option in [95, 37] {
            // PROXY_SETTINGS_CHANGED for all instances, then REFRESH.
            if unsafe { InternetSetOptionW(0, option, null(), 0) } == 0 {
                return Err(ERROR);
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::{Cell, RefCell};

    struct Fake {
        values: RefCell<Settings>,
        fail_after_field: Cell<u8>,
        fail_notify: Cell<bool>,
    }
    impl Store for Fake {
        fn connection(&self) -> Result<Wide> {
            Ok(Some("test RAS".encode_utf16().collect()))
        }
        fn read(&self, connection: &Wide) -> Result<Settings> {
            assert_eq!(connection, &self.connection()?);
            Ok(self.values.borrow().clone())
        }
        fn write_field(&self, name: &Wide, step: u8, value: &Settings) -> Result<()> {
            assert_eq!(name, &self.connection()?);
            let mut current = self.values.borrow_mut();
            match step {
                1 => current.server = value.server.clone(),
                2 => current.bypass = value.bypass.clone(),
                3 => current.flags = value.flags,
                _ => panic!("unexpected field"),
            }
            if self.fail_after_field.get() == step {
                self.fail_after_field.set(0);
                return Err(ERROR); // Simulates an error returned after the OS write.
            }
            Ok(())
        }
        fn notify(&self) -> Result<()> {
            if self.fail_notify.replace(false) {
                Err(ERROR)
            } else {
                Ok(())
            }
        }
    }

    #[test]
    fn takeover_restoration_and_foreign_ownership() {
        let dir = env::temp_dir().join(format!(
            "nexus-proxy-test-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let file = lock(&dir).unwrap();
        assert_eq!(lock(&dir).err(), Some(BUSY));
        let old = Settings {
            flags: 13,
            discovery: 1,
            server: None,
            bypass: Some("*.corp.invalid".encode_utf16().collect()),
            pac: Some("https://pac.invalid/profile".encode_utf16().collect()),
        };
        let store = Fake {
            values: RefCell::new(old.clone()),
            fail_after_field: Cell::new(0),
            fail_notify: Cell::new(false),
        };
        apply(&store, &dir, 32009).unwrap();
        let bypass = [
            vec![
                "localhost".to_owned(),
                "127.*".to_owned(),
                "10.*".to_owned(),
            ],
            (16..=31).map(|n| format!("172.{n}.*")).collect(),
            vec![
                "192.168.*".to_owned(),
                "169.254.*".to_owned(),
                "<local>".to_owned(),
            ],
        ]
        .concat()
        .join(";");
        assert!(
            *store.values.borrow() == old.at_step(32009, 3, &Some(bypass.encode_utf16().collect()))
        );
        drop(file); // Simulate process death: only the disk journal survives.
        let next_owner = lock(&dir).unwrap();
        store.fail_notify.set(true);
        assert_eq!(recover_locked(&store, &dir), Err(ERROR));
        assert!(*store.values.borrow() == old && dir.join(JOURNAL).is_file());
        recover_locked(&store, &dir).unwrap();
        // Simulate an old, fully applied NXP1 takeover. Its bytes match NXP2
        // up to the final owned-bypass field, which was implicit in NXP1.
        apply(&store, &dir, 32009).unwrap();
        let mut legacy = fs::read(dir.join(JOURNAL)).unwrap();
        legacy[..4].copy_from_slice(b"NXP1");
        legacy.truncate(legacy.len() - 4 - LOCAL_BYPASS.encode_utf16().count() * 2);
        fs::write(dir.join(JOURNAL), legacy).unwrap();
        store.values.borrow_mut().bypass = None; // WinINet normalizes an empty string.
        store.fail_after_field.set(3);
        assert_eq!(recover_locked(&store, &dir), Err(ERROR));
        recover_locked(&store, &dir).unwrap();
        assert!(*store.values.borrow() == old);
        store.fail_after_field.set(2);
        assert_eq!(apply(&store, &dir, 32009), Err(ERROR));
        recover_locked(&store, &dir).unwrap();
        assert!(*store.values.borrow() == old);
        apply(&store, &dir, 32009).unwrap();
        store.values.borrow_mut().pac = None; // Another app only changed the PAC.
        let foreign = store.values.borrow().clone();
        assert_eq!(recover_locked(&store, &dir), Err(CHANGED));
        assert!(*store.values.borrow() == foreign && !dir.join(JOURNAL).exists());
        drop(next_owner);
        fs::remove_file(dir.join("wininet.lock")).unwrap();
        fs::remove_dir(dir).unwrap();
    }
}
