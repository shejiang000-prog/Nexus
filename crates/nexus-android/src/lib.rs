#![cfg(target_os = "android")]
use jni::{
    objects::{GlobalRef, JByteArray, JObject, JString, JValue},
    JNIEnv, JavaVM,
};
use nexus_client::open;
use nexus_protocol::Flow;
use nexus_transport::{Connector, Mode, LIFETIME};
use rustls::{
    sign::{CertifiedKey, Signer, SigningKey, SingleCertAndKey},
    ClientConfig, RootCertStore, SignatureAlgorithm, SignatureScheme,
};
use std::{
    fmt, io,
    net::SocketAddr,
    os::fd::{FromRawFd, OwnedFd},
    sync::Arc,
};
use tokio::{
    io::{copy_bidirectional, AsyncReadExt},
    net::{TcpListener, UnixStream},
    task::JoinSet,
    time::timeout,
};

struct Device {
    vm: JavaVM,
    callback: GlobalRef,
}
impl fmt::Debug for Device {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("AndroidKeyStore")
    }
}
#[derive(Clone, Debug)]
struct Key(Arc<Device>);
const SCHEME: SignatureScheme = SignatureScheme::ECDSA_NISTP256_SHA256;
impl SigningKey for Key {
    fn choose_scheme(&self, offered: &[SignatureScheme]) -> Option<Box<dyn Signer>> {
        offered
            .contains(&SCHEME)
            .then(|| Box::new(self.clone()) as Box<dyn Signer>)
    }
    fn algorithm(&self) -> SignatureAlgorithm {
        SignatureAlgorithm::ECDSA
    }
}
impl Signer for Key {
    fn scheme(&self) -> SignatureScheme {
        SCHEME
    }
    fn sign(&self, msg: &[u8]) -> Result<Vec<u8>, rustls::Error> {
        let sign = || -> jni::errors::Result<Vec<u8>> {
            let mut env = self.0.vm.attach_current_thread()?;
            env.with_local_frame(4, |env| {
                let bytes = env.byte_array_from_slice(msg)?;
                let sig = env
                    .call_method(
                        &self.0.callback,
                        "sign",
                        "([B)[B",
                        &[JValue::Object(&bytes)],
                    )?
                    .l()?;
                env.convert_byte_array(JByteArray::from(sig))
            })
        };
        sign().map_err(|_| rustls::Error::General("device signing failed".into()))
    }
}
async fn serve(connector: Connector, target: SocketAddr, device: Arc<Device>) -> io::Result<()> {
    let connector = Arc::new(connector);
    // Validate the complete OPEN before announcing that the local port is ready.
    drop(open(&connector, &Mode::Auto, Flow::Tcp, target).await?);
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let mut active = JoinSet::new();
    {
        let mut env = device
            .vm
            .attach_current_thread()
            .map_err(io::Error::other)?;
        env.call_method(
            &device.callback,
            "onReady",
            "(I)V",
            &[JValue::Int(listener.local_addr()?.port().into())],
        )
        .map_err(io::Error::other)?;
    }
    loop {
        tokio::select! {
            result=listener.accept(), if active.len()<16 =>{
                let (mut local,_)=result?;
                let connector=connector.clone();
                active.spawn(async move {
                    let transfer=async {
                        let mut ch=open(&connector,&Mode::Auto,Flow::Tcp,target).await?;
                        copy_bidirectional(&mut local,&mut ch.stream).await?;
                        ch.drain().await;
                        Ok::<_,io::Error>(())
                    };
                    let _=timeout(LIFETIME,transfer).await;
                });
            }
            Some(_)=active.join_next(), if !active.is_empty()=>{}
        }
    }
}

#[no_mangle]
pub extern "system" fn Java_dev_nexus_localclient_Tunnel_run(
    mut env: JNIEnv,
    callback: JObject,
    cancel_fd: i32,
    server: JString,
    name: JString,
    target: JString,
    ca: JByteArray,
    cert: JByteArray,
) -> i32 {
    if cancel_fd < 0 {
        return 1;
    }
    // Kotlin transfers sole ownership with ParcelFileDescriptor.detachFd().
    let cancel = unsafe { OwnedFd::from_raw_fd(cancel_fd) };
    let run = || -> Result<(), Box<dyn std::error::Error>> {
        let server: String = env.get_string(&server)?.into();
        let name: String = env.get_string(&name)?.into();
        let target: String = env.get_string(&target)?.into();
        let server = server.parse()?;
        let target = target.parse()?;
        let mut roots = RootCertStore::empty();
        roots.add(env.convert_byte_array(ca)?.into())?;
        let device = Arc::new(Device {
            vm: env.get_java_vm()?,
            callback: env.new_global_ref(callback)?,
        });
        let key = CertifiedKey::new(
            vec![env.convert_byte_array(cert)?.into()],
            Arc::new(Key(device.clone())),
        );
        let tls = ClientConfig::builder_with_protocol_versions(&[&rustls::version::TLS13])
            .with_root_certificates(roots)
            .with_client_cert_resolver(Arc::new(SingleCertAndKey::from(key)));
        let connector = Connector::from_config(server, &name, tls)?;
        let rt = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .enable_all()
            .build()?;
        rt.block_on(async {
            let socket = std::os::unix::net::UnixStream::from(cancel);
            socket.set_nonblocking(true)?;
            let mut stop=UnixStream::from_std(socket)?;
            let mut byte=[0u8];
            tokio::select! {biased; value=stop.read(&mut byte)=>value.map(|_|()), outcome=serve(connector,target,device)=>outcome}
        })?;
        Ok(())
    };
    match run() {
        Ok(()) => 0,
        Err(_) => 1,
    }
}
