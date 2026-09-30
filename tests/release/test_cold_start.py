from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class ColdStartTests(unittest.TestCase):
    def test_plain_python_entry_points_do_not_create_checkout_bytecode(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            scripts = root / "scripts"
            scripts.mkdir()
            for name in ["build-release.py", "verify-release.py", "release_contract.py"]:
                shutil.copyfile(ROOT / "scripts" / name, scripts / name)
            subprocess.run(["git", "init", "--quiet", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            subprocess.run(["git", "-C", str(root), "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                            "commit", "--quiet", "-m", "synthetic fixture"], check=True)
            for name in ["build-release.py", "verify-release.py"]:
                with self.subTest(entry_point=name):
                    result = subprocess.run([sys.executable, str(scripts / name), "--help"], cwd=root,
                                            capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertFalse((scripts / "__pycache__").exists())
                    self.assertEqual(subprocess.check_output(["git", "-C", str(root), "status", "--porcelain"]), b"")


if __name__ == "__main__":
    unittest.main()
