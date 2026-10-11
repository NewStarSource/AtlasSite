"""Real dual-service HTTP tests. No graphical browser, real users, or external mail."""
import argparse
from datetime import date, timedelta
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from test_s03 import Browser, Forms, configure, expect, wait_ready


def form_post(client, origin, path, values):
    token = json.loads(expect(client.request(origin + "/api/v1/csrf"), 200, "csrf")[2])["csrf_token"]
    return client.request(origin + path, dict(values, **{"gorilla.csrf.Token": token}))


def authorize(client, account, atlas, username, password, start=None):
    result = start or client.request(atlas + "/login")
    if result[0] == 200:
        parsed = Forms(); parsed.feed(result[2])
        target = next(link for link in parsed.links if "/authorize?" in link)
    else:
        target = expect(result, 303, "login start")[1]["Location"]
    target = expect(client.request(target), 302, "authorize")[1]["Location"]
    form = client.form(urllib.parse.urljoin(account, target))
    values = dict(form.inputs)
    if "username" in values:
        values.update(username=username, password=password)
    result = expect(client.request(account + "/oidc/login", values), 200, "confirm account")
    parsed = Forms(); parsed.feed(result[2])
    target = next(link for link in parsed.links if "/authorize/callback?" in link)
    callback = expect(client.request(urllib.parse.urljoin(account, target)), 302, "issue code")[1]["Location"]
    expect(client.request(callback), 303, "complete login")


def check_management(client, account, atlas, account_bin, atlas_bin, account_dir, atlas_dir, owner):
    staff, username, password = Browser(), "managementrep", "synthetic representative password 123"
    generated = subprocess.run([str(account_bin), "generate-invite"], cwd=account_dir, check=True, capture_output=True, text=True, encoding="utf-8")
    invite = (generated.stdout or generated.stderr).strip().split("邀请码：")[-1].strip()
    expect(staff.request(account + "/api/v1/auth/register", {"username": username, "password": password, "invite_code": invite}, True), 201, "register representative")
    authorize(staff, account, atlas, username, password)
    staff_id = json.loads(staff.request(atlas + "/api/v1/session")[2])["user"]["id"]
    expect(staff.request(atlas + "/admin"), 403, "ordinary user denied management")
    subprocess.run([str(atlas_bin), "seed-communities"], cwd=atlas_dir, check=True, capture_output=True)
    grant = {"user_id": staff_id, "community_id": "pixel-game-dev", "version": "0", "authority": "合成临时授权记录", "expires": (date.today() + timedelta(days=60)).isoformat()}
    expect(form_post(client, atlas, "/admin/representatives", grant), 303, "register representative")
    for path in ("/admin", "/admin/representatives", "/admin/log", "/admin/communities/pixel-game-dev"):
        expect(staff.request(atlas + path), 200, "representative page")
    expect(staff.request(atlas + "/admin/team"), 403, "representative cannot read team")
    authorize(staff, account, atlas, username, password, form_post(staff, atlas, "/auth/reauthenticate", {}))
    edit = {"version": "1", "name": "合成管理社群", "description": "真实表单保存", "contact": "合成联系说明"}
    expect(staff.request(atlas + "/admin/communities/pixel-game-dev", edit), 403, "management csrf required")
    expect(form_post(staff, atlas, "/admin/communities/pixel-game-dev", edit), 303, "representative saves community")
    assert "真实表单保存" in expect(staff.request(atlas + "/c/pixel-game-dev"), 200, "public community updated")[2]
    expect(form_post(staff, atlas, "/admin/communities/pixel-game-dev", edit), 409, "old management version rejected")
    expect(form_post(client, atlas, "/admin/representatives/pixel-game-dev/remove", {"version": "1"}), 303, "revoke representative")
    expect(staff.request(atlas + "/admin"), 403, "revoked representative denied")
    grant.update(role="member", version="0")
    expect(form_post(client, atlas, "/admin/team", grant), 303, "add team member")
    expect(staff.request(atlas + "/admin/team"), 200, "team member can view team")
    expect(form_post(staff, atlas, "/admin/team", dict(grant, role="admin")), 403, "team member cannot grant admin")
    expect(form_post(client, atlas, "/admin/team/" + owner + "/remove", {"version": "1"}), 409, "last admin retained")
    print("PASS management with real OIDC sessions: representative scope/save/revoke, team roles, CSRF and stale versions")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--account-bin", type=Path, required=True)
    parser.add_argument("--atlas-bin", type=Path, required=True)
    args = parser.parse_args()
    account_bin, atlas_bin = args.account_bin.resolve(), args.atlas_bin.resolve()
    flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
    with tempfile.TemporaryDirectory(prefix="star-core-http-") as root:
        account_dir, atlas_dir, account, atlas = configure(account_bin, atlas_bin, Path(root))
        processes = []
        try:
            for binary, directory, origin in ((account_bin, account_dir, account), (atlas_bin, atlas_dir, atlas)):
                process = subprocess.Popen([str(binary)], cwd=directory, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, creationflags=flags)
                processes.append(process); wait_ready(origin)
            client, username, password = Browser(), "coreuser", "synthetic core password 123"
            generated = subprocess.run([str(account_bin), "generate-invite"], cwd=account_dir, check=True, capture_output=True, text=True, encoding="utf-8")
            invite = (generated.stdout or generated.stderr).strip().split("邀请码：")[-1].strip()
            expect(client.request(account + "/api/v1/auth/register", {"username": username, "password": password, "invite_code": invite}, True), 201, "register")
            authorize(client, account, atlas, username, password)
            session = json.loads(client.request(atlas + "/api/v1/session")[2])
            owner = session["user"]["id"]
            profile_bio = "## 合成个人简介\n\n**真实排版**\n\n- 阅读\n- 编程\n\n<script>alert(1)</script>\n\n[危险链接](javascript:alert(1))\n\n![图片](https://example.test/tracker.png)"
            expect(form_post(client, account, "/api/v1/security/update-profile", {"display_name": "合成显示名称", "bio": profile_bio, "location": "合成地点", "website": "https://example.test"}), 200, "save Markdown profile")
            assert "**真实排版**" in expect(client.request(account + "/profile"), 200, "profile source refill")[2]
            for path in ("/u/" + owner, "/u/" + owner + "/profile"):
                profile_html = expect(client.request(atlas + path), 200, "Markdown profile through account API")[2]
                assert "<strong>真实排版</strong>" in profile_html and "<ul>" in profile_html
                assert "<script>alert(1)</script>" not in profile_html and "javascript:" not in profile_html and "tracker.png" not in profile_html
            expect(form_post(client, account, "/api/v1/security/update-profile", {"website": "javascript:alert(1)"}), 400, "unsafe website rejected")
            print("PASS real OIDC identity and profile refill/proxy; unsafe website rejected")

            def publish(title):
                form = client.form(atlas + "/new")
                values = dict(form.inputs); values.update(title=title, content="合成正文", license="reserved", community="")
                result = expect(form_post(client, atlas, "/api/v1/posts", values), 303, "publish")
                assert form_post(client, atlas, "/api/v1/posts", values)[1]["Location"] == result[1]["Location"]
                return result[1]["Location"].split("/p/")[-1]
            kept, deleted = publish("保留内容合成"), publish("删除内容合成")
            expect(form_post(client, atlas, "/api/v1/p/" + kept + "/bookmark", {"bookmarked": "true"}), 303, "bookmark")
            assert "保留内容合成" in client.request(atlas + "/my/bookmarks")[2]
            form = client.form(atlas + "/p/" + kept)
            reply = dict(form.inputs); reply.update(content="合成回复", parent_id="")
            expect(form_post(client, atlas, "/api/v1/p/" + kept + "/replies", reply), 303, "reply")
            assert "合成回复" in client.request(atlas + "/p/" + kept)[2]
            assert "保留内容合成" in client.request(atlas + "/search?q=" + urllib.parse.quote("保留内容"))[2]
            expect(form_post(client, atlas, "/api/v1/cases", {"kind": "report", "request_id": __import__("uuid").uuid4().hex, "detail": "合成举报说明"}), 200, "case received")
            print("PASS publish/reply/bookmark/search/case flow with real CSRF and login identity")

            expect(client.request(atlas + "/admin"), 403, "ordinary identity has no admin role")
            subprocess.run([str(atlas_bin), "admin-bootstrap", owner], cwd=atlas_dir, check=True, capture_output=True)
            expect(client.request(atlas + "/admin"), 200, "bootstrapped management page")
            expect(form_post(client, atlas, "/admin/team", {"user_id": owner}), 403, "management requires fresh reauthentication")
            start = form_post(client, atlas, "/auth/reauthenticate", {})
            authorize(client, account, atlas, username, password, start)
            check_management(client, account, atlas, account_bin, atlas_bin, account_dir, atlas_dir, owner)
            expect(form_post(client, atlas, "/api/v1/me/deactivate", {"confirm": "deactivate", "retain_ids": kept}), 303, "cross-service deactivate")
            assert not json.loads(client.request(atlas + "/api/v1/session")[2])["authenticated"]
            anonymous = Browser()
            assert "已注销" in expect(anonymous.request(atlas + "/p/" + kept), 200, "selected retained content")[2]
            expect(anonymous.request(atlas + "/p/" + deleted), 404, "unselected content removed")
            expect(form_post(client, account, "/api/v1/security/restore", {"username": username, "password": password}), 200, "restore account")
            assert not json.loads(client.request(atlas + "/api/v1/session")[2])["authenticated"]
            authorize(client, account, atlas, username, password)
            expect(client.request(atlas + "/p/" + deleted), 404, "restoration cannot resurrect deleted content")
            print("PASS selective retention; account restore; old sessions and deleted content stay revoked")
            # Fresh encrypted archives and an independently launched watchdog, in temporary paths.
            for binary, path in ((account_bin, account_dir), (atlas_bin, atlas_dir)):
                subprocess.run([str(binary), "backup"], cwd=path, check=True, capture_output=True)
            watch_configs = []
            for name, path in (("account", account_dir), ("atlas", atlas_dir)):
                config = json.loads((path / ".local/development.json").read_text(encoding="utf-8"))
                for key in ("database", "key_file", "oidc_secret_file", "signing_key_file", "csrf_key_file"):
                    if key in config and config[key]:
                        config[key] = str((path / config[key]).resolve())
                filename = Path(root) / (name + "-watch.json")
                filename.write_text(json.dumps(config), encoding="utf-8")
                watch_configs.append(filename)
            watch = Path(__file__).with_name("watchdog.ps1").resolve()
            args = ["pwsh", "-NoProfile", "-File", str(watch), "-AccountBinary", str(account_bin), "-AtlasBinary", str(atlas_bin), "-AccountConfig", str(watch_configs[0]), "-AtlasConfig", str(watch_configs[1]), "-AlertDirectory", str(Path(root) / "alerts"), "-TestAlert"]
            result = subprocess.run(args, capture_output=True, text=True, encoding="utf-8", errors="replace")
            assert result.returncode == 1 and "synthetic:TEST_ALERT" in result.stdout, "watchdog test alert failed"
            repeated = subprocess.run(args, capture_output=True, text=True, encoding="utf-8", errors="replace")
            assert repeated.returncode == 1 and not repeated.stdout.strip(), "unchanged alert repeated"
            print("PASS both encrypted backups; independent local watchdog alert injection and deduplication")
        finally:
            for process in processes:
                if process.poll() is None:
                    process.terminate(); process.wait(10)
                output = process.stderr.read().decode(errors="replace")
                if output:
                    # Protocol logs contain only redacted event classes.
                    print(output[-2000:])


if __name__ == "__main__":
    main()
