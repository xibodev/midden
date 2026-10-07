"""The Unix installer's outcomes; Git Bash exercises shell logic, not native binaries.

All installations, PATH links, downloads and executable probes use synthetic
temporary homes. Python, Node, Go and jq are replaced by tools that fail.
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
INSTALLER = ROOT / "install.sh"
WINDOWS = os.name == "nt"
SHELL = os.environ.get("MIDDEN_UNIX_TEST_SHELL") or (
    r"C:\Program Files\Git\usr\bin\sh.exe" if WINDOWS else shutil.which("sh")
)
RECEIPT = "install-receipt.tsv"
LEGACY_RECEIPT = ".midden-bootstrap-receipt.tsv"
SKILL_NAMES = (
    "midden-article/SKILL.md", "midden-investigation/SKILL.md",
    "midden-long-form/SKILL.md", "midden-long-form/templates/epub.yaml",
    "midden-long-form/templates/html.yaml", "midden-shared/dependencies.md",
    "midden-shared/sources.md", "midden-shared/inspect_html.py",
    "midden-shared/html-inspection.md", "midden-shared/LICENSE", "midden-presentation/SKILL.md",
    "midden-presentation/templates/slides.css", "midden-presentation/templates/html.yaml",
    "midden-presentation/templates/explicit-slides.lua",
)
PANDOC_RELEASE = "https://github.com/jgm/pandoc/releases/download/3.12/"


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
            raise RuntimeError("Git Bash cygpath is required for Windows installer tests")
        # The runtime may prefer a mount alias such as /tmp over a drive path.
        return subprocess.check_output(
            [str(converter), "-u", value], text=True, encoding="utf-8", timeout=15,
        ).rstrip("\r\n")
    return value


def write_tar(path, entries, links=()):
    with tarfile.open(path, "w:gz", format=tarfile.PAX_FORMAT) as archive:
        for name, data in entries:
            info = tarfile.TarInfo(name)
            info.mode = 0o755 if name.rsplit("/", 1)[-1] in ("midden", "midden-ui", "compa-kernel", "pandoc") else 0o644
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        for name, target in links:
            info = tarfile.TarInfo(name)
            info.type = tarfile.SYMTYPE
            info.linkname = target
            archive.addfile(info)


def write_zip(path, entries):
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in entries:
            entry = zipfile.ZipInfo(name)
            entry.create_system = 3
            entry.external_attr = (0o100755 if name.endswith("/pandoc") else 0o100644) << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(entry, data)


@unittest.skipUnless(SHELL and Path(SHELL).is_file(), "A native sh or inspected Git Bash is required")
class UnixInstallerTests(unittest.TestCase):
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
        for key in ("BASH_ENV", "ENV", "CDPATH", "PYTHON", "TAR_OPTIONS", "GZIP", "UNZIP", "ZIPINFO", "COMPA_HOME"):
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

    @property
    def target(self):
        return self.system + "/" + self.arch

    @property
    def skills(self):
        return {name[len("skills/"):]: data for name, data in self.entries["bundle"]}

    def pandoc_layout(self):
        if self.system == "darwin":
            label = "x86_64" if self.arch == "amd64" else "arm64"
            return f"pandoc-3.12-{label}-macOS.zip", f"pandoc-3.12-{label}/bin/pandoc"
        return f"pandoc-3.12-linux-{self.arch}.tar.gz", "pandoc-3.12/bin/pandoc"

    def release(self, version, notices=False, pandoc_hash=None):
        self.version = version
        core = (
            "#!/bin/sh\n"
            'printf "core:%s\\n" "$*" >> "$MIDDEN_TEST_LOG"\n'
            '[ "$#" = 1 ] && [ "$1" = version ] || exit 65\n'
            f'printf "midden %s\\n" "${{MIDDEN_TEST_BAD_VERSION:-{version}}}"\n'
        ).encode()
        app = (
            "#!/bin/sh\n"
            'printf "app:%s\\n" "$*" >> "$MIDDEN_TEST_LOG"\n'
            'if [ "$#" = 1 ] && [ "$1" = --version ]; then\n'
            f'  printf "midden-ui %s\\n" "${{MIDDEN_TEST_BAD_UI_VERSION:-{version}}}"\n'
            'else\n  printf "foreground app fixture\\n"\nfi\n'
        ).encode()
        self.entries = {
            "core": [("midden", core), ("LICENSE", b"Synthetic license\n")],
            "app": [("midden-ui", app), ("app/compa-kernel", b"#!/bin/sh\nexit 0\n"), ("app/LICENSE", b"Synthetic license\n"),
                    ("app/NOTICE", b"Synthetic notice\n")],
            "bundle": [("skills/" + name, ("Synthetic " + name + " " + version + "\n").encode()) for name in SKILL_NAMES],
        }
        if notices:
            self.entries["core"].append(("THIRD_PARTY_NOTICES.txt", b"Synthetic dependency notice\n"))
            self.entries["app"].append(("app/THIRD_PARTY_NOTICES.txt", b"Synthetic app notice\n"))
        self.archives = {}
        for product in ("core", "app"):
            name = f"midden-{product}_{version}_{self.system}_{self.arch}.tar.gz"
            self.archives[product] = name
            write_tar(self.dist / name, self.entries[product])
        self.archives["bundle"] = f"midden-bundle_{version}.zip"
        write_zip(self.dist / self.archives["bundle"], self.entries["bundle"])
        self.pandoc_asset, self.pandoc_member = self.pandoc_layout()
        self.pandoc_binary = b"#!/bin/sh\nprintf 'pandoc 3.12\\n'\n"
        if self.pandoc_asset.endswith(".zip"):
            write_zip(self.dist / self.pandoc_asset, [(self.pandoc_member, self.pandoc_binary),
                                                       ("pandoc-3.12/share/man/man1/pandoc.1", b"Not installed\n")])
        else:
            write_tar(self.dist / self.pandoc_asset,
                      [(self.pandoc_member, self.pandoc_binary), ("pandoc-3.12/share/man/man1/pandoc.1.gz", b"x")],
                      links=(("pandoc-3.12/bin/pandoc-lua", "pandoc"),))
        self.build = {
            "version": version, "commit": "a" * 40,
            "go": "go version go1.26.0 " + self.target,
            "targets": [self.target], "products": ["core", "bundle", "app"],
            "core_binaries": {self.target: sha(core)}, "app_binaries": {self.target: sha(app)},
            "archives": list(self.archives.values()), "installers": ["install.ps1", "install.sh"],
        }
        (self.dist / "build-manifest.json").write_text(json.dumps(self.build), encoding="utf-8")
        rows = ["format\tmidden-release-v2", "version\t" + version, "commit\t" + "a" * 40]
        for product in ("core", "bundle", "app"):
            target = "universal" if product == "bundle" else self.target
            rows.append("\t".join(("archive", product, target, self.archives[product])))
            rows.extend("\t".join(("file", product, target, name, sha(data))) for name, data in self.entries[product])
        rows.append("\t".join(("fetch", "pandoc", self.target, PANDOC_RELEASE + self.pandoc_asset,
                               pandoc_hash or sha((self.dist / self.pandoc_asset).read_bytes()))))
        rows.append("\t".join(("take", "pandoc", self.target, self.pandoc_member, "app/tools/pandoc")))
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

    def installed(self, *products):
        expected = {RECEIPT}
        for product in products:
            expected |= {name for name, _ in self.entries[product]}
        if "app" in products:
            expected.add("app/tools/pandoc")
        return expected

    def files(self, directory):
        return {path.relative_to(directory).as_posix() for path in directory.rglob("*") if path.is_file()}

    def run_installer(self, *args, local=True, piped=False, shell=None, success=True, env=None):
        self.assertTrue(INSTALLER.is_file(), "install.sh is missing")
        arguments = ["--distribution-dir", unix_path(self.dist)] if local else []
        arguments += list(map(str, args))
        command = [shell or SHELL]
        command += ["-s", "--"] if piped else [unix_path(INSTALLER)]
        result = subprocess.run(
            command + arguments, input=INSTALLER.read_bytes() if piped else None,
            cwd=self.project, env=env or self.env, capture_output=True, timeout=180,
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

    def records(self):
        return [line.split("\t") for line in (self.install / RECEIPT).read_text(encoding="utf-8").splitlines()]

    def assert_clean_temporary(self):
        self.assertEqual(list(self.tmp.iterdir()), [], "Temporary extraction/rollback material leaked")

    def assert_absent(self, path):
        quoted = shlex.quote(unix_path(path))
        self.assertEqual(self.shell(f"test ! -e {quoted} && test ! -L {quoted}").returncode, 0, str(path))

    def test_plain_stdin_default_install_launches_the_app_in_the_foreground(self):
        output = self.run_installer(local=False, piped=True)
        self.assertIn("foreground app fixture", output)
        self.assertIn("integrity", output.lower())
        self.assertIn("authenticity", output.lower())
        self.assertEqual(self.files(self.install), self.installed("core", "bundle", "app"))
        self.assertEqual((self.install / "app" / "tools" / "pandoc").read_bytes(), self.pandoc_binary)
        self.assertEqual(self.log.read_text().splitlines(), ["core:version", "app:--version", "app:"])
        if not WINDOWS:
            self.assertTrue(os.access(self.install / "app" / "compa-kernel", os.X_OK), "the kernel is not executable")
        for name in ("midden", "midden-ui"):
            link = self.home / ".local" / "bin" / name
            result = self.shell("readlink " + shlex.quote(unix_path(link)))
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), unix_path(self.install / name))
        calls = self.curl_log.read_text()
        self.assertIn("/xibodev/midden/releases/latest/download/SHA256SUMS", calls)
        self.assertIn(PANDOC_RELEASE + self.pandoc_asset, calls)
        self.assert_clean_temporary()

    def test_piped_posix_dash_does_not_lose_the_script(self):
        dash = Path(SHELL).with_name("dash.exe" if WINDOWS else "dash")
        if not dash.is_file():
            candidate = shutil.which("dash")
            if not candidate:
                self.skipTest("dash is unavailable; native sh tests still run")
            dash = Path(candidate)
        self.run_installer("--no-path", "--no-launch", piped=True, shell=str(dash))
        self.assertTrue((self.install / "midden-ui").is_file())
        self.assertEqual(self.log.read_text().splitlines(), ["core:version", "app:--version"])

    def test_no_path_no_launch_lifecycle_keeps_unowned_state(self):
        self.run_installer("--no-path", "--no-launch")
        self.assert_absent(self.home / ".local" / "bin")
        retained = self.install / "personal notes.txt"
        retained.write_text("unowned", encoding="utf-8")
        state = self.home / "data" / "midden" / "sessions"
        state.mkdir(parents=True)
        (state / "keep").write_text("retained", encoding="utf-8")
        self.run_installer("--verify", local=False)
        self.release("1.2.4")
        self.run_installer("--upgrade", "--no-path", "--no-launch")
        self.assertIn(["version", "1.2.4"], self.records())
        shutil.rmtree(self.dist)
        self.run_installer("--uninstall", local=False)
        self.assertEqual(retained.read_text(), "unowned")
        self.assertEqual((state / "keep").read_text(), "retained")
        self.assertEqual({p.name for p in self.install.iterdir()}, {"personal notes.txt"})
        self.assertFalse(self.curl_log.exists(), "Local inputs and receipt operations must not download")
        self.assert_clean_temporary()

    def test_no_open_is_forwarded_only_to_the_foreground_launch(self):
        self.run_installer("--no-path", "--no-open")
        self.assertEqual(self.log.read_text().splitlines(), ["core:version", "app:--version", "app:--no-open"])

    def test_core_mode_installs_only_core(self):
        self.run_installer("--mode", "core", "--no-path")
        self.assertEqual(self.files(self.install), self.installed("core"))
        self.assertEqual(self.log.read_text().splitlines(), ["core:version"])
        self.assertNotIn(self.pandoc_asset, self.curl_log.read_text() if self.curl_log.exists() else "")
        self.run_installer("--verify", local=False)
        self.run_installer("--uninstall", local=False)
        self.assert_absent(self.install)

    def test_bundle_mode_copies_the_skills_unchanged_for_the_person_and_for_projects(self):
        self.run_installer("--mode", "bundle", "--harness", "claude", "--harness", "agents", "--no-path")
        self.assertEqual(self.files(self.install), self.installed("core", "bundle"))
        for folder in (self.home / ".claude" / "skills", self.home / ".agents" / "skills"):
            self.assertEqual({p.relative_to(folder).as_posix(): p.read_bytes() for p in folder.rglob("*") if p.is_file()},
                             self.skills)
        self.run_installer("--mode", "bundle", "--harness", "copilot", "--project", unix_path(self.project), "--no-path")
        project_skills = self.project / ".github" / "skills"
        self.assertEqual(self.files(project_skills), set(self.skills))
        places = [row for row in self.records() if row[0] == "place"]
        self.assertEqual([(r[2], r[3], r[4], r[6]) for r in places], [
            ("skills", "claude", "user", unix_path(self.home / ".claude" / "skills")),
            ("skills", "agents", "user", unix_path(self.home / ".agents" / "skills")),
            ("skills", "copilot", "project", unix_path(project_skills)),
        ])
        self.run_installer("--verify", local=False)
        self.run_installer("--uninstall", "--harness", "claude", local=False)
        self.assertEqual(list((self.home / ".claude" / "skills").iterdir()), [])
        self.assertEqual(self.files(self.home / ".agents" / "skills"), set(self.skills))
        self.run_installer("--uninstall", local=False)
        self.assertEqual(list((self.home / ".agents" / "skills").iterdir()), [])
        self.assertEqual(list(project_skills.iterdir()), [])
        self.assert_absent(self.install)
        self.assertFalse(self.log.read_text().count("forbidden"), "Python, Node, Go or jq were used")

    def test_products_add_up_and_a_newer_release_needs_upgrade(self):
        self.run_installer("--mode", "core", "--no-path")
        self.run_installer("--mode", "bundle", "--harness", "agents", "--no-path")
        self.assertEqual(self.files(self.install), self.installed("core", "bundle"))
        self.run_installer("--no-path", "--no-launch")
        self.assertEqual(self.files(self.install), self.installed("core", "bundle", "app"))
        self.assertEqual([r[1] for r in self.records() if r[0] == "product"], ["core", "bundle", "app"])
        self.release("1.2.4")
        before = self.snapshot()
        output = self.run_installer("--mode", "bundle", "--harness", "claude", "--no-path", success=False)
        self.assertIn("--upgrade", output)
        self.assertEqual(self.snapshot(), before)
        self.run_installer("--upgrade", "--no-path", "--no-launch")
        self.assertEqual((self.home / ".agents" / "skills" / "midden-article" / "SKILL.md").read_bytes(),
                         b"Synthetic midden-article/SKILL.md 1.2.4\n")
        self.assertIn(["version", "1.2.4"], self.records())
        self.run_installer("--uninstall", local=False)
        self.assert_absent(self.install)

    def test_upgrade_refreshes_the_persons_copies_keeps_edited_ones_and_lists_older_projects(self):
        self.run_installer("--mode", "bundle", "--harness", "claude,agents", "--no-path")
        self.run_installer("--mode", "bundle", "--harness", "copilot", "--project", ".", "--no-path")
        edited = self.home / ".agents" / "skills" / "midden-article" / "SKILL.md"
        edited.write_bytes(edited.read_bytes() + b"Local tuning.\n")
        project_skills = self.project / ".github" / "skills"
        old_project = {p: p.read_bytes() for p in project_skills.rglob("*") if p.is_file()}
        self.release("1.2.4")
        output = self.run_installer("--upgrade", "--no-path")
        self.assertEqual((self.home / ".claude" / "skills" / "midden-article" / "SKILL.md").read_bytes(),
                         b"Synthetic midden-article/SKILL.md 1.2.4\n")
        self.assertTrue(edited.read_bytes().endswith(b"Local tuning.\n"))
        self.assertIn(f"Left the skills in {unix_path(self.home / '.agents' / 'skills')} at 1.2.3", output)
        self.assertEqual({p: p.read_bytes() for p in project_skills.rglob("*") if p.is_file()}, old_project)
        self.assertIn(f"Older skills (1.2.3) in {unix_path(self.project)}; update them with: install.sh --mode bundle "
                      f"--harness copilot --project '{unix_path(self.project)}'", output)
        self.assertEqual(sorted((r[3], r[5]) for r in self.records() if r[0] == "place"),
                         [("agents", "1.2.3"), ("claude", "1.2.4"), ("copilot", "1.2.3")])
        output = self.run_installer("--mode", "bundle", "--harness", "agents", "--no-path", success=False)
        self.assertIn("were changed since Midden installed them", output)
        self.run_installer("--mode", "bundle", "--harness", "copilot", "--project", ".", "--no-path")
        self.assertEqual((project_skills / "midden-article" / "SKILL.md").read_bytes(),
                         b"Synthetic midden-article/SKILL.md 1.2.4\n")
        output = self.run_installer("--uninstall", local=False)
        self.assertIn("Kept " + unix_path(edited), output)
        self.assertEqual(self.files(self.home / ".agents" / "skills"), {"midden-article/SKILL.md"})
        self.assert_absent(self.install)

    def test_dry_run_validates_without_writing_home_or_running_payloads(self):
        before = self.snapshot()
        output = self.run_installer("--dry-run")
        self.assertIn("Dry-run: install Midden 1.2.3 (core, bundle, app)", output)
        self.assertIn("fetch " + unix_path(self.install / "app" / "tools" / "pandoc"), output)
        self.assertIn("create " + unix_path(self.install / RECEIPT), output)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()
        output = self.run_installer("--mode", "bundle", "--harness", "claude", "--dry-run")
        self.assertIn("create " + unix_path(self.home / ".claude" / "skills" / "midden-article" / "SKILL.md"), output)
        self.assertEqual(self.snapshot(), before)
        self.run_installer("--no-path", "--no-launch")
        before = self.snapshot()
        self.run_installer("--uninstall", "--dry-run", local=False)
        self.assertEqual(self.snapshot(), before)

    def test_system_awk_dry_run_normalizes_paths_and_rejects_root(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        before = self.snapshot()
        output = self.run_installer("--mode", "core", "--install-dir", "./unused parent/../planned install",
                                    "--dry-run", "--no-path", "--no-launch")
        self.assertEqual(output.splitlines()[0],
                         f"Dry-run: install Midden 1.2.3 (core) in {unix_path(self.project / 'planned install')}")
        for root in ("/", "/./"):
            with self.subTest(root=root):
                rejected = self.run_installer("--mode", "core", "--install-dir", root, "--dry-run", success=False)
                self.assertIn("dedicated directory", rejected)
        self.assertEqual(self.snapshot(), before)
        self.assert_absent(self.project / "planned install")
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_system_awk_full_manifest_upgrade_and_uninstall(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        state = self.home / "data" / "midden" / "user-state.txt"
        state.parent.mkdir(parents=True)
        state.write_text("retained state", encoding="utf-8")
        for mode, products in (("app", ("core", "bundle", "app")), ("core", ("core",))):
            with self.subTest(mode=mode):
                self.release("1.2.3", notices=True)
                self.log.unlink(missing_ok=True)
                before = self.snapshot()
                self.run_installer("--mode", mode, "--version", "v1.2.3",
                                   "--repository", "example/fork", "--dry-run", "--no-launch")
                self.assertEqual(self.snapshot(), before)
                self.assertFalse(self.log.exists())
                self.run_installer("--mode", mode, "--no-launch")
                note = self.install / "personal.txt"
                note.write_text("unowned neighbor", encoding="utf-8")
                self.run_installer("--verify", local=False)
                self.release("1.2.4", notices=True)
                self.run_installer("--upgrade", "--no-launch")
                self.assertIn("\nversion\t1.2.4\n", (self.install / RECEIPT).read_text())
                for product in products:
                    for name, data in self.entries[product]:
                        self.assertEqual((self.install / name).read_bytes(), data, name)
                self.run_installer("--verify", local=False)
                self.run_installer("--uninstall", local=False)
                self.assertFalse((self.install / RECEIPT).exists())
                self.assertEqual(self.files(self.install), {"personal.txt"})
                self.assertEqual(note.read_text(), "unowned neighbor")
                self.assertEqual(state.read_text(), "retained state")
                for name in ("midden", "midden-ui"):
                    self.assert_absent(self.home / ".local" / "bin" / name)
                note.unlink()
                self.assert_clean_temporary()

    def test_system_awk_rejects_manifest_collisions_and_misplaced_members_before_extraction(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        manifest = self.dist / "manifest.tsv"
        original = manifest.read_text()
        for product, target, name in (("bundle", "universal", "skills/midden-article"),
                                      ("bundle", "universal", "skills/MIDDEN-ARTICLE/new.md"),
                                      ("bundle", "universal", "skills/midden-article/../escape"),
                                      ("bundle", "universal", "skills/other/SKILL.md"),
                                      ("app", self.target, "app/tools/pandoc"),
                                      ("app", self.target, "midden"),
                                      ("core", self.target, "skills/midden-article/x.md")):
            with self.subTest(name=name):
                row = f"file\t{product}\t{target}\t{name}\t" + "0" * 64 + "\n"
                manifest.write_text(original + row, encoding="utf-8")
                self.seal()
                before = self.snapshot()
                output = self.run_installer("--no-path", "--no-launch", success=False)
                self.assertIn("Invalid release manifest.tsv", output)
                self.assertEqual(self.snapshot(), before)
                self.assertFalse(self.log.exists())
                self.assert_clean_temporary()

    def test_system_awk_rejects_receipts_that_do_not_describe_this_installation(self):
        self.tool("awk", 'exec /usr/bin/awk "$@"\n')
        self.run_installer("--no-path", "--no-launch")
        receipt = self.install / RECEIPT
        original = receipt.read_bytes()
        probes = self.log.read_bytes()
        for tail in (b"file\troot\tskills/midden-article\t" + b"0" * 64 + b"\n",
                     b"file\t9\tx.md\t" + b"0" * 64 + b"\n",
                     b"link\t/elsewhere/midden\t/elsewhere/midden\n",
                     b"product\tui\n"):
            with self.subTest(tail=tail):
                receipt.write_bytes(original + tail)
                before = self.snapshot()
                for operation in ("--verify", "--uninstall"):
                    output = self.run_installer(operation, local=False, success=False)
                    self.assertIn("Invalid or relocated installation receipt", output)
                    self.assertEqual(self.snapshot(), before)
                    self.assertEqual(self.log.read_bytes(), probes)
                    self.assert_clean_temporary()
        receipt.write_bytes(original.replace(unix_path(self.install).encode(), b"/elsewhere/midden", 1))
        self.run_installer("--verify", local=False, success=False)

    def test_corrupt_metadata_and_archives_are_rejected_before_execution(self):
        for name in ("manifest.tsv", "build-manifest.json", self.archives["app"], self.archives["bundle"]):
            with self.subTest(name=name):
                path = self.dist / name
                original = path.read_bytes()
                path.write_bytes(original + b"corrupt")
                output = self.run_installer("--no-path", "--no-launch", success=False)
                self.assertIn("checksum", output.lower())
                self.assertFalse(self.log.exists())
                self.assert_absent(self.install)
                path.write_bytes(original)
                self.assert_clean_temporary()

    def test_per_file_hash_mismatch_is_rejected_even_with_sealed_archives(self):
        entries = [(name, b"changed" if name == "midden-ui" else data) for name, data in self.entries["app"]]
        write_tar(self.dist / self.archives["app"], entries)
        self.seal()
        output = self.run_installer("--no-path", "--no-launch", success=False)
        self.assertIn("checksum", output.lower())
        self.assertFalse(self.log.exists())
        self.release("1.2.3")
        entries = [(name, b"changed" if name.endswith("SKILL.md") else data) for name, data in self.entries["bundle"]]
        write_zip(self.dist / self.archives["bundle"], entries)
        self.seal()
        output = self.run_installer("--mode", "bundle", "--harness", "claude", "--no-path", success=False)
        self.assertIn("checksum", output.lower())
        self.assert_absent(self.home / ".claude")

    def test_metadata_size_is_bounded_at_one_mebibyte(self):
        (self.dist / "manifest.tsv").write_bytes(b"x" * (1024 * 1024 + 1))
        self.seal()
        output = self.run_installer("--no-path", success=False)
        self.assertIn("limit", output.lower())
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_archive_expansion_is_bounded_before_execution(self):
        with gzip.open(self.dist / self.archives["core"], "wb") as stream:
            for _ in range(257):
                stream.write(b"\0" * (1024 * 1024))
        self.seal()
        output = self.run_installer("--mode", "core", "--no-path", success=False)
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
                self.run_installer("--mode", "core", "--no-path", success=False)
                self.assertFalse(self.log.exists())
                self.assertFalse((self.root / "escape").exists())
                self.assert_clean_temporary()

    def test_extra_duplicate_linked_and_directory_zip_members_are_refused(self):
        path = self.dist / self.archives["bundle"]
        for case in ("extra", "duplicate", "link", "directory"):
            with self.subTest(case=case):
                entries = list(self.entries["bundle"])
                if case == "extra":
                    entries.append(("skills/midden-article/extra.md", b"extra\n"))
                if case == "duplicate":
                    entries.append(entries[0])
                write_zip(path, entries)
                if case in ("link", "directory"):
                    with zipfile.ZipFile(path, "a") as archive:
                        entry = zipfile.ZipInfo("skills/midden-article/linked" if case == "link" else "skills/midden-article/")
                        entry.create_system = 3
                        entry.external_attr = (0o120777 if case == "link" else 0o040755) << 16
                        archive.writestr(entry, b"../../outside" if case == "link" else b"")
                self.seal()
                output = self.run_installer("--mode", "bundle", "--harness", "claude", "--no-path", success=False)
                self.assertIn("allowlist", output)
                self.assert_absent(self.home / ".claude")
                self.assertFalse(self.log.exists())
                self.assert_clean_temporary()

    def test_malformed_duplicate_and_unsafe_metadata_records_are_refused(self):
        manifest = self.dist / "manifest.tsv"
        original = manifest.read_text()
        malformed = (
            original + "format\tmidden-release-v2\n",
            original.replace("format\tmidden-release-v2", "format\tmidden-release-v1"),
            original + f"file\tapp\t{self.target}\t../outside\t" + "0" * 64 + "\n",
            original.replace("version\t1.2.3", "version\t../1.2.3"),
            original.replace("\tLICENSE\t", "\t./LICENSE\t"),
            original + "unknown\tvalue\n",
            original.replace(PANDOC_RELEASE, "https://example.invalid/pandoc/"),
            original.replace("\tapp/tools/pandoc\n", "\tpandoc\n"),
        )
        for data in malformed:
            with self.subTest(tail=data[-90:]):
                manifest.write_text(data, encoding="utf-8")
                self.seal()
                self.run_installer("--no-path", success=False)
                self.assertFalse(self.log.exists())
        manifest.write_text(original, encoding="utf-8")
        self.seal()
        sums = self.dist / "SHA256SUMS"
        sums.write_text(sums.read_text() + sums.read_text().splitlines()[0] + "\n", encoding="utf-8")
        self.run_installer("--no-path", success=False)
        self.assertFalse(self.log.exists())

    def test_native_target_has_no_foreign_fallback(self):
        for system, arch in (("Linux", "armv7l"), ("FreeBSD", "x86_64"),
                             ("Darwin" if self.system == "linux" else "Linux", "x86_64")):
            with self.subTest(system=system, arch=arch):
                env = dict(self.env, MIDDEN_TEST_SYSTEM=system, MIDDEN_TEST_MACHINE=arch)
                output = self.run_installer("--no-path", env=env, success=False)
                self.assertTrue("native" in output.lower() or "unsupported" in output.lower(), output)
                self.assertFalse(self.log.exists())

    def test_repository_version_and_options_are_explicit_and_safe(self):
        self.run_installer("--repository", "example/fork", "--version", "v1.2.3", "--no-path", "--no-launch", local=False)
        calls = self.curl_log.read_text()
        self.assertIn("https://github.com/example/fork/releases/download/v1.2.3/", calls)
        self.assertIn("-q\n", calls, "curl config must be disabled")
        self.assertNotIn("Authorization", calls)
        for args in (("--repository", "../fork"), ("--repository", "https://example/repo"),
                     ("--repository", "owner/repo/extra"), ("--version", "../escape"),
                     ("--mode", "unknown"), ("--mode", "ui"), ("--mode", "cli"), ("--host", "claude"),
                     ("--mode", "bundle"), ("--harness", "claude"), ("--mode", "bundle", "--harness", "cursor"),
                     ("--project", "."), ("--version",), ("--upgrade", "--uninstall"),
                     ("--install-dir", "--no-launch"), ("--verify", "--harness", "claude")):
            with self.subTest(args=args):
                before = self.curl_log.read_bytes()
                self.run_installer(*args, local=False, success=False)
                self.assertEqual(self.curl_log.read_bytes(), before)

    def test_release_download_failure_has_no_prerelease_or_auth_fallback(self):
        output = self.run_installer(local=False, success=False,
                                    env=dict(self.env, MIDDEN_TEST_DOWNLOAD_FAIL="1",
                                             GH_TOKEN="synthetic-never-use", GITHUB_TOKEN="synthetic-never-use"))
        self.assertIn("download", output.lower())
        calls = self.curl_log.read_text()
        self.assertNotIn("api.github.com", calls)
        self.assertNotIn("synthetic-never-use", calls)
        self.assert_absent(self.install)

    def test_stream_failure_after_valid_bytes_is_not_hidden_by_a_previous_success(self):
        self.tool("curl", """for value do url=$value; done
cat "$MIDDEN_TEST_DIST/${url##*/}"
case "$url" in */manifest.tsv) exit 22;; esac
""")
        self.run_installer("--mode", "core", "--no-path", local=False, success=False)
        self.assertFalse(self.log.exists(), "A failed download must not reach executable probes")
        self.assert_absent(self.install)
        self.assert_clean_temporary()

    def test_tar_failure_after_valid_member_bytes_is_still_fatal(self):
        self.tool("tar", """/usr/bin/tar "$@"
status=$?
case "$1" in *O*) case "$*" in *midden) exit 74;; esac;; esac
exit "$status"
""")
        self.run_installer("--mode", "core", "--no-path", success=False)
        self.assertFalse(self.log.exists())
        self.assert_absent(self.install)
        self.assert_clean_temporary()

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
        self.assertEqual(Path(lines[2]).resolve(), self.tmp, "Installer staging remains private")
        mapped = Path(lines[3]).resolve()
        self.assertNotIn(self.root, (mapped, *mapped.parents))
        self.assertRegex(lines[4], r"^\d+\.\d+", "Exercise the installed Bash runtime, not a fake shell")

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
        self.run_installer("--mode", "core", "--no-path")
        self.assertEqual((self.install / "LICENSE").read_bytes(), b"Synthetic license\n")
        self.run_installer("--uninstall", local=False)

    def test_packaged_dependency_notices_are_hash_verified_and_receipt_owned(self):
        self.release("1.2.3", notices=True)
        self.run_installer("--no-path", "--no-launch")
        self.assertEqual((self.install / "THIRD_PARTY_NOTICES.txt").read_bytes(), b"Synthetic dependency notice\n")
        self.assertEqual((self.install / "app" / "THIRD_PARTY_NOTICES.txt").read_bytes(), b"Synthetic app notice\n")
        self.assertIn(["file", "root", "app/THIRD_PARTY_NOTICES.txt", sha(b"Synthetic app notice\n")], self.records())
        self.run_installer("--verify", local=False)
        self.run_installer("--uninstall", local=False)
        self.assert_absent(self.install)

    def test_dry_run_rejects_file_ancestors_of_payloads_and_launch_links(self):
        self.install.mkdir(parents=True)
        obstacle = self.install / "skills"
        obstacle.write_text("unowned file, not a directory", encoding="utf-8")
        before = self.snapshot()
        self.run_installer("--dry-run", "--no-path", success=False)
        self.assertEqual(self.snapshot(), before)
        obstacle.unlink()
        obstacle = self.home / ".local" / "bin"
        obstacle.write_text("unowned PATH entry, not a directory", encoding="utf-8")
        before = self.snapshot()
        self.run_installer("--mode", "core", "--dry-run", success=False)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())

    def test_relative_user_paths_are_normalized_without_relaxing_archive_paths(self):
        self.run_installer("--mode", "core", "--install-dir", "./custom install", "--no-path")
        self.assertTrue((self.project / "custom install" / "midden").is_file())
        self.run_installer("--install-dir", "custom install", "--uninstall", local=False)
        self.assert_absent(self.project / "custom install")

    def assert_protected_state_root(self, state, env, aliases=()):
        state.mkdir(parents=True, exist_ok=True)
        (state / "sentinel.txt").write_text("retained user state", encoding="utf-8")
        before = self.snapshot()
        directories = {str(path) for base in (self.home, self.project)
                       for path in base.rglob("*") if path.is_dir()}
        destinations = (state, state / "binary install", state.parent, *aliases)
        for mode in ("core", "app"):
            for destination in destinations:
                output = self.run_installer("--mode", mode, "--install-dir", unix_path(destination),
                                            "--no-path", "--no-launch", env=env, success=False)
                diagnostic = "dedicated directory" if destination == self.home else "overlap"
                self.assertIn(diagnostic, output.lower(), str(destination))
                self.assertEqual(self.snapshot(), before, str(destination))
                self.assertEqual({str(path) for base in (self.home, self.project)
                                  for path in base.rglob("*") if path.is_dir()}, directories)
                self.assertFalse(self.log.exists(), "Reject state overlap before executing version probes")
                self.assert_clean_temporary()

    def test_install_roots_keep_linux_xdg_app_state(self):
        self.system = "linux"
        self.release("1.2.3")
        data = self.home / "xdg storage"
        env = dict(self.env, MIDDEN_TEST_SYSTEM="Linux", XDG_DATA_HOME=unix_path(data),
                   MIDDEN_HOME=unix_path(self.home / "separate core state"))
        self.assert_protected_state_root(data / "midden", env)

    def test_install_roots_keep_macos_default_app_state(self):
        self.system = "darwin"
        self.release("1.2.3")
        env = dict(self.env, MIDDEN_TEST_SYSTEM="Darwin", MIDDEN_HOME=unix_path(self.home / "separate core state"))
        state = self.home / "Library" / "Application Support" / "Midden"
        self.assert_protected_state_root(state, env, aliases=(state.with_name("midden"),))

    def test_install_roots_keep_default_and_explicit_core_and_compa_state(self):
        env = dict(self.env)
        env.pop("MIDDEN_HOME", None)
        self.assert_protected_state_root(self.home / ".midden", env)
        self.assert_protected_state_root(self.project / "storage" / "core state",
                                         dict(self.env, MIDDEN_HOME="storage/core state"))
        state = self.home / "shared storage" / "compa"
        self.assert_protected_state_root(state, dict(self.env, COMPA_HOME=unix_path(state)))

    def test_install_roots_reject_physical_state_alias_overlap(self):
        state = self.home / "physical state"
        state.mkdir()
        (state / "sentinel.txt").write_text("retained user state", encoding="utf-8")
        alias = self.home / "configured state"
        result = self.shell("ln -s " + shlex.quote(unix_path(state)) + " " + shlex.quote(unix_path(alias)))
        self.assertEqual(result.returncode, 0, result.stderr)
        before = self.snapshot()
        self.run_installer("--mode", "core", "--install-dir", unix_path(state / "tools"),
                           "--no-path", "--no-launch", success=False, env=dict(self.env, MIDDEN_HOME=unix_path(alias)))
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(self.log.exists())
        self.assert_clean_temporary()

    def test_owned_modified_files_block_verify_upgrade_and_uninstall(self):
        self.run_installer("--no-path", "--no-launch")
        (self.install / "app" / "NOTICE").write_text("user change", encoding="utf-8")
        before = self.snapshot()
        for operation in ("--verify", "--upgrade", "--uninstall"):
            with self.subTest(operation=operation):
                output = self.run_installer(operation, "--no-path", "--no-launch", success=False)
                self.assertTrue("modified" in output.lower() or "changed or removed" in output.lower(), output)
                self.assertEqual(self.snapshot(), before)
        self.assert_clean_temporary()

    def test_unowned_payload_skill_and_path_entries_are_never_replaced(self):
        self.install.mkdir(parents=True)
        (self.install / "midden").write_text("unowned binary", encoding="utf-8")
        before = self.snapshot()
        self.run_installer("--no-path", "--no-launch", success=False)
        self.assertEqual(self.snapshot(), before)
        (self.install / "midden").unlink()
        skill = self.home / ".claude" / "skills" / "midden-article" / "SKILL.md"
        skill.parent.mkdir(parents=True)
        skill.write_text("somebody else's skill", encoding="utf-8")
        before = self.snapshot()
        output = self.run_installer("--mode", "bundle", "--harness", "claude", "--no-path", success=False)
        self.assertIn("does not own", output)
        self.assertEqual(self.snapshot(), before)
        skill.unlink()
        bindir = self.home / ".local" / "bin"
        bindir.mkdir()
        (bindir / "midden-ui").write_text("unowned command", encoding="utf-8")
        before = self.snapshot()
        self.run_installer("--no-launch", success=False)
        self.assertEqual(self.snapshot(), before)
        self.run_installer("--no-path", "--no-launch")
        self.assertEqual((bindir / "midden-ui").read_text(), "unowned command")

    def test_owned_links_are_verified_and_removed_without_touching_neighbors(self):
        self.run_installer("--no-launch")
        neighbor = self.home / ".local" / "bin" / "other-command"
        neighbor.write_text("unowned", encoding="utf-8")
        self.run_installer("--verify", local=False)
        self.run_installer("--uninstall", "--no-path", local=False)
        self.assertEqual(neighbor.read_text(), "unowned")
        for name in ("midden", "midden-ui"):
            self.assert_absent(neighbor.parent / name)

    def test_symlinked_install_ancestors_and_internal_directories_are_refused(self):
        outside = self.root / "outside"
        outside.mkdir()
        linked = self.home / "linked"
        result = self.shell("ln -s " + shlex.quote(unix_path(outside)) + " " + shlex.quote(unix_path(linked)))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.run_installer("--install-dir", unix_path(linked / "midden"), "--no-path", success=False)
        self.run_installer("--mode", "bundle", "--harness", "copilot", "--project", unix_path(linked),
                           "--no-path", success=False)
        self.assertEqual(list(outside.iterdir()), [])
        self.run_installer("--no-path", "--no-launch")
        shutil.rmtree(self.install / "skills")
        result = self.shell("ln -s " + shlex.quote(unix_path(outside)) + " " + shlex.quote(unix_path(self.install / "skills")))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.run_installer("--uninstall", local=False, success=False)
        self.assertEqual(list(outside.iterdir()), [])

    def test_external_temporary_requirement_and_existing_lock_are_enforced(self):
        self.install.mkdir(parents=True)
        output = self.run_installer("--no-path", env=dict(self.env, TMPDIR=unix_path(self.install)), success=False)
        self.assertIn("external", output.lower())
        self.install.rmdir()
        self.install.parent.mkdir(parents=True, exist_ok=True)
        lock = self.install.with_name("midden.midden-install.lock")
        lock.mkdir()
        output = self.run_installer("--no-path", success=False)
        self.assertIn("lock", output.lower())
        self.assertTrue(lock.is_dir())
        self.assertFalse(self.log.exists())

    def test_upgrade_failure_rolls_back_all_published_files_skills_and_receipt(self):
        self.run_installer("--mode", "bundle", "--harness", "claude", "--no-path")
        self.run_installer("--no-path", "--no-launch")
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
        output = self.run_installer("--upgrade", "--no-path", "--no-launch", success=False, env=dict(
            self.env, MIDDEN_TEST_FAIL_DEST=unix_path(self.install / "midden-ui"),
            MIDDEN_TEST_FAIL_MARK=unix_path(self.root / "failed-once"),
        ))
        self.assertIn("failure", output.lower())
        self.assertEqual(self.snapshot(), before)
        self.assert_clean_temporary()
        self.run_installer("--verify", local=False)

    def test_version_probes_must_match_the_verified_manifest(self):
        for key in ("MIDDEN_TEST_BAD_VERSION", "MIDDEN_TEST_BAD_UI_VERSION"):
            with self.subTest(key=key):
                self.run_installer("--no-path", "--no-launch", success=False, env=dict(self.env, **{key: "9.9.9"}))
                self.assertFalse((self.install / RECEIPT).exists())
                self.assert_clean_temporary()

    def test_pandoc_must_match_its_pin_and_is_needed_only_by_the_app(self):
        archive = self.dist / self.pandoc_asset
        original = archive.read_bytes()
        archive.write_bytes(original + b"tampered")
        output = self.run_installer("--no-path", "--no-launch", success=False)
        self.assertIn("Pandoc archive checksum mismatch", output)
        self.assert_absent(self.install)
        archive.unlink()
        output = self.run_installer("--no-path", "--no-launch", success=False)
        self.assertIn(f"needs {self.pandoc_asset}", output)
        self.assertIn(PANDOC_RELEASE + self.pandoc_asset, output)
        self.run_installer("--mode", "bundle", "--harness", "claude", "--no-path")
        archive.write_bytes(original)
        self.run_installer("--no-path", "--no-launch")
        self.assertEqual((self.install / "app" / "tools" / "pandoc").read_bytes(), self.pandoc_binary)
        result = self.shell(shlex.quote(unix_path(self.install / "app" / "tools" / "pandoc")))
        self.assertEqual(result.stdout, "pandoc 3.12\n", "The App's Pandoc is executable")
        archive.unlink()
        self.run_installer("--upgrade", "--no-path", "--no-launch")
        self.run_installer("--uninstall", local=False)
        if self.pandoc_asset.endswith(".zip"):
            write_zip(archive, [("pandoc-3.12/other", b"x")])
        else:
            write_tar(archive, [("pandoc-3.12/other", b"x")])
        self.release("1.2.3", pandoc_hash=sha(archive.read_bytes()))
        if self.pandoc_asset.endswith(".zip"):
            write_zip(archive, [("pandoc-3.12/other", b"x")])
        else:
            write_tar(archive, [("pandoc-3.12/other", b"x")])
        output = self.run_installer("--no-path", "--no-launch", success=False)
        self.assertIn("Pandoc archive", output)
        self.assert_absent(self.install)

    def test_the_start_entry_opens_the_app_and_goes_with_it(self):
        for system in ("Linux", "Darwin"):
            with self.subTest(system=system):
                self.system = system.lower()
                self.release("1.2.3")
                env = dict(self.env, MIDDEN_TEST_SYSTEM=system)
                self.run_installer("--no-path", "--no-launch", env=env)
                if system == "Linux":
                    entry = self.home / "data" / "applications" / "midden.desktop"
                    text = entry.read_text(encoding="utf-8")
                    self.assertIn(f'Exec="{unix_path(self.install)}/midden-ui"', text)
                    self.assertIn("Name=Midden", text)
                else:
                    app = self.home / "Applications" / "Midden.app" / "Contents"
                    script = (app / "MacOS" / "Midden").read_text(encoding="utf-8")
                    self.assertIn(f"exec '{unix_path(self.install)}/midden-ui'", script)
                    self.assertIn("<string>1.2.3</string>", (app / "Info.plist").read_text(encoding="utf-8"))
                    entry = app / "MacOS" / "Midden"
                    if not WINDOWS:
                        self.assertTrue(os.access(entry, os.X_OK))
                self.run_installer("--verify", local=False, env=env)
                entry.write_text(entry.read_text(encoding="utf-8") + "# edited\n", encoding="utf-8")
                output = self.run_installer("--upgrade", "--no-path", "--no-launch", env=env)
                self.assertIn("Kept the Start entry", output)
                output = self.run_installer("--uninstall", local=False, env=env)
                self.assertIn("Kept " + unix_path(entry), output)
                self.assertTrue(entry.exists())
                self.assert_absent(self.install)
                shutil.rmtree(self.home / ("data" if system == "Linux" else "Applications"))

    def legacy_install(self, mode):
        """An installation made by the 0.3 installer, as it laid out files and its receipt."""
        files = [("midden", self.entries["core"][0][1]), ("LICENSE", b"Synthetic license\n")]
        if mode == "ui":
            files += [("midden-ui", self.entries["app"][0][1]), ("NOTICE", b"Old notice\n"),
                      ("start.sh", b"#!/bin/sh\n"), ("start.ps1", b"# old\n"), ("package-manifest.json", b"{}\n"),
                      ("bundles/article/SKILL.md", b"Old guidance\n")]
        for name, data in files:
            path = self.install / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        rows = [("format", "midden-bootstrap-v1"), ("mode", mode), ("version", "1.0.0"), ("target", self.target),
                ("root", unix_path(self.install)), ("home", unix_path(self.home)),
                ("project", unix_path(self.project)), ("host", "copilot")]
        rows += [("file", name, sha(data)) for name, data in files]
        bindir = self.home / ".local" / "bin"
        bindir.mkdir(parents=True, exist_ok=True)
        for name in (("midden", "midden-ui") if mode == "ui" else ("midden",)):
            result = self.shell("ln -s " + shlex.quote(unix_path(self.install / name)) + " "
                                + shlex.quote(unix_path(bindir / name)))
            self.assertEqual(result.returncode, 0, result.stderr)
            rows.append(("link", name, unix_path(self.install / name)))
        (self.install / LEGACY_RECEIPT).write_bytes(("\n".join("\t".join(row) for row in rows) + "\n").encode())

    def test_upgrade_replaces_an_installation_made_by_the_0_3_installer(self):
        for mode, products in (("ui", ("core", "bundle", "app")), ("core", ("core",))):
            with self.subTest(mode=mode):
                self.legacy_install(mode)
                (self.install / "unowned.txt").write_bytes(b"Keep me.\n")
                before = self.snapshot()
                output = self.run_installer("--no-path", "--no-launch", success=False)
                self.assertIn("--upgrade", output)
                self.assertEqual(self.snapshot(), before)
                output = self.run_installer("--verify", local=False)
                self.assertIn("0.3 installer", output)
                self.run_installer("--upgrade", "--no-launch")
                self.assertEqual(self.files(self.install) - {"unowned.txt"}, self.installed(*products))
                self.assert_absent(self.install / LEGACY_RECEIPT)
                self.assert_absent(self.install / "bundles")
                self.run_installer("--verify", local=False)
                self.run_installer("--uninstall", local=False)
                self.assertEqual(self.files(self.install), {"unowned.txt"})
                self.assert_absent(self.home / ".local" / "bin" / "midden")
                (self.install / "unowned.txt").unlink()

    def test_a_0_3_cli_installation_is_refused_with_instructions(self):
        self.install.mkdir(parents=True)
        (self.install / "midden-install-receipt.json").write_text("{}", encoding="utf-8")
        for operation in ((), ("--upgrade",), ("--uninstall",)):
            output = self.run_installer(*operation, "--no-path", success=False)
            self.assertIn("0.3 installer", output)
        (self.install / "midden-install-receipt.json").unlink()
        rows = [("format", "midden-bootstrap-v1"), ("mode", "cli"), ("version", "1.0.0"), ("target", self.target),
                ("root", unix_path(self.install)), ("home", unix_path(self.home)),
                ("project", unix_path(self.project)), ("host", "copilot"),
                ("file", ".midden-bootstrap-cli.py", sha(b"x"))]
        (self.install / ".midden-bootstrap-cli.py").write_bytes(b"x")
        (self.install / LEGACY_RECEIPT).write_bytes(("\n".join("\t".join(row) for row in rows) + "\n").encode())
        output = self.run_installer("--upgrade", "--no-path", success=False)
        self.assertIn("0.3 installer", output)


if __name__ == "__main__":
    unittest.main()
