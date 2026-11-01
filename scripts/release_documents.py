"""Package tracked documentation, keeping sibling-project links resolvable."""
import hashlib
import re
import shutil
import subprocess
from urllib.parse import unquote, urlsplit


def local_links(document):
    text = document.read_text(encoding="utf-8")
    text = re.sub(r"(?ms)^```.*?^```[^\n]*$", "", text)
    for match in re.finditer(r"\[[^\]]*\]\((?:<([^>]+)>|([^\s)]+))\)", text):
        target = urlsplit(match[1] or match[2])
        if not target.scheme and target.path and not target.path.startswith("/"):
            yield unquote(target.path)


def package_documents(atlas, account, destination):
    destination = destination.resolve()
    projects = destination / "projects"
    projects.mkdir()
    for name, source in (("AtlasSite", atlas), ("StarAccount", account)):
        files = subprocess.check_output(["git", "ls-files", "-z", "--", "README.md", "LICENSE", "NOTICE.md", "docs", "examples"], cwd=source).decode("utf-8").split("\0")
        for relative in filter(None, files):
            original = source / relative
            if original.is_symlink() or not original.resolve().is_relative_to(source.resolve()):
                raise RuntimeError("Documentation must not follow external symlinks: " + relative)
            target = projects / name / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(original, target)
    for name in ("LICENSE", "NOTICE.md"):
        shutil.copyfile(projects / "AtlasSite" / name, destination / name)
    (destination / "README.md").write_text("""# 新星内部候选包

本目录是待审核的内部候选，不代表已经部署或开放公测。

- [星图说明](projects/AtlasSite/README.md)
- [账户说明](projects/StarAccount/README.md)
- [内部运行验收](projects/AtlasSite/docs/INTERNAL_OPERATIONS_2026-11.md)
- [两端身份约定](projects/AtlasSite/docs/identity-contract.md)
- [代码与内容许可](NOTICE.md)及[依赖许可清单](licenses/inventory.json)
- [运行与部署模板](deploy/ubuntu)

程序位于 bin，运维脚本位于 scripts，精确提交和摘要见 manifest.json。
projects 保留两端文档的相邻目录关系，包含所引用的规则材料与公开示例，不包含业务数据库、凭据、日志或完整应用源码。文档中的开发命令需在源码检出目录执行。
分发前另行核对对应源码获取方式及许可条件；私有仓库的存在不等于接收者已经取得源码。
""", encoding="utf-8")

    # Only named policy documents referenced by packaged Markdown are eligible.
    policy_source = atlas / "reference/SAfiles"
    tracked_policies = set(filter(None, subprocess.check_output(["git", "ls-files", "-z", "--", "reference/SAfiles"], cwd=atlas).decode("utf-8").split("\0")))
    policy_target = projects / "SAfiles"
    pending = list(projects.rglob("*.md")) + [destination / "README.md", destination / "NOTICE.md"]
    visited = set()
    policies = []
    for document in pending:
        if document in visited:
            continue
        visited.add(document)
        for link in local_links(document):
            target = (document.parent / link).resolve()
            if not target.is_relative_to(destination):
                raise RuntimeError("Documentation link escapes package: " + str(document.relative_to(destination)) + " -> " + link)
            if not target.exists() and target.parent == policy_target:
                original = policy_source / target.name
                if target.suffix != ".md" or not target.name.startswith(("SA", "TECH-")) or "reference/SAfiles/"+target.name not in tracked_policies or original.is_symlink() or not original.is_file():
                    raise RuntimeError("Missing approved policy reference: " + target.name)
                target.parent.mkdir(exist_ok=True)
                shutil.copyfile(original, target)
                policies.append(target.name)
                pending.append(target)
            if not target.exists():
                raise RuntimeError("Missing documentation target: " + str(document.relative_to(destination)) + " -> " + link)
    files = list(projects.rglob("*")) + [destination / name for name in ("README.md", "LICENSE", "NOTICE.md")]
    return {"entry": "README.md", "policy_references": sorted(policies),
            "sha256": {p.relative_to(destination).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(files) if p.is_file()}}
