import importlib.util
from pathlib import Path
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("midden_release", ROOT / "scripts/build-release.py")
BUILD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILD)


class ArchiveTests(unittest.TestCase):
    def test_archive_has_only_selected_entries(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "sample.txt"
            source.write_text("synthetic", encoding="utf-8")
            archive = root / "out.zip"
            BUILD.write_archive(archive, [(source, "bundle/sample.txt")])
            with zipfile.ZipFile(archive) as opened:
                self.assertEqual(opened.namelist(), ["bundle/sample.txt"])
                self.assertEqual(opened.read("bundle/sample.txt"), b"synthetic")

    def test_linked_files_are_not_followed_into_public_archives(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "outside.txt"
            target.write_text("synthetic unapproved data", encoding="utf-8")
            link = root / "linked.txt"
            try:
                link.symlink_to(target)
            except OSError:
                self.skipTest("symlinks unavailable")
            with self.assertRaises(RuntimeError):
                BUILD.write_archive(root / "out.zip", [(link, "bundle/linked.txt")])

    def test_duplicate_and_traversal_names_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "sample.txt"
            source.write_text("synthetic", encoding="utf-8")
            for names in [["one", "one"], ["../outside"], ["/absolute"]]:
                with self.subTest(names=names), self.assertRaises(RuntimeError):
                    BUILD.write_archive(root / "out.zip", [(source, name) for name in names])


if __name__ == "__main__":
    unittest.main()
