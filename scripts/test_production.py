"""Linux: real TLS+OIDC with isolated CA, two sites and a local HTTPS CONNECT proxy.
No system hosts, trust stores, public DNS, user databases or external services are changed.
"""
import argparse, contextlib, http.client, http.cookiejar, http.server, json, os, select, socket, socketserver, ssl, subprocess, tempfile, threading, time, urllib.error, urllib.parse, urllib.request
from pathlib import Path
from test_s03 import Browser, NoRedirect, configure, expect
from test_core import authorize, form_post

class TLSProxy(http.server.BaseHTTPRequestHandler):
    backends={}
    def log_message(self,*args):pass
    def do_GET(self):self.forward()
    def do_POST(self):self.forward()
    def do_HEAD(self):self.forward()
    def forward(self):
        target=self.backends.get(self.headers.get('Host'))
        if not target:self.send_error(400);return
        headers={key:value for key,value in self.headers.items() if key.lower() not in ('forwarded','x-forwarded-for','x-forwarded-proto','x-forwarded-host','connection','transfer-encoding')}
        headers.update({'X-Forwarded-For':'198.51.100.8','X-Forwarded-Proto':'https'})
        data=self.rfile.read(int(self.headers.get('Content-Length',0)))
        connection=http.client.HTTPConnection(target,timeout=10)
        try:
            connection.request(self.command,self.path,body=data,headers=headers);response=connection.getresponse();body=response.read()
            self.send_response(response.status)
            for key,value in response.getheaders():
                if key.lower() not in ('connection','transfer-encoding','content-length'):self.send_header(key,value)
            self.send_header('Content-Length',str(len(body)));self.end_headers()
            if self.command!='HEAD':self.wfile.write(body)
        except OSError:self.send_error(503)
        finally:connection.close()

class Tunnel(socketserver.BaseRequestHandler):
    tls_port=0
    loopback="127.0.0.1"
    allowed=set()
    def handle(self):
        data=b''
        while b'\r\n\r\n' not in data and len(data)<16384:
            chunk=self.request.recv(4096)
            if not chunk:return
            data+=chunk
        first=data.split(b'\r\n',1)[0].decode('ascii',errors='replace').split()
        if len(first)!=3 or first[0]!='CONNECT' or first[1] not in self.allowed:return
        with socket.create_connection((self.loopback,self.tls_port),timeout=10) as upstream:
            self.request.sendall(b'HTTP/1.1 200 Connection Established\r\n\r\n')
            sockets=[self.request,upstream]
            while True:
                readable,_,_=select.select(sockets,[],[],20)
                if not readable:return
                for source in readable:
                    chunk=source.recv(65536)
                    if not chunk:return
                    (upstream if source is self.request else self.request).sendall(chunk)

class Threads(socketserver.ThreadingTCPServer):
    allow_reuse_address=True
    daemon_threads=True

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--account-bin',type=Path,required=True);parser.add_argument('--atlas-bin',type=Path,required=True);parser.add_argument('--ipv6',action='store_true');args=parser.parse_args()
    if os.name=='nt':parser.error('Run with Linux binaries on Linux/WSL; SSL_CERT_FILE is process-local there')
    account_bin=args.account_bin.resolve();atlas_bin=args.atlas_bin.resolve()
    with tempfile.TemporaryDirectory(prefix='newstar-production-') as directory:
        root=Path(directory);ad,td,_,_=configure(account_bin,atlas_bin,root)
        certificate=root/'cert.pem';key=root/'key.pem'
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(certificate),'-days','1','-subj','/CN=account.example.test','-addext','subjectAltName=DNS:account.example.test,DNS:atlas.other.test'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        loopback='::1' if args.ipv6 else '127.0.0.1'
        class TLSServer(http.server.ThreadingHTTPServer):address_family=socket.AF_INET6 if args.ipv6 else socket.AF_INET
        tls=TLSServer((loopback,0),TLSProxy)
        context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(certificate,key);tls.socket=context.wrap_socket(tls.socket,server_side=True)
        port=tls.server_port;account=f'https://account.example.test:{port}';atlas=f'https://atlas.other.test:{port}'
        for folder,origin,peer in ((ad,account,atlas),(td,atlas,account)):
            path=folder/'.local/development.json';config=json.loads(path.read_text());config.update(mode='production',origin=origin,trusted_proxies=['::1/128'] if args.ipv6 else ['127.0.0.1/32'])
            if args.ipv6:
                with socket.socket(socket.AF_INET6) as listener:listener.bind(('::1',0));backend_port=listener.getsockname()[1]
                config['address']=f'[::1]:{backend_port}'
            config['atlas_origin' if folder==ad else 'account_origin']=peer
            if folder==ad:config['mail_mode']='disabled'
            path.write_text(json.dumps(config));TLSProxy.backends[urllib.parse.urlsplit(origin).netloc]=config['address']
        Tunnel.tls_port=port;Tunnel.loopback=loopback;Tunnel.allowed={urllib.parse.urlsplit(account).netloc,urllib.parse.urlsplit(atlas).netloc}
        Threads.address_family=socket.AF_INET6 if args.ipv6 else socket.AF_INET
        tunnel=Threads((loopback,0),Tunnel);proxy='http://'+('[::1]' if args.ipv6 else loopback)+':'+str(tunnel.server_address[1])
        for server in (tls,tunnel):threading.Thread(target=server.serve_forever,daemon=True).start()
        env=dict(os.environ,SSL_CERT_FILE=str(certificate),HTTPS_PROXY=proxy,HTTP_PROXY=proxy,NO_PROXY='',https_proxy=proxy,http_proxy=proxy,no_proxy='')
        os.environ['NO_PROXY']='';os.environ['no_proxy']=''
        processes=[]; logs=[]

        try:
            for binary,folder in ((account_bin,ad),(atlas_bin,td)):
                log=(root/(binary.name+'.log')).open('w+');logs.append(log)
                processes.append(subprocess.Popen([str(binary)],cwd=folder,env=env,stdout=log,stderr=log))
            client=Browser();client.opener=urllib.request.build_opener(urllib.request.ProxyHandler({'https':proxy}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(certificate))),urllib.request.HTTPCookieProcessor(client.cookies),NoRedirect())
            for origin in (account,atlas):
                deadline=time.monotonic()+20
                while True:
                    try:
                        result=client.request(origin+'/health');last='HTTP '+str(result[0])+' '+result[2][:120]
                        if result[0]==200:break
                    except OSError as error:last=str(error)
                    if time.monotonic()>deadline:
                        for log in logs:log.flush();log.seek(0);print(log.read()[-1000:])
                        raise RuntimeError('production startup failed: '+last)
                    time.sleep(.1)
            invite_result=subprocess.run([str(account_bin),'generate-invite'],cwd=ad,env=env,check=True,capture_output=True,text=True)
            invite=(invite_result.stdout or invite_result.stderr).strip().split('邀请码：')[-1].strip()
            result=expect(client.request(account+'/api/v1/auth/register',dict(username='productionuser',password='Synthetic production password 123',invite_code=invite),True),201,'production registration')
            code=json.loads(result[2])['recovery_code']
            cookies=list(client.cookies);assert cookies and all(cookie.secure for cookie in cookies),'insecure production cookie'
            assert result[1].get('Strict-Transport-Security'),'missing HSTS'
            request=urllib.request.Request(account+'/login',headers={'Sec-Fetch-Site':'cross-site','Sec-Fetch-Mode':'navigate','Sec-Fetch-Dest':'document'})
            with client.opener.open(request,timeout=10) as response:assert response.status==200
            authorize(client,account,atlas,'productionuser','Synthetic production password 123')
            assert json.loads(client.request(atlas+'/api/v1/session')[2])['authenticated']
            expect(form_post(client,atlas,'/api/v1/posts',dict(title='TLS production post',content='Synthetic local-only data',license='reserved',request_id=__import__('uuid').uuid4().hex)),303,'TLS production publish')
            expect(form_post(client,account,'/api/v1/auth/recover-password',dict(username='productionuser',recovery_code=code,password='Replacement production password 456')),200,'TLS code recovery')
            assert not json.loads(client.request(atlas+'/api/v1/session')[2])['authenticated'],'atlas session survived recovery'
            expect(form_post(client,account,'/api/v1/auth/recover-password',dict(username='productionuser',recovery_code=code,password='Another replacement password 789')),400,'recovery replay')
            print('PASS Linux production HTTPS with verified isolated CA, cross-site navigation, Secure cookies, OIDC/PKCE, publish, password recovery and cross-service revocation')
        finally:
            for process in processes:
                if process.poll() is None:process.terminate();process.wait(timeout=15)
            tls.shutdown();tunnel.shutdown();tls.server_close();tunnel.server_close()
            for log in logs:log.close()
if __name__=='__main__':main()
