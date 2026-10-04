from pathlib import Path
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import release_contract as contract


DASH = "\u2014"
RULE = "=" * 72
HEADER = "Third-party licenses and attribution for this compiled artifact.\n"
MIT = ("MIT License\n\nCopyright (c) 2026 {holder}\n\n"
       "Permission is hereby granted, free of charge, to any person obtaining a copy\n"
       "of this synthetic software, subject to the following conditions:\n\n"
       "The above copyright notice and this permission notice shall be included in all\n"
       "copies or substantial portions of the Software.\n")
APACHE = "\n                                 Apache License\n                           Version 2.0, January 2004\n"
GO_LICENSE = "Synthetic Go standard library license, reproduced in full.\n"
ALPHA_LICENSE = "Synthetic alpha license, reproduced in full.\n"
ALPHA_NOTICE = "Synthetic alpha attribution notice.\n"
MITLIB_LICENSE = MIT.format(holder="Synthetic Third Party")


def section(title, body):
    return f"\n{RULE}\n{title}\n{RULE}\n{body}\n"


class NoticeTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.modules = set()
        self.module("github.com/xibodev/compa", "v1.0.0", {
            "LICENSE": MIT.format(holder="xibodev\nCopyright (c) 2026 Synthetic Upstream contributors (portions; see NOTICE)"),
            "NOTICE": "Compa\nThis synthetic product is a fork of Synthetic Upstream (https://example.invalid/upstream).\n",
        })
        self.module("github.com/xibodev/llmgw-core", "v1.3.0", {
            "LICENSE": MIT.format(holder="xibodev"),
            "NOTICE": "llmgw-core\nIncludes an adaptation of a synthetic sibling project's provider.\n",
        })
        self.module("github.com/xibodev/llm-provider-auth", "v1.0.0", {"LICENSE": MIT.format(holder="xibodev")})
        self.module("github.com/xibodev/llm-translate", "v0.3.0", {"LICENSE": MIT.format(holder="xibodev")})
        self.module("Go standard library", "go1.26.0", {"LICENSE": GO_LICENSE})
        self.module("example.com/alpha", "v1.2.3", {"LICENSE": ALPHA_LICENSE, "NOTICE": ALPHA_NOTICE})
        self.module("github.com/example/mitlib", "v1.0.0", {"LICENSE": MITLIB_LICENSE})

    def module(self, path, version, files, modules=None):
        directory = self.root / f"{path.replace('/', '_').replace(' ', '_')}@{version}"
        directory.mkdir()
        for name, text in files.items():
            (directory / name).write_bytes(text.encode("utf-8"))
        (self.modules if modules is None else modules).add((path, version, str(directory)))
        return directory

    def render(self, modules=None):
        return contract.format_notices(self.modules if modules is None else modules).decode("utf-8")

    def test_xibodev_components_are_named_in_one_line_and_others_keep_full_texts(self):
        self.assertEqual(self.render(), "".join((
            HEADER, "\n",
            f"Compa {DASH} https://github.com/xibodev/compa {DASH} MIT License\n",
            f"llm-provider-auth {DASH} https://github.com/xibodev/llm-provider-auth {DASH} MIT License\n",
            f"llm-translate {DASH} https://github.com/xibodev/llm-translate {DASH} MIT License\n",
            f"llmgw-core {DASH} https://github.com/xibodev/llmgw-core {DASH} MIT License\n",
            section("Go standard library go1.26.0 / LICENSE", GO_LICENSE),
            section("example.com/alpha v1.2.3 / LICENSE", ALPHA_LICENSE),
            section("example.com/alpha v1.2.3 / NOTICE", ALPHA_NOTICE),
            section("github.com/example/mitlib v1.0.0 / LICENSE", MITLIB_LICENSE),
        )))

    def test_xibodev_license_and_notice_texts_are_not_reproduced(self):
        text = self.render()
        for repository in ("compa", "llmgw-core", "llm-provider-auth", "llm-translate"):
            with self.subTest(repository=repository):
                lines = [line for line in text.splitlines() if f"https://github.com/xibodev/{repository} " in line]
                self.assertEqual(len(lines), 1)
        self.assertEqual(text.count("xibodev"), 4, "only the one-line entries may mention xibodev")
        for fragment in ("Copyright (c) 2026 xibodev", "synthetic product is a fork", "Synthetic Upstream", "adaptation of"):
            self.assertNotIn(fragment, text)
        self.assertEqual(text.count("Permission is hereby granted"), 1, "only the foreign MIT text is reproduced")

    def test_other_module_texts_are_byte_for_byte_and_unchanged_without_components(self):
        foreign = {entry for entry in self.modules if not entry[0].startswith("github.com/xibodev/")}
        self.assertEqual(self.render(foreign), "".join((
            HEADER,
            section("Go standard library go1.26.0 / LICENSE", GO_LICENSE),
            section("example.com/alpha v1.2.3 / LICENSE", ALPHA_LICENSE),
            section("example.com/alpha v1.2.3 / NOTICE", ALPHA_NOTICE),
            section("github.com/example/mitlib v1.0.0 / LICENSE", MITLIB_LICENSE),
        )))
        raw = "Synthetic \u00a9 holder\r\nwith CRLF and no final newline"
        modules = set()
        self.module("example.com/raw", "v0.0.1", {"COPYING": raw}, modules)
        self.assertIn(section("example.com/raw v0.0.1 / COPYING", raw).encode("utf-8"),
                      contract.format_notices(modules))

    def test_only_the_one_line_entries_mention_xibodev_components(self):
        self.assertEqual(contract.stray_xibodev_lines(self.render().encode("utf-8")), [])
        for text in (section("github.com/xibodev/compa v1.0.0 / NOTICE", "Synthetic attribution.\n"),
                     "Synthetic terms. Copyright (c) 2026 Xibodev.\n",
                     f"Compa {DASH} https://github.com/xibodev/compa {DASH} MIT License, with more text\n",
                     f"Compa {DASH} https://github.com/xibodev/compa {DASH} Synthetic License\n"):
            with self.subTest(text=text):
                self.assertTrue(contract.stray_xibodev_lines(text.encode("utf-8")))
        modules = set(self.modules)
        self.module("example.com/wording", "v1.0.0", {"LICENSE": "Synthetic terms. Copyright (c) 2026 xibodev.\n"}, modules)
        with self.assertRaises(RuntimeError) as refused:
            contract.format_notices(modules)
        self.assertNotIn("Synthetic terms", str(refused.exception), "release logs are public; lines are not repeated")

    def test_xibodev_license_type_is_read_from_its_license_file(self):
        modules = set()
        self.module("github.com/xibodev/compa/v2", "v2.0.0", {"LICENSE": APACHE}, modules)
        self.assertIn(f"\nCompa {DASH} https://github.com/xibodev/compa {DASH} Apache License 2.0\n",
                      self.render(modules))
        for index, files in enumerate(({"LICENSE": "Synthetic proprietary terms.\n"},
                                       {"NOTICE": "Synthetic notice without a license.\n"},
                                       {"LICENSE": MIT.format(holder="xibodev"), "LICENSE-APACHE": APACHE})):
            with self.subTest(files=sorted(files)), self.assertRaises(RuntimeError):
                modules = set()
                self.module("github.com/xibodev/synthetic", f"v0.1.{index}", files, modules)
                contract.format_notices(modules)

    def test_pinned_kagi_declaration_still_ships_the_standard_apache_text(self):
        path, version = "github.com/kagisearch/kagi-openapi-golang", "v0.0.0-20260526215348-96575e864d62"
        modules = set()
        directory = self.module(path, version, {}, modules)
        (directory / "api").mkdir()
        (directory / "api" / "openapi.yaml").write_text(
            "info:\n  license:\n    name: Apache-2.0\n    url: https://www.apache.org/licenses/LICENSE-2.0.html\n",
            encoding="utf-8")
        text = contract.format_notices(modules)
        self.assertIn(f"\n{RULE}\n{path} {version}\nThe generated SDK declares Apache-2.0".encode("utf-8"), text)
        self.assertTrue(text.endswith((ROOT / "licenses" / "Apache-2.0.txt").read_bytes()))


if __name__ == "__main__":
    unittest.main()
