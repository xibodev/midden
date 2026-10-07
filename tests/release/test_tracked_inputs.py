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
from release_contract import SKILL_FOLDERS, verify_bundle_members


def commit_fixture(root, files):
    subprocess.run(["git", "init", "--quiet", str(root)], check=True)
    for name, text in files.items():
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    subprocess.run(["git", "-C", str(root), "add", "."], check=True)
    subprocess.run(["git", "-C", str(root), "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                    "commit", "--quiet", "-m", "synthetic fixture"], check=True)


SKILLS = {f"bundles/{folder}/{'sources.md' if folder == 'midden-shared' else 'SKILL.md'}": "Synthetic public material\n"
          for folder in SKILL_FOLDERS}


class TrackedInputTests(unittest.TestCase):
    def test_extra_local_member_is_rejected_even_under_bundle_prefix(self):
        expected = {"skills/midden-shared/LICENSE", "skills/midden-article/SKILL.md"}
        with self.assertRaises(RuntimeError):
            verify_bundle_members(expected | {"skills/.vscode/settings.json"}, expected)

    def test_skills_are_named_as_installed_and_carry_the_license(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            commit_fixture(root, {"LICENSE": "synthetic license\n", **SKILLS})
            names = {name for _, name in BUILD.bundle_inputs(root, "HEAD")}
            self.assertEqual(names, {
                "skills/midden-shared/LICENSE", "skills/midden-shared/sources.md",
                "skills/midden-investigation/SKILL.md", "skills/midden-article/SKILL.md",
                "skills/midden-presentation/SKILL.md", "skills/midden-long-form/SKILL.md",
            })

    def test_ignored_local_settings_never_become_release_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            commit_fixture(root, {".gitignore": ".vscode/\n", "LICENSE": "synthetic license\n", **SKILLS})
            local = root / "bundles" / ".vscode"
            local.mkdir()
            (local / "settings.json").write_text('{"local_only":"NOT_RELEASE_MATERIAL"}', encoding="utf-8")
            self.assertEqual(subprocess.check_output(["git", "-C", str(root), "status", "--porcelain"]), b"")
            names = {name for _, name in BUILD.bundle_inputs(root, "HEAD")}
            self.assertFalse(any("vscode" in name for name in names))

    def test_unknown_skill_folders_and_file_types_are_refused(self):
        for extra in ("bundles/sample/SKILL.md", "bundles/article/run.sh", "bundles/article/install.ps1"):
            with self.subTest(extra=extra), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                commit_fixture(root, {"LICENSE": "synthetic license\n", **SKILLS, extra: "synthetic\n"})
                with self.assertRaises(RuntimeError):
                    BUILD.bundle_inputs(root, "HEAD")


if __name__ == "__main__":
    unittest.main()
