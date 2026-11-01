"""Real dual-service HTTP tests. No graphical browser, real users, or external mail."""
import argparse
from datetime import date, timedelta
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
import shutil
import sys
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


def check_collaborators(client, staff, account, atlas, account_bin, account_dir, tools, guide_path):
    helper, username, password = Browser(), "toolhelper", "synthetic helper password 123"
    generated = subprocess.run([str(account_bin), "generate-invite"], cwd=account_dir, check=True, capture_output=True, text=True, encoding="utf-8")
    invite = (generated.stdout or generated.stderr).strip().split("邀请码：")[-1].strip()
    expect(helper.request(account + "/api/v1/auth/register", {"username": username, "password": password, "invite_code": invite}, True), 201, "register collaborator")
    authorize(helper, account, atlas, username, password)
    helper_id = json.loads(helper.request(atlas + "/api/v1/session")[2])["user"]["id"]
    base = "/admin/communities/pixel-game-dev/collaborators"
    expect(client.request(atlas + base), 200, "collaboration management page")
    qualification = {"user_id": helper_id, "version": "0", "eligible": "1", "confirm": "verified", "reason": "合成既有成员资格核验", "standard_url": atlas + "/c/pixel-game-dev/guide"}
    expect(form_post(client, atlas, base + "/eligibility", qualification), 303, "team registers existing qualification")
    invitation = {"user_id": helper_id, "kind": "guide", "version": "0", "authority": "协助维护新人指引", "expires": (date.today() + timedelta(days=30)).isoformat()}
    expect(form_post(staff, atlas, base + "/invite", invitation), 303, "representative invites collaborator")
    expect(helper.request(atlas + tools), 403, "invitation requires active consent")
    inbox = expect(helper.request(atlas + "/my/community-tools"), 200, "personal collaboration inbox")[2]
    grant_id = re.search(r'/my/community-tools/([^/"?]+)/accept', inbox).group(1)
    assert "社群协作邀请已更新" in expect(helper.request(atlas + "/notifications"), 200, "collaboration notification")[2]
    authorize(helper, account, atlas, username, password, form_post(helper, atlas, "/auth/reauthenticate", {}))
    expect(form_post(helper, atlas, "/my/community-tools/" + grant_id + "/accept", {"version": "1", "confirm": "accept"}), 303, "accept collaboration")
    tool_html = expect(helper.request(atlas + tools), 200, "scoped tools list")[2]
    assert "新建新人指引" in tool_html and "新建公告" not in tool_html and "新建讨论合集" not in tool_html
    for path in ("/admin/team", base, "/admin/communities/pixel-game-dev", tools + "/new?kind=announcement"):
        expect(helper.request(atlas + path), 403, "collaborator cannot expand authority")
    edit = {"kind": "guide", "version": "1", "title": "合成新人指引", "content": "整理者维护的指引", "description": "", "status": "published", "order": "0", "summary": "整理者更新"}
    expect(form_post(helper, atlas, guide_path, edit), 303, "collaborator edits authorized guide")
    assert "整理者维护的指引" in expect(helper.request(atlas + "/c/pixel-game-dev/guide"), 200, "public collaborator update")[2]
    expect(form_post(staff, atlas, base + "/" + grant_id + "/revoke", {"version": "2"}), 303, "revoke collaborator")
    expect(helper.request(atlas + tools), 403, "revoked collaborator denied")
    invitation["version"] = "3"
    expect(form_post(staff, atlas, base + "/invite", invitation), 303, "reinvite collaborator")
    expect(helper.request(atlas + tools), 403, "reinvitation requires fresh consent")
    expect(form_post(helper, atlas, "/my/community-tools/" + grant_id + "/accept", {"version": "4", "confirm": "accept"}), 303, "accept renewed collaboration")
    expect(form_post(helper, atlas, "/my/community-tools/" + grant_id + "/leave", {"version": "5"}), 303, "voluntary collaboration withdrawal")
    expect(helper.request(atlas + tools), 403, "withdrawn collaborator denied")
    invitation["version"] = "6"
    expect(form_post(staff, atlas, base + "/invite", invitation), 303, "new invitation after withdrawal")
    expect(form_post(helper, atlas, "/my/community-tools/" + grant_id + "/accept", {"version": "7", "confirm": "accept"}), 303, "new active collaboration")
    print("PASS tool collaborators: real qualification/invitation/consent forms, scoped edits, revoke, reinvite and voluntary withdrawal")
    return helper


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
    tools = "/admin/communities/pixel-game-dev/tools"
    form = staff.form(atlas + tools + "/new?kind=announcement")
    announcement = dict(form.inputs)
    announcement.update(title="合成维护公告", content="**合成公告正文**", description="公开说明", status="draft", order="0", summary="创建合成草稿", pinned="1")
    location = expect(form_post(staff, atlas, tools, announcement), 303, "create announcement draft")[1]["Location"].split("?")[0]
    assert "合成维护公告" not in expect(staff.request(atlas + "/c/pixel-game-dev/announcements"), 200, "draft remains private")[2]
    announcement.update(version="1", status="published", summary="发布合成公告")
    expect(form_post(staff, atlas, location, announcement), 303, "publish announcement")
    public = expect(staff.request(atlas + "/c/pixel-game-dev/announcements"), 200, "public announcement")[2]
    assert "<strong>合成公告正文</strong>" in public and "置顶" in public and "更新于" in public
    expect(form_post(staff, atlas, location, announcement), 409, "stale tool edit rejected")
    assert "版本 2" in expect(staff.request(atlas + location + "/history"), 200, "tool history")[2]
    form = staff.form(atlas + tools + "/new?kind=guide")
    guide = dict(form.inputs); guide.update(title="合成新人指引", content="从这里开始。合成成员标准要求真实共同活动或两名成员确认，首批由团队核验。", status="published", order="0", summary="创建新人指引")
    guide_path = expect(form_post(staff, atlas, tools, guide), 303, "publish guide")[1]["Location"].split("?")[0]
    assert "从这里开始" in expect(staff.request(atlas + "/c/pixel-game-dev/guide"), 200, "public guide")[2]
    post = {"title": "合成合集原帖", "content": "原帖正文", "license": "reserved", "community": "pixel-game-dev", "request_id": __import__("uuid").uuid4().hex}
    post_path = expect(form_post(staff, atlas, "/api/v1/posts", post), 303, "publish collection original")[1]["Location"]
    form = staff.form(atlas + tools + "/new?kind=discussion")
    discussion = dict(form.inputs); discussion.update(title="合成讨论合集", description="按原帖引用", references=post_path, status="published", order="0", summary="创建合集")
    expect(form_post(staff, atlas, tools, discussion), 303, "create discussion collection")
    assert "合成合集原帖" in expect(staff.request(atlas + "/c/pixel-game-dev/collections"), 200, "collection reference")[2]
    expect(form_post(staff, atlas, "/api/v1" + post_path + "/delete", {"version": "1"}), 303, "author withdraws original")
    assert "合成合集原帖" not in expect(staff.request(atlas + "/c/pixel-game-dev/collections"), 200, "withdrawn reference disappears")[2]
    announcement.update(version="2", status="archived", summary="撤下合成公告")
    expect(form_post(staff, atlas, location, announcement), 303, "withdraw announcement")
    assert "合成维护公告" not in expect(staff.request(atlas + "/c/pixel-game-dev/announcements"), 200, "withdrawn announcement private")[2]
    helper = check_collaborators(client, staff, account, atlas, account_bin, account_dir, tools, guide_path)
    expect(form_post(client, atlas, "/admin/representatives/pixel-game-dev/remove", {"version": "1"}), 303, "revoke representative")
    expect(staff.request(atlas + "/admin"), 403, "revoked representative denied")
    announcement.update(version="3", status="published")
    expect(form_post(staff, atlas, location, announcement), 403, "revoked tool maintainer denied")
    expect(helper.request(atlas + tools), 403, "representative revocation invalidates delegated collaboration")
    grant.update(role="member", version="0")
    expect(form_post(client, atlas, "/admin/team", grant), 303, "add team member")
    expect(staff.request(atlas + "/admin/team"), 200, "team member can view team")
    expect(form_post(staff, atlas, "/admin/team", dict(grant, role="admin")), 403, "team member cannot grant admin")
    expect(form_post(client, atlas, "/admin/team/" + owner + "/remove", {"version": "1"}), 409, "last admin retained")
    print("PASS management with real OIDC sessions: representative scope/save/revoke, team roles, CSRF and stale versions")
    print("PASS community tools: real forms, draft/publish/withdraw, pin/history, guide, collection references and authorization revocation")


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
            bootstrap = subprocess.run([str(atlas_bin), "admin-bootstrap", owner], cwd=atlas_dir, capture_output=True)
            assert bootstrap.returncode == 0, ("admin-bootstrap", bootstrap.stdout.decode(errors="replace"), bootstrap.stderr.decode(errors="replace"))
            expect(client.request(atlas + "/admin"), 200, "bootstrapped management page")
            expect(form_post(client, atlas, "/admin/team", {"user_id": owner}), 403, "management requires fresh reauthentication")
            start = form_post(client, atlas, "/auth/reauthenticate", {})
            authorize(client, account, atlas, username, password, start)
            check_management(client, account, atlas, account_bin, atlas_bin, account_dir, atlas_dir, owner)
            recovery_result = expect(form_post(client, account, "/api/v1/security/recovery-code", {"password": password}), 200, "recovery code before password change")
            # The one-time secret stays in the isolated client, never in test output.
            recovery_code = re.search(r"<pre>([^<]+)</pre>", recovery_result[2]).group(1)
            for path in ("/security", "/security/recovery"):
                status_html = expect(client.request(account + path), 200, "security status page")[2]
                assert "可用" in status_html and "有效至" in status_html
                assert recovery_code not in status_html, "one-time recovery secret repeated in status page"
            confirmation = expect(form_post(client, account, "/api/v1/security/reauthenticate", {"password": password}), 303, "native security confirmation")
            assert confirmation[1]["Location"] == "/security"
            assert "确认有效至" in expect(client.request(account + "/security"), 200, "return to security controls")[2]
            print("PASS security status and native confirmation: metadata shown, one-time secret not repeated")
            replacement = "changed daily account password 456"
            expect(client.request(account + "/security/password"), 200, "password settings page")
            changed = form_post(client, account, "/api/v1/security/change-password", {"current_password": password, "password": replacement, "password_confirmation": replacement})
            expect(changed, 200, "daily password change")
            assert "密码已修改" in changed[2]
            assert not json.loads(client.request(account + "/api/v1/auth/session")[2])["authenticated"], "account session survived password change"
            assert not json.loads(client.request(atlas + "/api/v1/session")[2])["authenticated"], "atlas session survived password change"
            expect(client.request(account + "/api/v1/auth/login", {"username": username, "password": password}, True), 401, "old password rejected")
            expect(form_post(client, account, "/api/v1/auth/recover-password", {"username": username, "recovery_code": recovery_code, "password": "should never become password 789"}), 400, "old recovery code rejected after change")
            password = replacement
            authorize(client, account, atlas, username, password)
            assert "未设置" in expect(client.request(account + "/security/recovery"), 200, "recovery state after password change")[2]
            authorize(client, account, atlas, username, password, form_post(client, atlas, "/auth/reauthenticate", {}))
            print("PASS daily password change: real form, old password/recovery code rejected, account and Atlas sessions revoked")
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
            expected_exit = 1
            if not shutil.which("pwsh"):
                portable_configs = Path(root) / "watch-configs"
                portable_configs.mkdir()
                for name, source in zip(("account", "atlas"), watch_configs):
                    shutil.copyfile(source, portable_configs / (name + ".json"))
                binary_dir = Path(root) / "watch-binaries"
                binary_dir.mkdir()
                for name, source in (("staraccount", account_bin), ("staratlas", atlas_bin)):
                    (binary_dir / name).symlink_to(source)
                args = [sys.executable, str(watch.with_suffix(".py")), "--config-dir", str(portable_configs), "--bin-dir", str(binary_dir), "--state-dir", str(Path(root) / "alerts"), "--test-alert"]
                expected_exit = 2  # No external notification channel in an isolated test.
            result = subprocess.run(args, capture_output=True, text=True, encoding="utf-8", errors="replace")
            assert result.returncode == expected_exit and "synthetic:TEST_ALERT" in result.stdout, ("watchdog test alert failed", result.stdout, result.stderr)
            repeated = subprocess.run(args, capture_output=True, text=True, encoding="utf-8", errors="replace")
            assert repeated.returncode == expected_exit and not repeated.stdout.strip(), "unchanged alert repeated"
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
