"""Create a NEW production configuration set, never reuse development secrets."""
import argparse, json, os, subprocess, tempfile, shutil
from pathlib import Path
from urllib.parse import urlsplit

def origin(value):
    u=urlsplit(value)
    if u.scheme!='https' or not u.hostname or u.path or u.query or u.fragment or u.username or u.password or u.hostname.endswith('.invalid'):
        raise argparse.ArgumentTypeError('Use your purchased HTTPS domain, without a path')
    return value

def prepare(args):
    output=args.output.resolve()
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    flags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0
    # Initializers run in an isolated temporary directory, with newly generated keys.
    with tempfile.TemporaryDirectory(prefix='newstar-production-') as temp:
        root=Path(temp);account=root/'account';atlas=root/'atlas';account.mkdir();atlas.mkdir()
        def run(binary,cwd,*options):
            subprocess.run([str(binary.resolve()),*options],cwd=cwd,check=True,capture_output=True,creationflags=flags)
        run(args.account_bin,account,'init-dev');run(args.account_bin,account,'init-oidc')
        run(args.atlas_bin,atlas,'init-dev');run(args.atlas_bin,atlas,'init-oidc',str(account/'.local/oidc-client.secret'))
        for name,folder in [('account',account),('atlas',atlas)]:
            dest=output/name;dest.mkdir(mode=0o700)
            config=json.loads((folder/'.local/development.json').read_text(encoding='utf-8'))
            config.update(mode='production',origin=args.account_origin if name=='account' else args.atlas_origin,trusted_proxies=['127.0.0.1/32'])
            config['atlas_origin' if name=='account' else 'account_origin']=args.atlas_origin if name=='account' else args.account_origin
            runtime='/var/lib/newstar/'+name
            config['database']=runtime+'/'+name+'.db'
            for key in ['key_file','oidc_secret_file','signing_key_file','csrf_key_file']:
                if config.get(key):
                    source=folder/config[key];shutil.copyfile(source,dest/source.name);os.chmod(dest/source.name,0o600)
                    config[key]='/var/lib/newstar/'+name+'/'+source.name
            if name=='account':config['mail_mode']='disabled'
            else:config['support_url']=args.support_url or ''
            target=output/(name+'.json');target.write_text(json.dumps(config,ensure_ascii=False,indent=2),encoding='utf-8');os.chmod(target,0o600)
    print('Created new production configuration and secrets. Protect this directory; no credentials printed.')

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--account-bin',type=Path,required=True);p.add_argument('--atlas-bin',type=Path,required=True)
    p.add_argument('--account-origin',type=origin,required=True);p.add_argument('--atlas-origin',type=origin,required=True)
    p.add_argument('--support-url',default='');p.add_argument('--output',type=Path,required=True)
    prepare(p.parse_args())
