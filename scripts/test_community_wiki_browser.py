"""Real Chromium community/Wiki flows in disposable services and synthetic identities."""
import argparse
from datetime import date, timedelta
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

from test_core import authorize, form_post
from test_s03 import configure, expect, wait_ready
from test_wiki_examples import register


def browser_state(client):
    return {"cookies": [{"name": cookie.name, "value": cookie.value, "domain": cookie.domain,
                         "path": cookie.path, "expires": cookie.expires or -1,
                         "httpOnly": any(key.lower() == "httponly" for key in cookie._rest),
                         "secure": cookie.secure, "sameSite": "Lax"} for cookie in client.cookies], "origins": []}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--account-bin", type=Path, required=True)
    parser.add_argument("--atlas-bin", type=Path, required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    args = parser.parse_args()
    account_bin, atlas_bin = args.account_bin.resolve(), args.atlas_bin.resolve()
    artifacts = args.artifacts.resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="newstar-wiki-browser-") as temporary:
        account_dir, atlas_dir, account, atlas = configure(account_bin, atlas_bin, Path(temporary))
        filename = atlas_dir / ".local/development.json"
        config = json.loads(filename.read_text())
        config["wiki_enabled"] = True
        filename.write_text(json.dumps(config))
        processes = []
        try:
            for binary, directory, origin in ((account_bin, account_dir, account), (atlas_bin, atlas_dir, atlas)):
                processes.append(subprocess.Popen([str(binary)], cwd=directory, stdout=subprocess.DEVNULL,
                                                  stderr=subprocess.DEVNULL,
                                                  creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0))
                wait_ready(origin)
            admin, admin_password = register(account_bin, account_dir, account, atlas, "browserreviewer")
            representative, rep_password = register(account_bin, account_dir, account, atlas, "browserrepresentative")
            author, _ = register(account_bin, account_dir, account, atlas, "browserauthor")
            admin_id = json.loads(admin.request(atlas + "/api/v1/session")[2])["user"]["id"]
            rep_id = json.loads(representative.request(atlas + "/api/v1/session")[2])["user"]["id"]
            subprocess.run([str(atlas_bin), "admin-bootstrap", admin_id], cwd=atlas_dir, check=True, capture_output=True)
            subprocess.run([str(atlas_bin), "seed-communities"], cwd=atlas_dir, check=True, capture_output=True)
            authorize(admin, account, atlas, "browserreviewer", admin_password, form_post(admin, atlas, "/auth/reauthenticate", {}))
            grant = {"user_id": rep_id, "community_id": "pixel-game-dev", "version": "0", "authority": "临时页面验收授权",
                     "expires": (date.today() + timedelta(days=30)).isoformat()}
            expect(form_post(admin, atlas, "/admin/representatives", grant), 303, "synthetic representative grant")
            authorize(representative, account, atlas, "browserrepresentative", rep_password,
                      form_post(representative, atlas, "/auth/reauthenticate", {}))
            posts = []
            for number in range(4):
                values = {"title": "浏览器合集原帖 " + str(number), "content": "仅在临时实例中创建的合成正文。", "license": "reserved",
                          "community": "pixel-game-dev", "request_id": uuid.uuid4().hex}
                posts.append(expect(form_post(author, atlas, "/api/v1/posts", values), 303, "collection fixture")[1]["Location"])
            samples = json.loads((Path(__file__).resolve().parents[1] / "examples/wiki/2026-11-public-samples.json").read_text(encoding="utf-8"))["samples"][:2]
            payload = {"atlas": atlas, "account": account, "artifacts": str(artifacts), "posts": posts, "samples": samples,
                       "states": {"admin": browser_state(admin), "representative": browser_state(representative), "author": browser_state(author)}}
            filename = Path(temporary) / "browser.json"
            filename.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            subprocess.run(["node", str(Path(__file__).with_suffix(".cjs"))],
                           env=dict(os.environ, NEWSTAR_COMMUNITY_WIKI_CONFIG=str(filename)), check=True)
        finally:
            for process in processes:
                if process.poll() is None:
                    process.terminate()
                    process.wait(15)


if __name__ == "__main__":
    main()
