"""Collect project and dependency notices for the exact local Go build inputs."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


def json_objects(raw):
    decoder = json.JSONDecoder()
    while raw.strip():
        raw = raw.lstrip()
        value, end = decoder.raw_decode(raw)
        yield value
        raw = raw[end:]


def package_licenses(atlas, account, destination):
    root = destination / "licenses"
    root.mkdir()
    inventory = {"projects": {}, "modules": {}, "toolchains": {}}

    def copy_notice(source, target):
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
        return {"file": target.relative_to(destination).as_posix(), "sha256": hashlib.sha256(target.read_bytes()).hexdigest()}

    def notices(directory, target):
        results = []
        for source in sorted(directory.rglob("*")):
            if source.is_file() and source.name.upper().startswith(("LICENSE", "COPYING", "NOTICE", "COPYRIGHT", "PATENTS", "AUTHORS")):
                results.append(copy_notice(source, target / source.relative_to(directory)))
        if not results:
            raise RuntimeError("Missing dependency notices: " + target.name)
        return results

    for project, directory in (("atlas", atlas), ("account", account)):
        inventory["projects"][project] = [copy_notice(directory / name, root / project / name) for name in ("LICENSE", "NOTICE.md")]
        # Include native verification binaries and the shipped Linux/amd64 target.
        environments = [dict(os.environ), dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")]
        raw = "\n".join(subprocess.check_output(["go", "list", "-deps", "-json", "./cmd/star" + project], cwd=directory, env=env, text=True, encoding="utf-8") for env in environments)
        for package in json_objects(raw):
            module = package.get("Module", {})
            if not module or module.get("Main"):
                continue
            if module.get("Replace"):
                raise RuntimeError("Release notice collection requires unmodified module dependencies")
            key = module["Path"] + "@" + module["Version"]
            if key not in inventory["modules"]:
                folder = root / "modules" / module["Path"] / module["Version"]
                inventory["modules"][key] = {"path": module["Path"], "version": module["Version"], "used_by": [], "notices": notices(Path(module["Dir"]), folder)}
            if project not in inventory["modules"][key]["used_by"]:
                inventory["modules"][key]["used_by"].append(project)
        goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], cwd=directory, text=True).strip())
        version = subprocess.check_output(["go", "version"], cwd=directory, text=True).strip()
        inventory["toolchains"][project] = {"version": version, "notices": [copy_notice(goroot / name, root / project / ("GO-" + name)) for name in ("LICENSE", "PATENTS") if (goroot / name).is_file()]}
    (root / "inventory.json").write_text(json.dumps(inventory, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return inventory
