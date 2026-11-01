"""Verify both pinned local checkouts; build a reproducible Ubuntu release directory."""
import argparse, hashlib, json, os, shutil, subprocess, sys
from pathlib import Path
from release_licenses import package_licenses

def run(cmd,cwd,env=None):subprocess.run(cmd,cwd=cwd,env=env,check=True)
def output(cmd,cwd):return subprocess.check_output(cmd,cwd=cwd,text=True).strip()
def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--account-dir',type=Path,default=Path(__file__).resolve().parents[2]/'StarAccount');p.add_argument('--output',type=Path,required=True);p.add_argument('--race',action='store_true');args=p.parse_args()
    atlas=Path(__file__).resolve().parents[1];account=args.account_dir.resolve();dest=args.output.resolve()
    if dest.exists():p.error('output must be a new directory')
    dest.mkdir(parents=True);(dest/'bin').mkdir()
    native=dest/'verification';native.mkdir();suffix='.exe' if os.name=='nt' else ''
    meta={'target':'linux/amd64','go':{},'source':{}}
    for root,name in ((account,'account'),(atlas,'atlas')):
        dirty=output(['git','status','--porcelain'],root)
        meta['source'][name]={'commit':output(['git','rev-parse','HEAD'],root),'dirty':bool(dirty)}
        meta['go'][name]=output(['go','version'],root)
        # Race instrumentation makes the real bcrypt checks much slower.
        # Keep the production password cost and allow the complete suite to finish.
        cmd=['go','test','./...','-count=1','-timeout','900s' if args.race else '120s'];
        if args.race:cmd.insert(2,'-race')
        run(cmd,root);run(['go','vet','./...'],root);run(['go','run','golang.org/x/vuln/cmd/govulncheck@v1.8.0','./...'],root)
        run(['go','build','-trimpath','-o',str(native/('star'+name+suffix)),'./cmd/star'+name],root)
        env=dict(os.environ,GOOS='linux',GOARCH='amd64',CGO_ENABLED='0')
        run(['go','build','-trimpath','-buildvcs=true','-o',str(dest/'bin'/('star'+name)),'./cmd/star'+name],root,env)
    for script in ('test_s03.py','test_core.py','test_release_boundaries.py'):
        run([sys.executable,str(atlas/'scripts'/script),'--account-bin',str(native/('staraccount'+suffix)),'--atlas-bin',str(native/('staratlas'+suffix))],atlas)
    run(['node',str(atlas/'scripts/test_ui_actions.cjs')],atlas)
    if sys.platform.startswith('linux'):run(['bash','-n',str(atlas/'scripts/export_recovery_bundle.sh')],atlas)
    shutil.copytree(atlas/'deploy',dest/'deploy');shutil.copytree(atlas/'docs',dest/'docs');shutil.copyfile(atlas/'README.md',dest/'README.md');(dest/'scripts').mkdir()
    for name in ('prepare_production.py','watchdog.py','export_recovery_bundle.sh'):
        shutil.copyfile(atlas/'scripts'/name,dest/'scripts'/name)
    run([sys.executable,str(atlas/'scripts/test_watchdog.py')],atlas)
    package_licenses(atlas,account,dest)
    meta['license_inventory']='licenses/inventory.json'
    meta['license_inventory_sha256']=hashlib.sha256((dest/'licenses/inventory.json').read_bytes()).hexdigest()
    meta['sha256']={f.relative_to(dest).as_posix():hashlib.sha256(f.read_bytes()).hexdigest() for f in (dest/'bin').iterdir()}
    (dest/'manifest.json').write_text(json.dumps(meta,indent=2),encoding='utf-8')
    print('Release prepared. Review manifest dirty flags; deploy only reviewed revisions.')
if __name__=='__main__':main()
