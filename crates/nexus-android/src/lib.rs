#![cfg(target_os = "android")]
mod forwarding;
mod split;
mod vpn;
use jni::{
    objects::{GlobalRef, JByteArray, JObject, JString, JValue},
    JNIEnv, JavaVM,
};
use std::{
    io,
    os::fd::{FromRawFd, OwnedFd},
    sync::Arc,
    time::Duration,
};
use tokio::{io::AsyncReadExt, net::UnixStream};

const SETUP: Duration = Duration::from_secs(8);

struct Device {
    vm: JavaVM,
    callback: GlobalRef,
}
impl Device {
    fn protect(&self, fd: i32) -> io::Result<()> {
        let mut env = self.vm.attach_current_thread().map_err(io::Error::other)?;
        let ok = env
            .call_method(
                &self.callback,
                "protectTransport",
                "(I)Z",
                &[JValue::Int(fd)],
            )
            .and_then(|v| v.z())
            .map_err(io::Error::other)?;
        if ok {
            Ok(())
        } else {
            Err(io::ErrorKind::PermissionDenied.into())
        }
    }
    fn ready(&self, port: u16) -> io::Result<()> {
        let mut env = self.vm.attach_current_thread().map_err(io::Error::other)?;
        env.call_method(
            &self.callback,
            "onReady",
            "(I)V",
            &[JValue::Int(port.into())],
        )
        .map_err(io::Error::other)?;
        Ok(())
    }
}

#[no_mangle]
pub extern "system" fn Java_dev_nexus_localclient_Tunnel_split(
    mut env: JNIEnv,
    callback: JObject,
    cancel_fd: i32,
    tun_fd: i32,
    engine_fd: i32,
    dns_socket: JString,
    data: JByteArray,
) -> i32 {
    // The caller transfers duplications, so cancellation never closes the service's TUN.
    let (cancel, tun, engine) = unsafe {
        (
            OwnedFd::from_raw_fd(cancel_fd),
            OwnedFd::from_raw_fd(tun_fd),
            std::os::unix::net::UnixDatagram::from(OwnedFd::from_raw_fd(engine_fd)),
        )
    };
    let run = || -> Result<(), Box<dyn std::error::Error>> {
        let device = Arc::new(Device {
            vm: env.get_java_vm()?,
            callback: env.new_global_ref(callback)?,
        });
        let path: String = env.get_string(&dns_socket)?.into();
        let text = String::from_utf8(env.convert_byte_array(data)?)?;
        let cancel = std::os::unix::net::UnixStream::from(cancel);
        cancel.set_nonblocking(true)?;
        engine.set_nonblocking(true)?;
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(async {
                let mut cancel = UnixStream::from_std(cancel)?;
                let routing = forwarding::Forwarding::new(
                    tokio::net::UnixDatagram::from_std(engine)?,
                    path.into(),
                    device,
                    &text,
                )?;
                let mut byte = [0];
                tokio::select! {biased; r=cancel.read(&mut byte)=>r.map(|_|()),
                r=vpn::forward(tun,Arc::new(routing))=>r}
            })?;
        Ok(())
    };
    i32::from(run().is_err())
}
