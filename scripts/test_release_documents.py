"""Release-document isolation and link integrity checks, using temporary repositories."""
from pathlib import Path
import subprocess
import tempfile
import unittest

from release_documents import package_documents


class DocumentPackaging(unittest.TestCase):
    def fixture(self, root, link):
        for name in ("AtlasSite", "StarAccount"):
            repo = root / name
            (repo / "docs").mkdir(parents=True)
            (repo / "README.md").write_text("[notice](NOTICE.md)", encoding="utf-8")
            (repo / "LICENSE").write_text("test fixture", encoding="utf-8")
            (repo / "NOTICE.md").write_text("[license](LICENSE)", encoding="utf-8")
            for document in ("INTERNAL_OPERATIONS_2026-11.md", "identity-contract.md"):
                (repo / "docs" / document).write_text(link if name == "AtlasSite" else "fixture", encoding="utf-8")
            (repo / ".gitignore").write_text(".env\n", encoding="utf-8")
            (repo / "docs/.env").write_text("SYNTHETIC_SECRET_MUST_NOT_SHIP", encoding="utf-8")
            subprocess.run(["git", "init", "-q", str(repo)], check=True, capture_output=True)
            subprocess.run(["git", "add", "."], cwd=repo, check=True, capture_output=True)
        destination = root / "package"
        (destination / "licenses").mkdir(parents=True)
        (destination / "licenses/inventory.json").write_text("{}", encoding="utf-8")
        (destination / "deploy/ubuntu").mkdir(parents=True)
        return root / "AtlasSite", root / "StarAccount", destination

    def test_sibling_links_policy_closure_and_ignored_material(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            atlas, account, destination = self.fixture(root, "[account](../../StarAccount/README.md)\n[policy](../../SAfiles/SA1-example.md)\n```markdown\n[example](missing-demo.md)\n```")
            (atlas / "reference/SAfiles").mkdir(parents=True)
            (atlas / "reference/SAfiles/SA1-example.md").write_text("approved fixture", encoding="utf-8")
            subprocess.run(["git", "add", "reference"], cwd=atlas, check=True, capture_output=True)
            result = package_documents(atlas, account, destination)
            self.assertTrue((destination / "LICENSE").is_file())
            self.assertTrue((destination / "projects/StarAccount/README.md").is_file())
            self.assertEqual(result["policy_references"], ["SA1-example.md"])
            self.assertFalse(list(destination.rglob(".env")))
            self.assertFalse(any("SYNTHETIC_SECRET_MUST_NOT_SHIP" in path.read_text(encoding="utf-8") for path in destination.rglob("*") if path.is_file()))

    def test_external_path_cannot_be_copied(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            atlas, account, destination = self.fixture(root, "[private](../../../../private.txt)")
            (root / "private.txt").write_text("MUST_NOT_COPY", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "escapes package"):
                package_documents(atlas, account, destination)
            self.assertFalse((destination / "private.txt").exists())

    def test_broken_link_blocks_artifact(self):
        with tempfile.TemporaryDirectory() as directory:
            atlas, account, destination = self.fixture(Path(directory), "[missing](unknown.md)")
            with self.assertRaisesRegex(RuntimeError, "Missing documentation target"):
                package_documents(atlas, account, destination)


if __name__ == "__main__":
    unittest.main()
