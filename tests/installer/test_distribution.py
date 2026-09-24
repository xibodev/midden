"""Local release installation using public synthetic archives, never a model."""

import gzip
import io
import json
import os
from contextlib import redirect_stdout
from pathlib import Path
import platform
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
from types import SimpleNamespace
from unittest.mock import patch
import warnings
import zipfile

import test_install as fixtures


SYSTEM = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
ARCH = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[
    platform.machine().lower()
]
TARGET = SYSTEM + "/" + ARCH


def write_archive(path, entries):
    if path.suffix == ".zip":
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
            for name, data in entries:
                entry = zipfile.ZipInfo(name)
                # ZipInfo otherwise normalizes backslashes on Windows before writing.
                entry.filename = name
                entry.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(entry, data)
    else:
        with tarfile.open(path, "w:gz") as archive:
            for name, data in entries:
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                entry.mode = 0o755 if name == "midden" else 0o644
                archive.addfile(entry, io.BytesIO(data))


class DistributionTests(fixtures.InstallerFixture):
    def setUp(self):
        super().setUp()
        core, package = self.extracted_artifacts()
        self.distribution = self.root / "built release with spaces"
        self.distribution.mkdir()
        self.core_entries = [(path.name, path.read_bytes()) for path in core.iterdir()]
        self.bundle_entries = [
            (path.relative_to(package).as_posix(), path.read_bytes())
            for path in package.rglob("*") if path.is_file()
        ]
        suffix = ".zip" if SYSTEM == "windows" else ".tar.gz"
        self.core_archive = self.distribution / f"midden-core_0.3.0-dev_{SYSTEM}_{ARCH}{suffix}"
        self.bundle_archive = self.distribution / "midden-bundle_0.3.0-dev.zip"
        write_archive(self.core_archive, self.core_entries)
        write_archive(self.bundle_archive, self.bundle_entries)
        self.build_manifest = {
            "version": "0.3.0-dev",
            "commit": "a" * 40,
            "go": "go version go1.26.5 " + TARGET,
            "targets": [TARGET],
            "products": ["core", "bundle"],
            "core_binaries": {TARGET: fixtures.digest(self.core)},
            "archives": [self.core_archive.name, self.bundle_archive.name],
        }
        self.seal_distribution()

    def seal_distribution(self):
        manifest = self.distribution / "build-manifest.json"
        manifest.write_text(json.dumps(self.build_manifest), encoding="utf-8")
        paths = [manifest] + [
            self.distribution / name for name in self.build_manifest["archives"]
        ]
        (self.distribution / "SHA256SUMS").write_text(
            "".join(fixtures.digest(path) + "  " + path.name + "\n" for path in sorted(paths)),
            encoding="utf-8",
        )

    def distribution_arguments(self, *extra):
        return [
            "--distribution-dir", str(self.distribution),
            "--project", str(self.project), "--home-dir", str(self.home),
            *map(str, extra),
        ]

    def run_distribution(self, *extra, success=True, launcher=None, env=None):
        result = subprocess.run(
            (launcher or [sys.executable, "-B", str(fixtures.BACKEND)])
            + self.distribution_arguments(*extra),
            env=env or self.env, capture_output=True, text=True,
            encoding="utf-8", errors="replace", timeout=60,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("Traceback", result.stderr)
        return result

    def assert_rejected(self, diagnostic, *extra, env=None):
        before = fixtures.snapshot(self.project)
        directories = sorted(str(path) for path in self.project.rglob("*") if path.is_dir())
        result = self.run_distribution(*extra, success=False, env=env)
        self.assertIn(diagnostic.lower(), result.stderr.lower())
        self.assertEqual(fixtures.snapshot(self.project), before)
        self.assertEqual(
            sorted(str(path) for path in self.project.rglob("*") if path.is_dir()), directories
        )
        self.assertFalse(self.log.exists(), "Rejected archives must never execute the core")

    def test_distribution_install_upgrade_verify_uninstall_preserves_unowned_data(self):
        source_before = fixtures.snapshot(self.distribution)
        self.state.mkdir(parents=True)
        (self.state / "keep.txt").write_text("Synthetic retained state.\n")
        result = self.run_distribution()
        self.assertIn("Installed midden 0.3.0-dev", result.stdout)
        self.assertEqual(fixtures.digest(self.bin / fixtures.BINARY), fixtures.digest(self.core))
        self.assert_bindings_and_links(self.skills, self.bin / fixtures.BINARY, self.state)
        self.assertEqual(self.log.read_text().splitlines(), ["version|" + str(self.state)])
        note = self.skills / "midden-article" / "personal.md"
        note.write_text("Keep this unowned note.\n")
        self.assertEqual(fixtures.snapshot(self.distribution), source_before)
        with self.core.open("ab") as stream:
            stream.write(b"\n# synthetic next release\n")
        (self.bundle / "article" / "obsolete.md").unlink()
        (self.bundle / "article" / "new.md").write_text("Synthetic new release guidance.\n")
        fixtures.fixture_manifest(self.bundle, self.manifest)
        core_entries = [
            (name, self.core.read_bytes() if name == fixtures.BINARY else data)
            for name, data in self.core_entries
        ]
        bundle_entries = [
            (name, self.manifest.read_bytes() if name == "installer/bundle-manifest.json" else data)
            for name, data in self.bundle_entries if name != "bundles/article/obsolete.md"
        ] + [("bundles/article/new.md", (self.bundle / "article" / "new.md").read_bytes())]
        self.core_archive = self.core_archive.with_name(self.core_archive.name.replace("0.3.0-dev", "0.3.1"))
        self.bundle_archive = self.bundle_archive.with_name("midden-bundle_0.3.1.zip")
        write_archive(self.core_archive, core_entries)
        write_archive(self.bundle_archive, bundle_entries)
        self.build_manifest.update(
            version="0.3.1", core_binaries={TARGET: fixtures.digest(self.core)},
            archives=[self.core_archive.name, self.bundle_archive.name],
        )
        self.seal_distribution()
        source_before = fixtures.snapshot(self.distribution)
        self.run_distribution("--upgrade", env=dict(self.env, MIDDEN_UNIT_VERSION="midden 0.3.1"))
        self.assertEqual(fixtures.snapshot(self.distribution), source_before)
        self.assertEqual(fixtures.digest(self.bin / fixtures.BINARY), fixtures.digest(self.core))
        self.assertEqual(json.loads((self.bin / fixtures.RECEIPT).read_text())["version"], "midden 0.3.1")
        self.assertFalse((self.skills / "midden-article" / "obsolete.md").exists())
        self.assertEqual(
            (self.skills / "midden-article" / "new.md").read_text(), "Synthetic new release guidance.\n"
        )
        shutil.rmtree(self.distribution)
        self.run_installer("--verify")
        self.run_installer("--uninstall")
        self.assertEqual(note.read_text(), "Keep this unowned note.\n")
        self.assertEqual((self.state / "keep.txt").read_text(), "Synthetic retained state.\n")
        self.assertFalse((self.bin / fixtures.RECEIPT).exists())
        self.assertFalse((self.bin / fixtures.BINARY).exists())

    def test_selected_archive_corruption_is_rejected_before_execution(self):
        for archive in (self.core_archive, self.bundle_archive):
            with self.subTest(archive=archive.name):
                original = archive.read_bytes()
                archive.write_bytes(original + b"synthetic corruption")
                self.assert_rejected("archive checksum")
                archive.write_bytes(original)

    def test_manifest_corruption_is_rejected_before_using_its_binary_digest(self):
        manifest = self.distribution / "build-manifest.json"
        manifest.write_bytes(manifest.read_bytes() + b"\n")
        self.assert_rejected("build manifest checksum")

    def test_binary_digest_is_verified_even_when_archive_checksums_match(self):
        self.build_manifest["core_binaries"][TARGET] = "0" * 64
        self.seal_distribution()
        self.assert_rejected("core checksum")

    def test_missing_duplicate_and_unsafe_checksum_entries_are_rejected(self):
        checksum_file = self.distribution / "SHA256SUMS"
        original = checksum_file.read_text()
        cases = (
            "\n".join(original.splitlines()[:-1]) + "\n",
            original + original.splitlines()[0] + "\n",
            original + "0" * 64 + "  ../outside.zip\n",
        )
        for content in cases:
            with self.subTest(content=content[-80:]):
                checksum_file.write_text(content, encoding="utf-8")
                self.assert_rejected("checksum")

    def test_archive_version_mismatch_is_rejected_even_with_matching_checksums(self):
        self.build_manifest["version"] = "0.3.1"
        self.seal_distribution()
        self.assert_rejected("archive")

    def test_version_probe_must_match_the_build_manifest_before_installation(self):
        result = self.run_distribution(
            success=False, env=dict(self.env, MIDDEN_UNIT_VERSION="midden 0.3.1")
        )
        self.assertIn("version", result.stderr.lower())
        self.assertIn("manifest", result.stderr.lower())
        self.assertEqual(fixtures.snapshot(self.project), {})
        self.assertTrue(self.log.exists(), "Only the verified version probe may run")

    def test_native_archive_must_be_declared_instead_of_guessing_a_foreign_one(self):
        other = "linux/arm64" if TARGET != "linux/arm64" else "windows/amd64"
        system, arch = other.split("/")
        suffix = ".zip" if system == "windows" else ".tar.gz"
        replacement = self.distribution / f"midden-core_0.3.0-dev_{system}_{arch}{suffix}"
        write_archive(replacement, self.core_entries)
        self.build_manifest["targets"] = [other]
        self.build_manifest["core_binaries"] = {other: fixtures.digest(self.core)}
        self.build_manifest["archives"] = [replacement.name, self.bundle_archive.name]
        self.seal_distribution()
        self.assert_rejected("native")

    def test_native_detection_maps_supported_systems_without_unknown_architecture_fallback(self):
        module = self.backend_module()
        for system, machine, expected in (
            ("win32", "AMD64", "windows/amd64"),
            ("linux", "x86_64", "linux/amd64"),
            ("linux", "aarch64", "linux/arm64"),
            ("darwin", "x86_64", "darwin/amd64"),
            ("darwin", "arm64", "darwin/arm64"),
            ("linux", "armv7l", None),
            ("win32", "x86", None),
            ("freebsd", "x86_64", None),
        ):
            with self.subTest(system=system, machine=machine), patch.object(
                module.sys, "platform", system
            ), patch.object(module.os, "uname", return_value=SimpleNamespace(machine=machine), create=True), patch.dict(
                os.environ, PROCESSOR_ARCHITECTURE=machine, PROCESSOR_ARCHITEW6432=""
            ):
                if expected is None:
                    with self.assertRaisesRegex(module.InstallError, "native"):
                        module.native_target()
                else:
                    self.assertEqual(module.native_target(), expected)
        with patch.object(module.sys, "platform", "win32"), patch.dict(
            os.environ, PROCESSOR_ARCHITECTURE="x86", PROCESSOR_ARCHITEW6432="AMD64"
        ):
            self.assertEqual(module.native_target(), "windows/amd64")

    def test_matching_zip_and_tar_are_selected_from_a_multi_target_build(self):
        targets = ("windows/amd64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")
        names = {}
        for target in targets:
            system, arch = target.split("/")
            suffix = ".zip" if system == "windows" else ".tar.gz"
            name = f"midden-core_0.3.0-dev_{system}_{arch}{suffix}"
            binary = "midden.exe" if system == "windows" else "midden"
            write_archive(self.distribution / name, [
                (binary, self.core.read_bytes()), ("LICENSE", b"Synthetic license"),
                ("CORE.md", b"Synthetic core guide"),
            ])
            names[target] = name
        self.build_manifest["targets"] = list(targets)
        self.build_manifest["core_binaries"] = {target: fixtures.digest(self.core) for target in targets}
        self.build_manifest["archives"] = list(names.values()) + [self.bundle_archive.name]
        self.seal_distribution()
        module = self.backend_module()
        for system, machine, expected in (
            ("win32", "AMD64", "windows/amd64"), ("linux", "x86_64", "linux/amd64"),
            ("linux", "aarch64", "linux/arm64"), ("darwin", "x86_64", "darwin/amd64"),
            ("darwin", "arm64", "darwin/arm64"),
        ):
            with self.subTest(target=expected), patch.object(
                module.sys, "platform", system
            ), patch.object(module.os, "uname", return_value=SimpleNamespace(machine=machine), create=True), patch.dict(
                os.environ, PROCESSOR_ARCHITECTURE=machine, PROCESSOR_ARCHITEW6432=""
            ):
                selected = module.verified_distribution(self.distribution)
                self.assertEqual(selected.target, expected)
                self.assertEqual(selected.core_archive, names[expected])
                binary = "midden.exe" if system == "win32" else "midden"
                self.assertEqual(selected.core[binary], self.core.read_bytes())
        self.assertFalse(self.log.exists())
        self.assertFalse(self.bin.exists())

    def test_tar_links_devices_and_traversal_are_refused_without_extracting(self):
        module = self.backend_module()
        for kind, name in (
            (tarfile.SYMTYPE, "link"), (tarfile.LNKTYPE, "link"),
            (tarfile.FIFOTYPE, "fifo"), (tarfile.CHRTYPE, "device"),
            (tarfile.REGTYPE, "../escaped"), (tarfile.REGTYPE, "C:/escaped"),
        ):
            with self.subTest(kind=kind, name=name):
                buffer = io.BytesIO()
                with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    entry.linkname = "../outside" if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE) else ""
                    archive.addfile(entry)
                with self.assertRaisesRegex(module.InstallError, "archive"):
                    module.archive_members(buffer.getvalue(), "synthetic.tar.gz")
        self.assertEqual(fixtures.snapshot(self.project), {})

    def test_archive_expansion_and_member_count_have_fixed_limits(self):
        module = self.backend_module()
        for suffix in (".zip", ".tar.gz"):
            with self.subTest(suffix=suffix, limit="members"):
                path = self.root / ("many-members" + suffix)
                write_archive(path, [(f"file-{number}", b"x") for number in range(4097)])
                with self.assertRaisesRegex(module.InstallError, "member limit"):
                    module.archive_members(path.read_bytes(), path.name)
        path = self.root / "expanded.zip"
        write_archive(path, [("synthetic.md", b"x")])
        data = bytearray(path.read_bytes())
        central = data.index(b"PK\x01\x02")
        struct.pack_into("<I", data, central + 24, 256 * 1024 * 1024 + 1)
        entry = tarfile.TarInfo("synthetic.md")
        entry.size = 256 * 1024 * 1024 + 1
        oversized_tar = gzip.compress(entry.tobuf() + b"\0" * 1024)
        for data, name in ((data, "expanded.zip"), (oversized_tar, "expanded.tar.gz")):
            with self.subTest(name=name), self.assertRaisesRegex(module.InstallError, "size limit"):
                module.archive_members(data, name)

    def test_metadata_size_is_bounded_before_parsing(self):
        (self.distribution / "SHA256SUMS").write_bytes(b"x" * (1024 * 1024 + 1))
        self.assert_rejected("size limit")

    def test_tar_extended_headers_cannot_bypass_the_expansion_limit(self):
        module = self.backend_module()
        entry = tarfile.TarInfo("synthetic-extended-header")
        entry.type = tarfile.XHDTYPE
        entry.size = 4096
        data = gzip.compress(entry.tobuf() + b"x" * 4096 + b"\0" * 1024)
        with patch.object(module, "ARCHIVE_LIMIT", 2048):
            with self.assertRaisesRegex(module.InstallError, "size limit"):
                module.archive_members(data, "synthetic.tar.gz")

    def test_extraction_is_external_and_removed_after_a_real_probe(self):
        module = self.backend_module()
        original = module.probe
        stages = []

        def observe_probe(data, ctx, work):
            self.assertNotIn(self.project, work.parents)
            self.assertNotIn(self.distribution, work.parents)
            self.assertEqual((work / "core" / fixtures.BINARY).read_bytes(), self.core.read_bytes())
            staged = work / "bundle" / "bundles" / "article" / "SKILL.md"
            self.assertEqual(staged.read_bytes(), (self.bundle / "article" / "SKILL.md").read_bytes())
            stages.append(work)
            return original(data, ctx, work)

        args = module.parser().parse_args(self.distribution_arguments())
        with patch.object(module, "probe", observe_probe), patch.dict(
            os.environ, self.env, clear=True
        ), redirect_stdout(io.StringIO()):
            module.execute(args)
        self.assertEqual(len(stages), 1)
        self.assertFalse(stages[0].exists())
        self.assertEqual(self.log.read_text().splitlines(), ["version|" + str(self.state)])

    def test_temporary_parent_inside_project_is_refused_before_execution(self):
        temporary = self.project / "scratch"
        temporary.mkdir()
        env = dict(self.env, TMP=str(temporary), TEMP=str(temporary), TMPDIR=str(temporary))
        self.assert_rejected("temporary", env=env)

    def directory_link(self, name, target):
        link = self.root / name
        if os.name == "nt":
            shell = shutil.which("powershell") or shutil.which("pwsh")
            if not shell:
                self.skipTest("PowerShell is required for the Windows junction fixture")
            quote = lambda value: "'" + str(value).replace("'", "''") + "'"
            result = subprocess.run(
                [shell, "-NoProfile", "-NonInteractive", "-Command",
                 f"New-Item -ItemType Junction -Path {quote(link)} -Target {quote(target)} | Out-Null"],
                capture_output=True, text=True, timeout=30,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.addCleanup(link.rmdir)
        else:
            try:
                link.symlink_to(target, target_is_directory=True)
            except OSError as error:
                self.skipTest(f"Directory symlinks unavailable: {error}")
            self.addCleanup(link.unlink)
        self.assertEqual(link.resolve(), target.resolve())
        return link

    def test_linked_temporary_ancestor_uses_canonical_external_parent_for_install_and_upgrade(self):
        canonical = self.root / "canonical temporary area"
        temporary = canonical / "scratch"
        temporary.mkdir(parents=True)
        link = self.directory_link("temporary alias", canonical)
        linked_temporary = link / "scratch"
        env = dict(self.env, TMP=str(linked_temporary), TEMP=str(linked_temporary), TMPDIR=str(linked_temporary))
        self.run_distribution("--plan", launcher=fixtures.read_only_launcher(), env=env)
        self.assertFalse(self.log.exists())
        self.assertEqual(list(temporary.iterdir()), [])
        self.run_distribution(env=env)
        self.run_distribution("--upgrade", env=env)
        self.assertEqual(fixtures.digest(self.bin / fixtures.BINARY), fixtures.digest(self.core))
        self.assertEqual(self.log.read_text().splitlines(), ["version|" + str(self.state)] * 2)
        self.assertEqual(list(temporary.iterdir()), [])
        module = self.backend_module()
        args = module.parser().parse_args(self.distribution_arguments())
        ctx, _ = module.load_context(args)
        with patch.object(module.tempfile, "gettempdir", return_value=str(linked_temporary)):
            self.assertEqual(module.distribution_temporary_parent(ctx, self.distribution), temporary)
        self.run_installer("--verify")
        self.run_installer("--uninstall")

    def test_linked_temporary_parent_still_refuses_canonical_protected_locations(self):
        package = self.root / "extracted bundle"
        cases = (
            ("project", self.project, (), None),
            ("distribution", self.distribution, (), None),
            ("binary", self.root / "separate bin", ("--bin-dir", self.root / "separate bin"), None),
            ("state", self.root / "separate state", ("--state-dir", self.root / "separate state"), None),
            ("skills", self.home / ".claude" / "skills", ("--scope", "user", "--host", "claude"), None),
            ("installer", package, (), [sys.executable, "-B", str(package / "installer" / "install.py")]),
        )
        for name, target, options, launcher in cases:
            target.mkdir(parents=True, exist_ok=True)
            (target / "scratch").mkdir()
            link = self.directory_link("protected alias " + name, target)
            for suffix in ("", "scratch"):
                with self.subTest(location=name, suffix=suffix):
                    temporary = link / suffix
                    env = dict(self.env, TMP=str(temporary), TEMP=str(temporary), TMPDIR=str(temporary))
                    before = fixtures.snapshot(target)
                    result = self.run_distribution(*options, success=False, launcher=launcher, env=env)
                    self.assertIn("external temporary directory", result.stderr)
                    self.assertNotIn("linked/reparse", result.stderr)
                    self.assertEqual(fixtures.snapshot(target), before)
                    self.assertFalse(self.log.exists())

    def test_unsafe_archive_names_are_rejected_on_every_platform(self):
        for name in (
            "../escaped.md", "/absolute.md", "C:/absolute.md", "bundles\\escaped.md",
            "bundles/article/../../escaped.md", "bundles/article/note.md:stream",
            "bundles/article/NUL", "bundles/article/trailing. ",
            "bundles/article/SKILL.md/child", "bundles/ARTICLE/skill.md",
        ):
            with self.subTest(name=name):
                write_archive(self.bundle_archive, self.bundle_entries + [(name, b"synthetic")])
                self.seal_distribution()
                self.assert_rejected("archive")

    def test_zip_links_and_duplicate_members_are_rejected(self):
        for kind in ("symlink", "duplicate"):
            with self.subTest(kind=kind):
                write_archive(self.bundle_archive, self.bundle_entries)
                with zipfile.ZipFile(self.bundle_archive, "a") as archive:
                    if kind == "symlink":
                        entry = zipfile.ZipInfo("bundles/article/link.md")
                        entry.create_system = 3
                        entry.external_attr = (stat.S_IFLNK | 0o777) << 16
                        archive.writestr(entry, "../outside.md")
                    else:
                        with warnings.catch_warnings():
                            warnings.filterwarnings("ignore", message="Duplicate name:", category=UserWarning)
                            archive.writestr("LICENSE", b"duplicate synthetic license")
                self.seal_distribution()
                self.assert_rejected("archive")

    def test_bundle_manifest_remains_authoritative_for_installed_sources(self):
        changed = [
            (name, b"changed synthetic guidance" if name == "bundles/article/SKILL.md" else data)
            for name, data in self.bundle_entries
        ]
        write_archive(self.bundle_archive, changed)
        self.seal_distribution()
        self.assert_rejected("bundle checksum")

    def test_empty_bundle_manifest_path_is_rejected_without_a_traceback(self):
        manifest = json.loads(self.manifest.read_text())
        manifest["files"][0]["source"] = "."
        changed = [
            (name, json.dumps(manifest).encode() if name == "installer/bundle-manifest.json" else data)
            for name, data in self.bundle_entries
        ]
        write_archive(self.bundle_archive, changed)
        self.seal_distribution()
        self.assert_rejected("path")

    def test_windows_device_aliases_are_refused_before_any_extraction(self):
        module = self.backend_module()
        for name in ("bundles/article/COM\xb9.txt", "bundles/article/LPT\xb2.md"):
            with self.subTest(name=name), self.assertRaisesRegex(module.InstallError, "archive"):
                module.archive_path(name)

    def test_mixed_distribution_and_explicit_sources_are_refused(self):
        for option, value in (
            ("--core-binary", self.core), ("--core-sha256", fixtures.digest(self.core)),
            ("--bundle-root", self.bundle), ("--manifest", self.manifest),
        ):
            with self.subTest(option=option):
                self.assert_rejected("combine", option, value)

    def test_modified_owned_files_block_all_distribution_lifecycle_operations(self):
        self.run_distribution()
        installed = self.skills / "midden-article" / "SKILL.md"
        installed.write_text(installed.read_text() + "\nKeep this local edit.\n")
        self.log.unlink()
        for operation in ("--upgrade", "--verify", "--uninstall"):
            with self.subTest(operation=operation):
                self.assert_rejected("modified", operation)

    def test_distribution_upgrade_uses_existing_transaction_rollback(self):
        self.run_distribution()
        before = fixtures.snapshot(self.project)
        module = self.backend_module()
        original = module.atomic_write
        failed = False

        def fail_after_binary(path, data, mode, expected):
            nonlocal failed
            if path.name == "SKILL.md" and not failed:
                failed = True
                raise OSError("Synthetic distribution write failure")
            return original(path, data, mode, expected)

        args = module.parser().parse_args(self.distribution_arguments("--upgrade"))
        with patch.object(module, "atomic_write", fail_after_binary), patch.dict(
            os.environ, self.env, clear=True
        ):
            with self.assertRaisesRegex(OSError, "Synthetic distribution write failure"):
                module.execute(args)
        self.assertTrue(failed)
        self.assertEqual(fixtures.snapshot(self.project), before)
        self.run_installer("--verify")

    def test_distribution_plan_verifies_inputs_without_extraction_writes_or_execution(self):
        before = fixtures.snapshot(self.root)
        result = self.run_distribution("--plan", launcher=fixtures.read_only_launcher())
        plan = json.loads(result.stdout)
        self.assertEqual(plan["operation"], "install")
        self.assertEqual(plan["distribution"]["target"], TARGET)
        self.assertEqual(plan["distribution"]["core_archive"], self.core_archive.name)
        self.assertEqual(plan["distribution"]["bundle_archive"], self.bundle_archive.name)
        self.assertEqual(plan["distribution"]["declared_version"], "0.3.0-dev")
        self.assertIn("not cryptographic authenticity", json.dumps(plan["notes"]).lower())
        planned = {entry["path"] for entry in plan["destinations"]}
        self.assertEqual(fixtures.snapshot(self.root), before)
        self.assertFalse(self.bin.exists())
        self.assertFalse(self.log.exists())
        self.run_distribution()
        installed = {str(path) for path in self.project.rglob("*") if path.is_file()}
        self.assertEqual(installed, planned)

    def test_distribution_plan_rejects_corruption_instead_of_reporting_a_valid_plan(self):
        self.core_archive.write_bytes(self.core_archive.read_bytes() + b"synthetic corruption")
        result = self.run_distribution(
            "--dry-run", success=False, launcher=fixtures.read_only_launcher()
        )
        self.assertIn("Archive checksum", result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertFalse(self.bin.exists())
        self.assertFalse(self.log.exists())

    def test_packaged_powershell_distribution_alias_runs_the_real_installer(self):
        shells = [shutil.which(name) for name in ("powershell", "pwsh")]
        shells = [shell for shell in shells if shell]
        if not shells:
            self.skipTest("PowerShell unavailable; the Python entry point is covered separately")
        package = self.root / "extracted bundle"
        for shell in shells:
            with self.subTest(shell=Path(shell).name):
                result = subprocess.run(
                    [shell, "-NoProfile", "-NonInteractive", "-File",
                     str(package / "install.ps1"), "-DistributionDir", str(self.distribution),
                     "-ProjectDir", str(self.project)],
                    env=self.env, capture_output=True, text=True, timeout=60,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(fixtures.digest(self.bin / fixtures.BINARY), fixtures.digest(self.core))
                self.run_installer("--uninstall")
                result = subprocess.run(
                    [shell, "-NoProfile", "-NonInteractive", "-File",
                     str(package / "install.ps1"), "-DistributionDir", str(self.distribution),
                     "-ProjectDir", str(self.project), "-DryRun"],
                    env=self.env, capture_output=True, text=True, timeout=60,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(json.loads(result.stdout)["operation"], "install")
                self.assertFalse((self.bin / fixtures.RECEIPT).exists())
