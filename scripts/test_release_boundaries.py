"""CLI failure statuses and fresh production-secret preparation, using temporary data."""
import argparse,json,os,socket,subprocess,sys,tempfile
from pathlib import Path
from test_s03 import configure

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--account-bin',type=Path,required=True);p.add_argument('--atlas-bin',type=Path,required=True);args=p.parse_args()
 account=args.account_bin.resolve();atlas=args.atlas_bin.resolve();flags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0
 with tempfile.TemporaryDirectory(prefix='newstar-release-boundaries-') as directory:
  root=Path(directory);ad,td,_,_=configure(account,atlas,root)
  for binary,folder in ((account,ad),(atlas,td)):
   with socket.socket() as listener:
    listener.bind(('127.0.0.1',0));listener.listen()
    path=folder/'.local/development.json';config=json.loads(path.read_text(encoding='utf-8'));config.update(address='127.0.0.1:'+str(listener.getsockname()[1]));config['origin']='http://'+config['address'];path.write_text(json.dumps(config),encoding='utf-8')
    result=subprocess.run([str(binary)],cwd=folder,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15,creationflags=flags)
    assert result.returncode!=0,'listen failure reported success'
  stage=root/'production';script=Path(__file__).with_name('prepare_production.py')
  cmd=[sys.executable,str(script),'--account-bin',str(account),'--atlas-bin',str(atlas),'--account-origin','https://account.example.test','--atlas-origin','https://atlas.example.test','--output',str(stage)]
  subprocess.run(cmd,check=True,capture_output=True,creationflags=flags)
  ac=json.loads((stage/'account.json').read_text(encoding='utf-8'));at=json.loads((stage/'atlas.json').read_text(encoding='utf-8'))
  assert ac['mode']==at['mode']=='production' and ac['mail_mode']=='disabled'
  for config in (ac,at):
   for field in ('database','key_file','oidc_secret_file','signing_key_file','csrf_key_file'):
    if config.get(field):assert config[field].startswith('/var/lib/newstar/'),'non-portable runtime path'
  assert (stage/'account/development.keys').stat().st_size==96
  assert (stage/'account/oidc-client.secret').read_bytes()==(stage/'atlas/oidc-client.secret').read_bytes()
  assert (stage/'account/development.keys').read_bytes()!=(ad/'.local/development.keys').read_bytes(),'development secrets reused'
  assert subprocess.run(cmd,capture_output=True,creationflags=flags).returncode!=0,'production secrets overwritten'
  assert not list(stage.rglob('*.db')),'development database copied'
 print('PASS nonzero startup failure and fresh production config/secrets, matching OIDC client, no development data and no overwrite')
if __name__=='__main__':main()
