#!/usr/bin/env bash
set -euo pipefail
umask 077
: "${NEWSTAR_BACKUP_RECIPIENT:?Configure an age recipient}"
: "${NEWSTAR_BACKUP_DESTINATION:?Configure an SSH rsync destination}"
[[ "$NEWSTAR_BACKUP_DESTINATION" == *:* && "$NEWSTAR_BACKUP_DESTINATION" != -* ]] || { echo 'Expected SSH rsync destination' >&2; exit 2; }
command -v age >/dev/null
command -v rsync >/dev/null
work=$(mktemp -d /var/lib/newstar/export-XXXXXX)
restart=0
cleanup() {
 result=$?
 if (( result )); then
  install -d -o newstar -g newstar -m 0700 /var/lib/newstar/monitor
  touch /var/lib/newstar/monitor/offsite-failed
  chown newstar:newstar /var/lib/newstar/monitor/offsite-failed
 fi
 if (( restart )); then systemctl start newstar-account.service newstar-atlas.service; fi
 # Delete only the checked, fixed-prefix temporary export directory.
 case "$work" in /var/lib/newstar/export-*) rm -rf -- "$work" ;; esac
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
restart=1
systemctl stop newstar-atlas.service newstar-account.service
archive="$work/newstar-$(date -u +%Y%m%dT%H%M%SZ).tar.age"
# Full short-lived recovery set includes current journals, anchors, private DB and keys.
tar -C / --exclude='*.backups' -cf - etc/newstar var/lib/newstar/account var/lib/newstar/atlas | age -r "$NEWSTAR_BACKUP_RECIPIENT" -o "$archive"
systemctl start newstar-account.service newstar-atlas.service
restart=0
sha256sum "$archive" | sed "s|$work/||" > "$archive.sha256"
rsync --protect-args -- "$archive" "$archive.sha256" "$NEWSTAR_BACKUP_DESTINATION"

install -d -o newstar -g newstar -m 0700 /var/lib/newstar/monitor
date -u +%s > /var/lib/newstar/monitor/offsite-ok
chown newstar:newstar /var/lib/newstar/monitor/offsite-ok
chmod 0600 /var/lib/newstar/monitor/offsite-ok
rm -f -- /var/lib/newstar/monitor/offsite-failed
