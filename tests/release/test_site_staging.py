import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
COMMIT = "a" * 40


class SiteStagingTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.release = self.root / "release"
        self.release.mkdir()
        for name in ("install.ps1", "install.sh"):
            (self.release / name).write_text("synthetic verified " + name)
        (self.release / "midden-core_0.3.0_linux_amd64.tar.gz").write_text("synthetic archive, not for the site")
        (self.release / "build-manifest.json").write_text(json.dumps({"version": "0.3.0", "commit": COMMIT}))
        (self.release / "SHA256SUMS").write_text("".join(
            hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n"
            for p in sorted(self.release.iterdir())))
        self.output = self.root / "site"

    def stage(self, tag="v0.3.0", contains=lambda commit: commit == COMMIT):
        spec = importlib.util.spec_from_file_location("site_stage", ROOT / "scripts" / "stage-site.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        module.stage_site(self.release, ROOT / "site", self.output, tag, contains)

    def test_the_page_is_published_beside_the_matching_release_installers(self):
        self.stage()
        self.assertEqual({p.name for p in self.output.iterdir()},
                         {"install.ps1", "install.sh", "SHA256SUMS", "build-manifest.json",
                          "index.html", "site.css", "favicon.svg"})
        self.assertEqual((self.output / "install.ps1").read_bytes(), (self.release / "install.ps1").read_bytes())
        page = (self.output / "index.html").read_text(encoding="utf-8")
        self.assertIn("releases/tag/v0.3.0", page)
        self.assertIn("Install Midden 0.3.0", page)
        self.assertNotIn("@TAG@", page)
        self.assertNotIn("@VERSION@", page)

    def test_corrupt_download_does_not_create_a_site(self):
        (self.release / "install.sh").write_text("changed")
        with self.assertRaises(ValueError):
            self.stage()
        self.assertFalse(self.output.exists())

    def test_prereleases_mismatches_and_releases_outside_this_history_do_not_publish(self):
        for tag, contains in (("v0.3.0-rc.1", lambda commit: True), ("v0.3.1", lambda commit: True),
                              ("v0.3.0", lambda commit: False)):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                self.stage(tag, contains)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
