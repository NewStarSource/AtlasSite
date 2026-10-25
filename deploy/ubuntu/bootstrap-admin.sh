#!/usr/bin/env bash
set -euo pipefail
if [ "$(id -u)" -ne 0 ] || [ "$#" -ne 1 ]; then
 echo 'Usage: sudo newstar-bootstrap-admin <existing-atlas-user-id-or-unique-alias>' >&2
 exit 64
fi
systemctl stop newstar-atlas.service
trap 'systemctl start newstar-atlas.service' EXIT
setpriv --reuid newstar-atlas --regid newstar-atlas --init-groups -- /opt/newstar/current/bin/staratlas -config /etc/newstar/atlas.json admin-bootstrap "$1"
