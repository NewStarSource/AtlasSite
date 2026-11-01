"""Exercise curated public-source samples in disposable services, using synthetic identities.

This is an HTTP/form rehearsal, not graphical QA or a real participant trial.
The runner never accepts an existing service URL or an existing database.
"""
import argparse
from datetime import datetime, timezone
import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.parse

from test_core import authorize, form_post
from test_s03 import Browser, configure, expect, wait_ready


class PageForms(HTMLParser):
    """Read native values from one form, including textareas and selected options."""

    def __init__(self, content):
        super().__init__(convert_charrefs=True)
        self.forms, self.current, self.textarea, self.select = [], None, None, None
        self.feed(content)

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == "form":
            self.current = {"action": values.get("action", ""), "values": {}}
            self.forms.append(self.current)
        elif self.current is not None:
            name = values.get("name")
            if tag == "input" and name:
                if values.get("type") not in ("checkbox", "radio") or "checked" in values:
                    self.current["values"][name] = values.get("value", "")
            elif tag == "textarea" and name:
                self.textarea = name
                self.current["values"][name] = ""
            elif tag == "select" and name:
                self.select = name
            elif tag == "option" and self.select:
                if self.select not in self.current["values"] or "selected" in values:
                    self.current["values"][self.select] = values.get("value", "")

    def handle_data(self, data):
        if self.current is not None and self.textarea:
            self.current["values"][self.textarea] += data

    def handle_endtag(self, tag):
        if tag == "form":
            self.current, self.textarea, self.select = None, None, None
        elif tag == "textarea":
            self.textarea = None
        elif tag == "select":
            self.select = None

    def values(self, action):
        matches = [form["values"] for form in self.forms if form["action"] == action]
        assert len(matches) == 1, "expected one native form for " + action
        return dict(matches[0])


def page_form(client, atlas, path, action):
    content = expect(client.request(atlas + path), 200, "load native form")[2]
    return PageForms(content).values(action)


def source_lines(sample):
    return "\n".join(" | ".join(source[key] for key in ("id", "title", "url", "accessed")) for source in sample["sources"])


def register(binary, directory, account, atlas, username):
    client, password = Browser(), "isolated wiki example password 2026"
    generated = subprocess.run([str(binary), "generate-invite"], cwd=directory, check=True, capture_output=True, text=True, encoding="utf-8")
    invite = (generated.stdout or generated.stderr).strip().split("邀请码：")[-1].strip()
    expect(client.request(account + "/api/v1/auth/register", {"username": username, "password": password, "invite_code": invite}, True), 201, "synthetic registration")
    authorize(client, account, atlas, username, password)
    return client, password


def review(reviewer, atlas, path, number, decision, note):
    action = "/admin" + path + "/versions/" + str(number) + "/review"
    expect(form_post(reviewer, atlas, action, {"decision": decision, "note": note, "confirm": "reviewed"}), 303, "review candidate")


def suggestion(author, atlas, path, base, sample):
    result = expect(form_post(author, atlas, path + "/suggestions", {"base": str(base), "target": "summary", "proposed": sample["suggested_summary"], "evidence": sample["suggestion_evidence"], "license": "CC BY-SA 4.0"}), 303, "submit public suggestion")
    return urllib.parse.parse_qs(urllib.parse.urlsplit(result[1]["Location"]).query)["suggestion"][0]


def resolve(reviewer, atlas, path, identifier, decision, note, status=303):
    return expect(form_post(reviewer, atlas, "/admin" + path + "/suggestions/" + identifier + "/resolve", {"version": "1", "decision": decision, "note": note, "confirm": "reviewed"}), status, "resolve suggestion")


def exercise_sample(author, editor, reviewer, atlas, sample, result):
    def passed(stage):
        result["stages"].append(stage)

    values = page_form(author, atlas, "/wiki/new", "/wiki")
    values.update(title=sample["title"] + " · 线索", summary=sample["summary"], body=sample["body"], sources=source_lines(sample), community="", edit_summary="记录官方公开资料线索", license="CC BY-SA 4.0")
    discovery = expect(form_post(author, atlas, "/wiki", values), 303, "create sourced discovery")[1]["Location"]
    edit = page_form(editor, atlas, discovery + "/edit", discovery + "/edit")
    edit.update(body=sample["body"] + "\n\n## 整理备注\n\n本记录保留公开来源及访问日期。", sources=source_lines(sample), license="CC BY-SA 4.0", edit_summary="第二位合成编辑者整理")
    expect(form_post(editor, atlas, discovery + "/edit", edit), 303, "second editor saves")
    stale = dict(edit, edit_summary="另一位编辑者的旧表单", body=sample["body"] + "\n\n本次冲突输入需保留。")
    conflict = expect(form_post(author, atlas, discovery + "/edit", stale), 409, "stale edit rejected")
    assert PageForms(conflict[2]).values(discovery + "/edit")["body"] == stale["body"], "conflict lost discovery input"
    passed("发现页与双身份冲突")

    values = page_form(author, atlas, "/wiki/entries/new?kind=" + sample["kind"], "/wiki/entries")
    values.update({key: sample[key] for key in ("title", "summary", "body", "status_value")})
    for key in ("first_verified_at", "work_type", "creator", "public_url"):
        if key in sample:
            values[key] = sample[key]
    values.update(sources=source_lines(sample), community="", edit_summary="依据公开资料提交正式条目", public_scope="public", license="CC BY-SA 4.0")
    submission = expect(form_post(author, atlas, "/wiki/entries", values), 303, "submit sourced entry")[1]["Location"]
    path = submission.split("/submissions/")[0]
    result["discovery"], result["entry"] = discovery, path
    expect(editor.request(atlas + submission), 404, "other editor cannot read private submission")
    expect(editor.request(atlas + path), 404, "pending entry not public")
    review(reviewer, atlas, path, 1, "accept", "演练：已核对本样例的官方公开来源与脚注范围")
    published = expect(editor.request(atlas + path), 200, "approved source page")[2]
    assert sample["title"] in published
    for source in sample["sources"]:
        assert source["url"] in published and "wiki-source-" + source["id"] in published
    passed("私有提交、发布与来源")

    old_suggestion = suggestion(editor, atlas, path, 1, sample)
    revision = page_form(author, atlas, path + "/propose", path + "/propose")
    marker = "退回稿的整理备注需要保留。"
    revision.update(body=sample["body"] + "\n\n## 整理备注\n\n" + marker, edit_summary="补充整理备注", license="CC BY-SA 4.0", public_scope="public")
    expect(form_post(author, atlas, path + "/propose", revision), 303, "submit revision")
    review(reviewer, atlas, path, 2, "reject", "演练退回：请将整理备注与事实正文分开，保留来源")
    reopened = page_form(author, atlas, path + "/propose?submission=2", path + "/propose")
    assert marker in reopened["body"], "reopening rejected submission lost its original draft"
    assert reopened["version"] == "2" and reopened["base"] == "1", "resubmission has incorrect conflict base"
    reopened.update(edit_summary="保留原稿并复核公开范围", license="CC BY-SA 4.0", public_scope="public")
    expect(form_post(author, atlas, path + "/propose", reopened), 303, "resubmit rejected draft")
    review(reviewer, atlas, path, 3, "accept", "演练：整理备注明确，事实仍有原来源支持")
    expect(editor.request(atlas + path + "/versions/2"), 404, "rejected revision absent from public history")
    passed("附理由退回与原稿重提")

    resolve(reviewer, atlas, path, old_suggestion, "accepted", "旧建议不可覆盖新的公开版本", 409)
    resolve(reviewer, atlas, path, old_suggestion, "rejected", "公开版本已有修订，请基于当前版本重提")
    fresh = suggestion(editor, atlas, path, 3, dict(sample, suggestion_evidence=sample["suggestion_evidence"] + " 已重新核对版本 3。"))
    resolve(reviewer, atlas, path, fresh, "accepted", "演练：摘要与公开资料一致，采纳")
    assert sample["suggested_summary"] in expect(author.request(atlas + path + "/versions/4"), 200, "adopted version")[2]
    passed("旧建议冲突、具体理由与新建议采纳")

    invalid = {"base": "4", "target": "body", "proposed": "演练脚注错误，输入应保留。[^missing]", "evidence": "保留本次说明便于修正", "license": "CC BY-SA 4.0"}
    failed = expect(form_post(editor, atlas, path + "/suggestions", invalid), 400, "invalid footnote rejected")
    retained = PageForms(failed[2]).values(path + "/suggestions")
    assert retained["proposed"] == invalid["proposed"] and retained["evidence"] == invalid["evidence"], "failed suggestion lost draft"
    passed("建议错误就地提示并保留输入")
    assert sample["title"] in expect(author.request(atlas + "/wiki?q=" + urllib.parse.quote(sample["title"])), 200, "Wiki search")[2]
    assert path not in expect(author.request(atlas + "/search?q=" + urllib.parse.quote(sample["title"])), 200, "ordinary search separation")[2]
    result["status"] = "通过"


def artifact_info(path):
    data = subprocess.run(["go", "version", "-m", str(path)], capture_output=True, text=True, encoding="utf-8", errors="replace", check=True).stdout
    metadata = [line.strip() for line in data.splitlines() if "vcs.revision=" in line or "vcs.modified=" in line]
    return {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "build": metadata}


def write_report(path, pack, samples_path, binaries, results, duration, failure):
    lines = ["# Wiki 六个公开来源示例演练", "", "执行时间：" + datetime.now(timezone.utc).isoformat(), "", "本记录来自临时双服务与三个合成身份的真实 HTTP 表单演练。审核动作由脚本发起；不代表真人参与、独立人工复核或图形浏览器验收。临时实例在结束时清理，没有写入现有服务或服务器。", "", "来源核对日期：" + pack["verified_at"] + "。该日期不代表对象创立时间。", "", "示例文件 SHA-256：`" + hashlib.sha256(samples_path.read_bytes()).hexdigest() + "`", "", "| 示例 | 类型 | 结果 | 已完成步骤 |", "| --- | --- | --- | --- |"]
    for result in results:
        lines.append("| " + result["title"] + " | " + result["kind"] + " | " + result["status"] + " | " + "；".join(result["stages"]) + " |")
    lines += ["", "耗时：" + format(duration, ".2f") + " 秒。", "", "## 实际问题与限制", ""]
    lines.append("本次失败：`" + failure.replace("`", "'").replace("\n", " ") + "`" if failure else "本次自动演练全部通过。人工填表、多人实际试用、窄屏、键盘与关闭 JavaScript 的图形操作仍待验收。")
    lines += ["", "## 构建证据", ""]
    for name, info in binaries.items():
        lines += ["- " + name + " SHA-256：`" + info["sha256"] + "`；" + "，".join(info["build"])]
    lines += ["", "## 来源", ""]
    seen = set()
    for sample in pack["samples"]:
        for source in sample["sources"]:
            if source["url"] not in seen:
                lines.append("- [" + source["title"] + "](" + source["url"] + ")，核对日期 " + source["accessed"] + "。")
                seen.add(source["url"])
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--account-bin", type=Path, required=True)
    parser.add_argument("--atlas-bin", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    options = parser.parse_args()
    account_bin, atlas_bin = options.account_bin.resolve(), options.atlas_bin.resolve()
    samples_path = Path(__file__).resolve().parents[1] / "examples/wiki/2026-11-public-samples.json"
    pack = json.loads(samples_path.read_text(encoding="utf-8"))
    binaries = {"StarAccount": artifact_info(account_bin), "AtlasSite": artifact_info(atlas_bin)}
    results = [{"title": sample["title"], "kind": sample["kind"], "status": "未执行", "stages": []} for sample in pack["samples"]]
    started, failure = time.monotonic(), ""
    try:
        with tempfile.TemporaryDirectory(prefix="star-wiki-examples-") as root:
            account_dir, atlas_dir, account, atlas = configure(account_bin, atlas_bin, Path(root))
            filename = atlas_dir / ".local/development.json"
            config = json.loads(filename.read_text())
            config["wiki_enabled"] = True
            filename.write_text(json.dumps(config))
            processes = []
            try:
                for binary, directory, origin in ((account_bin, account_dir, account), (atlas_bin, atlas_dir, atlas)):
                    processes.append(subprocess.Popen([str(binary)], cwd=directory, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0))
                    wait_ready(origin)
                author, _ = register(account_bin, account_dir, account, atlas, "sampleauthor")
                editor, _ = register(account_bin, account_dir, account, atlas, "sampleeditor")
                reviewer, password = register(account_bin, account_dir, account, atlas, "samplereviewer")
                owner = json.loads(reviewer.request(atlas + "/api/v1/session")[2])["user"]["id"]
                subprocess.run([str(atlas_bin), "admin-bootstrap", owner], cwd=atlas_dir, check=True, capture_output=True)
                authorize(reviewer, account, atlas, "samplereviewer", password, form_post(reviewer, atlas, "/auth/reauthenticate", {}))
                for sample, result in zip(pack["samples"], results):
                    result["status"] = "失败"
                    exercise_sample(author, editor, reviewer, atlas, sample, result)
                    print("PASS " + sample["key"])
            finally:
                for process in processes:
                    if process.poll() is None:
                        process.terminate()
                        process.wait(10)
    except Exception as error:
        failure = type(error).__name__ + ": " + str(error)
        raise
    finally:
        write_report(options.report.resolve(), pack, samples_path, binaries, results, time.monotonic() - started, failure)


if __name__ == "__main__":
    main()
