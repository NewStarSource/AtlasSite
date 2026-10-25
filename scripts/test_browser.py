"""Launch isolated synthetic services for Playwright desktop/mobile browser checks."""
import argparse,json,os,subprocess,tempfile
from pathlib import Path
from test_s03 import configure,wait_ready

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--account-bin',type=Path,required=True);p.add_argument('--atlas-bin',type=Path,required=True);p.add_argument('--artifacts',type=Path);args=p.parse_args()
 account_bin=args.account_bin.resolve();atlas_bin=args.atlas_bin.resolve();flags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0
 with tempfile.TemporaryDirectory(prefix='newstar-browser-') as directory:
  account_dir,atlas_dir,account,atlas=configure(account_bin,atlas_bin,Path(directory));invites=[];processes=[]
  for _ in range(2):
   result=subprocess.run([str(account_bin),'generate-invite'],cwd=account_dir,check=True,capture_output=True,text=True,encoding='utf-8',creationflags=flags)
   invites.append((result.stdout or result.stderr).strip().split('邀请码：')[-1].strip())
  try:
   for binary,folder,origin in ((account_bin,account_dir,account),(atlas_bin,atlas_dir,atlas)):
    processes.append(subprocess.Popen([str(binary)],cwd=folder,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=flags));wait_ready(origin)
   artifacts=args.artifacts.resolve() if args.artifacts else None
   if artifacts:artifacts.mkdir(parents=True,exist_ok=True)
   config=Path(directory)/'browser.json';config.write_text(json.dumps(dict(account=account,atlas=atlas,invites=invites,artifacts=str(artifacts) if artifacts else None)),encoding='utf-8')
   env=dict(os.environ,NEWSTAR_BROWSER_TEST_CONFIG=str(config))
   subprocess.run(['node',str(Path(__file__).with_suffix('.cjs'))],check=True,env=env)
  finally:
   for process in processes:
    if process.poll() is None:process.terminate();process.wait(timeout=15)
if __name__=='__main__':main()
