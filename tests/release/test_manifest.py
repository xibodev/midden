from pathlib import Path
import sys
import unittest


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import release_contract as contract


class ReleaseManifestTests(unittest.TestCase):
    def test_version_is_safe_for_tags_archives_and_linker_flags(self):
        for value in ("0.3.0", "0.3.0-rc.1", "0.3.0-dev"):
            self.assertEqual(contract.release_version(value), value)
        for value in ("v0.3.0", "../0.3.0", "0.3.0;bad", "01.3.0", "0.3", "0.3.0\n"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                contract.release_version(value)

    def test_tsv_manifest_is_explicit_and_independent_of_json_tools(self):
        entries = [("core", "windows/amd64", "midden-core_0.3.0_windows_amd64.zip",
                    {"midden.exe": "a" * 64, "LICENSE": "b" * 64})]
        text = contract.release_tsv("0.3.0", "c" * 40, entries)
        self.assertEqual(text, "format\tmidden-release-v2\nversion\t0.3.0\ncommit\t" + "c" * 40 +
                         "\narchive\tcore\twindows/amd64\tmidden-core_0.3.0_windows_amd64.zip\n" +
                         "file\tcore\twindows/amd64\tLICENSE\t" + "b" * 64 + "\n" +
                         "file\tcore\twindows/amd64\tmidden.exe\t" + "a" * 64 + "\n")
        parsed = contract.parse_release_tsv(text)
        self.assertEqual(parsed["version"], "0.3.0")
        self.assertEqual(parsed["files"][("core", "windows/amd64")]["midden.exe"], "a" * 64)

    def test_products_are_core_bundle_and_app_on_every_target(self):
        self.assertEqual(contract.archive_name("app", "0.4.0", "windows/arm64"), "midden-app_0.4.0_windows_arm64.zip")
        self.assertEqual(contract.archive_name("core", "0.4.0", "linux/arm64"), "midden-core_0.4.0_linux_arm64.tar.gz")
        self.assertEqual(contract.archive_name("bundle", "0.4.0", "universal"), "midden-bundle_0.4.0.zip")
        for product, target in (("ui", "linux/amd64"), ("cli", "linux/amd64"), ("bundle", "linux/amd64"),
                                ("core", "windows/386")):
            with self.subTest(product=product, target=target), self.assertRaises(ValueError):
                contract.archive_name(product, "0.4.0", target)

    def test_pandoc_is_pinned_for_every_target_and_installs_only_into_app_tools(self):
        entries = [("core", "linux/amd64", "midden-core_0.4.0_linux_amd64.tar.gz", {"midden": "b" * 64})]
        text = contract.release_tsv("0.4.0", "a" * 40, entries, contract.pandoc_records(contract.TARGETS))
        parsed = contract.parse_release_tsv(text)
        self.assertEqual(set(parsed["fetch"]), set(contract.TARGETS))
        url, value = parsed["fetch"]["darwin/arm64"]
        self.assertEqual(url, "https://github.com/jgm/pandoc/releases/download/3.12/pandoc-3.12-arm64-macOS.zip")
        self.assertEqual(parsed["take"]["windows/arm64"]["pandoc-3.12/pandoc.exe"], "app/tools/pandoc.exe")
        base = contract.release_tsv("0.4.0", "a" * 40, entries)
        for extra in ("fetch\tpandoc\tlinux/amd64\thttps://example.invalid/pandoc.tar.gz\t" + "c" * 64 + "\n",
                      "fetch\tpython\tlinux/amd64\t" + contract.PANDOC_RELEASE + "a.tar.gz\t" + "c" * 64 + "\n",
                      "fetch\tpandoc\tlinux/amd64\t" + contract.PANDOC_RELEASE + "a.tar.gz\t" + "c" * 64 + "\n"
                      "take\tpandoc\tlinux/amd64\tpandoc/bin/pandoc\tpandoc\n",
                      "take\tpandoc\tlinux/amd64\tpandoc/bin/pandoc\tapp/tools/pandoc\n",
                      "fetch\tpandoc\tlinux/amd64\t" + contract.PANDOC_RELEASE + "a.tar.gz\t" + "c" * 64 + "\n"):
            with self.subTest(extra=extra), self.assertRaises(ValueError):
                contract.parse_release_tsv(base + extra)

    def test_portable_members_reject_ambiguous_or_unsafe_paths(self):
        for name in ("../outside", "/absolute", "C:/outside", "bundles\\bad", "a//b",
                     "./a", "a/../b", "a\tb", "a\nb", "CON", "a/NUL.txt", "trailing.", "trailing "):
            with self.subTest(name=name), self.assertRaises(ValueError):
                contract.safe_member(name)
        self.assertEqual(contract.safe_member("bundles/midden-shared/tools.md"), "bundles/midden-shared/tools.md")

    def test_manifest_rejects_duplicate_keys_case_collisions_and_bad_checksums(self):
        valid = contract.release_tsv("0.3.0", "a" * 40, [
            ("core", "linux/amd64", "midden-core_0.3.0_linux_amd64.tar.gz", {"midden": "b" * 64})
        ])
        for extra in ("version\t0.3.1\n",
                      "file\tcore\tlinux/amd64\tmidden\t" + "b" * 64 + "\n",
                      "file\tcore\tlinux/amd64\tMIDDEN\t" + "c" * 64 + "\n",
                      "file\tcore\tlinux/amd64\tother\tnot-a-digest\n",
                      "file\tui\tlinux/amd64\tmidden-ui\t" + "b" * 64 + "\n"):
            with self.subTest(extra=extra), self.assertRaises(ValueError):
                contract.parse_release_tsv(valid + extra)


if __name__ == "__main__":
    unittest.main()
