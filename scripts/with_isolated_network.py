"""Run local-only Linux checks in a disposable user/network namespace.

Example: python3 scripts/with_isolated_network.py -- go test -race ./... -count=1 -timeout 900s
No host routes, firewall settings, services or global proxy settings are changed.
Dependencies must already be available: this namespace only enables loopback.
"""
import argparse
import shutil
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not sys.platform.startswith("linux"):
        parser.error("Run inside Linux/WSL")
    if not command:
        parser.error("Provide a local test command after --")
    if not shutil.which("unshare") or not shutil.which("ip"):
        parser.error("unshare and ip are required; no host-network fallback is performed")
    # The shell program is fixed. User arguments are passed as argv, never interpolated.
    setup = "set -eu\nip link set lo up\nexec \"$@\""
    result = subprocess.run(["unshare", "--user", "--map-root-user", "--net", "--",
                             "sh", "-c", setup, "isolated-local-check", *command])
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
