import hashlib
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import release_contract as contract


class SourceByteTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "repo"
        self.root.mkdir()
        subprocess.run(["git", "init", "--quiet", str(self.root)], check=True)
        path = self.root / "bundles" / "article" / "SKILL.md"
        path.parent.mkdir(parents=True)
        path.write_bytes(b"Reviewed synthetic guidance.\n")
        subprocess.run(["git", "-C", str(self.root), "add", "."], check=True)
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Test",
                        "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "synthetic"], check=True)
        self.commit = subprocess.check_output(["git", "-C", str(self.root), "rev-parse", "HEAD"], text=True).strip()
        self.source = path

    def test_packaging_materializes_reviewed_blob_not_local_transformed_bytes(self):
        self.source.write_bytes(b"Unreviewed local replacement.\r\n")
        materialized = contract.materialize_inputs(
            self.root, self.commit, [(self.source, "bundles/article/SKILL.md")],
            Path(self.temporary.name) / "stage")
        self.assertEqual(materialized[0][0].read_bytes(), b"Reviewed synthetic guidance.\n")
        self.assertEqual(materialized[0][1], "bundles/article/SKILL.md")

    def test_resealed_static_replacement_is_rejected_against_the_claimed_commit(self):
        spec = importlib.util.spec_from_file_location("verify_release", ROOT / "scripts" / "verify-release.py")
        verifier = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(verifier)
        actual = {"bundles/article/SKILL.md": hashlib.sha256(b"Resealed altered guidance.\n").hexdigest()}
        with self.assertRaises(ValueError):
            verifier.verify_static_source(self.root, self.commit,
                                          [(self.source, "bundles/article/SKILL.md")], actual)
        actual["bundles/article/SKILL.md"] = hashlib.sha256(b"Reviewed synthetic guidance.\n").hexdigest()
        verifier.verify_static_source(self.root, self.commit,
                                      [(self.source, "bundles/article/SKILL.md")], actual)


if __name__ == "__main__":
    unittest.main()
