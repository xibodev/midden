import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class SiteStagingTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "source"
        self.release = self.root / "release"
        (self.source / "docs").mkdir(parents=True)
        self.release.mkdir()
        for name in ("index.html", "site.css", "favicon.svg"):
            (self.source / "docs" / name).write_text("synthetic public " + name)
        (self.source / "docs" / "private.txt").write_text("must not be published")
        for name in ("install.ps1", "install.sh"):
            (self.release / name).write_text("synthetic verified " + name)
        (self.release / "build-manifest.json").write_text(json.dumps({"version": "0.3.0", "commit": "a" * 40}))
        (self.release / "SHA256SUMS").write_text("".join(
            hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n"
            for p in sorted(self.release.iterdir())))
        self.output = self.root / "site"

    def stage(self, tag="v0.3.0", commit=None):
        spec = importlib.util.spec_from_file_location("site_stage", ROOT / "scripts" / "stage-site.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        module.stage_site(self.source, self.release, self.output, tag, commit or "a" * 40)

    def test_only_reviewed_page_and_matching_release_bootstraps_are_published(self):
        self.stage()
        self.assertEqual({p.name for p in self.output.iterdir()},
                         {"index.html", "site.css", "favicon.svg", "install.ps1", "install.sh", "SHA256SUMS", "build-manifest.json"})
        self.assertEqual((self.output / "install.ps1").read_bytes(), (self.release / "install.ps1").read_bytes())
        self.assertFalse((self.output / "private.txt").exists())

    def test_corrupt_download_does_not_create_a_site(self):
        (self.release / "install.sh").write_text("changed")
        with self.assertRaises(ValueError):
            self.stage()
        self.assertFalse(self.output.exists())

    def test_prerelease_and_mismatched_source_do_not_publish(self):
        for tag, commit in (("v0.3.0-rc.1", None), ("v0.3.0", "b" * 40), ("v0.3.1", None)):
            with self.subTest(tag=tag, commit=commit), self.assertRaises(ValueError):
                self.stage(tag, commit)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
