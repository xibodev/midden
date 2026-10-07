"""The App's pinned compa-kernel: every target, its files and the notice line naming Compa."""
from pathlib import Path
import sys
import unittest

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts"))
import release_contract as contract  # noqa: E402


class CompaPinTests(unittest.TestCase):
    def test_every_target_takes_the_kernel_and_compas_license_and_notice(self):
        self.assertEqual(set(contract.COMPA_ARCHIVES), set(contract.TARGETS))
        for target in contract.TARGETS:
            with self.subTest(target=target):
                asset, archive_digest, kernel_digest = contract.COMPA_ARCHIVES[target]
                system, arch = target.split("/")
                suffix = ".zip" if system == "windows" else ".tar.gz"
                self.assertEqual(asset, f"compa_{contract.COMPA_VERSION}_{system}_{arch}{suffix}")
                self.assertTrue(contract.HASH.fullmatch(archive_digest) and contract.HASH.fullmatch(kernel_digest))
                members = contract.compa_members(target)
                executable = ".exe" if system == "windows" else ""
                self.assertEqual([name for _, name, _ in members],
                                 [f"app/compa-kernel{executable}", "app/compa/LICENSE", "app/compa/NOTICE"])
                self.assertEqual(members[0][0], f"compa-kernel{executable}")
                for _, name, digest in members:
                    contract.safe_member(name)
                    self.assertTrue(contract.HASH.fullmatch(digest))
                    self.assertFalse(name.startswith("app/tools/"), "the installer owns app/tools")
                contract.validate_member_set([name for _, name, _ in members] + ["midden-ui" + executable, "app/LICENSE"])

    def test_the_kernel_comes_from_compas_own_release(self):
        self.assertEqual(contract.COMPA_RELEASE, f"https://github.com/xibodev/compa/releases/download/v{contract.COMPA_VERSION}/")
        self.assertEqual(len({digest for _, _, digest in (contract.compa_members(t)[0] for t in contract.TARGETS)}),
                         len(contract.TARGETS), "each target has its own kernel")

    def test_the_notice_names_compa_in_one_line(self):
        text = (contract.COMPA_NOTICE + contract.PANDOC_NOTICE).encode("utf-8")
        self.assertEqual(contract.stray_xibodev_lines(text), [])
        self.assertIn(f"\nCompa \u2014 https://github.com/xibodev/compa \u2014 MIT License\n", contract.COMPA_NOTICE)
        self.assertIn(f"compa-kernel {contract.COMPA_VERSION}", contract.COMPA_NOTICE)


if __name__ == "__main__":
    unittest.main()
