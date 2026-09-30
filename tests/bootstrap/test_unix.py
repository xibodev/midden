"""Unix bootstrap contracts; Git Bash exercises shell logic, not native binaries.

All installations, PATH links, downloads and executable probes use synthetic
temporary homes. Native CLI integration additionally runs on Linux/macOS.
"""

import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parents[2]
BOOTSTRAP = ROOT / "bootstrap" / "install.sh"
WINDOWS = os.name == "nt"
SHELL = os.environ.get("MIDDEN_UNIX_TEST_SHELL") or (
    r"C:\Program Files\Git\usr\bin\sh.exe" if WINDOWS else shutil.which("sh")
)
RECEIPT = ".midden-bootstrap-receipt.tsv"
BUNDLE_NAMES = (
    "README.md", "article/SKILL.md", "investigation/SKILL.md",
    "long-form/SKILL.md", "long-form/templates/epub.yaml",
    "long-form/templates/html.yaml", "midden-shared/tools.md",
    "midden-shared/sources.md", "midden-shared/inspect_html.py",
    "midden-shared/html-inspection.md", "presentation/SKILL.md",
    "presentation/templates/slides.css", "presentation/templates/html.yaml",
    "presentation/templates/explicit-slides.lua",
)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def unix_path(path):
    value = str(path)
    if WINDOWS:
        directory = Path(SHELL).parent
        converter = directory / "cygpath.exe"
        if not converter.is_file():
            converter = directory.parent / "usr" / "bin" / "cygpath.exe"
        if not converter.is_file():
            raise RuntimeError("Git Bash cygpath is required for Windows bootstrap tests")
        # The runtime may prefer a mount alias such as /tmp over a drive path.
        return subprocess.check_output(
            [str(converter), "-u", value], text=True, encoding="utf-8", timeout=15,
        ).rstrip("\r\n")
    return value


def write_tar(path, entries):
    with tarfile.open(path, "w:gz", format=tarfile.PAX_FORMAT) as archive:
        for name, data in entries:
            info = tarfile.TarInfo(name)
            info.mode = 0o755 if name in ("midden", "midden-ui") else 0o644
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))


@unittest.skipUnless(SHELL and Path(SHELL).is_file(), "A native sh or inspected Git Bash is required")
class UnixBootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="midden-unix-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.home = self.root / "synthetic home"
        self.project = self.root / "project"
        self.install = self.home / ".local" / "lib" / "midden"
        self.dist = self.root / "release with spaces"
        self.tmp = self.root / "external temporary"
        self.tools = self.root / "tools"
        for path in (self.home, self.project, self.dist, self.tmp, self.tools):
            path.mkdir()
        self.log = self.root / "executions"
        self.curl_log = self.root / "curl-arguments"
        self.system = "darwin" if sys.platform == "darwin" else "linux"
        machine = platform.machine().lower()
        self.arch = "arm64" if machine in ("aarch64", "arm64") else "amd64"
        runtime_temp = runtime_tmp = str(self.tmp)
        if WINDOWS:
            # MSYS shares its usertemp mount across overlapping shell processes.
            # Only TMPDIR may point at staging removed by an individual test.
            runtime_temp = os.environ.get("TEMP") or tempfile.gettempdir()
            runtime_tmp = os.environ.get("TMP") or tempfile.gettempdir()
        self.env = dict(
            os.environ, HOME=unix_path(self.home), USERPROFILE=str(self.home),
            XDG_DATA_HOME=unix_path(self.home / "data"),
            MIDDEN_HOME=unix_path(self.home / "data" / "midden"),
            TMPDIR=unix_path(self.tmp), TEMP=runtime_temp, TMP=runtime_tmp,
            PATH=unix_path(self.tools) + ":/usr/bin:/bin",
            MSYS="winsymlinks:lnk",
            MIDDEN_TEST_LOG=unix_path(self.log),
            MIDDEN_TEST_CURL_LOG=unix_path(self.curl_log),
            MIDDEN_TEST_DIST=unix_path(self.dist),
            MIDDEN_TEST_SYSTEM="Darwin" if self.system == "darwin" else "Linux",
            MIDDEN_TEST_MACHINE="aarch64" if self.arch == "arm64" else "x86_64",
        )
        for key in ("BASH_ENV", "ENV", "CDPATH", "PYTHON", "TAR_OPTIONS", "GZIP", "COMPA_HOME"):
            self.env.pop(key, None)
        self.tool("uname", """case "$1" in
  -s) printf '%s\\n' "$MIDDEN_TEST_SYSTEM";;
  -m) printf '%s\\n' "$MIDDEN_TEST_MACHINE";;
  *) exit 2;;
esac
""")
        self.tool("curl", """printf '%s\\n' "$@" >> "$MIDDEN_TEST_CURL_LOG"
for value do url=$value; done
case "$url" in https://github.com/*/releases/*/download/*|https://github.com/*/releases/download/*) ;;
  *) printf 'Unexpected release URL: %s\\n' "$url" >&2; exit 22;;
esac
[ "${MIDDEN_TEST_DOWNLOAD_FAIL:-0}" = 0 ] || exit 22
exec cat "$MIDDEN_TEST_DIST/${url##*/}"
""")
        for name in ("python", "python3", "node", "go", "jq"):
            self.tool(name, """printf 'forbidden dependency\\n' >> "$MIDDEN_TEST_LOG"; exit 89\n""")
        self.release("1.2.3")

    def tool(self, name, script):
        path = self.tools / name
        path.write_text("#!/bin/sh\n" + script, encoding="utf-8", newline="\n")
        path.chmod(0o755)

    def release(self, version, actual_backend=False, notices=False, backend=None):
        self.version = version
        core = (
            "#!/bin/sh\n"
            'printf "core:%s\\n" "$*" >> "$MIDDEN_TEST_LOG"\n'
            '[ "$#" = 1 ] && [ "$1" = version ] || exit 65\n'
            f'printf "midden %s\\n" "${{MIDDEN_TEST_BAD_VERSION:-{version}}}"\n'
        ).encode()
        ui = (
            "#!/bin/sh\n"
            'printf "ui:%s\\n" "$*" >> "$MIDDEN_TEST_LOG"\n'
            'if [ "$#" = 1 ] && [ "$1" = --version ]; then\n'
            f'  printf "midden-ui %s\\n" "${{MIDDEN_TEST_BAD_UI_VERSION:-{version}}}"\n'
            'else\n  printf "foreground UI fixture\\n"\nfi\n'
        ).encode()
        self.entries = {
            "core": [("midden", core), ("LICENSE", b"Synthetic license\n"),
                     ("CORE.md", b"Synthetic core guide\n")],
            "ui": [("midden", core), ("midden-ui", ui),
                   ("LICENSE", b"Synthetic license\n"), ("NOTICE", b"Synthetic notice\n"),
                   ("README.md", b"Synthetic UI guide\n"), ("start.ps1", b"# Not executed\n"),
                   ("start.sh", b"#!/bin/sh\nexit 99\n")]
            + [("bundles/" + name, ("Synthetic " + name + "\n").encode()) for name in BUNDLE_NAMES],
        }
        if notices:
            for product in ("core", "ui"):
                self.entries[product].append(("THIRD_PARTY_NOTICES.txt", b"Synthetic dependency notice\n"))
        package = {
            "kind": "midden-ui-release", "version": version,
            "platform": self.system + "/" + self.arch,
            "source_commit": "a" * 40, "kernel": "synthetic",
            "files": {name: sha(data) for name, data in self.entries["ui"]},
        }
        self.entries["ui"].append(("package-manifest.json", json.dumps(package).encode()))
        sources = {name: data for name, data in self.entries["ui"] if name.startswith("bundles/")}
        layout = {
            "article": "midden-article", "investigation": "midden-investigation",
            "presentation": "midden-presentation", "long-form": "midden-long-form",
            "midden-shared": "midden-shared",
        }
        bundle_manifest = {"schema": "midden.bundle-files/v1", "files": []}
        for name, data in sources.items():
            relative = name[len("bundles/"):]
            if "/" not in relative:
                continue
            folder, tail = relative.split("/", 1)
            bundle_manifest["files"].append({
                "source": relative, "destination": layout[folder] + "/" + tail, "sha256": sha(data),
            })
        if backend is None:
            backend = (ROOT / "installer" / "install.py").read_bytes() if actual_backend else b"raise SystemExit(92)\n"
        self.entries["bundle"] = [
            ("LICENSE", b"Synthetic license\n"), ("install.sh", b"exit 93\n"),
            ("install.ps1", b"exit 93\n"), ("installer/install.py", backend),
            ("installer/bundle-manifest.json", json.dumps(bundle_manifest).encode()),
            *sources.items(),
        ]
        self.archives = {}
        for product in ("core", "ui"):
            name = f"midden-{product}_{version}_{self.system}_{self.arch}.tar.gz"
            self.archives[product] = name
            write_tar(self.dist / name, self.entries[product])
        self.archives["bundle"] = f"midden-bundle_{version}.zip"
        with zipfile.ZipFile(self.dist / self.archives["bundle"], "w", zipfile.ZIP_DEFLATED) as archive:
            for name, data in self.entries["bundle"]:
                entry = zipfile.ZipInfo(name)
                entry.create_system = 3
                entry.external_attr = 0o100644 << 16
                entry.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(entry, data)
        self.build = {
            "version": version, "commit": "a" * 40,
            "go": "go version go1.26.0 " + self.system + "/" + self.arch,
            "targets": [self.system + "/" + self.arch], "products": ["core", "bundle", "ui"],
            "core_binaries": {self.system + "/" + self.arch: sha(core)},
            "ui_binaries": {self.system + "/" + self.arch: sha(ui)},
            "archives": list(self.archives.values()), "installers": ["install.ps1", "install.sh"],
        }
        (self.dist / "build-manifest.json").write_text(json.dumps(self.build), encoding="utf-8")
        rows = ["format\tmidden-release-v1", "version\t" + version, "commit\t" + "a" * 40]
        for product in ("core", "bundle", "ui"):
            target = "universal" if product == "bundle" else self.system + "/" + self.arch
            rows.append("\t".join(("archive", product, target, self.archives[product])))
            rows.extend("\t".join(("file", product, target, name, sha(data)))
                        for name, data in self.entries[product])
        (self.dist / "manifest.tsv").write_text("\n".join(rows) + "\n", encoding="utf-8", newline="\n")
        (self.dist / "install.sh").write_bytes(b"# Synthetic download metadata only\n")
        (self.dist / "install.ps1").write_bytes(b"# Synthetic download metadata only\n")
        self.seal()

    def seal(self):
        names = [*self.archives.values(), "build-manifest.json", "manifest.tsv", "install.sh", "install.ps1"]
        (self.dist / "SHA256SUMS").write_text(
            "".join(sha((self.dist / name).read_bytes()) + "  " + name + "\n" for name in sorted(names)),
            encoding="utf-8", newline="\n",
        )

    def run_bootstrap(self, *args, local=True, piped=False, shell=None, success=True, env=None):
        self.assertTrue(BOOTSTRAP.is_file(), "The standalone bootstrap/install.sh is not implemented")
        arguments = ["--distribution-dir", unix_path(self.dist)] if local else []
        arguments += list(map(str, args))
        command = [shell or SHELL]
        command += ["-s", "--"] if piped else [unix_path(BOOTSTRAP)]
        result = subprocess.run(
            command + arguments, input=BOOTSTRAP.read_bytes() if piped else None,
            cwd=self.project, env=env or self.env, capture_output=True, timeout=90,
        )
        output = (result.stdout + result.stderr).decode("utf-8", errors="replace")
        if success:
            self.assertEqual(result.returncode, 0, output)
        else:
            self.assertNotEqual(result.returncode, 0, output)
        return output

    def shell(self, command):
        return subprocess.run(
            [SHELL, "-c", command], cwd=self.project, env=self.env,
            text=True, capture_output=True, timeout=15,
        )

    def snapshot(self):
        return {
            str(path.relative_to(self.root)): path.read_bytes()
            for directory in (self.home, self.project)
            for path in directory.rglob("*") if path.is_file()
        }

    def assert_clean_temporary(self):
        self.assertEqual(list(self.tmp.iterdir()), [], "Temporary extraction/rollback material leaked")

    def cached_cli_receipt(self):
        self.install.mkdir(parents=True, exist_ok=True)
        backend = (
            "from pathlib import Path\n"
            f"Path({str(self.log)!r}).write_text('cached backend executed', encoding='utf-8')\n"
        ).encode()
        (self.install / ".midden-bootstrap-cli.py").write_bytes(backend)
        (self.install / "personal.txt").write_text("unowned sentinel", encoding="utf-8")
        fields = [
            ("format", "midden-bootstrap-v1"), ("mode", "cli"), ("version", "1.2.3"),
            ("target", self.system + "/" + self.arch),
            ("root", unix_path(self.install)), ("home", unix_path(self.home)),
            ("project", unix_path(self.project)), ("host", "copilot"),
            ("file", ".midden-bootstrap-cli.py", sha(backend)),
        ]
        (self.install / RECEIPT).write_bytes(
            ("\n".join("\t".join(row) for row in fields) + "\n").encode()
        )
        self.log.unlink(missing_ok=True)

    def assert_protected_state_root(self, state, env, aliases=()):
        state.mkdir(parents=True, exist_ok=True)
        (state / "sentinel.txt").write_text("retained user state", encoding="utf-8")
        before = self.snapshot()
        directories = {str(path) for base in (self.home, self.project)
                       for path in base.rglob("*") if path.is_dir()}
        destinations = (state, state / "binary install", state.parent, *aliases)
        for mode in ("core", "ui"):
            for destination in destinations:
                output = self.run_bootstrap(
                    "--mode", mode, "--install-dir", unix_path(destination),
                    "--no-path", "--no-launch", env=env, success=False,
                )
                diagnostic = "dedicated directory" if destination == self.home else "overlap"
                self.assertIn(diagnostic, output.lower(), str(destination))
                self.assertEqual(self.snapshot(), before, str(destination))
                self.assertEqual(
                    {str(path) for base in (self.home, self.project)
                     for path in base.rglob("*") if path.is_dir()}, directories,
                )
                self.assertFalse(self.log.exists(), "Reject state overlap before executing version probes")
                self.assert_clean_temporary()
            self.run_bootstrap("--mode", mode, "--install-dir", unix_path(state / "planned install"),
                               "--dry-run", "--no-path", env=env, success=False)
            self.assertEqual(self.snapshot(), before)
            self.assertFalse(self.log.exists())
            self.assert_clean_temporary()

    def test_plain_stdin_default_install_launches_foreground_ui(self):
        output = self.run_bootstrap(local=False, piped=True)
        self.assertIn("foreground UI fixture", output)
        self.assertIn("integrity", output.lower())
        self.assertIn("authenticity", output.lower())
        self.assertTrue((self.install / RECEIPT).is_file())
        self.assertEqual(self.log.read_text().splitlines(), ["core:version", "ui:--version", "ui:"])
        for name in ("midden", "midden-ui"):
            link = self.home / ".local" / "bin" / name
            result = self.shell("readlink " + shlex.quote(unix_path(link)))
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), unix_path(self.install / name))
        self.assertIn("/xibodev/midden/releases/latest/download/SHA256SUMS", self.curl_log.read_text())
        self.assert_clean_temporary()

    def test_piped_posix_dash_does_not_lose_the_script(self):
        dash = Path(SHELL).with_name("dash.exe" if WINDOWS else "dash")
        if not dash.is_file():
            candidate = shutil.which("dash")
            if not candidate:
                self.skipTest("dash is unavailable; native sh tests still run")
            dash = Path(candidate)
        self.run_bootstrap("--no-path", "--no-launch", piped=True, shell=str(dash))
        self.assertTrue((self.install / "midden-ui").is_file())
        self.assertEqual(self.log.read_text().splitlines(), ["core:version", "ui:--version"])

    @unittest.skipUnless(WINDOWS, "Git Bash usertemp mounts are Windows-specific")
    def test_windows_shells_keep_stable_runtime_temp_with_private_staging(self):
        result = self.shell(
            'cygpath -w "$TEMP"; cygpath -w "$TMP"; cygpath -w "$TMPDIR"; '
            "/usr/bin/bash -c 'test -d /tmp || exit 1; cygpath -w /tmp; "
            'printf "%s\\n" "$BASH_VERSION"\''
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stderr, "", "Shell initialization must not add warnings to version probes")
        lines = result.stdout.splitlines()
        self.assertEqual(len(lines), 5, result.stdout)
        for index, name in enumerate(("TEMP", "TMP")):
            expected = Path(os.environ.get(name) or tempfile.gettempdir()).resolve()
            self.assertEqual(Path(lines[index]).resolve(), expected,
                             "A shared MSYS usertemp mount must not use a per-test directory")
        self.assertEqual(Path(lines[2]).resolve(), self.tmp, "Bootstrap staging remains private")
        mapped = Path(lines[3]).resolve()
        self.assertNotIn(self.root, (mapped, *mapped.parents))
        self.assertRegex(lines[4], r"^\d+\.\d+", "Exercise the installed Bash runtime, not a fake shell")
        working = self.shell("pwd -P")
        self.assertEqual(working.returncode, 0, working.stderr)
        self.assertEqual(working.stderr, "")
        self.assertEqual(unix_path(self.project), working.stdout.strip(),
                         "Absolute paths must use the same mount aliases as relative state bindings")

    def test_no_path_no_launch_lifecycle_preserves_unowned_state(self):
        self.run_bootstrap("--no-path", "--no-launch")
        self.assertFalse((self.home / ".local" / "bin").exists())
        retained = self.install / "personal notes.txt"
        retained.write_text("unowned", encoding="utf-8")
        state = self.home / "data" / "midden" / "sessions"
        state.mkdir(parents=True)
        (state / "keep").write_text("retained", encoding="utf-8")
        self.run_bootstrap("--verify", local=False)
        self.release("1.2.4")
        self.run_bootstrap("--upgrade", "--no-path", "--no-launch")
        self.assertIn("1.2.4", (self.install / RECEIPT).read_text())
        shutil.rmtree(self.dist)
        self.run_bootstrap("--uninstall", local=False)
        self.assertEqual(retained.read_text(), "unowned")
        self.assertEqual((state / "keep").read_text(), "retained")
        self.assertFalse((self.install / "midden").exists())
        self.assertFalse((self.install / RECEIPT).exists())
        self.assertFalse(self.curl_log.exists(), "Local inputs and receipt operations must not download")
        self.assert_clean_temporary()

    def test_no_open_is_forwarded_only_to_foreground_launch(self):
        self.run_bootstrap("--no-path", "--no-open")
        self.assertEqual(self.log.read_text().splitlines(), ["core:version", "ui:--version", "ui:--no-open"])

    def test_core_mode_has_no_ui_or_runtime_dependency(self):
        self.run_bootstrap("--mode", "core", "--no-path")
        self.assertEqual((self.install / "CORE.md").read_bytes(), b"Synthetic core guide\n")
        self.assertFalse((self.install / "midden-ui").exists())
        self.assertEqual(self.log.read_text().splitlines(), ["core:version"])
        self.run_bootstrap("--mode", "core", "--verify", local=False)

    def test_packaged_dependency_notices_are_hash_verified_and_receipt_owned(self):
        self.release("1.2.3", notices=True)
        for mode in ("ui", "core"):
            with self.subTest(mode=mode):
                self.run_bootstrap("--mode", mode, "--no-path", "--no-launch")
                self.assertEqual((self.install / "THIRD_PARTY_NOTICES.txt").read_bytes(),
                                 b"Synthetic dependency notice\n")
                self.run_bootstrap("--verify", local=False)
                self.run_bootstrap("--uninstall", local=False)
                self.assertFalse((self.install / "THIRD_PARTY_NOTICES.txt").exists())

    def test_available_bsd_tar_handles_pax_metadata_without_extracting_links(self):
        if WINDOWS:
            native_tar = Path(os.environ["SystemRoot"]) / "System32" / "tar.exe"
        else:
            value = shutil.which("tar" if sys.platform == "darwin" else "bsdtar")
            if not value:
                self.skipTest("BSD tar is not installed; the native tar contracts still run")
            native_tar = Path(value)
        if not native_tar.is_file():
            self.skipTest("BSD tar is unavailable")
        executable = shlex.quote(unix_path(native_tar))
        if WINDOWS:
            # Windows BSD tar writes textual listings with CRLF; do not alter
            # streamed member bytes, which are the production integrity boundary.
            script = (
                'case "$1" in *O*) exec ' + executable + ' "$@";; esac\n'
                'listing=$(' + executable + ' "$@") || exit $?\n'
                'printf "%s\\n" "$listing" | tr -d "\\r"\n'
            )
        else:
            script = "exec " + executable + ' "$@"\n'
        self.tool("tar", script)
        with tarfile.open(self.dist / self.archives["core"], "w:gz", format=tarfile.PAX_FORMAT) as archive:
            for name, data in self.entries["core"]:
                info = tarfile.TarInfo(name)
                info.mtime = 1.25
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
        self.seal()
        self.run_bootstrap("--mode", "core", "--no-path")
        self.assertEqual((self.install / "CORE.md").read_bytes(), b"Synthetic core guide\n")
        self.run_bootstrap("--uninstall", local=False)

    def test_dry_run_validates_without_writing_home_or_running_payloads(self):
        before = self.snapshot()
        output = self.run_bootstrap("--dry-run")
        self.assertIn("dry", output.lower())
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()
        self.run_bootstrap("--no-path", "--no-launch")
        before = self.snapshot()
        self.run_bootstrap("--uninstall", "--dry-run", local=False)
        self.assertEqual(self.snapshot(), before)

    def test_system_awk_dry_run_normalizes_paths_and_rejects_root(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        before = self.snapshot()
        output = self.run_bootstrap(
            "--mode", "core", "--install-dir", "./unused parent/../planned install",
            "--project", ".", "--dry-run", "--no-path", "--no-launch",
        )
        self.assertEqual(
            output.splitlines()[0],
            f"Dry-run: install Midden 1.2.3 (core, {self.system}/{self.arch}) "
            f"in {unix_path(self.project / 'planned install')}",
        )
        for root in ("/", "/./"):
            with self.subTest(root=root):
                rejected = self.run_bootstrap(
                    "--mode", "core", "--install-dir", root, "--dry-run", success=False,
                )
                self.assertIn("dedicated directory", rejected)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse((self.project / "planned install").exists())
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_system_awk_full_manifest_upgrade_and_uninstall(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        state = self.home / "data" / "midden" / "user-state.txt"
        state.parent.mkdir(parents=True)
        state.write_text("retained state", encoding="utf-8")
        for mode in ("ui", "core"):
            with self.subTest(mode=mode):
                self.release("1.2.3", notices=True)
                self.log.unlink(missing_ok=True)
                before = self.snapshot()
                self.run_bootstrap("--mode", mode, "--version", "v1.2.3",
                                   "--repository", "example/fork", "--dry-run", "--no-launch")
                self.assertEqual(self.snapshot(), before)
                self.assertFalse(self.log.exists())
                self.run_bootstrap("--mode", mode, "--no-launch")
                note = self.install / "personal.txt"
                note.write_text("unowned neighbor", encoding="utf-8")
                self.run_bootstrap("--mode", mode, "--verify", local=False)
                self.release("1.2.4", notices=True)
                self.run_bootstrap("--mode", mode, "--upgrade", "--no-launch")
                self.assertIn("\nversion\t1.2.4\n", (self.install / RECEIPT).read_text())
                for name, data in self.entries[mode]:
                    self.assertEqual((self.install / name).read_bytes(), data, name)
                self.run_bootstrap("--mode", mode, "--verify", local=False)
                self.run_bootstrap("--mode", mode, "--uninstall", local=False)
                self.assertFalse((self.install / RECEIPT).exists())
                self.assertTrue(all(not (self.install / name).exists() for name, _ in self.entries[mode]))
                self.assertEqual(note.read_text(), "unowned neighbor")
                self.assertEqual(state.read_text(), "retained state")
                for name in ("midden", "midden-ui"):
                    target = shlex.quote(unix_path(self.home / ".local" / "bin" / name))
                    self.assertEqual(self.shell(f"test ! -e {target} && test ! -L {target}").returncode, 0)
                self.assert_clean_temporary()

    def test_system_awk_rejects_nested_manifest_collisions_before_extraction(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        manifest = self.dist / "manifest.tsv"
        original = manifest.read_text()
        for name in ("bundles/article", "bundles/Article/new.md", "bundles/article/../escape"):
            with self.subTest(name=name):
                row = f"file\tui\t{self.system}/{self.arch}\t{name}\t" + "0" * 64 + "\n"
                manifest.write_text(original + row, encoding="utf-8")
                self.seal()
                before = self.snapshot()
                output = self.run_bootstrap("--no-path", "--no-launch", success=False)
                self.assertIn("Invalid release manifest.tsv", output)
                self.assertEqual(self.snapshot(), before)
                self.assertFalse(self.log.exists())
                self.assert_clean_temporary()

    def test_system_awk_rejects_receipt_file_directory_conflicts(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        self.run_bootstrap("--no-path", "--no-launch")
        receipt = self.install / RECEIPT
        receipt.write_bytes(receipt.read_bytes() + b"file\tbundles/article\t" + b"0" * 64 + b"\n")
        before = self.snapshot()
        probes = self.log.read_bytes()
        for operation in ("--verify", "--uninstall"):
            with self.subTest(operation=operation):
                output = self.run_bootstrap(operation, local=False, success=False)
                self.assertIn("Invalid bootstrap installation receipt", output)
                self.assertEqual(self.snapshot(), before)
                self.assertEqual(self.log.read_bytes(), probes)
                self.assert_clean_temporary()

    def test_corrupt_metadata_and_archive_are_rejected_before_execution(self):
        for name in ("manifest.tsv", "build-manifest.json", self.archives["ui"]):
            with self.subTest(name=name):
                path = self.dist / name
                original = path.read_bytes()
                path.write_bytes(original + b"corrupt")
                output = self.run_bootstrap("--no-path", "--no-launch", success=False)
                self.assertIn("checksum", output.lower())
                self.assertFalse(self.log.exists())
                self.assertFalse(self.install.exists())
                path.write_bytes(original)
                self.assert_clean_temporary()

    def test_per_file_hash_mismatch_is_rejected_even_with_sealed_archive(self):
        entries = [(name, b"changed" if name == "midden" else data) for name, data in self.entries["ui"]]
        write_tar(self.dist / self.archives["ui"], entries)
        self.seal()
        output = self.run_bootstrap("--no-path", "--no-launch", success=False)
        self.assertIn("checksum", output.lower())
        self.assertFalse(self.log.exists())

    def test_metadata_size_is_bounded_at_one_mebibyte(self):
        (self.dist / "manifest.tsv").write_bytes(b"x" * (1024 * 1024 + 1))
        self.seal()
        output = self.run_bootstrap("--no-path", success=False)
        self.assertIn("limit", output.lower())
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_archive_expansion_is_bounded_before_execution(self):
        with gzip.open(self.dist / self.archives["core"], "wb") as stream:
            for _ in range(257):
                stream.write(b"\0" * (1024 * 1024))
        self.seal()
        output = self.run_bootstrap("--mode", "core", "--no-path", success=False)
        self.assertIn("limit", output.lower())
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_noncanonical_extra_duplicate_and_special_tar_members_are_refused(self):
        archive_path = self.dist / self.archives["core"]
        cases = (
            ("../escape", tarfile.REGTYPE), ("/absolute", tarfile.REGTYPE),
            ("./midden", tarfile.REGTYPE), ("a//b", tarfile.REGTYPE),
            ("extra", tarfile.REGTYPE), ("midden", tarfile.REGTYPE),
            ("MIDDEN", tarfile.REGTYPE), ("linked", tarfile.SYMTYPE),
            ("hard", tarfile.LNKTYPE), ("device", tarfile.CHRTYPE),
            ("pipe", tarfile.FIFOTYPE), ("directory/", tarfile.DIRTYPE),
            ("new\nline", tarfile.REGTYPE),
        )
        for name, kind in cases:
            with self.subTest(name=name, kind=kind):
                with tarfile.open(archive_path, "w:gz", format=tarfile.PAX_FORMAT) as archive:
                    for existing, data in self.entries["core"]:
                        info = tarfile.TarInfo(existing)
                        info.size = len(data)
                        archive.addfile(info, io.BytesIO(data))
                    info = tarfile.TarInfo(name)
                    info.type = kind
                    info.linkname = "midden" if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE) else ""
                    archive.addfile(info, io.BytesIO(b""))
                self.seal()
                self.run_bootstrap("--mode", "core", "--no-path", success=False)
                self.assertFalse(self.log.exists())
                self.assertFalse((self.root / "escape").exists())
                self.assert_clean_temporary()

    def test_malformed_duplicate_and_unsafe_metadata_records_are_refused(self):
        manifest = self.dist / "manifest.tsv"
        original = manifest.read_text()
        malformed = (
            original + "format\tmidden-release-v1\n",
            original + f"file\tui\t{self.system}/{self.arch}\t../outside\t" + "0" * 64 + "\n",
            original.replace("version\t1.2.3", "version\t../1.2.3"),
            original.replace("\tLICENSE\t", "\t./LICENSE\t"),
            original + "unknown\tvalue\n",
        )
        for data in malformed:
            with self.subTest(tail=data[-90:]):
                manifest.write_text(data, encoding="utf-8")
                self.seal()
                self.run_bootstrap("--no-path", success=False)
                self.assertFalse(self.log.exists())
        manifest.write_text(original, encoding="utf-8")
        self.seal()
        sums = self.dist / "SHA256SUMS"
        sums.write_text(sums.read_text() + sums.read_text().splitlines()[0] + "\n", encoding="utf-8")
        self.run_bootstrap("--no-path", success=False)
        self.assertFalse(self.log.exists())

    def test_native_target_has_no_foreign_fallback(self):
        for system, arch in (("Linux", "armv7l"), ("FreeBSD", "x86_64"),
                             ("Darwin" if self.system == "linux" else "Linux", "x86_64")):
            with self.subTest(system=system, arch=arch):
                env = dict(self.env, MIDDEN_TEST_SYSTEM=system, MIDDEN_TEST_MACHINE=arch)
                output = self.run_bootstrap("--no-path", env=env, success=False)
                self.assertTrue("native" in output.lower() or "unsupported" in output.lower(), output)
                self.assertFalse(self.log.exists())

    def test_repository_and_version_are_explicit_safe_release_selectors(self):
        self.run_bootstrap("--repository", "example/fork", "--version", "v1.2.3",
                           "--no-path", "--no-launch", local=False)
        calls = self.curl_log.read_text()
        self.assertIn("https://github.com/example/fork/releases/download/v1.2.3/", calls)
        self.assertIn("-q\n", calls, "curl config must be disabled")
        self.assertNotIn("Authorization", calls)
        for args in (("--repository", "../fork"), ("--repository", "https://example/repo"),
                     ("--repository", "owner/repo/extra"), ("--version", "../escape"),
                     ("--mode", "unknown"), ("--host", "unknown"), ("--version",),
                     ("--upgrade", "--uninstall"), ("--install-dir", "--no-launch")):
            with self.subTest(args=args):
                before = self.curl_log.read_bytes()
                self.run_bootstrap(*args, local=False, success=False)
                self.assertEqual(self.curl_log.read_bytes(), before)

    def test_release_download_failure_has_no_prerelease_or_auth_fallback(self):
        output = self.run_bootstrap(local=False, success=False,
                                    env=dict(self.env, MIDDEN_TEST_DOWNLOAD_FAIL="1",
                                             GH_TOKEN="synthetic-never-use", GITHUB_TOKEN="synthetic-never-use"))
        self.assertIn("download", output.lower())
        calls = self.curl_log.read_text()
        self.assertNotIn("api.github.com", calls)
        self.assertNotIn("synthetic-never-use", calls)
        self.assertFalse(self.install.exists())

    def test_stream_failure_after_valid_bytes_is_not_hidden_by_a_previous_success(self):
        self.tool("curl", """for value do url=$value; done
cat "$MIDDEN_TEST_DIST/${url##*/}"
case "$url" in */manifest.tsv) exit 22;; esac
""")
        self.run_bootstrap("--mode", "core", "--no-path", local=False, success=False)
        self.assertFalse(self.log.exists(), "A failed download must not reach executable probes")
        self.assertFalse(self.install.exists())
        self.assert_clean_temporary()

    def test_tar_failure_after_valid_member_bytes_is_still_fatal(self):
        self.tool("tar", """/usr/bin/tar "$@"
status=$?
case "$1" in *O*) case "$*" in *midden) exit 74;; esac;; esac
exit "$status"
""")
        self.run_bootstrap("--mode", "core", "--no-path", success=False)
        self.assertFalse(self.log.exists())
        self.assertFalse(self.install.exists())
        self.assert_clean_temporary()

    def test_dry_run_rejects_file_ancestors_of_payloads_and_launch_links(self):
        self.install.mkdir(parents=True)
        obstacle = self.install / "bundles"
        obstacle.write_text("unowned file, not a directory", encoding="utf-8")
        before = self.snapshot()
        self.run_bootstrap("--dry-run", "--no-path", success=False)
        self.assertEqual(self.snapshot(), before)
        obstacle.unlink()
        obstacle = self.home / ".local" / "bin"
        obstacle.write_text("unowned PATH entry, not a directory", encoding="utf-8")
        before = self.snapshot()
        self.run_bootstrap("--mode", "core", "--dry-run", success=False)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())

    def test_relative_user_paths_are_normalized_without_relaxing_archive_paths(self):
        self.run_bootstrap("--mode", "core", "--install-dir", "./custom install",
                           "--project", ".", "--no-path")
        self.assertTrue((self.project / "custom install" / "midden").is_file())
        self.run_bootstrap("--install-dir", "custom install", "--project", ".", "--uninstall", local=False)
        self.assertFalse((self.project / "custom install" / "midden").exists())

    def test_install_roots_preserve_linux_xdg_ui_state(self):
        self.system = "linux"
        self.release("1.2.3")
        data = self.home / "xdg storage"
        env = dict(self.env, MIDDEN_TEST_SYSTEM="Linux", XDG_DATA_HOME=unix_path(data),
                   MIDDEN_HOME=unix_path(self.home / "separate core state"))
        self.assert_protected_state_root(data / "midden", env)

    def test_install_roots_preserve_linux_default_ui_state(self):
        self.system = "linux"
        self.release("1.2.3")
        env = dict(self.env, MIDDEN_TEST_SYSTEM="Linux",
                   MIDDEN_HOME=unix_path(self.home / "separate core state"))
        env.pop("XDG_DATA_HOME", None)
        self.assert_protected_state_root(self.home / ".local" / "share" / "midden", env)

    def test_install_roots_preserve_macos_default_ui_state(self):
        self.system = "darwin"
        self.release("1.2.3")
        env = dict(self.env, MIDDEN_TEST_SYSTEM="Darwin",
                   MIDDEN_HOME=unix_path(self.home / "separate core state"))
        state = self.home / "Library" / "Application Support" / "Midden"
        self.assert_protected_state_root(state, env, aliases=(state.with_name("midden"),))

    def test_install_roots_preserve_default_core_state(self):
        env = dict(self.env)
        env.pop("MIDDEN_HOME", None)
        self.assert_protected_state_root(self.home / ".midden", env)

    def test_install_roots_preserve_explicit_core_state(self):
        state = self.project / "storage" / "core state"
        env = dict(self.env, MIDDEN_HOME="storage/core state")
        self.assert_protected_state_root(state, env)

    def test_install_roots_preserve_explicit_compa_state(self):
        state = self.home / "shared storage" / "compa"
        env = dict(self.env, COMPA_HOME=unix_path(state))
        self.assert_protected_state_root(state, env)

    def test_install_roots_reject_physical_state_alias_overlap(self):
        state = self.home / "physical state"
        state.mkdir()
        (state / "sentinel.txt").write_text("retained user state", encoding="utf-8")
        alias = self.home / "configured state"
        result = self.shell("ln -s " + shlex.quote(unix_path(state)) + " " + shlex.quote(unix_path(alias)))
        self.assertEqual(result.returncode, 0, result.stderr)
        before = self.snapshot()
        self.run_bootstrap("--mode", "core", "--install-dir", unix_path(state / "tools"),
                           "--no-path", "--no-launch", success=False,
                           env=dict(self.env, MIDDEN_HOME=unix_path(alias)))
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_owned_modified_files_block_verify_upgrade_and_uninstall(self):
        self.run_bootstrap("--no-path", "--no-launch")
        (self.install / "README.md").write_text("user change", encoding="utf-8")
        before = self.snapshot()
        for operation in ("--verify", "--upgrade", "--uninstall"):
            with self.subTest(operation=operation):
                output = self.run_bootstrap(operation, "--no-path", "--no-launch", success=False)
                self.assertTrue("modified" in output.lower() or "checksum" in output.lower(), output)
                self.assertEqual(self.snapshot(), before)
        self.assert_clean_temporary()

    def test_unowned_payload_and_path_entries_are_never_replaced(self):
        self.install.mkdir(parents=True)
        (self.install / "midden").write_text("unowned binary", encoding="utf-8")
        before = self.snapshot()
        self.run_bootstrap("--no-path", "--no-launch", success=False)
        self.assertEqual(self.snapshot(), before)
        (self.install / "midden").unlink()
        bindir = self.home / ".local" / "bin"
        bindir.mkdir()
        (bindir / "midden-ui").write_text("unowned command", encoding="utf-8")
        before = self.snapshot()
        self.run_bootstrap("--no-launch", success=False)
        self.assertEqual(self.snapshot(), before)
        self.run_bootstrap("--no-path", "--no-launch")
        self.assertEqual((bindir / "midden-ui").read_text(), "unowned command")

    def test_owned_links_are_verified_and_removed_without_touching_neighbors(self):
        self.run_bootstrap("--no-launch")
        neighbor = self.home / ".local" / "bin" / "other-command"
        neighbor.write_text("unowned", encoding="utf-8")
        self.run_bootstrap("--verify", local=False)
        self.run_bootstrap("--uninstall", "--no-path", local=False)
        self.assertEqual(neighbor.read_text(), "unowned")
        for name in ("midden", "midden-ui"):
            result = self.shell("test ! -e " + shlex.quote(unix_path(neighbor.parent / name))
                                + " && test ! -L " + shlex.quote(unix_path(neighbor.parent / name)))
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_symlinked_install_ancestors_and_internal_directories_are_refused(self):
        outside = self.root / "outside"
        outside.mkdir()
        linked = self.home / "linked"
        result = self.shell("ln -s " + shlex.quote(unix_path(outside)) + " " + shlex.quote(unix_path(linked)))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.run_bootstrap("--install-dir", unix_path(linked / "midden"), "--no-path", success=False)
        self.assertEqual(list(outside.iterdir()), [])
        self.run_bootstrap("--no-path", "--no-launch")
        shutil.rmtree(self.install / "bundles")
        result = self.shell("ln -s " + shlex.quote(unix_path(outside)) + " "
                            + shlex.quote(unix_path(self.install / "bundles")))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.run_bootstrap("--uninstall", local=False, success=False)
        self.assertEqual(list(outside.iterdir()), [])

    def test_external_temporary_requirement_and_existing_lock_are_enforced(self):
        output = self.run_bootstrap("--no-path", env=dict(self.env, TMPDIR=unix_path(self.project)), success=False)
        self.assertIn("external", output.lower())
        self.install.parent.mkdir(parents=True)
        lock = self.install.with_name("midden.midden-bootstrap.lock")
        lock.mkdir()
        output = self.run_bootstrap("--no-path", success=False)
        self.assertIn("lock", output.lower())
        self.assertTrue(lock.is_dir())
        self.assertFalse(self.log.exists())

    def test_upgrade_failure_rolls_back_all_published_files_and_receipt(self):
        self.run_bootstrap("--no-path", "--no-launch")
        before = self.snapshot()
        self.release("1.2.4")
        self.tool("mv", """for argument do destination=$argument; done
if [ "$destination" = "$MIDDEN_TEST_FAIL_DEST" ] && [ ! -e "$MIDDEN_TEST_FAIL_MARK" ]; then
  : > "$MIDDEN_TEST_FAIL_MARK"
  printf 'injected publication failure\\n' >&2
  exit 71
fi
exec /usr/bin/mv "$@"
""")
        output = self.run_bootstrap("--upgrade", "--no-path", "--no-launch", success=False, env=dict(
            self.env, MIDDEN_TEST_FAIL_DEST=unix_path(self.install / "midden-ui"),
            MIDDEN_TEST_FAIL_MARK=unix_path(self.root / "failed-once"),
        ))
        self.assertIn("failure", output.lower())
        self.assertEqual(self.snapshot(), before)
        self.assert_clean_temporary()
        self.run_bootstrap("--verify", local=False)

    def test_version_probes_must_match_verified_manifest(self):
        for key in ("MIDDEN_TEST_BAD_VERSION", "MIDDEN_TEST_BAD_UI_VERSION"):
            with self.subTest(key=key):
                self.run_bootstrap("--no-path", "--no-launch", success=False,
                                   env=dict(self.env, **{key: "9.9.9"}))
                self.assertFalse((self.install / RECEIPT).exists())
                self.assert_clean_temporary()

    def test_cli_requires_explicit_existing_python39(self):
        output = self.run_bootstrap("--mode", "cli", "--no-path", "--no-launch", success=False,
                                    env=dict(self.env, PYTHON="/bin/false"))
        self.assertIn("Python 3.9+", output)
        self.assertFalse(self.install.exists())

    def test_cli_rejects_nonordinary_zip_before_running_its_backend(self):
        env = dict(self.env, PYTHON=unix_path(Path(sys.executable)))
        path = self.dist / self.archives["bundle"]
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
            for name, data in self.entries["bundle"]:
                entry = zipfile.ZipInfo(name)
                entry.create_system = 3
                entry.external_attr = (0o120777 if name == "installer/install.py" else 0o100644) << 16
                archive.writestr(entry, data)
        self.seal()
        output = self.run_bootstrap("--mode", "cli", "--no-path", env=env, success=False)
        self.assertIn("ordinary", output.lower())
        self.assertFalse(self.install.exists())
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_cli_backend_failure_rolls_back_bootstrap_ownership(self):
        # The verified synthetic Python backend exits 92; root installers exit 93.
        env = dict(self.env, PYTHON=unix_path(Path(sys.executable)), MIDDEN_HOME=unix_path(self.install))
        before = self.snapshot()
        output = self.run_bootstrap("--mode", "cli", "--no-path", env=env, success=False)
        self.assertIn("backend failed", output.lower())
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_cli_lifecycle_rejects_missing_empty_wrong_version_and_corrupt_distribution(self):
        empty = self.root / "empty release"
        empty.mkdir()
        env = dict(self.env, PYTHON=unix_path(Path(sys.executable)))
        backend = (
            "from pathlib import Path\n"
            f"Path({str(self.log)!r}).write_text('distribution backend executed', encoding='utf-8')\n"
        ).encode()
        for scenario in ("missing", "empty", "wrong-version", "corrupt"):
            self.release("1.2.4" if scenario == "wrong-version" else "1.2.3", notices=True, backend=backend)
            if scenario == "corrupt":
                archive = self.dist / self.archives["bundle"]
                archive.write_bytes(archive.read_bytes() + b"corruption")
            arguments = [] if scenario == "missing" else [
                "--distribution-dir", unix_path(empty if scenario == "empty" else self.dist)
            ]
            for operation in ("--verify", "--uninstall"):
                for dry_run in (False, True):
                    with self.subTest(scenario=scenario, operation=operation, dry_run=dry_run):
                        self.cached_cli_receipt()
                        before = self.snapshot()
                        args = ["--mode", "cli", operation, *arguments]
                        if dry_run:
                            args.append("--dry-run")
                        self.run_bootstrap(*args, local=False, env=env, success=False)
                        self.assertFalse(self.log.exists(), "Neither a cached backend nor a version probe may run")
                        self.assertEqual(self.snapshot(), before)
                        self.assertFalse(self.curl_log.exists(), "Lifecycle must not implicitly fetch another release")
                        self.assert_clean_temporary()

    def test_cli_lifecycle_uses_reviewed_distribution_backend_and_original_metadata(self):
        backend = (
            "import hashlib, json, sys\nfrom pathlib import Path\n"
            "directory = Path(sys.argv[sys.argv.index('--distribution-dir') + 1])\n"
            "record = {'origin': str(Path(__file__)), 'arguments': sys.argv[1:], "
            "'files': sorted(p.name for p in directory.iterdir()), "
            "'metadata': {name: hashlib.sha256((directory / name).read_bytes()).hexdigest() "
            "for name in ('build-manifest.json', 'manifest.tsv', 'SHA256SUMS')}}\n"
            f"Path({str(self.log)!r}).write_text(json.dumps(record), encoding='utf-8')\n"
        ).encode()
        self.release("1.2.3", notices=True, backend=backend)
        self.cached_cli_receipt()
        env = dict(self.env, PYTHON=unix_path(Path(sys.executable)),
                   MIDDEN_HOME=unix_path(self.install), COMPA_HOME=unix_path(self.install),
                   XDG_DATA_HOME=unix_path(self.install.parent))
        metadata = {name: sha((self.dist / name).read_bytes())
                    for name in ("build-manifest.json", "manifest.tsv", "SHA256SUMS")}
        before = self.snapshot()
        for operation in ("--verify", "--uninstall"):
            with self.subTest(operation=operation):
                self.log.unlink(missing_ok=True)
                args = [operation] if operation == "--verify" else ["--mode", "cli", operation]
                self.run_bootstrap(*args, env=env)
                text = self.log.read_text()
                self.assertIn('"origin"', text, "Execute the reviewed bundle backend, never the installed cache")
                record = json.loads(text)
                self.assertEqual(Path(record["origin"]).name, "install.py")
                self.assertIn(operation, record["arguments"])
                self.assertEqual(record["metadata"], metadata)
                self.assertEqual(set(record["files"]), {
                    "build-manifest.json", "manifest.tsv", "SHA256SUMS",
                    self.archives["core"], self.archives["bundle"],
                })
                if operation == "--verify":
                    self.assertEqual(self.snapshot(), before)
                else:
                    self.assertFalse((self.install / RECEIPT).exists())
                    self.assertFalse((self.install / ".midden-bootstrap-cli.py").exists())
                self.assertEqual((self.install / "personal.txt").read_text(), "unowned sentinel")
                self.assert_clean_temporary()

    @unittest.skipIf(WINDOWS, "The real Python backend selects Windows artifacts, not Git Bash's synthetic Linux target")
    def test_cli_uses_verified_existing_backend_and_preserves_project_state(self):
        self.release("1.2.3", actual_backend=True, notices=True)
        env = dict(self.env, PYTHON=sys.executable)
        self.run_bootstrap("--mode", "cli", "--no-path", "--no-launch", "--host", "claude", env=env)
        skill = self.project / ".claude" / "skills" / "midden-article" / "SKILL.md"
        self.assertIn("Local core binding", skill.read_text())
        (skill.parent / "personal.md").write_text("retained", encoding="utf-8")
        saved = self.root / "saved release"
        self.dist.rename(saved)
        before = self.snapshot()
        for operation in ("--verify", "--uninstall"):
            self.run_bootstrap("--mode", "cli", operation, "--host", "claude",
                               local=False, env=env, success=False)
            self.assertEqual(self.snapshot(), before)
        saved.rename(self.dist)
        self.run_bootstrap("--mode", "cli", "--verify", "--host", "claude", env=env)
        self.run_bootstrap("--mode", "cli", "--uninstall", "--host", "claude", env=env)
        self.assertFalse(skill.exists())
        self.assertEqual((skill.parent / "personal.md").read_text(), "retained")
        self.assertFalse((self.install / "midden").exists())
        self.assert_clean_temporary()


if __name__ == "__main__":
    unittest.main()
