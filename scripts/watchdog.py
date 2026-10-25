"""Ubuntu/portable monitor; notify only on state changes; retry failed deliveries."""
import argparse, json, os, shutil, subprocess, sys, time, urllib.request, re
from pathlib import Path

def check(config_dir,bin_dir):
    issues=[]
    for name in ('account','atlas'):
        try:
            config=json.loads((config_dir/(name+'.json')).read_text(encoding='utf-8'))
            with urllib.request.urlopen(config['origin']+'/health',timeout=5) as response:
                if not json.load(response).get('ok'):issues.append(name+':HEALTH_FAILED')
        except Exception:issues.append(name+':HEALTH_UNAVAILABLE')
        try:
            suffix='.exe' if os.name=='nt' else ''
            command=[str(bin_dir/('star'+name+suffix)),'-config',str(config_dir/(name+'.json')),'diagnose']
            run_as=os.environ.get('NEWSTAR_'+name.upper()+'_USER','')
            if run_as:
                if os.name!='posix' or os.geteuid()!=0 or not re.fullmatch(r'[a-z_][a-z0-9_-]{0,31}',run_as):raise ValueError('Invalid diagnostic service user')
                command=['setpriv','--reuid',run_as,'--regid',run_as,'--init-groups','--',*command]
            result=subprocess.run(command,capture_output=True,text=True,encoding='utf-8',timeout=30,check=True)
            state=json.loads(result.stdout)
            if state.get('integrity')!='ok':issues.append(name+':DATABASE_INTEGRITY')
            if not state.get('journal_valid'):issues.append(name+':JOURNAL_INVALID')
            if state.get('backup_stale'):issues.append(name+':BACKUP_STALE')
            for counter in ('invalid_backups','failed_jobs','overdue_cases','queue_backlog'):
                if state.get(counter,0)>0:issues.append(name+':'+counter.upper())
            if state.get('mode')=='isolated':issues.append(name+':ISOLATED')
            if state.get('wal_bytes',0)>1024**3:issues.append(name+':WAL_GROWTH')
            space=shutil.disk_usage(Path(config['database']).parent)
            if space.free<5*1024**3 or space.free/space.total<0.2:issues.append(name+':DISK_LOW')
        except Exception:issues.append(name+':DIAGNOSTIC_UNAVAILABLE')
    return issues

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args):return None

def record(state_dir,issues,url='',bearer=''):
    state_dir.mkdir(parents=True,exist_ok=True,mode=0o700)
    signature='|'.join(sorted(set(issues)));observed=state_dir/'observed';notified=state_dir/'notified'
    previous=observed.read_text() if observed.exists() else ''
    event={'time':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'codes':sorted(set(issues)),'recovered':not issues}
    if previous!=signature:
        with (state_dir/'alerts.jsonl').open('a',encoding='utf-8') as f:f.write(json.dumps(event)+'\n')
        observed.write_text(signature,encoding='utf-8');print(json.dumps(event))
    delivered=notified.read_text() if notified.exists() else ''
    if delivered!=signature:
        if not url:return 2
        if not url.startswith('https://'):return 2
        headers={'Content-Type':'application/json'}
        if bearer:headers['Authorization']='Bearer '+bearer
        try:
            with urllib.request.build_opener(NoRedirect()).open(urllib.request.Request(url,data=json.dumps(event).encode(),headers=headers,method='POST'),timeout=10) as response:
                if not 200<=response.status<300:return 2
        except Exception:return 2
        notified.write_text(signature,encoding='utf-8')
    return 1 if issues else 0

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--config-dir',type=Path,default=Path('/etc/newstar'));p.add_argument('--bin-dir',type=Path,default=Path('/opt/newstar/current/bin'));p.add_argument('--state-dir',type=Path,default=Path('/var/lib/newstar/monitor'));p.add_argument('--test-alert',action='store_true');args=p.parse_args()
    issues=check(args.config_dir.resolve(),args.bin_dir.resolve())
    if os.environ.get('NEWSTAR_REQUIRE_OFFSITE')=='1':
        marker=args.state_dir/'offsite-ok'
        try:
            if time.time()-int(marker.read_text())>8*3600:issues.append('offsite:BACKUP_STALE')
        except (OSError,ValueError):issues.append('offsite:BACKUP_MISSING')
        if (args.state_dir/'offsite-failed').exists():issues.append('offsite:BACKUP_FAILED')
    if args.test_alert:issues.append('synthetic:TEST_ALERT')
    return record(args.state_dir,issues,os.environ.get('NEWSTAR_ALERT_URL',''),os.environ.get('NEWSTAR_ALERT_BEARER',''))
if __name__=='__main__':sys.exit(main())
