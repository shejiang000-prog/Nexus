"""Offline amd64 interop. Run under unshare --mount --net with NODE and ASSET_DIR."""
import ipaddress
import argparse
import hashlib
import contextlib
import json
import os
from pathlib import Path
import secrets
import select
import socket
import socketserver
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.parse as url
import zipfile

PUBLIC=str(ipaddress.IPv4Address((40 << 24) | secrets.randbits(24)))
DOMAIN='node.example.test'

def run(*args, **kwargs):
    return subprocess.run(args,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,**kwargs).stdout

def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1',0))
        return s.getsockname()[1]

def wait(ok, deadline=10):
    end=time.monotonic()+deadline
    while time.monotonic()<end:
        if ok(): return
        time.sleep(.04)
    raise AssertionError("readiness deadline exceeded")

class TCP(socketserver.BaseRequestHandler):
    def handle(self):
        try:
            while data:=self.request.recv(4096):
                self.request.sendall(data)
        except (ConnectionError,ssl.SSLError):
            pass  # A rejected/closed REALITY session can abort the fixture TLS handshake.
class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        data,sock=self.request
        sock.sendto(data,self.client_address)
class Stream(socketserver.ThreadingTCPServer):
    daemon_threads=True
class Datagram(socketserver.ThreadingUDPServer):
    daemon_threads=True

@contextlib.contextmanager
def process(argv, log):
    with log.open('wb') as out:
        proc=subprocess.Popen(argv,stdout=out,stderr=subprocess.STDOUT)
        try: yield proc
        finally:
            if proc.poll() is None: proc.terminate()
            try: proc.wait(4)
            except subprocess.TimeoutExpired: proc.kill();proc.wait()

def write(p,obj):
    p.write_text(json.dumps(obj))

class Client:
    def __init__(self,stack,root,uri,tcp,udp,target=PUBLIC):
        self.a,self.b=port(),port()
        d=Path(stack.enter_context(tempfile.TemporaryDirectory(dir=root)))
        cfg=d/'client.json'
        src=url.urlsplit(uri)
        q=dict(url.parse_qsl(src.query))
        opts=[(self.a,tcp,'tcp'),(self.b,udp,'udp')]
        if src.scheme=='vless':
            v={'inbounds':[{'listen':'127.0.0.1','port':i,'protocol':'dokodemo-door','settings':{'address':target,'port':j,'network':k}}
                            for i,j,k in opts],
                'outbounds':[{'protocol':'vless','settings':{'vnext':[{'address':src.hostname,'port':src.port,'users':[{'id':src.username,'encryption':q['encryption'],'flow':q['flow']}]}]},
                              'streamSettings':{'network':'tcp','security':q['security'],'realitySettings':{'serverName':q['sni'],'fingerprint':q['fp'],'password':q['pbk'],'shortId':q['sid']}}}]}
            write(cfg,v)
            args=[root/'opt/nexus-v2/bin/xray','run','-config',cfg]
        else:
            write(cfg,{'server':uri,'tls':{'ca':str(root/'etc/nexus-v2/cert.pem')},
                       'tcpForwarding':[{'listen':'127.0.0.1:'+str(self.a),'remote':target+':'+str(tcp)}],
                       'udpForwarding':[{'listen':'127.0.0.1:'+str(self.b),'remote':target+':'+str(udp)}],
                       'lazy':True})
            args=[root/'opt/nexus-v2/bin/hysteria','client','-c',cfg]
        self.proc=stack.enter_context(process(args,d/'client.log'))
        def ready():
            if self.proc.poll() is not None:
                raise RuntimeError("upstream client exited")
            try:
                with socket.create_connection(('127.0.0.1',self.a),timeout=.15):
                    return True
            except OSError:
                return False
        wait(ready)
    def tcp(self,timeout=12):
        s=socket.create_connection(('127.0.0.1',self.a),timeout=timeout);s.settimeout(timeout)
        try: echo(s);return s
        except BaseException:
            s.close()
            raise
    def udp(self):
        with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sock:
            sock.settimeout(3);sock.connect(('127.0.0.1',self.b))
            sock.send(b'datagram');assert sock.recv(100)==b'datagram'

def echo(s):
    payload=os.urandom(32);s.sendall(payload);assert s.recv(32,socket.MSG_WAITALL)==payload

def refused(probe):
    try:
        result=probe()
    except (OSError,AssertionError):
        return
    if result is not None:
        result.close()
    raise AssertionError("unexpected successful echo")

def observe_existing(stream,start,unrelated=None):
    """Time starts before the administrative CLI. Sample for three seconds after it returns."""
    end=time.monotonic()+3
    while True:
        if unrelated is not None:
            echo(unrelated)
        try:
            echo(stream)
        except (OSError,AssertionError):
            return False,time.monotonic()-start
        if time.monotonic()>=end:
            return True,time.monotonic()-start
        time.sleep(.05)

def reality_parameters(uri):
    parsed=url.urlsplit(uri)
    query=dict(url.parse_qsl(parsed.query))
    return parsed.username,query['pbk'],query['sid'],query['sni']

def check_mihomo(document,uris):
    """Parse the exact YAML mapping/list/scalar subset emitted by the local exporter."""
    assert document.splitlines()[0]=='proxies:'
    nodes=[]
    for line in document.splitlines()[1:]:
        if line.startswith('  - '):
            node={};nodes.append(node);line='    '+line[4:]
        indent=len(line)-len(line.lstrip())
        assert indent in (4,6)
        dest=node if indent==4 else sub
        key,_,value=line.strip().partition(':')
        assert key and key not in dest
        value=value.strip()
        if not value:
            sub={};dest[key]=sub
        elif value.startswith('"') or value in ('true','false') or value.isdigit():
            dest[key]=json.loads(value)
        else:
            dest[key]=value
    assert len(nodes)==len(uris)==2
    for node,uri in zip(nodes,uris):
        src=url.urlsplit(uri);q=dict(url.parse_qsl(src.query))
        expected={'name':url.unquote(src.fragment),'type':src.scheme,'server':src.hostname,'port':src.port,'udp':True}
        if src.scheme=='vless':
            expected.update(uuid=src.username,flow=q['flow'],network=q['type'],tls=True,servername=q['sni'],
                            **{'client-fingerprint':q['fp'],'reality-opts':{'public-key':q['pbk'],'short-id':q['sid']}})
        else:
            expected.update(password=src.username+':'+src.password,sni=q['sni'],**{'skip-cert-verify':False})
        if node.get('packet-encoding')=='xudp' and src.scheme=='vless':
            expected['packet-encoding']='xudp'
        assert node==expected,"mihomo field mismatch"

def main():
    if not __debug__:
        raise RuntimeError('run without -O to enable all interop assertions')
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('node',type=Path)
    parser.add_argument('asset_dir',type=Path)
    opt=parser.parse_args()
    for ns in ['net','mnt']:
        if os.readlink('/proc/self/ns/'+ns)==os.readlink('/proc/1/ns/'+ns):
            raise RuntimeError('use unshare --mount --net')
    run('mount','--make-rprivate','/')
    run('ip','link','set','lo','up')
    run('ip','addr','add',PUBLIC+'/32','dev','lo')
    os.umask(0o077)
    with contextlib.ExitStack() as st:
        root=Path(st.enter_context(tempfile.TemporaryDirectory(prefix='n21-',dir='/tmp')))
        etc=root/'etc/nexus-v2';etc.mkdir(parents=True)
        bindir=root/'opt/nexus-v2/bin';bindir.mkdir(parents=True)
        for name,digest in [('Xray-linux-64.zip','b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82'),
                            ('hysteria-linux-amd64','fc25edff9948708cb00a4ff01f3bccde677660aca050133bb1ad46c6860362d2')]:
            with (opt.asset_dir/name).open('rb') as f:
                if hashlib.file_digest(f,'sha256').hexdigest()!=digest:
                    raise ValueError('unexpected upstream SHA-256: '+name)
        with zipfile.ZipFile(opt.asset_dir/'Xray-linux-64.zip') as z:
            (bindir/'xray').write_bytes(z.read('xray'))
        (bindir/'hysteria').write_bytes((opt.asset_dir/'hysteria-linux-amd64').read_bytes())
        for f in bindir.iterdir(): f.chmod(0o700)
        run('openssl','req','-x509','-nodes','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256',
            '-days','2','-subj','/CN=node.example.test','-addext','subjectAltName=DNS:node.example.test,DNS:tls.example.test','-keyout',str(etc/'key.pem'),'-out',str(etc/'cert.pem'))
        decoy=st.enter_context(Stream(('127.0.0.1',0),TCP))
        tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.set_alpn_protocols(['h2','http/1.1'])
        tls.load_cert_chain(etc/'cert.pem',etc/'key.pem')
        # Each worker negotiates TLS in its own request thread.
        decoy.socket=tls.wrap_socket(decoy.socket,server_side=True,do_handshake_on_connect=False)
        tcp=st.enter_context(Stream(('',0),TCP));udp=st.enter_context(Datagram(('',0),UDP))
        for srv in [decoy,tcp,udp]: threading.Thread(target=srv.serve_forever,daemon=True).start()
        ports={'hysteria2':port(),'reality':port()}
        hy_stream=None
        write(etc/'deployment.json',{'domain':DOMAIN,'ports':ports,'reality_target':'127.0.0.1:'+str(decoy.server_address[1]),'reality_server_names':['tls.example.test']})
        # These mounts only exist in this test's private mount namespace.
        db=Path('/var/lib/nexus-v2/ledger.db')
        (root/'var/lib/nexus-v2').mkdir(parents=True)
        (root/'etc/hosts').write_text('127.0.0.1 localhost private.example.test '+DOMAIN+'\n')
        for dest in ['etc','opt','var/lib']:
            run('mount','--bind',root/dest,'/'+dest)
        def ledger(*args):
            return run(str(opt.node),'ledger',str(db),*args).decode().strip()
        def reset_reality(args, a_stream, b_stream):
            # Observe real EOF/reset concurrently with the CLI. A read timeout
            # would only show a stalled stream, not the required disconnect.
            start=time.monotonic()
            sockets={a_stream:'A',b_stream:'B'}
            with subprocess.Popen([opt.node,'ledger',str(db),*args],stdout=subprocess.PIPE) as cmd:
                while sockets and time.monotonic()-start<3:
                    ready,_,_=select.select(list(sockets),[],[],max(0,3-(time.monotonic()-start)))
                    for sock in ready:
                        try:
                            assert sock.recv(1)==b'',"unexpected data while waiting for disconnect"
                        except ConnectionResetError:
                            pass
                        elapsed=time.monotonic()-start
                        assert elapsed<=3,"Reality session reset exceeded 3 s"
                        print('Reality',args[1] if args[0]=='compat' else args[0],sockets.pop(sock),
                              'old TCP closed at %.3fs'%elapsed,flush=True)
                out,_=cmd.communicate(timeout=15)
                assert cmd.returncode==0 and b'ALL Reality users' in out
            assert not sockets,"Reality stream remained open after 3 s"
        a,b=ledger('create-user'),ledger('create-user')
        def exported(user,protocol):
            scheme='vless://' if protocol=='reality' else 'hysteria2://'
            return next(line for line in ledger('compat','export',user,'links').splitlines() if line.startswith(scheme))
        for proto in ports:
            print('start',proto,flush=True)
            service=st.enter_context(process([opt.node,'compat-serve',proto],root/(proto+'.log')))
            addr=root/f'var/lib/nexus-v2/compat-{proto}/control.sock'
            def ready():
                if service.poll() is not None: raise ValueError('service failed '+(root/(proto+'.log')).read_text()[-200:])
                return addr.exists()
            wait(ready)
            for ident in [a,b]:ledger('compat','enable',ident,proto)
            def factory(uri,target=PUBLIC):
                return Client(st,root,uri,tcp.server_address[1],udp.server_address[1],target)
            oldlink=exported(a,proto)
            b_link=exported(b,proto)
            if proto=='reality':
                ia,pa,sa,na=reality_parameters(oldlink)
                ib,pb,sb,nb=reality_parameters(b_link)
                assert ia!=ib and (pa,sa,na)==(pb,sb,nb) and na=='tls.example.test'
            ac,bc=factory(oldlink),factory(b_link)
            time.sleep(.4)
            ast=st.enter_context(ac.tcp());bst=st.enter_context(bc.tcp())
            ac.udp();bc.udp()
            def change(action,stream,other):
                if proto=='reality':
                    reset_reality(['compat',action,a,proto],stream,other)
                    # Original B link and client reconnect after the reset.
                    return st.enter_context(bc.tcp())
                start=time.monotonic();ledger('compat',action,a,proto)
                live,elapsed=observe_existing(stream,start,other)
                print(proto,action,'A old TCP closed at %.3fs'%elapsed,flush=True)
                assert not live
                return other
            bst=change('rotate',ast,bst)
            ledger('compat','apply',proto)
            echo(bst)  # Idempotent replay must preserve the reconnected B stream.
            newlink=exported(a,proto)
            bad,valid=factory(oldlink),factory(newlink)
            time.sleep(.4)
            refused(lambda:bad.tcp(12));refused(bad.udp)
            current=st.enter_context(valid.tcp());valid.udp();echo(bst);bc.udp()
            private=factory(exported(b,proto),'127.0.0.1')
            mixed=factory(exported(b,proto),'private.example.test')
            time.sleep(.4)
            for denied in [private,mixed]:
                refused(lambda:denied.tcp(4));refused(denied.udp)
            bst=change('disable',current,bst)
            disabled=factory(newlink)
            refused(lambda:disabled.tcp(12));refused(disabled.udp)
            echo(bst);bc.udp()
            if proto=='reality':
                ledger('compat','enable',a,proto)
                for args in (['compat','rotate',a,proto], ['compat','disable',a,proto], ['revoke-user',a]):
                    old=exported(a,proto);old_client=factory(old)
                    stream=st.enter_context(old_client.tcp())
                    start=time.monotonic()
                    assert '--keep-connections' in ledger(*args,'--keep-connections')
                    live,elapsed=observe_existing(stream,start,bst)
                    assert live
                    print('Reality keep',args[1] if args[0]=='compat' else args[0],
                          'A and B live at %.3fs'%elapsed,flush=True)
                    refused(lambda:old_client.tcp(4));refused(old_client.udp)
                    if args[0]=='revoke-user':
                        # Tighten the policy after a graceful revoke without
                        # requiring the revoked user to be re-enabled.
                        reset_reality(['revoke-user',a],stream,bst)
                        bst=st.enter_context(bc.tcp());bc.udp()
                        ledger('revoke-user',a);echo(bst)
                    ledger('compat','enable',a,proto)
                    factory(exported(a,proto)).tcp().close()
                # Revoke an enabled user with the default policy as well.
                active_link=exported(a,proto)
                reset_reality(['revoke-user',a],st.enter_context(factory(active_link).tcp()),bst)
                bst=st.enter_context(bc.tcp())
                invalid=factory(active_link);refused(lambda:invalid.tcp(4));refused(invalid.udp)
                ledger('compat','enable',a,proto)
                first_a=exported(a,proto);first_b=exported(b,proto)
                active_a=st.enter_context(factory(first_a).tcp())
                start=time.monotonic();ledger('compat','rotate-node-key','reality')
                for label,stream in [('A',active_a),('B',bst)]:
                    live,elapsed=observe_existing(stream,start,hy_stream)
                    assert not live,'old Reality node stream still active'
                    print('node key rotation: %s old TCP observed closed at %.3fs'%(label,elapsed),flush=True)
                for user,old in [(a,first_a),(b,first_b)]:
                    fresh=exported(user,proto)
                    before,after=reality_parameters(old),reality_parameters(fresh)
                    assert before[0]==after[0] and before[1]!=after[1] and before[2]!=after[2] and before[3]==after[3]
                    invalid=factory(old)
                    refused(lambda:invalid.tcp(12));refused(invalid.udp)
                    usable=factory(fresh);usable.tcp().close();usable.udp()
                echo(hy_stream)
            print(proto,'2 users TCP/UDP; old exports and private destinations rejected',flush=True)

            old_b=exported(b,proto)
            service.terminate()
            service.wait(8)
            try:
                ledger('compat','rotate',b,proto)
            except subprocess.CalledProcessError:
                pass
            else:
                raise AssertionError("offline apply unexpectedly succeeded")
            def status():
                return next(row for row in json.loads(ledger('compat','status',b)) if row['protocol']==proto)
            pending=status()
            assert pending['result']!='applied' and pending['applied_generation']!=pending['generation']
            # A legacy cache belongs to an earlier service instance. Startup must use the DB.
            (addr.parent/'active.json').write_text('invalid stale cache')
            service=st.enter_context(process([opt.node,'compat-serve',proto],root/(proto+'-restarted.log')))
            wait(ready)
            revoked=factory(old_b)
            refused(lambda:revoked.tcp(12));refused(revoked.udp)
            ledger('compat','apply',proto)
            confirmed=status()
            assert confirmed['result']=='applied' and confirmed['applied_generation']==pending['generation']
            restored=factory(exported(b,proto))
            restored_stream=st.enter_context(restored.tcp());restored.udp()
            if proto=='hysteria2':hy_stream=restored_stream
            assert not (addr.parent/'active.json').exists()
            print(proto,'offline rotation survived restart; old credential rejected before replay; same generation confirmed',flush=True)

        check_mihomo(ledger('compat','export',b,'mihomo'),ledger('compat','export',b,'links').splitlines())
        print('mihomo fields checked',flush=True)

if __name__=='__main__': main()
