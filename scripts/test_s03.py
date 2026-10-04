import argparse
from contextlib import closing
import base64
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from html.parser import HTMLParser


class Forms(HTMLParser):
    def __init__(self):
        super().__init__()
        self.inputs = {}
        self.links = []

    def handle_starttag(self, tag, attributes):
        values = dict(attributes)
        if tag == "input" and values.get("name"):
            self.inputs[values["name"]] = values.get("value", "")
        if tag == "a" and values.get("href"):
            self.links.append(values["href"])


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, target):
        return None


class Browser:
    def __init__(self):
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(self.cookies), NoRedirect()
        )

    def request(self, url, values=None, json_body=False, origin=None):
        headers = {}
        payload = None
        if values is not None:
            headers["Origin"] = origin or urllib.parse.urlunsplit(
                (*urllib.parse.urlsplit(url)[:2], "", "", "")
            )
            if json_body:
                payload = json.dumps(values).encode()
                headers["Content-Type"] = "application/json"
                csrf_url = headers["Origin"] + "/api/v1/csrf"
                status, _, content = self.request(csrf_url)
                assert status == 200
                headers["X-CSRF-Token"] = json.loads(content)["csrf_token"]
            else:
                payload = urllib.parse.urlencode(values).encode()
                headers["Content-Type"] = "application/x-www-form-urlencoded"
        try:
            response = self.opener.open(
                urllib.request.Request(url, data=payload, headers=headers), timeout=10
            )
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, response.headers, response.read().decode()

    def form(self, url):
        status, _, content = self.request(url)
        assert status == 200, ("form", status)
        parsed = Forms()
        parsed.feed(content)
        return parsed

    def cookie(self, name):
        return next(cookie.value for cookie in self.cookies if cookie.name == name)


def expect(result, status, label):
    assert result[0] == status, (label, result[0], result[2][:180])
    return result


def wait_ready(url):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url + "/health", timeout=1) as response:
                if json.load(response)["ok"]:
                    return
        except (OSError, urllib.error.URLError):
            time.sleep(0.1)
    raise RuntimeError("service not ready")


def configure(account_bin, atlas_bin, root):
    account_path = root / "account"
    atlas_path = root / "atlas"
    account_path.mkdir()
    atlas_path.mkdir()
    for binary, path in ((account_bin, account_path), (atlas_bin, atlas_path)):
        subprocess.run([str(binary), "init-dev"], cwd=path, check=True, capture_output=True)
    subprocess.run([str(account_bin), "init-oidc"], cwd=account_path, check=True, capture_output=True)
    subprocess.run(
        [str(atlas_bin), "init-oidc", str(account_path / ".local/oidc-client.secret")],
        cwd=atlas_path, check=True, capture_output=True,
    )
    import socket
    listeners = []
    try:
        for _ in range(2):
            listener = socket.socket()
            listener.bind(("127.0.0.1", 0))
            listeners.append(listener)
        account_address, atlas_address = [f"127.0.0.1:{listener.getsockname()[1]}" for listener in listeners]
    finally:
        for listener in listeners:
            listener.close()
    account_origin, atlas_origin = "http://" + account_address, "http://" + atlas_address
    for path, address, origin in ((account_path, account_address, account_origin), (atlas_path, atlas_address, atlas_origin)):
        filename = path / ".local/development.json"
        config = json.loads(filename.read_text())
        config.update(mode="test", address=address, origin=origin)
        config["atlas_origin" if path == account_path else "account_origin"] = atlas_origin if path == account_path else account_origin
        filename.write_text(json.dumps(config))
    return account_path, atlas_path, account_origin, atlas_origin


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--account-bin", type=Path, required=True)
    parser.add_argument("--atlas-bin", type=Path, required=True)
    options = parser.parse_args()
    account_bin, atlas_bin = options.account_bin.resolve(), options.atlas_bin.resolve()
    flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
    with tempfile.TemporaryDirectory(prefix="star-s03-") as directory:
        account_path, atlas_path, account, atlas = configure(account_bin, atlas_bin, Path(directory))
        invite_result = subprocess.run([str(account_bin), "generate-invite"], cwd=account_path, check=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
        invite = (invite_result.stdout or invite_result.stderr).strip().split("邀请码：")[-1].strip()
        assert invite, "invite generation failed"
        processes = []
        def start(binary, path, origin):
            process = subprocess.Popen([str(binary)], cwd=path, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, creationflags=flags)
            processes.append(process)
            wait_ready(origin)
            return process
        try:
            account_process = start(account_bin, account_path, account)
            start(atlas_bin, atlas_path, atlas)
            browser = Browser()
            password = "synthetic integration password"
            username = "s03user"
            expect(browser.request(account + "/api/v1/auth/register", {"username": username, "password": password, "invite_code": invite}, True), 201, "register")

            def authorize(start_result=None, account_username=username, account_password=password):
                result = start_result or browser.request(atlas + "/login")
                if result[0] == 200:
                    continuation = Forms(); continuation.feed(result[2])
                    auth_url = next(link for link in continuation.links if "/authorize?" in link)
                else:
                    expect(result, 303, "start login")
                    auth_url = result[1]["Location"]
                result = browser.request(auth_url)
                expect(result, 302, "authorization")
                login_url = urllib.parse.urljoin(account, result[1]["Location"])
                form = browser.form(login_url)
                values = dict(form.inputs)
                if "username" in values:
                    values.update(username=account_username, password=account_password)
                result = expect(browser.request(account + "/oidc/login", values), 200, "account confirmation")
                continuation = Forms(); continuation.feed(result[2])
                continue_url = next(link for link in continuation.links if "/authorize/callback?" in link)
                result = expect(browser.request(urllib.parse.urljoin(account,continue_url)), 302, "issue code")
                callback = result[1]["Location"]
                return callback, auth_url

            callback, auth_url = authorize()
            expect(browser.request(callback), 303, "callback")
            session = json.loads(expect(browser.request(atlas + "/api/v1/session"), 200, "session")[2])
            assert session["authenticated"] and "subject_id" not in session["user"]
            identity_id = session["user"]["id"]
            expect(browser.request(callback), 400, "callback replay")
            print("PASS standard code + PKCE login; private claims excluded; callback replay rejected")

            form = browser.form(atlas + "/security")
            expect(browser.request(atlas + "/auth/revoke-all", {"gorilla.csrf.Token": form.inputs["gorilla.csrf.Token"]}), 403, "reauth required")
            result = browser.request(atlas + "/auth/reauthenticate", {"gorilla.csrf.Token": form.inputs["gorilla.csrf.Token"]})
            callback, _ = authorize(result)
            expect(browser.request(callback), 303, "reauth callback")
            form = browser.form(atlas + "/security")
            expect(browser.request(atlas + "/auth/revoke-all", {"gorilla.csrf.Token": form.inputs["gorilla.csrf.Token"]}), 303, "revoke atlas sessions")
            assert not json.loads(browser.request(atlas + "/api/v1/session")[2])["authenticated"]
            print("PASS sensitive operations require explicit account reauthentication")

            callback, _ = authorize()
            expect(browser.request(callback), 303, "second login")
            assert json.loads(browser.request(atlas + "/api/v1/session")[2])["user"]["id"] == identity_id
            account_process.terminate(); account_process.wait(10)
            during_outage = json.loads(expect(browser.request(atlas + "/api/v1/session"), 200, "outage session")[2])
            assert during_outage["authenticated"] and during_outage["identity_service_unavailable"]
            expect(browser.request(atlas + "/login"), 503, "outage new login")
            account_process = start(account_bin, account_path, account)
            expect(browser.request(account + "/api/v1/auth/logout", {}, True), 200, "account logout")
            assert not json.loads(browser.request(atlas + "/api/v1/session")[2])["authenticated"]
            print("PASS stable identity; account restart; outage behavior; account-to-atlas revocation")

            callback, _ = authorize()
            expect(browser.request(callback), 303, "third login")
            form = browser.form(account + "/security")
            csrf_value = form.inputs["gorilla.csrf.Token"]
            expect(browser.request(account + "/api/v1/security/deactivate", {"gorilla.csrf.Token": csrf_value, "confirm": "deactivate"}), 403, "deactivate requires reauth")
            expect(browser.request(account + "/api/v1/security/reauthenticate", {"gorilla.csrf.Token": csrf_value, "password": password}), 200, "reauthenticate")
            expect(browser.request(account + "/api/v1/security/deactivate", {"gorilla.csrf.Token": csrf_value, "confirm": "deactivate"}), 200, "deactivate")
            assert not json.loads(browser.request(atlas + "/api/v1/session")[2])["authenticated"]
            expect(browser.request(account + "/api/v1/auth/login", {"username":username,"password":password}, True),401,"deactivated login")
            form = browser.form(account + "/recover")
            expect(browser.request(account + "/api/v1/security/restore", {"gorilla.csrf.Token":form.inputs["gorilla.csrf.Token"],"username":username,"password":password}),200,"restore")
            assert not json.loads(browser.request(atlas + "/api/v1/session")[2])["authenticated"]
            callback, _ = authorize();expect(browser.request(callback),303,"restored login")
            print("PASS deactivation + restoration requires password; old sessions stay revoked")

            callback, auth_url = authorize()
            state_values = urllib.parse.parse_qs(urllib.parse.urlsplit(callback).query)
            bad_values = dict(state_values)
            bad_values["state"] = ["wrong-state"]
            bad_callback = atlas + "/auth/callback?" + urllib.parse.urlencode(bad_values, doseq=True)
            expect(browser.request(bad_callback), 400, "wrong state")
            expect(Browser().request(callback), 400, "wrong browser binding")
            expect(browser.request(callback), 303, "valid bound callback")
            malformed_auth = urllib.parse.parse_qs(urllib.parse.urlsplit(auth_url).query)
            malformed_auth["redirect_uri"] = ["https://untrusted.invalid/callback"]
            result = browser.request(account + "/authorize?" + urllib.parse.urlencode(malformed_auth, doseq=True))
            assert result[0] == 400 and "Location" not in result[1], "unregistered redirect accepted"

            callback, _ = authorize()
            with closing(sqlite3.connect(atlas_path / ".local/development.db")) as database:
                database.execute("UPDATE oidc_flows SET nonce='wrong-nonce'")
                database.commit()
            expect(browser.request(callback),400,"nonce mismatch")

            callback, _ = authorize()
            with closing(sqlite3.connect(account_path / ".local/development.db")) as database:
                database.execute("UPDATE oidc_requests SET expires_at=0")
                database.commit()
            expect(browser.request(callback),400,"expired authorization code")

            callback, _ = authorize()
            code = urllib.parse.parse_qs(urllib.parse.urlsplit(callback).query)["code"][0]
            secret = (atlas_path / ".local/oidc-client.secret").read_text()
            payload = urllib.parse.urlencode({"grant_type":"authorization_code","code":code,"redirect_uri":atlas+"/auth/callback","code_verifier":"incorrect-verifier"}).encode()
            authorization = base64.b64encode(("star-atlas:"+secret).encode()).decode()
            request = urllib.request.Request(account+"/oauth/token",data=payload,headers={"Content-Type":"application/x-www-form-urlencoded","Authorization":"Basic "+authorization})
            try:
                urllib.request.urlopen(request,timeout=10).close()
                raise AssertionError("bad PKCE accepted")
            except urllib.error.HTTPError as error:
                assert error.code == 400
                error.close()
            expect(browser.request(callback),400,"code reuse after exchange attempt")
            print("PASS wrong state/browser/nonce/redirect/PKCE, expired and replayed codes rejected")

            with closing(sqlite3.connect(account_path / ".local/development.db")) as database:
                database.execute("UPDATE users SET status='suspended' WHERE username=?",(username,))
                database.commit()
            assert not json.loads(browser.request(atlas + "/api/v1/session")[2])["authenticated"]
            expect(browser.request(account + "/api/v1/auth/login", {"username":username,"password":password}, True),401,"suspended login")
            print("PASS suspended account denied and status event synchronized")
            print("S03 integration checks complete; no external email sent")
        finally:
            for process in processes:
                if process.poll() is None:
                    process.terminate()
                    process.wait(10)
                stderr = process.stderr.read().decode(errors="replace")
                if stderr:
                    print(stderr[-5000:])


if __name__ == "__main__":
    main()
