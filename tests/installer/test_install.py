"""Installer unit behavior: synthetic bundles and a fake version-only executable.

No host/model is run. These tests do not establish actual-core compatibility or
agent skill discovery. All installations, receipts, and probe logs are external
temporary fixtures.
"""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile


ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "installer" / "install.py"
MANIFEST_WRITER = ROOT / "installer" / "make_manifest.py"
LAYOUT = {
    "investigation": "midden-investigation",
    "article": "midden-article",
    "presentation": "midden-presentation",
    "long-form": "midden-long-form",
    "midden-shared": "midden-shared",
}
RECEIPT = "midden-install-receipt.json"
BINARY = "midden.exe" if os.name == "nt" else "midden"


def external_temp():
    parent = Path(tempfile.gettempdir()).resolve()
    if parent == ROOT or ROOT in parent.parents:
        raise RuntimeError("Installer test scratch must be outside the repository")
    return tempfile.TemporaryDirectory(prefix="midden-installer-unit-", dir=parent)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(path):
    return {
        item.relative_to(path).as_posix(): item.read_bytes()
        for item in path.rglob("*")
        if item.is_file()
    }


def fixture_manifest(bundle, output):
    entries = []
    for source_dir, destination_dir in LAYOUT.items():
        for path in sorted((bundle / source_dir).rglob("*")):
            if path.is_file():
                entries.append(
                    {
                        "source": path.relative_to(bundle).as_posix(),
                        "destination": destination_dir
                        + "/"
                        + path.relative_to(bundle / source_dir).as_posix(),
                        "sha256": digest(path),
                    }
                )
    output.write_text(
        json.dumps({"schema": "midden.bundle-files/v1", "files": entries}),
        encoding="utf-8",
    )


def build_probe(directory):
    executable = directory / BINARY
    if os.name == "nt":
        source = directory / "SyntheticProbe.cs"
        source.write_text(
            """using System;
using System.IO;
class SyntheticProbe {
    public static int Main(string[] args) {
        string log = Environment.GetEnvironmentVariable("MIDDEN_UNIT_LOG");
        if (!String.IsNullOrEmpty(log))
            File.AppendAllText(log, String.Join(" ", args) + "|" +
                Environment.GetEnvironmentVariable("MIDDEN_HOME") + "\\n");
        if (args.Length != 1 || args[0] != "version") {
            Console.Error.WriteLine("The synthetic probe permits only version");
            return 73;
        }
        if (Environment.GetEnvironmentVariable("MIDDEN_UNIT_FAIL") == "1")
            return 74;
        Console.WriteLine(Environment.GetEnvironmentVariable("MIDDEN_UNIT_VERSION")
            ?? "midden 0.3.0-dev");
        return 0;
    }
}
""",
            encoding="utf-8",
        )
        quote = lambda value: "'" + str(value).replace("'", "''") + "'"
        command = (
            "Add-Type -TypeDefinition ([IO.File]::ReadAllText("
            + quote(source)
            + ")) -OutputAssembly "
            + quote(executable)
            + " -OutputType ConsoleApplication"
        )
        subprocess.run(
            [
                "powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                command,
            ],
            check=True,
            capture_output=True,
            text=True,
            timeout=60,
        )
    else:
        executable.write_text(
            '#!/bin/sh\n'
            'if [ -n "${MIDDEN_UNIT_LOG:-}" ]; then\n'
            '  printf "%s|%s\\n" "$*" "${MIDDEN_HOME:-}" >> "$MIDDEN_UNIT_LOG"\n'
            'fi\n'
            '[ "$#" = 1 ] && [ "$1" = version ] || exit 73\n'
            '[ "${MIDDEN_UNIT_FAIL:-0}" != 1 ] || exit 74\n'
            'printf "%s\\n" "${MIDDEN_UNIT_VERSION:-midden 0.3.0-dev}"\n'
            'exit 0\n',
            encoding="utf-8",
        )
        executable.chmod(0o755)
    return executable


class InstallerUnitTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.probe_temporary = external_temp()
        cls.addClassCleanup(cls.probe_temporary.cleanup)
        cls.probe = build_probe(Path(cls.probe_temporary.name))

    def setUp(self):
        self.temporary = external_temp()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.project = self.root / "project with spaces"
        self.home = self.root / "synthetic home"
        self.bundle = self.root / "source bundles"
        self.project.mkdir()
        self.home.mkdir()
        self.bundle.mkdir()
        self.core = self.root / BINARY
        shutil.copy2(self.probe, self.core)
        self.manifest = self.root / "bundle-manifest.json"
        self.bin = self.project / ".midden" / "bin"
        self.skills = self.project / ".github" / "skills"
        self.state = self.project / ".midden" / "state"
        self.log = self.root / "probe.log"
        self.env = dict(os.environ)
        self.env.update(
            PYTHON=sys.executable,
            HOME=str(self.home),
            USERPROFILE=str(self.home),
            MIDDEN_UNIT_LOG=str(self.log),
        )
        self.env.pop("BASH_ENV", None)
        self.env.pop("MIDDEN_UNIT_VERSION", None)
        self.env.pop("MIDDEN_UNIT_FAIL", None)
        for source, destination in LAYOUT.items():
            directory = self.bundle / source
            directory.mkdir()
            if source != "midden-shared":
                (directory / "SKILL.md").write_text(
                    f"---\nname: {destination}\n"
                    "description: Use when developing a synthetic example.\n---\n\n"
                    "[Sources](../midden-shared/sources.md)\n"
                    "[Tools](../midden-shared/tools.md)\n",
                    encoding="utf-8",
                )
        (self.bundle / "midden-shared" / "sources.md").write_text(
            "Synthetic source guidance.\n", encoding="utf-8"
        )
        (self.bundle / "midden-shared" / "tools.md").write_text(
            "[Sources](sources.md)\n", encoding="utf-8"
        )
        templates = self.bundle / "presentation" / "templates"
        templates.mkdir()
        (templates / "html.yaml").write_text(
            "to: dzslides\ncss:\n  - ${.}/slides.css\n", encoding="utf-8"
        )
        (templates / "slides.css").write_text("body { color: black; }\n")
        (self.bundle / "article" / "obsolete.md").write_text("Synthetic old file.\n")
        fixture_manifest(self.bundle, self.manifest)

    def arguments(self, *extra):
        return [
            "--project",
            str(self.project),
            "--home-dir",
            str(self.home),
            "--core-binary",
            str(self.core),
            "--core-sha256",
            digest(self.core),
            "--bundle-dir",
            str(self.bundle),
            "--manifest",
            str(self.manifest),
            *map(str, extra),
        ]

    def run_installer(self, *extra, success=True, launcher=None, env=None):
        command = launcher or [sys.executable, "-B", str(BACKEND)]
        result = subprocess.run(
            command + self.arguments(*extra),
            env=env or self.env,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=60,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def backend_module(self):
        spec = importlib.util.spec_from_file_location("midden_installer_unit_module", BACKEND)
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        self.addCleanup(sys.modules.pop, spec.name)
        spec.loader.exec_module(module)
        return module

    def extracted_artifacts(self):
        core = self.root / "extracted core"
        core.mkdir()
        shutil.copy2(self.core, core / BINARY)
        (core / "LICENSE").write_text("Synthetic core license fixture.\n")
        (core / "CORE.md").write_text("Standalone synthetic core fixture.\n")
        stage = self.root / "bundle zip stage"
        stage.mkdir()
        shutil.copytree(self.bundle, stage / "bundles")
        shutil.copytree(
            ROOT / "installer", stage / "installer",
            ignore=shutil.ignore_patterns("__pycache__"),
        )
        shutil.copyfile(self.manifest, stage / "installer" / "bundle-manifest.json")
        for name in ("install.ps1", "install.sh"):
            shutil.copyfile(ROOT / name, stage / name)
        (stage / "LICENSE").write_text("Synthetic bundle license fixture.\n")
        archive = self.root / "universal-bundle.zip"
        with zipfile.ZipFile(archive, "w") as output:
            for path in stage.rglob("*"):
                if path.is_file():
                    output.write(path, path.relative_to(stage).as_posix())
        extracted = self.root / "extracted bundle"
        with zipfile.ZipFile(archive) as package:
            package.extractall(extracted)
        self.assertEqual({path.name for path in core.iterdir()}, {BINARY, "LICENSE", "CORE.md"})
        self.assertEqual(
            {path.name for path in extracted.iterdir()},
            {"bundles", "installer", "install.ps1", "install.sh", "LICENSE"},
        )
        self.assertFalse(any(path.name == BINARY for path in extracted.rglob("*")))
        return core, extracted

    def assert_bindings_and_links(self, skills, binary, state):
        for directory in ("investigation", "article", "presentation", "long-form"):
            installed = skills / ("midden-" + directory) / "SKILL.md"
            content = installed.read_text(encoding="utf-8")
            self.assertTrue(
                content.startswith((self.bundle / directory / "SKILL.md").read_text()),
                "The canonical guidance must be preserved before the generated footer",
            )
            footer = content.split("## Local core binding", 1)[1]
            binding = json.loads(re.search(r"```json\n(.*?)\n```", footer, re.S)[1])
            self.assertEqual(
                binding,
                {"executable": str(binary), "environment": {"MIDDEN_HOME": str(state)}},
            )
            for relative in re.findall(r"\]\(([^)]+)\)", content):
                if "://" not in relative:
                    self.assertTrue((installed.parent / relative).is_file(), relative)
        self.assertTrue((skills / "midden-shared" / "sources.md").is_file())
        self.assertTrue(
            (skills / "midden-presentation" / "templates" / "slides.css").is_file()
        )

    def test_project_install_verify_uninstall_preserves_unowned_files_and_state(self):
        self.state.mkdir(parents=True)
        (self.state / "retained.txt").write_text("Synthetic retained state.\n")
        unrelated = self.skills / "shared" / "sources.md"
        unrelated.parent.mkdir(parents=True)
        unrelated.write_text("Unrelated shared resource.\n")
        (self.project / ".mcp.json").write_text('{"synthetic":true}\n')
        (self.home / ".profile").write_text("# Synthetic profile; preserve it.\n")
        home_before = snapshot(self.home)
        self.run_installer()
        self.assertEqual(digest(self.bin / BINARY), digest(self.core))
        self.assert_bindings_and_links(self.skills, self.bin / BINARY, self.state)
        self.assertEqual(self.log.read_text().strip(), "version|" + str(self.state))
        foreign = self.skills / "midden-article" / "user-note.md"
        foreign.write_text("Unowned note.\n")
        before = snapshot(self.project)
        self.run_installer("--verify")
        self.assertEqual(snapshot(self.project), before)
        shutil.rmtree(self.bundle)
        self.run_installer("--uninstall")
        self.assertFalse((self.bin / BINARY).exists())
        self.assertFalse((self.bin / RECEIPT).exists())
        self.assertFalse((self.skills / "midden-article" / "SKILL.md").exists())
        self.assertEqual(foreign.read_text(), "Unowned note.\n")
        self.assertEqual(unrelated.read_text(), "Unrelated shared resource.\n")
        self.assertEqual((self.state / "retained.txt").read_text(), "Synthetic retained state.\n")
        self.assertEqual((self.project / ".mcp.json").read_text(), '{"synthetic":true}\n')
        self.assertEqual(snapshot(self.home), home_before)

    def test_upgrade_requires_receipt_and_preserves_unowned_files(self):
        self.run_installer("--upgrade", success=False)
        self.run_installer()
        foreign = self.skills / "midden-article" / "user-note.md"
        foreign.write_text("Keep this note.\n")
        (self.bundle / "article" / "obsolete.md").unlink()
        (self.bundle / "article" / "new.md").write_text("Synthetic new file.\n")
        with self.core.open("ab") as stream:
            stream.write(b"\n# synthetic-unit-second-build\n")
        fixture_manifest(self.bundle, self.manifest)
        before = snapshot(self.project)
        self.run_installer(success=False)
        self.assertEqual(snapshot(self.project), before)
        self.run_installer("--upgrade")
        self.assertFalse((self.skills / "midden-article" / "obsolete.md").exists())
        self.assertEqual((self.skills / "midden-article" / "new.md").read_text(), "Synthetic new file.\n")
        self.assertEqual(foreign.read_text(), "Keep this note.\n")
        self.assertEqual(digest(self.bin / BINARY), digest(self.core))
        self.run_installer("--verify")

    def test_unowned_collision_is_refused_even_when_bytes_match(self):
        collision = self.skills / "midden-article" / "SKILL.md"
        collision.parent.mkdir(parents=True)
        shutil.copyfile(self.bundle / "article" / "SKILL.md", collision)
        before = snapshot(self.project)
        self.run_installer(success=False)
        self.assertEqual(snapshot(self.project), before)

    def test_modified_owned_file_blocks_upgrade_verify_and_uninstall(self):
        self.run_installer()
        changed = self.skills / "midden-investigation" / "SKILL.md"
        changed.write_text(changed.read_text() + "\nUser changes to preserve.\n")
        before = snapshot(self.project)
        for operation in ("--upgrade", "--verify", "--uninstall"):
            with self.subTest(operation=operation):
                self.run_installer(operation, success=False)
                self.assertEqual(snapshot(self.project), before)

    def test_checksums_are_verified_before_running_the_binary(self):
        self.run_installer("--core-sha256", "0" * 64, success=False)
        self.assertFalse(self.log.exists())
        self.assertEqual(snapshot(self.project), {})
        (self.bundle / "article" / "SKILL.md").write_text("Changed after manifest.\n")
        self.run_installer(success=False)
        self.assertFalse(self.log.exists())
        self.assertEqual(snapshot(self.project), {})

    def test_unlisted_bundle_file_and_traversing_manifest_are_refused(self):
        extra = self.bundle / "article" / "unlisted.md"
        extra.write_text("Synthetic unlisted material.\n")
        self.run_installer(success=False)
        extra.unlink()
        manifest = json.loads(self.manifest.read_text())
        manifest["files"][0]["destination"] = "../unowned.txt"
        self.manifest.write_text(json.dumps(manifest))
        self.run_installer(success=False)
        self.assertEqual(snapshot(self.project), {})
        self.assertFalse(self.log.exists())

    def test_old_or_failed_core_probe_is_refused_without_installing(self):
        for value in ("midden 0.2.9", "not a core version"):
            with self.subTest(version=value):
                env = dict(self.env, MIDDEN_UNIT_VERSION=value)
                self.run_installer(success=False, env=env)
                self.assertEqual(snapshot(self.project), {})
        self.run_installer(success=False, env=dict(self.env, MIDDEN_UNIT_FAIL="1"))
        self.assertEqual(snapshot(self.project), {})

    def test_receipt_cannot_claim_files_outside_the_installation_namespaces(self):
        self.run_installer()
        outside = self.project / "unowned.txt"
        outside.write_text("Preserve this file.\n")
        receipt_path = self.bin / RECEIPT
        receipt = json.loads(receipt_path.read_text())
        receipt["files"].append(
            {"root": "skills", "path": "../../unowned.txt", "sha256": digest(outside)}
        )
        receipt_path.write_text(json.dumps(receipt))
        before = snapshot(self.project)
        self.run_installer("--uninstall", success=False)
        self.assertEqual(snapshot(self.project), before)

    def test_each_supported_host_and_scope_keeps_namespaced_links_and_bindings(self):
        destinations = {
            ("copilot", "project"): (".github", "skills"),
            ("copilot", "user"): (".copilot", "skills"),
            ("agents", "project"): (".agents", "skills"),
            ("agents", "user"): (".agents", "skills"),
            ("claude", "project"): (".claude", "skills"),
            ("claude", "user"): (".claude", "skills"),
        }
        for (host, scope), relative in destinations.items():
            with self.subTest(host=host, scope=scope):
                project = self.root / f"project-{host}-{scope}"
                home = self.root / f"home-{host}-{scope}"
                project.mkdir()
                home.mkdir()
                base = project if scope == "project" else home
                binary_dir = base / ".midden" / "bin"
                state = base / ".midden" / "state"
                args = ("--host", host, "--scope", scope, "--project", project, "--home-dir", home)
                self.run_installer(*args)
                self.assert_bindings_and_links(base.joinpath(*relative), binary_dir / BINARY, state)
                self.run_installer(*args, "--verify")
                self.run_installer(*args, "--uninstall")
                self.assertFalse((binary_dir / BINARY).exists())

    def test_explicit_binary_and_state_paths_are_bound_without_creating_state(self):
        binary_dir = self.root / "scoped binary"
        state = self.root / "scoped state"
        self.run_installer("--bin-dir", binary_dir, "--state-dir", state)
        self.assert_bindings_and_links(self.skills, binary_dir / BINARY, state)
        self.assertFalse(state.exists())
        self.run_installer("--bin-dir", binary_dir, "--verify")
        self.run_installer("--bin-dir", binary_dir, "--uninstall")

    def test_custom_state_receipt_ignores_an_unused_default_state_path(self):
        state = self.root / "separate state"
        self.run_installer("--state-dir", state)
        self.state.write_text("An unrelated file at the unused default path.\n")
        before = snapshot(self.project)
        self.run_installer("--verify")
        self.assertEqual(snapshot(self.project), before)
        self.run_installer("--uninstall")
        self.assertTrue(self.state.is_file())

    def test_busy_lock_and_missing_owned_file_block_changes(self):
        self.run_installer()
        lock = self.bin / ".midden-install.lock"
        lock.write_text("Synthetic busy operation.\n")
        before = snapshot(self.project)
        for operation in ("--upgrade", "--verify", "--uninstall"):
            self.run_installer(operation, success=False)
            self.assertEqual(snapshot(self.project), before)
        lock.unlink()
        (self.skills / "midden-article" / "SKILL.md").unlink()
        before = snapshot(self.project)
        for operation in ("--upgrade", "--verify", "--uninstall"):
            self.run_installer(operation, success=False)
            self.assertEqual(snapshot(self.project), before)

    def test_failed_upgrade_restores_the_previous_owned_bytes(self):
        self.run_installer()
        before = snapshot(self.project)
        with self.core.open("ab") as stream:
            stream.write(b"\n# synthetic rollback fixture\n")
        module = self.backend_module()
        write = module.atomic_write
        failed = False

        def fail_one_write(path, data, mode, expected):
            nonlocal failed
            if path.name == "SKILL.md" and not failed:
                failed = True
                raise OSError("Synthetic write failure after the binary changed")
            return write(path, data, mode, expected)

        args = module.parser().parse_args(self.arguments("--upgrade"))
        with patch.object(module, "atomic_write", fail_one_write), patch.dict(
            os.environ, self.env, clear=True
        ):
            with self.assertRaisesRegex(OSError, "Synthetic write failure"):
                module.execute(args)
        self.assertTrue(failed)
        self.assertEqual(snapshot(self.project), before)
        self.run_installer("--verify")

    def test_post_publication_cleanup_failure_rolls_back_the_target_and_prior_updates(self):
        self.run_installer()
        before = snapshot(self.project)
        addition = self.bundle / "article" / "added.md"
        addition.write_text("Synthetic upgrade addition.\n")
        fixture_manifest(self.bundle, self.manifest)
        with self.core.open("ab") as stream:
            stream.write(b"\n# synthetic updated core\n")
        target = self.skills / "midden-article" / "added.md"
        module = self.backend_module()
        unlink = Path.unlink
        failed = False

        def fail_published_link_cleanup(path, *args, **kwargs):
            nonlocal failed
            if (
                not failed and path.name.startswith(".midden-write-")
                and path.exists() and target.exists() and os.path.samefile(path, target)
            ):
                failed = True
                raise PermissionError("Synthetic post-publication cleanup failure")
            return unlink(path, *args, **kwargs)

        args = module.parser().parse_args(self.arguments("--upgrade"))
        with patch.object(Path, "unlink", fail_published_link_cleanup), patch.dict(
            os.environ, self.env, clear=True
        ):
            with self.assertRaisesRegex(OSError, "post-publication cleanup failure"):
                module.execute(args)
        self.assertTrue(failed, "The failure must occur after a real hard-link publication")
        self.assertEqual(snapshot(self.project), before)
        self.run_installer("--upgrade")
        self.assertEqual(target.read_text(), "Synthetic upgrade addition.\n")
        self.run_installer("--verify")

    @unittest.skipUnless(os.name == "nt", "Windows deny-delete sharing semantics")
    def test_locked_published_target_retains_explicit_recovery_state(self):
        self.assert_locked_publication_recovery(lock_target=True)

    @unittest.skipUnless(os.name == "nt", "Windows deny-delete sharing semantics")
    def test_locked_staging_link_retains_explicit_cleanup_recovery_state(self):
        self.assert_locked_publication_recovery(lock_target=False)

    def assert_locked_publication_recovery(self, lock_target):
        import ctypes
        from ctypes import wintypes

        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        open_file = kernel.CreateFileW
        open_file.argtypes = [
            wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID,
            wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE,
        ]
        open_file.restype = wintypes.HANDLE
        close_handle = kernel.CloseHandle
        close_handle.argtypes = [wintypes.HANDLE]
        close_handle.restype = wintypes.BOOL
        module = self.backend_module()
        link = module.os.link
        target = self.bin / BINARY
        work = self.root / "retained publication recovery"
        work.mkdir()
        sentinel = self.project / "keep.txt"
        sentinel.write_text("Unowned content to preserve.\n")
        handles = []
        temporary_paths = []

        def publish_then_lock(source, destination, *args, **kwargs):
            link(source, destination, *args, **kwargs)
            if Path(destination) == target:
                locked_names = (source, destination) if lock_target else (source,)
                for locked_path in locked_names:
                    handle = open_file(str(locked_path), 0x80000000, 0x1 | 0x2, None, 3, 0x80, None)
                    if handle == wintypes.HANDLE(-1).value:
                        raise ctypes.WinError(ctypes.get_last_error())
                    handles.append(handle)
                temporary_paths.append(Path(source))

        args = module.parser().parse_args(self.arguments())
        outcome = None
        try:
            with patch.object(module.os, "link", publish_then_lock), patch.object(
                module.tempfile, "mkdtemp", return_value=str(work)
            ), patch.dict(os.environ, self.env, clear=True):
                try:
                    module.execute(args)
                except (OSError, module.InstallError) as error:
                    outcome = error
            self.assertEqual(len(handles), 2 if lock_target else 1)
            self.assertIsInstance(outcome, module.RecoveryRequired)
            self.assertIn(str(work), str(outcome))
            if lock_target:
                self.assertTrue(target.is_file())
            self.assertFalse((self.bin / RECEIPT).exists())
            if target.exists():
                self.assertEqual(digest(target), digest(self.core))
            self.assertEqual(digest(temporary_paths[0]), digest(self.core))
            self.assertEqual(sentinel.read_text(), "Unowned content to preserve.\n")
            recovery = json.loads((work / "recovery.json").read_text())
            entry = next(item for item in recovery if item["path"] == str(target))
            self.assertIsNone(entry["backup"])
            self.assertEqual(entry["replacement_sha256"], digest(self.core))
            self.assertTrue(entry["published"])
            self.assertIn(str(temporary_paths[0]), entry["temporary_files"])
        finally:
            for handle in handles:
                if not close_handle(handle):
                    raise ctypes.WinError(ctypes.get_last_error())

    def test_recovery_backups_survive_rollback_and_lock_cleanup_failures(self):
        self.run_installer()
        original_core = (self.bin / BINARY).read_bytes()
        with self.core.open("ab") as stream:
            stream.write(b"\n# synthetic recovery fixture\n")
        module = self.backend_module()
        write = module.atomic_write
        unlink = Path.unlink
        work = self.root / "retained recovery"
        work.mkdir()

        def fail_apply_and_restore(path, data, mode, expected):
            if path.name == "SKILL.md":
                raise OSError("Synthetic apply failure")
            if path == self.bin / BINARY and data == original_core:
                raise OSError("Synthetic restore failure")
            return write(path, data, mode, expected)

        def fail_lock_cleanup(path, *args, **kwargs):
            if path == self.bin / ".midden-install.lock":
                raise OSError("Synthetic lock cleanup failure")
            return unlink(path, *args, **kwargs)

        args = module.parser().parse_args(self.arguments("--upgrade"))
        with patch.object(module, "atomic_write", fail_apply_and_restore), patch.object(
            Path, "unlink", fail_lock_cleanup
        ), patch.object(module.tempfile, "mkdtemp", return_value=str(work)), patch.dict(
            os.environ, self.env, clear=True
        ):
            with self.assertRaises(module.RecoveryRequired) as error:
                module.execute(args)
        self.assertIn(str(work), str(error.exception))
        self.assertIn("Synthetic lock cleanup failure", str(error.exception))
        recovery = json.loads((work / "recovery.json").read_text())
        core_backup = next(item for item in recovery if item["path"] == str(self.bin / BINARY))
        self.assertEqual((work / core_backup["backup"]).read_bytes(), original_core)
        self.assertEqual(core_backup["sha256"], hashlib.sha256(original_core).hexdigest())

    def test_linked_skill_root_is_refused_without_touching_its_target(self):
        target = self.root / "unrelated skill area"
        target.mkdir()
        (target / "keep.txt").write_text("Unrelated fixture.\n")
        self.skills.parent.mkdir(parents=True)
        if os.name == "nt":
            quote = lambda value: "'" + str(value).replace("'", "''") + "'"
            command = (
                "New-Item -ItemType Junction -Path " + quote(self.skills)
                + " -Target " + quote(target) + " | Out-Null"
            )
            result = subprocess.run(
                ["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command],
                capture_output=True, text=True, timeout=30,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.skills.symlink_to(target, target_is_directory=True)
        before = snapshot(target)
        self.run_installer(success=False)
        self.assertEqual(snapshot(target), before)
        self.assertFalse(self.log.exists())

    def test_published_checksums_survive_a_canonical_lf_checkout(self):
        canonical = self.root / "canonical checkout bundles"
        shutil.copytree(ROOT / "bundles", canonical)
        for path in canonical.rglob("*"):
            if path.is_file() and path.suffix in (".md", ".yaml", ".css", ".lua"):
                path.write_bytes(path.read_bytes().replace(b"\r\n", b"\n"))
        self.bundle = canonical
        self.manifest = ROOT / "installer" / "bundle-manifest.json"
        self.run_installer()
        self.assert_bindings_and_links(self.skills, self.bin / BINARY, self.state)
        self.run_installer("--uninstall")

    def test_manifest_generator_matches_real_bundle_bytes(self):
        result = subprocess.run(
            [sys.executable, "-B", str(MANIFEST_WRITER), "--bundle-dir", str(self.bundle),
             "--out", str(self.root / "generated.json")],
            capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(
            sorted(json.loads((self.root / "generated.json").read_text())["files"],
                   key=lambda entry: entry["source"]),
            sorted(json.loads(self.manifest.read_text())["files"],
                   key=lambda entry: entry["source"]),
        )

    def test_two_artifact_install_accepts_package_root_and_direct_bundles(self):
        core, package = self.extracted_artifacts()
        before = snapshot(core)
        for bundle_root in (package, package / "bundles"):
            with self.subTest(bundle_root=bundle_root.name):
                result = subprocess.run(
                    [sys.executable, "-B", str(BACKEND),
                     "--core-binary", str(core / BINARY),
                     "--core-sha256", digest(core / BINARY),
                     "--bundle-root", str(bundle_root), "--project", str(self.project)],
                    env=self.env, capture_output=True, text=True, timeout=60,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assert_bindings_and_links(self.skills, self.bin / BINARY, self.state)
                self.run_installer("--verify")
                self.run_installer("--uninstall")
        self.assertEqual(snapshot(core), before)

    def test_packaged_powershell_launcher_accepts_native_option_spellings(self):
        shells = [name for name in ("powershell", "pwsh") if shutil.which(name)]
        if not shells:
            self.skipTest("No PowerShell launcher available; GNU options have separate coverage")
        core, package = self.extracted_artifacts()
        for shell in shells:
            with self.subTest(shell=shell):
                bundle_option = "-BundleRoot" if shell == "pwsh" else "-BundleDir"
                result = subprocess.run(
                    [shutil.which(shell), "-NoProfile", "-NonInteractive", "-File",
                     str(package / "install.ps1"), "-CoreBinary", str(core / BINARY),
                     "-CoreSha256", digest(core / BINARY), bundle_option, str(package),
                     "-ProjectDir", str(self.project)],
                    env=self.env, capture_output=True, text=True, timeout=60,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assert_bindings_and_links(self.skills, self.bin / BINARY, self.state)
                self.run_installer("--uninstall")

    def test_manifest_generation_writes_only_its_explicit_staged_output(self):
        generator = self.root / "isolated generator"
        generator.mkdir()
        for path in (BACKEND, MANIFEST_WRITER):
            shutil.copyfile(path, generator / path.name)
        output_dir = self.root / "staged metadata"
        output_dir.mkdir()
        sentinel = output_dir / "keep.txt"
        sentinel.write_text("Preserve staged neighbors.\n")
        output = output_dir / "bundle-manifest.json"
        before_source = snapshot(self.bundle)
        before_helper = snapshot(generator)
        source_times = {path: path.stat().st_mtime_ns for path in self.bundle.rglob("*") if path.is_file()}
        helper_times = {path: path.stat().st_mtime_ns for path in generator.iterdir()}
        published = ROOT / "installer" / "bundle-manifest.json"
        before_published = (published.read_bytes(), published.stat().st_mtime_ns)
        env = dict(self.env)
        env.pop("PYTHONDONTWRITEBYTECODE", None)
        package_source = self.root / "manifest source artifact"
        shutil.copytree(self.bundle, package_source / "bundles")
        for source in (self.bundle, package_source):
            result = subprocess.run(
                [sys.executable, str(generator / "make_manifest.py"),
                 "--bundle-dir", str(source), "--out", str(output)],
                cwd=self.root, env=env, capture_output=True, text=True, timeout=30,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(snapshot(self.bundle), before_source)
        self.assertEqual(set(snapshot(generator)), set(before_helper), "No implicit bytecode writes")
        self.assertEqual(snapshot(generator), before_helper, "No implicit bytecode writes")
        self.assertEqual({path: path.stat().st_mtime_ns for path in source_times}, source_times)
        self.assertEqual({path: path.stat().st_mtime_ns for path in helper_times}, helper_times)
        self.assertEqual((published.read_bytes(), published.stat().st_mtime_ns), before_published)
        self.assertEqual(set(snapshot(output_dir)), {"keep.txt", "bundle-manifest.json"})
        self.assertEqual(sentinel.read_text(), "Preserve staged neighbors.\n")
        self.assertEqual(json.loads(output.read_text()), json.loads(self.manifest.read_text()))

    def test_ambiguous_bundle_roots_are_refused_before_execution(self):
        core, package = self.extracted_artifacts()
        (package / "article").mkdir()
        result = subprocess.run(
            [sys.executable, "-B", str(BACKEND), "--core-binary", str(core / BINARY),
             "--core-sha256", digest(core / BINARY), "--bundle-root", str(package),
             "--project", str(self.project)],
            env=self.env, capture_output=True, text=True, timeout=60,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Ambiguous", result.stderr)
        self.assertFalse(self.log.exists())
        self.assertEqual(snapshot(self.project), {})

    def test_available_native_launchers_perform_install_verify_and_uninstall(self):
        launchers = []
        for name in ("powershell", "pwsh"):
            executable = shutil.which(name)
            if executable:
                launchers.append(
                    (name, [executable, "-NoProfile", "-NonInteractive", "-File", str(ROOT / "install.ps1")])
                )
        bash = os.environ.get("INSTALLER_TEST_BASH")
        if not bash and os.name == "nt":
            git = shutil.which("git")
            if git:
                for parent in Path(git).parents:
                    candidate = parent / "bin" / "bash.exe"
                    if candidate.is_file():
                        bash = str(candidate)
                        break
        elif not bash:
            bash = shutil.which("bash")
        if bash:
            launchers.append(("bash", [bash, "--noprofile", "--norc", str(ROOT / "install.sh")]))
        self.assertTrue(launchers, "At least one native launcher must be exercised")
        for name, launcher in launchers:
            with self.subTest(launcher=name):
                self.run_installer(launcher=launcher)
                self.run_installer("--verify", launcher=launcher)
                self.run_installer("--uninstall", launcher=launcher)

    @unittest.skipUnless(os.name == "nt", "Windows native interpreter discovery")
    def test_powershell_selects_one_suitable_python_and_preserves_explicit_override(self):
        shells = [shutil.which(name) for name in ("powershell", "pwsh")]
        shells = [shell for shell in shells if shell]
        self.assertTrue(shells, "A native PowerShell launcher is required")
        # Venv redirectors need pyvenv.cfg; relocated candidates need the base runtime.
        base_python = Path(getattr(sys, "_base_executable", None) or sys.executable)
        self.assertTrue(base_python.is_file(), "The native base interpreter must exist")
        candidates = {}
        for label in ("python-first", "python-second", "python-override"):
            directory = self.root / label
            directory.mkdir()
            candidates[label] = directory / "python.exe"
            shutil.copy2(base_python, candidates[label])
        support = self.root / "interpreter discovery fixture"
        support.mkdir()
        choice_log = support / "choices.txt"
        # Real interpreter copies exercise native lookup; this hook can reject one.
        (support / "sitecustomize.py").write_text(
            "import os\n"
            "from pathlib import Path\n"
            "import sys\n"
            "label = Path(sys.executable).parent.name\n"
            "with open(os.environ['MIDDEN_UNIT_PYTHON_CHOICES'], 'a', encoding='utf-8') as stream:\n"
            "    stream.write(label + '\\n')\n"
            "if label == os.environ.get('MIDDEN_UNIT_REJECT_PYTHON'):\n"
            "    os._exit(1)\n",
            encoding="utf-8",
        )
        cases = (
            ("path-first", None, "", ["python-first", "python-first"], True),
            ("path-next", None, "python-first",
             ["python-first", "python-second", "python-second"], True),
            ("override", candidates["python-override"], "",
             ["python-override", "python-override"], True),
            ("override-rejected", candidates["python-override"], "python-override",
             ["python-override"], False),
        )
        for shell in shells:
            for name, override, rejected, choices, success in cases:
                with self.subTest(shell=Path(shell).name, selection=name):
                    choice_log.unlink(missing_ok=True)
                    env = dict(self.env)
                    env.pop("PYTHON", None)
                    env.update(
                        PATH=os.pathsep.join(
                            [str(candidates["python-first"].parent),
                             str(candidates["python-second"].parent),
                             str(base_python.parent),
                             str(Path(os.environ["SystemRoot"]) / "System32")]
                        ),
                        PYTHONHOME=sys.base_prefix,
                        PYTHONPATH=str(support),
                        PYTHONNOUSERSITE="1",
                        PYTHONDONTWRITEBYTECODE="1",
                        MIDDEN_UNIT_PYTHON_CHOICES=str(choice_log),
                        MIDDEN_UNIT_REJECT_PYTHON=rejected,
                    )
                    if override is not None:
                        env["PYTHON"] = str(override)
                    before = snapshot(self.project)
                    self.run_installer(
                        launcher=[shell, "-NoProfile", "-NonInteractive", "-File",
                                  str(ROOT / "install.ps1")],
                        env=env, success=success,
                    )
                    self.assertEqual(choice_log.read_text().splitlines(), choices)
                    if success:
                        self.assertEqual(digest(self.bin / BINARY), digest(self.core))
                        self.run_installer("--uninstall")
                    else:
                        self.assertEqual(snapshot(self.project), before)

    def test_shipped_bundle_manifest_and_installed_pandoc_defaults(self):
        pandoc = shutil.which(os.environ.get("PANDOC", "pandoc"))
        if not pandoc:
            self.skipTest("Pandoc is required for installed-template rendering")
        self.bundle = ROOT / "bundles"
        self.manifest = ROOT / "installer" / "bundle-manifest.json"
        self.run_installer()
        self.assert_bindings_and_links(self.skills, self.bin / BINARY, self.state)
        for name in ("inspect_html.py", "html-inspection.md"):
            self.assertEqual(
                (self.skills / "midden-shared" / name).read_bytes(),
                (ROOT / "bundles" / "midden-shared" / name).read_bytes(),
            )
        source = self.root / "synthetic.md"
        source.write_text("---\ntitle: Synthetic install check\n---\n\n# Evidence\n\nSynthetic text.\n")
        for relative, output in (
            (("midden-presentation", "templates", "html.yaml"), "slides.html"),
            (("midden-long-form", "templates", "html.yaml"), "book.html"),
            (("midden-long-form", "templates", "epub.yaml"), "book.epub"),
        ):
            result = subprocess.run(
                [pandoc, "--defaults", str(self.skills.joinpath(*relative)),
                 str(source), "--output", str(self.root / output)],
                cwd=self.root, capture_output=True, text=True, timeout=60,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertGreater((self.root / output).stat().st_size, 0)
        self.run_installer("--uninstall")


if __name__ == "__main__":
    unittest.main()
