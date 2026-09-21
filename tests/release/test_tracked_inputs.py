import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("release_build", ROOT / "scripts/build-release.py")
BUILD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILD)
from release_contract import verify_bundle_members


class TrackedInputTests(unittest.TestCase):
    def test_extra_local_member_is_rejected_even_under_bundle_prefix(self):
        expected = {"LICENSE", "install.ps1", "install.sh", "bundles/sample/SKILL.md"}
        with self.assertRaises(RuntimeError):
            verify_bundle_members(expected | {"bundles/.vscode/settings.json"}, expected)

    def test_ignored_local_settings_never_become_release_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            subprocess.run(["git", "init", "--quiet", str(root)], check=True)
            (root / ".gitignore").write_text(".vscode/\n", encoding="utf-8")
            (root / "bundles" / "sample").mkdir(parents=True)
            (root / "bundles" / "sample" / "SKILL.md").write_text("Synthetic public material\n", encoding="utf-8")
            for name in ["LICENSE", "install.ps1", "install.sh"]:
                (root / name).write_text("synthetic public fixture\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            subprocess.run(["git", "-C", str(root), "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                            "commit", "--quiet", "-m", "synthetic fixture"], check=True)
            local = root / "bundles" / ".vscode"
            local.mkdir()
            (local / "settings.json").write_text('{"local_only":"NOT_RELEASE_MATERIAL"}', encoding="utf-8")
            self.assertEqual(subprocess.check_output(["git", "-C", str(root), "status", "--porcelain"]), b"")
            files = BUILD.bundle_inputs(root, "HEAD")
            names = {name for _, name in files}
            self.assertEqual(names, {"LICENSE", "install.ps1", "install.sh", "bundles/sample/SKILL.md"})


if __name__ == "__main__":
    unittest.main()
