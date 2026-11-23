"""Run a NexusLab command with an existing Caddy ACME authority and TLS target.

Usage: unshare --mount --propagation private python3 local_acme.py /path/to/caddy COMMAND [ARG ...]
The host trust store and system hosts file are unchanged. The child sees three
NEXUS_LAB_ variables (CA, DIRECTORY, TARGET) and trusts the disposable CA.
"""
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.request


def main():
    if len(sys.argv) < 3 or os.readlink('/proc/self/ns/mnt') == os.readlink('/proc/1/ns/mnt'):
        raise SystemExit(__doc__)
    subprocess.run(['mount', '--make-rprivate', '/'], check=True)
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='nexus-acme-') as tmp:
        work = Path(tmp)
        def openssl(*args):
            subprocess.run(['openssl', *args], cwd=tmp, check=True, stdout=subprocess.DEVNULL,
                           stderr=subprocess.DEVNULL)
        hosts = work/'hosts'
        hosts.write_bytes(Path('/etc/hosts').read_bytes()+b'\n127.0.0.1 nexus.test target.example.test\n')
        subprocess.run(['mount', '--bind', hosts, '/etc/hosts'], check=True)
        openssl('req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256',
                '-nodes', '-days', '2', '-subj', '/CN=Nexus Lab CA', '-addext',
                'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign',
                '-keyout', 'ca.key', '-out', 'ca.pem')
        openssl('req', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes',
                '-subj', '/CN=target.example.test', '-keyout', 'tls.key', '-out', 'tls.csr')
        (work/'extensions').write_text('subjectAltName=IP:127.0.0.1,DNS:target.example.test\nextendedKeyUsage=serverAuth\n')
        openssl('x509', '-req', '-in', 'tls.csr', '-CA', 'ca.pem', '-CAkey', 'ca.key',
                '-CAcreateserial', '-days', '1', '-extfile', 'extensions', '-out', 'tls.pem')
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            addr = '127.0.0.1:'+str(s.getsockname()[1])
        root = {'certificate': str(work/'ca.pem'), 'private_key': str(work/'ca.key')}
        site = {'listen': [addr], 'protocols': ['h1','h2'], 'automatic_https': {'disable':True},
                'tls_connection_policies': [{}], 'routes': [{'handle': [
                    {'handler':'acme_server','ca':'lab','lifetime':'5m',
                     'challenges':['http-01','tls-alpn-01']}, {'handler':'static_response','body':'Lab site'}]}]}
        cfg = {'admin':{'disabled':True},'apps':{'pki':{'certificate_authorities':{
                    'lab':{'root':root,'install_trust':False,'intermediate_lifetime':'12h'}}},
               'tls':{'certificates':{'load_files':[{'certificate':str(work/'tls.pem'),'key':str(work/'tls.key')}]}},
               'http':{'servers':{'fixture':site}}}}
        config = work/'ca.json'
        config.write_text(json.dumps(cfg))
        env = dict(os.environ, HOME=tmp, XDG_CONFIG_HOME=tmp+'/config', XDG_DATA_HOME=tmp+'/data')
        directory = 'https://'+addr+'/acme/lab/directory'
        ca = str(work/'ca.pem')
        log_path = work/'server.log'
        with log_path.open('wb') as log:
            proc = subprocess.Popen([sys.argv[1], 'run','--config',config], env=env, stdout=log, stderr=log)
            try:
                deadline = time.monotonic()+12
                tls = ssl.create_default_context(cafile=ca)
                while time.monotonic()<deadline and proc.poll() is None:
                    try:
                        with urllib.request.urlopen(directory,context=tls,timeout=1) as resp:
                            if 'newOrder' in json.load(resp):
                                break
                    except (OSError,ValueError):
                        time.sleep(.1)
                else:
                    raise RuntimeError('local ACME startup: '+log_path.read_text()[-1200:])
                env = dict(os.environ, NEXUS_LAB_CA=ca,NEXUS_LAB_DIRECTORY=directory,
                           NEXUS_LAB_TARGET=addr,SSL_CERT_FILE=ca)
                subprocess.run(sys.argv[2:],env=env,check=True)
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    try:
                        proc.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()


if __name__ == '__main__':
    main()
