"""The Windows installer's outcomes, using only synthetic releases, homes and registries."""

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import stat
import struct
import subprocess
import tempfile
import threading
import unittest
import warnings
import zipfile


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "install.ps1"
SHELLS = [path for name in ("powershell", "pwsh") if (path := shutil.which(name))]
FIRST = SHELLS[:1]
VERSION = "0.4.0"
COMMIT = "a" * 40
TARGET = "windows/amd64"
RECEIPT = "install-receipt.tsv"
LEGACY_RECEIPT = ".midden-bootstrap-receipt.json"
LOCK = ".midden-install.lock"
PANDOC_ASSET = "pandoc-3.12-windows-x86_64.zip"
PANDOC_URL = "https://github.com/jgm/pandoc/releases/download/3.12/" + PANDOC_ASSET
PANDOC_TAKE = {"pandoc-3.12/pandoc.exe": "app/tools/pandoc.exe", "pandoc-3.12/COPYING.rtf": "app/tools/COPYING.rtf",
               "pandoc-3.12/COPYRIGHT.txt": "app/tools/COPYRIGHT.txt"}
START = Path("Microsoft") / "Windows" / "Start Menu" / "Programs" / "Midden.lnk"
SKILLS = ("midden-investigation", "midden-article", "midden-presentation", "midden-long-form")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def quote(value):
    return "'" + str(value).replace("'", "''") + "'"


def snapshot(root):
    return {
        path.relative_to(root).as_posix(): path.read_bytes() if path.is_file() else None
        for path in root.rglob("*")
    }


def files(root):
    return {name: data for name, data in snapshot(root).items() if data is not None}


def native_probe(root):
    source = root / "SyntheticBootstrapProbe.cs"
    output = root / "probe.exe"
    source.write_text(
        r'''using System;
using System.IO;
using System.Security.AccessControl;
using System.Security.Principal;
class SyntheticBootstrapProbe {
    public static int Main(string[] args) {
        string name = Path.GetFileName(Environment.GetCommandLineArgs()[0]);
        string log = Environment.GetEnvironmentVariable("MIDDEN_BOOTSTRAP_TEST_LOG");
        if (!String.IsNullOrEmpty(log))
            File.AppendAllText(log, name + "|" + String.Join(" ", args) + "\n");
        string version = Environment.GetEnvironmentVariable("MIDDEN_BOOTSTRAP_TEST_VERSION") ?? "0.4.0";
        if (name == "midden.exe" && args.Length == 1 && args[0] == "version") {
            if (Environment.GetEnvironmentVariable("MIDDEN_BOOTSTRAP_TEST_DENY_UI_EXECUTE") == "1") {
                string ui = Path.Combine(Path.GetDirectoryName(Environment.GetCommandLineArgs()[0]), "midden-ui.exe");
                FileSecurity access = File.GetAccessControl(ui);
                access.AddAccessRule(new FileSystemAccessRule(WindowsIdentity.GetCurrent().User,
                    FileSystemRights.ExecuteFile, AccessControlType.Deny));
                File.SetAccessControl(ui, access);
            }
            Console.WriteLine("midden " + version); return 0;
        }
        if (name == "midden-ui.exe" && args.Length == 1 && args[0] == "--version") {
            Console.WriteLine("midden-ui " + version); return 0;
        }
        if (name == "midden-ui.exe" && (args.Length == 0 ||
            (args.Length == 1 && args[0] == "--no-open"))) {
            Console.WriteLine("synthetic foreground app completed"); return 0;
        }
        Console.Error.WriteLine("Unexpected synthetic probe arguments");
        return 73;
    }
}
''',
        encoding="utf-8",
    )
    result = subprocess.run(
        [shutil.which("powershell"), "-NoProfile", "-NonInteractive", "-Command",
         f"Add-Type -TypeDefinition ([IO.File]::ReadAllText({quote(source)})) "
         f"-OutputAssembly {quote(output)} -OutputType ConsoleApplication"],
        capture_output=True, text=True, timeout=60,
    )
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return output.read_bytes()


TRANSPORT = r'''using System;
using System.IO;
using System.Net;
public class SyntheticReleaseTransport : IWebRequestCreate {
    public static string Root, Log, Repository, Version;
    public WebRequest Create(Uri uri) { return new SyntheticReleaseRequest(uri); }
}
public class SyntheticReleaseRequest : WebRequest {
    Uri uri;
    public SyntheticReleaseRequest(Uri value) { uri = value; }
    public override string Method { get; set; }
    public override int Timeout { get; set; }
    public override ICredentials Credentials { get; set; }
    public override bool UseDefaultCredentials { get; set; }
    public string UserAgent { get; set; }
    public bool AllowAutoRedirect { get; set; }
    public override WebResponse GetResponse() {
        if (Credentials != null || UseDefaultCredentials) throw new Exception("Credentials must not be used");
        File.AppendAllText(SyntheticReleaseTransport.Log, uri.AbsoluteUri + "\n");
        string api = "https://api.github.com/repos/" + SyntheticReleaseTransport.Repository + "/releases/latest";
        string prefix = "https://github.com/" + SyntheticReleaseTransport.Repository + "/releases/download/v" +
            SyntheticReleaseTransport.Version + "/";
        string pandoc = "https://github.com/jgm/pandoc/releases/download/3.12/";
        string name;
        if (uri.AbsoluteUri == api) name = "latest.json";
        else if (uri.AbsoluteUri.StartsWith(prefix, StringComparison.Ordinal))
            name = uri.AbsoluteUri.Substring(prefix.Length);
        else if (uri.AbsoluteUri.StartsWith(pandoc, StringComparison.Ordinal))
            name = uri.AbsoluteUri.Substring(pandoc.Length);
        else throw new WebException("Unexpected synthetic URL: " + uri);
        if (name != Path.GetFileName(name)) throw new WebException("Unsafe synthetic asset name");
        return new SyntheticReleaseResponse(uri, File.ReadAllBytes(Path.Combine(SyntheticReleaseTransport.Root, name)));
    }
}
public class SyntheticReleaseResponse : WebResponse {
    Uri uri; byte[] bytes;
    public SyntheticReleaseResponse(Uri value, byte[] data) { uri = value; bytes = data; }
    public HttpStatusCode StatusCode { get { return HttpStatusCode.OK; } }
    public override long ContentLength { get { return bytes.Length; } set { throw new NotSupportedException(); } }
    public override Uri ResponseUri { get { return uri; } }
    public override Stream GetResponseStream() { return new MemoryStream(bytes); }
    public override void Close() {}
}
public class SyntheticBootstrapEnvironment {
    public static string PathLog;
    public static bool FailPathOnce;
    public static OperatingSystem OSVersion { get { return System.Environment.OSVersion; } }
    public static string ExpandEnvironmentVariables(string value) { return System.Environment.ExpandEnvironmentVariables(value); }
    public static string GetEnvironmentVariable(string name) {
        if (!name.StartsWith("MIDDEN_") && name != "COMPA_HOME")
            throw new Exception("Unexpected installer environment lookup");
        return System.Environment.GetEnvironmentVariable(name);
    }
    public static string GetEnvironmentVariable(string name, EnvironmentVariableTarget target) {
        if (name != "Path" || target != EnvironmentVariableTarget.User)
            throw new Exception("Only user PATH may be read");
        if (!SyntheticRegistry.Exists) return null;
        return SyntheticRegistry.Kind == Microsoft.Win32.RegistryValueKind.ExpandString
            ? System.Environment.ExpandEnvironmentVariables(SyntheticRegistry.Value) : SyntheticRegistry.Value;
    }
    public static void SetEnvironmentVariable(string name, string value, EnvironmentVariableTarget target) {
        if (name != "Path" || target != EnvironmentVariableTarget.User)
            throw new Exception("Only user PATH may be changed");
        if (value == null) SyntheticRegistry.Delete();
        else SyntheticRegistry.Write(value, Microsoft.Win32.RegistryValueKind.String);
    }
}
public class SyntheticRegistry {
    public static bool Exists = true;
    public static string Value = @"C:\synthetic-existing-bin";
    public static Microsoft.Win32.RegistryValueKind Kind = Microsoft.Win32.RegistryValueKind.String;
    public static int Reads, Writes;
    public static int ChangeOnRead;
    public static string ConcurrentValue;
    public static SyntheticRegistryRoot CurrentUser = new SyntheticRegistryRoot();
    public static void Write(string value, Microsoft.Win32.RegistryValueKind kind) {
        Exists = true; Value = value; Kind = kind; Writes++;
        if (SyntheticBootstrapEnvironment.PathLog != null)
            File.AppendAllText(SyntheticBootstrapEnvironment.PathLog, value + "\n");
        if (SyntheticBootstrapEnvironment.FailPathOnce) {
            SyntheticBootstrapEnvironment.FailPathOnce = false;
            throw new Exception("Synthetic PATH publication failure");
        }
    }
    public static void Delete() {
        Exists = false; Value = null; Writes++;
        if (SyntheticBootstrapEnvironment.PathLog != null)
            File.AppendAllText(SyntheticBootstrapEnvironment.PathLog, "<absent>\n");
    }
}
public class SyntheticRegistryRoot {
    public SyntheticRegistryKey OpenSubKey(string name, bool writable) {
        if (name != "Environment") throw new Exception("Only the synthetic user Environment key is allowed");
        return new SyntheticRegistryKey();
    }
    public SyntheticRegistryKey CreateSubKey(string name) { return OpenSubKey(name, true); }
}
public class SyntheticRegistryKey : IDisposable {
    public string[] GetValueNames() { return SyntheticRegistry.Exists ? new [] { "Path" } : new string[0]; }
    public Microsoft.Win32.RegistryValueKind GetValueKind(string name) { Check(name); return SyntheticRegistry.Kind; }
    public object GetValue(string name, object fallback, Microsoft.Win32.RegistryValueOptions options) {
        Check(name);
        SyntheticRegistry.Reads++;
        if (SyntheticRegistry.Reads == SyntheticRegistry.ChangeOnRead) {
            SyntheticRegistry.Value = SyntheticRegistry.ConcurrentValue;
        }
        if (!SyntheticRegistry.Exists) return fallback;
        return options == Microsoft.Win32.RegistryValueOptions.DoNotExpandEnvironmentNames
            ? SyntheticRegistry.Value : System.Environment.ExpandEnvironmentVariables(SyntheticRegistry.Value);
    }
    public void SetValue(string name, object value, Microsoft.Win32.RegistryValueKind kind) {
        Check(name); SyntheticRegistry.Write((string)value, kind);
    }
    public void DeleteValue(string name, bool throwOnMissing) { Check(name); SyntheticRegistry.Delete(); }
    static void Check(string name) { if (name != "Path") throw new Exception("Only synthetic PATH is allowed"); }
    public void Dispose() {}
}
public class SyntheticEnvironmentNotification {
    public static int Calls, ReturnCode;
    public static bool Throw;
    public static string ConcurrentValue, LastSection;
    public static uint LastTimeout;
    public static System.Collections.Generic.List<string> Values = new System.Collections.Generic.List<string>();
    public static System.Collections.Generic.List<int> Writes = new System.Collections.Generic.List<int>();
    public static int Broadcast(string section, uint timeout) {
        Calls++; LastSection = section; LastTimeout = timeout;
        Values.Add(SyntheticRegistry.Value);
        Writes.Add(SyntheticRegistry.Writes);
        if (ConcurrentValue != null) SyntheticRegistry.Value = ConcurrentValue;
        if (Throw) throw new InvalidOperationException("Synthetic environment notification failure");
        return ReturnCode;
    }
}
'''


def receipt_records(install):
    return [line.split("\t") for line in (install / RECEIPT).read_text(encoding="utf-8").splitlines()]


@unittest.skipUnless(os.name == "nt", "install.ps1 installs Windows assets")
class PowerShellInstallerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not SHELLS or not shutil.which("powershell"):
            raise RuntimeError("Native Windows PowerShell is required for the probe fixture")
        cls.build = tempfile.TemporaryDirectory(prefix="midden-install-probe-")
        cls.addClassCleanup(cls.build.cleanup)
        cls.probe = native_probe(Path(cls.build.name))

    def setUp(self):
        parent = Path(tempfile.gettempdir()).resolve()
        self.assertNotEqual(parent, ROOT)
        self.assertNotIn(ROOT, parent.parents)
        self.temporary = tempfile.TemporaryDirectory(prefix="midden-install-test-", dir=parent)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.release = self.root / "reviewed release"
        self.release.mkdir()
        self.version = VERSION
        bundle = {"skills/midden-shared/LICENSE": b"Synthetic license.\n"}
        for skill in SKILLS:
            bundle[f"skills/{skill}/SKILL.md"] = (
                f"---\nname: {skill}\ndescription: Synthetic guidance.\n---\n"
                "[Sources](../midden-shared/sources.md)\n"
            ).encode()
        bundle.update({
            "skills/midden-shared/sources.md": b"Synthetic source guidance.\n",
            "skills/midden-shared/dependencies.md": b"Synthetic dependency guidance.\n",
            "skills/midden-shared/inspect_html.py": b"# Synthetic nonexecuted helper.\n",
            "skills/midden-shared/html-inspection.md": b"Synthetic inspection guidance.\n",
            "skills/midden-presentation/templates/html.yaml": b"to: dzslides\n",
            "skills/midden-long-form/templates/epub.yaml": b"to: epub3\n",
            "skills/midden-article/template.md": b"Synthetic article template.\n",
        })
        self.products = {
            "core": {"midden.exe": self.probe, "LICENSE": b"Synthetic license.\n",
                     "THIRD_PARTY_NOTICES.txt": b"Synthetic core notices.\n"},
            "bundle": bundle,
            "app": {"midden-ui.exe": self.probe, "app/LICENSE": b"Synthetic license.\n",
                    "app/NOTICE": b"Synthetic notice.\n", "app/THIRD_PARTY_NOTICES.txt": b"Synthetic app notices.\n"},
        }
        self.pandoc = {"pandoc-3.12/pandoc.exe": b"Synthetic Pandoc, never executed.\n",
                       "pandoc-3.12/COPYING.rtf": b"Synthetic GPL text.\n",
                       "pandoc-3.12/COPYRIGHT.txt": b"Synthetic copyright.\n",
                       "pandoc-3.12/MANUAL.html": b"Not installed.\n"}
        self.write_release()

    def archive_name(self, product):
        suffix = "" if product == "bundle" else "_windows_amd64"
        return f"midden-{product}_{self.version}{suffix}.zip"

    def write_archive(self, product):
        with zipfile.ZipFile(self.release / self.archive_name(product), "w", zipfile.ZIP_DEFLATED) as archive:
            for name, data in self.products[product].items():
                entry = zipfile.ZipInfo(name)
                entry.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(entry, data)

    def write_pandoc(self):
        with zipfile.ZipFile(self.release / PANDOC_ASSET, "w", zipfile.ZIP_DEFLATED) as archive:
            for name, data in self.pandoc.items():
                archive.writestr(name, data)

    def write_release(self):
        for product in self.products:
            self.write_archive(product)
        self.write_pandoc()
        self.build_manifest = {
            "version": self.version, "commit": COMMIT, "targets": [TARGET],
            "products": ["core", "bundle", "app"],
            "core_binaries": {TARGET: digest(self.products["core"]["midden.exe"])},
            "app_binaries": {TARGET: digest(self.products["app"]["midden-ui.exe"])},
            "archives": [self.archive_name(product) for product in self.products],
            "installers": ["install.ps1", "install.sh"],
        }
        self.write_metadata()

    def write_metadata(self, pandoc_hash=None):
        (self.release / "build-manifest.json").write_text(json.dumps(self.build_manifest), encoding="utf-8")
        lines = ["format\tmidden-release-v2", "version\t" + self.version, "commit\t" + COMMIT]
        for product, members in self.products.items():
            target = "universal" if product == "bundle" else TARGET
            lines.append(f"archive\t{product}\t{target}\t{self.archive_name(product)}")
            lines.extend(f"file\t{product}\t{target}\t{name}\t{digest(data)}" for name, data in members.items())
        lines.append(f"fetch\tpandoc\t{TARGET}\t{PANDOC_URL}\t{pandoc_hash or digest((self.release / PANDOC_ASSET).read_bytes())}")
        lines.extend(f"take\tpandoc\t{TARGET}\t{member}\t{destination}" for member, destination in PANDOC_TAKE.items())
        (self.release / "manifest.tsv").write_text("\n".join(lines) + "\n", encoding="utf-8")
        (self.release / "install.ps1").write_bytes(SCRIPT.read_bytes())
        (self.release / "install.sh").write_bytes(b"# synthetic Unix installer\n")
        self.seal()

    def seal(self):
        names = self.build_manifest["archives"] + ["install.ps1", "install.sh", "build-manifest.json", "manifest.tsv"]
        (self.release / "SHA256SUMS").write_text(
            "".join(digest((self.release / name).read_bytes()) + "  " + name + "\n" for name in sorted(names)),
            encoding="utf-8",
        )

    def installed(self, *products):
        expected = {RECEIPT: None}
        for product in products:
            expected.update(self.products[product])
        if "app" in products:
            expected.update({destination: self.pandoc[member] for member, destination in PANDOC_TAKE.items()})
        return expected

    def assert_installed(self, install, *products):
        actual = files(install)
        expected = self.installed(*products)
        self.assertEqual(set(actual), set(expected))
        for name, data in expected.items():
            if data is not None:
                self.assertEqual(actual[name], data, name)

    def context(self, shell):
        root = self.root / Path(shell).stem
        root.mkdir(exist_ok=True)
        home = root / "isolated home"
        home.mkdir(exist_ok=True)
        local = home / "AppData" / "Local"
        local.mkdir(parents=True, exist_ok=True)
        temporary = root / "external scratch"
        temporary.mkdir(exist_ok=True)
        project = root / "project with spaces"
        project.mkdir(exist_ok=True)
        env = dict(os.environ, HOME=str(home), USERPROFILE=str(home), LOCALAPPDATA=str(local),
                   APPDATA=str(home / "AppData" / "Roaming"), TMP=str(temporary), TEMP=str(temporary),
                   TMPDIR=str(temporary), MIDDEN_BOOTSTRAP_TEST_LOG=str(root / "probe.log"),
                   MIDDEN_BOOTSTRAP_TEST_VERSION=self.version)
        for name in ("MIDDEN_HOME", "COMPA_HOME", "MIDDEN_COPILOT_ROOT", "MIDDEN_CLAUDE_ROOT", "MIDDEN_OPENCODE_DB"):
            env.pop(name, None)
        return root, root / "install with spaces", project, env

    def start_entry(self, env):
        return Path(env["APPDATA"]) / START

    def shortcut_target(self, shell, path):
        result = subprocess.run(
            [shell, "-NoProfile", "-NonInteractive", "-Command",
             f"(New-Object -ComObject WScript.Shell).CreateShortcut({quote(path)}).TargetPath"],
            capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout.strip()

    def isolated_registry_script(self):
        text = SCRIPT.read_text(encoding="utf-8")
        text = text.replace("[Microsoft.Win32.Registry]::CurrentUser", "[SyntheticRegistry]::CurrentUser")
        text = text.replace("MiddenEnvironmentNotification", "SyntheticEnvironmentNotification")
        self.assertNotIn("[Microsoft.Win32.Registry]", text, "Unmocked registry access is forbidden")
        self.assertNotIn("MiddenEnvironmentNotification", text, "A real environment broadcast is forbidden")
        self.assertNotIn("[System.Environment]", text, "Environment writes must use the isolated test type")
        return text

    def registry_driver(self, shell, install, project, env, body, success=True):
        source = self.root / "SyntheticRawRegistry.cs"
        source.write_text(TRANSPORT, encoding="utf-8")
        isolated = self.root / "isolated-registry-installer.txt"
        isolated.write_text(self.isolated_registry_script(), encoding="utf-8")
        prefix = (
            "$ErrorActionPreference = 'Stop'; "
            f"Add-Type -TypeDefinition ([IO.File]::ReadAllText({quote(source)})) "
            "-IgnoreWarnings -WarningAction SilentlyContinue; "
            "$accelerators = [psobject].Assembly.GetType('System.Management.Automation.TypeAccelerators'); "
            "$accelerators::Add('Environment', [SyntheticBootstrapEnvironment]); "
            "& ([scriptblock]::Create('if ([Environment] -ne [SyntheticBootstrapEnvironment]) "
            "{ throw \"PATH isolation failed\" }')); "
            f"$bootstrap = [scriptblock]::Create([IO.File]::ReadAllText({quote(isolated)})); "
            f"$install = {quote(install)}; $project = {quote(project)}; $release = {quote(self.release)}; "
            "function RawState { @{ exists=[SyntheticRegistry]::Exists; "
            "kind=$(if ([SyntheticRegistry]::Exists) { [SyntheticRegistry]::Kind.ToString() } else { $null }); "
            "value=$(if ([SyntheticRegistry]::Exists) { [SyntheticRegistry]::Value } else { $null }) } }; "
        )
        result = subprocess.run(
            [shell, "-NoProfile", "-NonInteractive", "-Command", prefix + body],
            cwd=self.root, env=env, capture_output=True, text=True, encoding="utf-8",
            errors="replace", timeout=180,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def run_installer(self, shell, install, env, *extra, success=True, pipeline=False, launch=False, local=True):
        args = ["-InstallDir", str(install), "-NoPath"]
        if local:
            args += ["-DistributionDir", str(self.release)]
        if not launch:
            args.append("-NoLaunch")
        args += list(map(str, extra))
        if pipeline:
            # The script is evaluated from text, not dot-sourced or run beside its repository.
            tail = " ".join(arg if arg.startswith("-") else quote(arg) for arg in args)
            expression = ('("& {`n" + [IO.File]::ReadAllText(' + quote(SCRIPT)
                          + ') + "`n} " + ' + quote(tail) + ') | Invoke-Expression')
            command = [shell, "-NoProfile", "-NonInteractive", "-Command", expression]
        else:
            command = [shell, "-NoProfile", "-NonInteractive", "-File", str(SCRIPT), *args]
        result = subprocess.run(command, cwd=self.root, env=env, capture_output=True, text=True,
                                encoding="utf-8", errors="replace", timeout=180)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def test_app_install_verify_and_uninstall_keep_unowned_files_and_data(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                data = root / "application data"
                data.mkdir()
                (data / "retained.txt").write_text("Synthetic private state stays outside installation.\n")
                before_data = snapshot(data)
                result = self.run_installer(shell, install, env)
                self.assertIn("Installed Midden 0.4.0 (core, bundle, app)", result.stdout)
                self.assert_installed(install, "core", "bundle", "app")
                self.assertIn(["product", "app"], receipt_records(install))
                self.assertIn(["fetched", "pandoc", PANDOC_URL, digest((self.release / PANDOC_ASSET).read_bytes())],
                              receipt_records(install))
                start = self.start_entry(env)
                self.assertEqual(self.shortcut_target(shell, start), str(install / "midden-ui.exe"))
                log = Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines()
                self.assertEqual(set(log), {"midden.exe|version", "midden-ui.exe|--version"})
                note = install / "skills" / "midden-article" / "unowned.md"
                note.write_text("Keep this local note.\n")
                before = snapshot(install)
                result = self.run_installer(shell, install, env, "-Verify", local=False)
                self.assertIn("every owned file matches", result.stdout)
                self.assertEqual(snapshot(install), before)
                self.run_installer(shell, install, env, "-Uninstall", local=False)
                self.assertEqual({p.relative_to(install).as_posix() for p in install.rglob("*") if p.is_file()},
                                 {"skills/midden-article/unowned.md"})
                self.assertEqual({p.name for p in install.iterdir()}, {"skills"})
                self.assertEqual(note.read_text(), "Keep this local note.\n")
                self.assertFalse(start.exists())
                self.assertEqual(snapshot(data), before_data)
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_core_mode_installs_only_core_and_needs_no_python(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["PATH"] = str(Path(os.environ["SystemRoot"]) / "System32")
                env["PYTHON"] = "nonexistent-python-is-not-a-prerequisite"
                self.run_installer(shell, install, env, "-Mode", "core")
                self.assert_installed(install, "core")
                self.assertFalse(self.start_entry(env).exists())
                self.run_installer(shell, install, env, "-Verify", local=False)
                self.run_installer(shell, install, env, "-Uninstall", local=False)
                self.assertFalse(install.exists())

    def test_bundle_mode_copies_the_skills_unchanged_for_the_person_and_for_projects(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                home = Path(env["USERPROFILE"])
                env["PATH"] = str(Path(os.environ["SystemRoot"]) / "System32")
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "claude,agents")
                self.assert_installed(install, "core", "bundle")
                skills = {name[len("skills/"):]: data for name, data in self.products["bundle"].items()}
                for folder in (home / ".claude" / "skills", home / ".agents" / "skills"):
                    self.assertEqual(files(folder), skills)
                self.assertFalse(self.start_entry(env).exists())
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "copilot", "-Project", project)
                self.assertEqual(files(project / ".github" / "skills"), skills)
                places = [record for record in receipt_records(install) if record[0] == "place"]
                self.assertEqual([(r[2], r[3], r[4], r[6]) for r in places], [
                    ("skills", "claude", "user", str(home / ".claude" / "skills")),
                    ("skills", "agents", "user", str(home / ".agents" / "skills")),
                    ("skills", "copilot", "project", str(project / ".github" / "skills")),
                ])
                self.run_installer(shell, install, env, "-Verify", local=False)
                self.run_installer(shell, install, env, "-Uninstall", "-Harness", "claude", local=False)
                self.assertEqual(list((home / ".claude" / "skills").iterdir()), [])
                self.assertEqual(files(home / ".agents" / "skills"), skills)
                self.run_installer(shell, install, env, "-Uninstall", local=False)
                self.assertEqual(list((home / ".agents" / "skills").iterdir()), [])
                self.assertEqual(list((project / ".github" / "skills").iterdir()), [])
                self.assertFalse(install.exists())

    def next_release(self, version="0.4.1"):
        self.version = version
        self.products["core"]["midden.exe"] = self.probe + b"\nsynthetic second build\n"
        self.products["app"]["midden-ui.exe"] = self.probe + b"\nsynthetic second build\n"
        del self.products["bundle"]["skills/midden-article/template.md"]
        self.products["bundle"]["skills/midden-article/new.md"] = b"New synthetic guidance.\n"
        self.write_release()

    def test_products_add_up_and_a_newer_release_needs_upgrade(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                home = Path(env["USERPROFILE"])
                self.run_installer(shell, install, env, "-Mode", "core")
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "agents")
                self.assert_installed(install, "core", "bundle")
                self.run_installer(shell, install, env)
                self.assert_installed(install, "core", "bundle", "app")
                self.assertEqual([r[1] for r in receipt_records(install) if r[0] == "product"], ["core", "bundle", "app"])
                self.next_release()
                env["MIDDEN_BOOTSTRAP_TEST_VERSION"] = self.version
                before = snapshot(install)
                result = self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "claude", success=False)
                self.assertIn("-Upgrade", result.stderr)
                self.assertEqual(snapshot(install), before)
                self.run_installer(shell, install, env, "-Upgrade")
                self.assert_installed(install, "core", "bundle", "app")
                self.assertEqual((home / ".agents" / "skills" / "midden-article" / "new.md").read_bytes(),
                                 b"New synthetic guidance.\n")
                self.assertFalse((home / ".agents" / "skills" / "midden-article" / "template.md").exists())
                self.assertEqual(receipt_records(install)[1], ["version", "0.4.1"])
                self.run_installer(shell, install, env, "-Verify", local=False)
                self.run_installer(shell, install, env, "-Uninstall", local=False)
                self.assertFalse(install.exists())

    def test_upgrade_refreshes_the_persons_copies_keeps_edited_ones_and_lists_older_projects(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                home = Path(env["USERPROFILE"])
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "claude,agents")
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "copilot", "-Project", project)
                edited = home / ".agents" / "skills" / "midden-article" / "SKILL.md"
                edited.write_bytes(edited.read_bytes() + b"Local tuning.\n")
                old_project = files(project / ".github" / "skills")
                self.next_release()
                env["MIDDEN_BOOTSTRAP_TEST_VERSION"] = self.version
                result = self.run_installer(shell, install, env, "-Upgrade")
                self.assertTrue((home / ".claude" / "skills" / "midden-article" / "new.md").is_file())
                self.assertTrue(edited.read_bytes().endswith(b"Local tuning.\n"))
                self.assertTrue((home / ".agents" / "skills" / "midden-article" / "template.md").is_file())
                self.assertIn("Left the skills in " + str(home / ".agents" / "skills") + " at 0.4.0", result.stdout)
                self.assertEqual(files(project / ".github" / "skills"), old_project)
                self.assertIn(f"Older skills (0.4.0) in {project}; update them with: install.ps1 -Mode bundle "
                              f"-Harness copilot -Project '{project}'", result.stdout)
                self.assertEqual(sorted((r[3], r[5]) for r in receipt_records(install) if r[0] == "place"),
                                 [("agents", "0.4.0"), ("claude", "0.4.1"), ("copilot", "0.4.0")])
                result = self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "agents", success=False)
                self.assertIn("were changed since Midden installed them", result.stderr)
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "copilot", "-Project", project)
                self.assertTrue((project / ".github" / "skills" / "midden-article" / "new.md").is_file())
                result = self.run_installer(shell, install, env, "-Uninstall", local=False)
                self.assertIn("Kept " + str(edited), result.stdout)
                self.assertEqual(files(home / ".agents" / "skills"), {"midden-article/SKILL.md": edited.read_bytes()})
                self.assertFalse(install.exists())

    def test_dry_run_has_no_install_temp_harness_or_execution_side_effects(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                home = Path(env["USERPROFILE"])
                before = snapshot(self.release)
                plan = json.loads(self.run_installer(shell, install, env, "-DryRun").stdout)
                self.assertEqual((plan["operation"], plan["version"], plan["install_dir"]), ("install", VERSION, str(install)))
                self.assertTrue(plan["dry_run"])
                actions = {entry["path"]: entry["action"] for entry in plan["destinations"]}
                self.assertEqual(actions[str(install / "app" / "tools" / "pandoc.exe")], "fetch")
                self.assertEqual(actions[str(self.start_entry(env))], "create")
                self.assertEqual(actions[str(install / RECEIPT)], "create")
                plan = json.loads(self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "claude",
                                                     "-DryRun").stdout)
                self.assertEqual(plan["skills"], [str(home / ".claude" / "skills")])
                self.assertIn(str(home / ".claude" / "skills" / "midden-article" / "SKILL.md"),
                              {entry["path"] for entry in plan["destinations"]})
                self.assertFalse(install.exists())
                self.assertFalse((home / ".claude").exists())
                self.assertFalse(self.start_entry(env).exists())
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
                self.assertEqual(snapshot(self.release), before)

    def test_piped_iex_defaults_to_the_app_and_launches_it_with_no_open(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                result = self.run_installer(shell, install, env, "-NoOpen", pipeline=True, launch=True)
                self.assertIn("synthetic foreground app completed", result.stdout)
                self.assertEqual(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines()[-1],
                                 "midden-ui.exe|--no-open")
                result = self.run_installer(shell, install, env, "-NoOpen", launch=True)
                self.assertIn("synthetic foreground app completed", result.stdout, "Running it again opens the App")
                self.run_installer(shell, install, env, "-Uninstall", local=False)

    def test_modified_owned_file_blocks_verify_upgrade_and_uninstall(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                self.run_installer(shell, install, env)
                (install / "app" / "NOTICE").write_text("Keep a synthetic local edit.\n")
                before = snapshot(install)
                Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).unlink()
                for operation in ("-Verify", "-Upgrade", "-Uninstall"):
                    result = self.run_installer(shell, install, env, operation, success=False)
                    self.assertIn("modified" if operation != "-Verify" else "changed or removed", result.stderr)
                    self.assertEqual(snapshot(install), before)
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_unowned_collisions_and_a_pending_lock_block_installation_before_execution(self):
        for shell in FIRST:
            _, install, project, env = self.context(shell)
            install.mkdir()
            for name in ("midden.exe", LOCK):
                with self.subTest(shell=shell, name=name):
                    collision = install / name
                    collision.write_bytes(self.probe if name == "midden.exe" else b"pending\n")
                    before = snapshot(install)
                    self.run_installer(shell, install, env, success=False)
                    self.assertEqual(snapshot(install), before)
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                    collision.unlink()
            for collision in (Path(env["USERPROFILE"]) / ".claude" / "skills" / "midden-article" / "SKILL.md",
                              self.start_entry(env)):
                with self.subTest(shell=shell, collision=collision.name):
                    collision.parent.mkdir(parents=True, exist_ok=True)
                    collision.write_bytes(b"Somebody else's file.\n")
                    arguments = ("-Mode", "bundle", "-Harness", "claude") if collision.name == "SKILL.md" else ()
                    result = self.run_installer(shell, install, env, *arguments, success=False)
                    self.assertIn("does not own", result.stderr)
                    self.assertEqual(collision.read_bytes(), b"Somebody else's file.\n")
                    self.assertEqual(list(install.iterdir()), [])
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                    collision.unlink()

    def test_corrupt_metadata_archives_and_binary_hashes_never_execute(self):
        for name in ("build-manifest.json", "manifest.tsv", self.archive_name("app"), self.archive_name("bundle")):
            original = (self.release / name).read_bytes()
            (self.release / name).write_bytes(original + b"synthetic corruption")
            for shell in FIRST:
                with self.subTest(shell=shell, corruption=name):
                    _, install, project, env = self.context(shell)
                    result = self.run_installer(shell, install, env, success=False)
                    self.assertIn("checksum", result.stderr.lower())
                    self.assertFalse(install.exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
            (self.release / name).write_bytes(original)
        self.build_manifest["app_binaries"][TARGET] = "0" * 64
        self.write_metadata()
        for shell in FIRST:
            _, install, project, env = self.context(shell)
            self.run_installer(shell, install, env, success=False)
            self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_wrong_version_repository_options_and_architecture_are_refused(self):
        for shell in FIRST:
            _, install, project, env = self.context(shell)
            for options in (("-Version", "0.9.9"), ("-Repository", "owner/../other"), ("-Upgrade", "-Uninstall"),
                            ("-Mode", "bundle"), ("-Harness", "claude"), ("-Mode", "bundle", "-Harness", "cursor"),
                            ("-Mode", "bundle", "-Harness", "claude", "-Project", project / "missing"),
                            ("-Project", project), ("-Mode", "ui"), ("-Mode", "cli")):
                with self.subTest(shell=shell, options=options):
                    self.run_installer(shell, install, env, *options, success=False)
                    self.assertFalse(install.exists())
            foreign = dict(env, PROCESSOR_ARCHITECTURE="x86")
            foreign.pop("PROCESSOR_ARCHITEW6432", None)
            result = self.run_installer(shell, install, foreign, success=False)
            self.assertIn("architecture", result.stderr.lower())
            arm = dict(env, PROCESSOR_ARCHITECTURE="ARM64")
            arm.pop("PROCESSOR_ARCHITEW6432", None)
            self.run_installer(shell, install, arm, "-Mode", "core")
            self.assertIn(["target", "windows/amd64"], receipt_records(install), "Arm64 runs the x64 build")
            self.run_installer(shell, install, env, "-Uninstall", local=False)

    def test_publication_failures_restore_the_old_receipt_and_files(self):
        import ctypes
        from ctypes import wintypes

        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        open_file = kernel.CreateFileW
        open_file.argtypes = [
            wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID,
            wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE,
        ]
        open_file.restype = wintypes.HANDLE
        close = kernel.CloseHandle
        close.argtypes = [wintypes.HANDLE]
        close.restype = wintypes.BOOL
        contexts = [(shell, *self.context(shell)) for shell in SHELLS]
        for shell, _, install, project, env in contexts:
            self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "claude")
            self.run_installer(shell, install, env)
        self.products["app"]["app/NOTICE"] = b"New synthetic notice.\n"
        self.products["app"]["midden-ui.exe"] = self.probe + b"\nsecond synthetic build\n"
        del self.products["bundle"]["skills/midden-article/template.md"]
        self.write_release()
        for shell, _, install, project, env in contexts:
            home = Path(env["USERPROFILE"])
            before = snapshot(install)
            before_skills = snapshot(home / ".claude")
            handle = open_file(str(install / "app" / "NOTICE"), 0x80000000, 3, None, 3, 0x80, None)
            self.assertNotEqual(handle, wintypes.HANDLE(-1).value)
            try:
                for operation in ("-Upgrade", "-Uninstall"):
                    with self.subTest(shell=shell, operation=operation):
                        self.run_installer(shell, install, env, operation, success=False)
                        self.assertEqual(snapshot(install), before)
                        self.assertEqual(snapshot(home / ".claude"), before_skills)
                        self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
            finally:
                self.assertTrue(close(handle))
            self.run_installer(shell, install, env, "-Verify", local=False)

    def test_unsafe_roots_and_linked_distribution_or_temporary_targets_are_refused(self):
        for shell in FIRST:
            root, install, project, env = self.context(shell)
            for target in (Path(env["USERPROFILE"]), Path(install.anchor), Path(env["LOCALAPPDATA"]) / "Midden"):
                with self.subTest(shell=shell, target=target.name):
                    self.run_installer(shell, target, env, "-DryRun", success=False)
            target = root / "canonical target"
            target.mkdir()
            alias = root / "linked target"
            result = subprocess.run(
                [shell, "-NoProfile", "-NonInteractive", "-Command",
                 f"New-Item -ItemType Junction -Path {quote(alias)} -Target {quote(target)} | Out-Null"],
                capture_output=True, text=True, timeout=30,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            try:
                self.run_installer(shell, alias, env, success=False)
                result = self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "copilot",
                                            "-Project", alias, success=False)
                self.assertIn("linked/reparse", result.stderr)
                temp_env = dict(env, TEMP=str(alias), TMP=str(alias), TMPDIR=str(alias))
                self.run_installer(shell, target, temp_env, success=False)
                self.assertEqual(snapshot(target), {})
                self.run_installer(shell, install, temp_env)
                self.assertEqual(snapshot(target), {}, "External canonical scratch must be cleaned")
                self.run_installer(shell, install, temp_env, "-Uninstall", local=False)
            finally:
                alias.rmdir()

    def test_relative_inputs_use_the_powershell_location_without_changing_the_process_directory(self):
        for shell in FIRST:
            root, install, project, env = self.context(shell)
            process_directory = root / "process directory"
            location = root / "PowerShell location"
            process_directory.mkdir()
            location.mkdir()
            for parent in (process_directory, location):
                (parent / "relative project").mkdir()
            cases = (
                ("install", location, r".\relative install", str(self.release), str(project),
                 location / "relative install", project),
                ("distribution", self.release, str(install), ".", str(project), install, project),
                ("project", location, str(install), str(self.release), r".\relative project",
                 install, location / "relative project"),
            )
            for name, cwd, install_arg, release_arg, project_arg, expected_install, expected_project in cases:
                with self.subTest(shell=shell, input=name):
                    command = (
                        "$ErrorActionPreference = 'Stop'; "
                        f"Set-Location -LiteralPath {quote(cwd)}; "
                        f"[IO.Directory]::SetCurrentDirectory({quote(process_directory)}); "
                        "$before = [IO.Directory]::GetCurrentDirectory(); "
                        f"$output = & {quote(SCRIPT)} -InstallDir {quote(install_arg)} "
                        f"-DistributionDir {quote(release_arg)} -Mode bundle -Harness copilot -Project {quote(project_arg)} "
                        "-NoPath -NoLaunch -DryRun | Out-String; "
                        "@{ before = $before; after = [IO.Directory]::GetCurrentDirectory(); "
                        "plan = ($output | ConvertFrom-Json) } | ConvertTo-Json -Depth 15"
                    )
                    result = subprocess.run(
                        [shell, "-NoProfile", "-NonInteractive", "-Command", command],
                        cwd=process_directory, env=env, capture_output=True, text=True, timeout=60,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    report = json.loads(result.stdout)
                    self.assertEqual(report["before"], str(process_directory))
                    self.assertEqual(report["after"], report["before"])
                    self.assertEqual(report["plan"]["install_dir"], str(expected_install))
                    skills = report["plan"]["skills"]
                    self.assertEqual(skills if isinstance(skills, list) else [skills],
                                     [str(expected_project / ".github" / "skills")])
                    self.assertEqual(report["plan"]["version"], VERSION)
                    self.assertFalse(expected_install.exists())
                    self.assertFalse((process_directory / "relative install").exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
            with self.subTest(shell=shell, input="non-filesystem provider"):
                result = self.run_installer(shell, "HKCU:\\Software", env, "-DryRun", success=False)
                self.assertIn("filesystem", result.stderr.lower())
            with self.subTest(shell=shell, input="relative explicit data root"):
                state = location / "relative state"
                state.mkdir()
                command = (
                    "$ErrorActionPreference = 'Stop'; "
                    f"Set-Location -LiteralPath {quote(location)}; "
                    f"[IO.Directory]::SetCurrentDirectory({quote(process_directory)}); "
                    f"& {quote(SCRIPT)} -InstallDir {quote(state)} "
                    f"-DistributionDir {quote(self.release)} -NoPath -NoLaunch -DryRun"
                )
                result = subprocess.run(
                    [shell, "-NoProfile", "-NonInteractive", "-Command", command],
                    cwd=process_directory, env=dict(env, MIDDEN_HOME=r".\relative state"),
                    capture_output=True, text=True, timeout=60,
                )
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("data", result.stderr.lower())
                self.assertEqual(list(state.iterdir()), [])

    def test_the_install_root_cannot_overlap_core_app_or_kernel_data(self):
        for shell in FIRST:
            root, _, project, env = self.context(shell)
            home = Path(env["USERPROFILE"])
            default_core = home / ".midden"
            default_app = home / "AppData" / "Local" / "Midden"
            alternate_local = root / "alternate local data"
            explicit_core = root / "explicit core data"
            explicit_kernel = root / "explicit kernel data"
            data_roots = (default_core, default_app, alternate_local / "Midden", explicit_core, explicit_kernel)
            for path in data_roots:
                path.mkdir(parents=True, exist_ok=True)
                (path / "retained.txt").write_text("Synthetic retained data.\n")
            cases = (
                ("default core", default_core, env),
                ("default core child", default_core / "installation", env),
                ("default app", default_app, env),
                ("explicit core", explicit_core, dict(env, MIDDEN_HOME=str(explicit_core))),
                ("explicit app", alternate_local / "Midden", dict(env, LOCALAPPDATA=str(alternate_local))),
                ("default app with override", default_app, dict(env, LOCALAPPDATA=str(alternate_local))),
                ("explicit kernel", explicit_kernel, dict(env, COMPA_HOME=str(explicit_kernel))),
            )
            for name, install, case_env in cases:
                with self.subTest(shell=shell, data_root=name):
                    before = {path: snapshot(path) for path in data_roots}
                    result = self.run_installer(shell, install, case_env, "-DryRun", success=False)
                    self.assertIn("data", result.stderr.lower())
                    self.assertEqual({path: snapshot(path) for path in data_roots}, before)
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_default_data_junctions_cannot_hide_an_installation_overlap(self):
        for shell in FIRST:
            root, _, project, env = self.context(shell)
            target = root / "linked data"
            target.mkdir()
            (target / "retained.txt").write_text("Synthetic data behind a junction.\n")
            for link in (Path(env["USERPROFILE"]) / ".midden", Path(env["LOCALAPPDATA"]) / "Midden"):
                with self.subTest(shell=shell, data_root=link.name):
                    created = subprocess.run(
                        [shell, "-NoProfile", "-NonInteractive", "-Command",
                         f"New-Item -ItemType Junction -Path {quote(link)} -Target {quote(target)} | Out-Null"],
                        capture_output=True, text=True, timeout=30,
                    )
                    self.assertEqual(created.returncode, 0, created.stdout + created.stderr)
                    try:
                        before = snapshot(target)
                        result = self.run_installer(shell, target, env, "-DryRun", success=False)
                        self.assertIn("linked/reparse", result.stderr)
                        self.assertEqual(snapshot(target), before)
                        self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                    finally:
                        link.rmdir()

    def test_archive_size_limits_are_checked_before_extracting(self):
        archive = self.release / self.archive_name("app")
        original = archive.read_bytes()
        changed = bytearray(original)
        central = changed.index(b"PK\x01\x02")
        struct.pack_into("<I", changed, central + 24, 256 * 1024 * 1024 + 1)
        archive.write_bytes(changed)
        self.seal()
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                result = self.run_installer(shell, install, env, success=False)
                self.assertIn("size limit", result.stderr.lower())
                self.assertFalse(install.exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
        archive.write_bytes(original)
        self.seal()
        (self.release / "SHA256SUMS").write_bytes(b"x" * (1024 * 1024 + 1))
        for shell in FIRST:
            _, install, project, env = self.context(shell)
            result = self.run_installer(shell, install, env, success=False)
            self.assertIn("size limit", result.stderr.lower())

    def test_remote_latest_and_pinned_fork_download_only_the_needed_assets_without_credentials(self):
        transport_source = self.root / "SyntheticReleaseTransport.cs"
        transport_source.write_text(TRANSPORT, encoding="utf-8")
        (self.release / "latest.json").write_text(json.dumps({"tag_name": "v" + VERSION}), encoding="utf-8")
        for shell in FIRST:
            for repository, pinned, mode in (("xibodev/midden", False, "app"), ("example/synthetic-fork", True, "core")):
                with self.subTest(shell=shell, repository=repository, pinned=pinned):
                    root, install, project, env = self.context(shell)
                    log = root / "transport.log"
                    log.unlink(missing_ok=True)
                    prefix = (
                        "$ErrorActionPreference = 'Stop'; "
                        f"Add-Type -TypeDefinition ([IO.File]::ReadAllText({quote(transport_source)})) "
                        "-IgnoreWarnings -WarningAction SilentlyContinue; "
                        f"[SyntheticReleaseTransport]::Root = {quote(self.release)}; "
                        f"[SyntheticReleaseTransport]::Log = {quote(log)}; "
                        f"[SyntheticReleaseTransport]::Repository = {quote(repository)}; "
                        f"[SyntheticReleaseTransport]::Version = {quote(VERSION)}; "
                        "if (-not [Net.WebRequest]::RegisterPrefix('https://', (New-Object SyntheticReleaseTransport))) "
                        "{ throw 'Offline test transport registration failed' }; "
                    )
                    args = (f"-Mode {mode} -InstallDir {quote(install)} -Repository {quote(repository)} -NoPath -NoLaunch"
                            + (f" -Version {VERSION}" if pinned else ""))
                    result = subprocess.run(
                        [shell, "-NoProfile", "-NonInteractive", "-Command", prefix + f"& {quote(SCRIPT)} {args}"],
                        cwd=self.root, env=env, capture_output=True, text=True, timeout=120,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    base = f"https://github.com/{repository}/releases/download/v{VERSION}/"
                    expected = ([] if pinned else [f"https://api.github.com/repos/{repository}/releases/latest"])
                    expected += [base + name for name in ("SHA256SUMS", "build-manifest.json", "manifest.tsv")]
                    products = ("core", "bundle", "app") if mode == "app" else ("core",)
                    expected += [base + self.archive_name(product) for product in products]
                    if mode == "app":
                        expected.append(PANDOC_URL)
                    self.assertEqual(log.read_text().splitlines(), expected)
                    self.run_installer(shell, install, env, "-Uninstall", local=False)

    def test_literal_irm_iex_installs_and_launches_the_app_without_adjacent_files(self):
        source = self.root / "SyntheticDefaultTransport.cs"
        source.write_text(TRANSPORT, encoding="utf-8")
        (self.release / "latest.json").write_text(json.dumps({"tag_name": "v" + VERSION}), encoding="utf-8")
        script_bytes = self.isolated_registry_script().encode("utf-8")
        isolated = self.root / "literal-registry-installer.txt"
        isolated.write_bytes(script_bytes)

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path != "/install.ps1":
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/plain; charset=utf-8")
                self.send_header("Content-Length", str(len(script_bytes)))
                self.end_headers()
                self.wfile.write(script_bytes)

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            for shell in SHELLS:
                with self.subTest(shell=shell):
                    root, _, _, env = self.context(shell)
                    install = Path(env["LOCALAPPDATA"]) / "Programs" / "Midden"
                    network_log, path_log = root / "default-downloads.log", root / "isolated-path.log"
                    prefix = (
                        "$ErrorActionPreference = 'Stop'; "
                        f"Add-Type -TypeDefinition ([IO.File]::ReadAllText({quote(source)})) "
                        "-IgnoreWarnings -WarningAction SilentlyContinue; "
                        f"[SyntheticReleaseTransport]::Root = {quote(self.release)}; "
                        f"[SyntheticReleaseTransport]::Log = {quote(network_log)}; "
                        "[SyntheticReleaseTransport]::Repository = 'xibodev/midden'; "
                        f"[SyntheticReleaseTransport]::Version = {quote(VERSION)}; "
                        "if (-not [Net.WebRequest]::RegisterPrefix('https://', (New-Object SyntheticReleaseTransport))) "
                        "{ throw 'Offline transport failed' }; "
                        f"[SyntheticBootstrapEnvironment]::PathLog = {quote(path_log)}; "
                        "$accelerators = [psobject].Assembly.GetType('System.Management.Automation.TypeAccelerators'); "
                        "$accelerators::Add('Environment', [SyntheticBootstrapEnvironment]); "
                        "& ([scriptblock]::Create('if ([Environment] -ne [SyntheticBootstrapEnvironment]) "
                        "{ throw \"PATH isolation failed\" }')); "
                        f"$bootstrap = [scriptblock]::Create([IO.File]::ReadAllText({quote(isolated)})); "
                    )
                    result = subprocess.run(
                        [shell, "-NoProfile", "-NonInteractive", "-Command", prefix
                         + f"irm 'http://127.0.0.1:{server.server_port}/install.ps1' | iex; "
                         + "& $bootstrap -Uninstall"],
                        cwd=root, env=env, capture_output=True, text=True, timeout=120,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn("synthetic foreground app completed", result.stdout)
                    self.assertEqual(path_log.read_text().splitlines(),
                                     [r"C:\synthetic-existing-bin;" + str(install), r"C:\synthetic-existing-bin"])
                    self.assertEqual(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines()[-1], "midden-ui.exe|")
                    self.assertFalse(install.exists())
                    self.assertFalse(self.start_entry(env).exists())
                    self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=10)

    def test_a_native_version_mismatch_leaves_no_installation_or_probe_scratch(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["MIDDEN_BOOTSTRAP_TEST_VERSION"] = "0.9.9"
                result = self.run_installer(shell, install, env, success=False)
                self.assertIn("version", result.stderr.lower())
                self.assertFalse(install.exists())
                self.assertFalse(self.start_entry(env).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_a_denied_native_probe_names_the_operation_without_installing_or_falling_back(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["MIDDEN_BOOTSTRAP_TEST_DENY_UI_EXECUTE"] = "1"
                before_release = snapshot(self.release)
                result = self.run_installer(shell, install, env, success=False)
                for text in ("Process.Start", "midden-ui.exe", "--version", "Win32=5", "application-control"):
                    self.assertIn(text, result.stderr)
                self.assertFalse(install.exists())
                self.assertEqual(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines(), ["midden.exe|version"])
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
                self.assertEqual(snapshot(self.release), before_release)

    def test_unsafe_or_misplaced_archive_members_are_refused_even_when_checksums_match(self):
        original = dict(self.products["app"])
        for name in ("../escape.txt", "C:/escape.txt", "app\\escape.txt", "app/NUL", "app/NOTICE:stream",
                     "APP/new.md", "app/NOTICE/child", "app/tools/pandoc.exe", "midden.exe", "skills/midden-article/x.md"):
            self.products["app"] = {**original, name: b"Synthetic unsafe member.\n"}
            self.write_release()
            for shell in FIRST:
                with self.subTest(shell=shell, member=name):
                    _, install, project, env = self.context(shell)
                    self.run_installer(shell, install, env, success=False)
                    self.assertFalse(install.exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
        self.products["app"] = original
        for kind in ("duplicate", "symlink", "directory"):
            self.write_release()
            with zipfile.ZipFile(self.release / self.archive_name("app"), "a") as archive:
                if kind == "duplicate":
                    with warnings.catch_warnings():
                        warnings.simplefilter("ignore", UserWarning)
                        archive.writestr("app/LICENSE", b"Synthetic duplicate.\n")
                else:
                    entry = zipfile.ZipInfo("app/link" if kind == "symlink" else "app/directory/")
                    entry.create_system = 3
                    entry.external_attr = ((stat.S_IFLNK if kind == "symlink" else stat.S_IFDIR) | 0o755) << 16
                    archive.writestr(entry, b"../outside")
            self.seal()
            for shell in FIRST:
                with self.subTest(shell=shell, kind=kind):
                    _, install, project, env = self.context(shell)
                    self.run_installer(shell, install, env, success=False)
                    self.assertFalse(install.exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    @staticmethod
    def path_line(which, state):
        if state["exists"]:
            return f"path\t{which}\t1\t{state['kind']}\t{state['value']}"
        return f"path\t{which}\t0\t-\t"

    def test_raw_path_keeps_tokens_kind_and_absent_vs_empty_across_profiles(self):
        states = (
            {"exists": True, "kind": "ExpandString", "value": r"%USERPROFILE%\tools;%LOCALAPPDATA%\apps;;C:\Shared;"},
            {"exists": True, "kind": "String", "value": r"%USERPROFILE%\literal;C:\Shared;;"},
            {"exists": True, "kind": "String", "value": ""},
            {"exists": True, "kind": "ExpandString", "value": ""},
            {"exists": False, "kind": None, "value": None},
        )
        for shell in SHELLS:
            root, install, project, env = self.context(shell)
            other = root / "different profile"
            other.mkdir()
            for index, state in enumerate(states):
                with self.subTest(shell=shell, state=state):
                    report = root / f"raw-path-{index}.json"
                    body = (
                        f"[SyntheticRegistry]::Exists = ${str(state['exists']).lower()}; "
                        f"[SyntheticRegistry]::Kind = {quote(state['kind'] or 'String')}; "
                        f"[SyntheticRegistry]::Value = {quote(state['value']) if state['value'] is not None else '$null'}; "
                        "$before = RawState; "
                        "& $bootstrap -Mode core -DistributionDir $release -InstallDir $install -NoLaunch; "
                        f"$receipt = @([IO.File]::ReadAllLines((Join-Path $install {quote(RECEIPT)})) | Where-Object {{ $_.StartsWith('path') }}); "
                        "$installed = RawState; "
                        f"$env:USERPROFILE = {quote(other)}; "
                        "& $bootstrap -InstallDir $install -NoLaunch -Uninstall; "
                        f"[IO.File]::WriteAllText({quote(report)}, "
                        "(@{ before=$before; installed=$installed; restored=(RawState); ownership=$receipt } "
                        "| ConvertTo-Json -Depth 10))"
                    )
                    self.registry_driver(shell, install, project, env, body)
                    actual = json.loads(report.read_text(encoding="utf-8-sig"))
                    expected_after = {
                        "exists": True, "kind": state["kind"] if state["exists"] else "String",
                        "value": (state["value"] + ";" if state["value"] else "") + str(install),
                    }
                    self.assertEqual(actual["before"], state)
                    self.assertEqual(actual["installed"], expected_after)
                    self.assertEqual(actual["restored"], state)
                    self.assertEqual(actual["ownership"], [self.path_line("before", state),
                                                           self.path_line("after", expected_after)])
                    self.assertFalse(install.exists())

    def test_raw_path_refuses_an_unsupported_kind_before_extraction_or_execution(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                result = self.registry_driver(
                    shell, install, project, env,
                    "[SyntheticRegistry]::Kind = 'DWord'; "
                    "& $bootstrap -DistributionDir $release -InstallDir $install -NoLaunch",
                    success=False,
                )
                self.assertIn("kind", result.stderr.lower())
                self.assertFalse(install.exists())
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_raw_path_edited_kind_or_text_is_kept_on_uninstall(self):
        for shell in FIRST:
            for edit in ("kind", "text"):
                with self.subTest(shell=shell, edit=edit):
                    root, install, project, env = self.context(shell)
                    report = root / ("concurrent-" + edit + ".json")
                    mutation = ("[SyntheticRegistry]::Kind = 'String'; " if edit == "kind"
                                else "[SyntheticRegistry]::Value += ';C:\\synthetic-user-addition'; ")
                    body = (
                        "[SyntheticRegistry]::Kind = 'ExpandString'; "
                        "[SyntheticRegistry]::Value = '%USERPROFILE%\\tools'; "
                        "& $bootstrap -Mode core -DistributionDir $release -InstallDir $install -NoLaunch; "
                        + mutation
                        + "$edited = RawState; $writes = [SyntheticRegistry]::Writes; "
                        "$notifications = [SyntheticEnvironmentNotification]::Calls; "
                        "& $bootstrap -InstallDir $install -Uninstall; "
                        f"[IO.File]::WriteAllText({quote(report)}, (@{{ edited=$edited; final=(RawState); "
                        "writes=([SyntheticRegistry]::Writes - $writes); "
                        "notifications=([SyntheticEnvironmentNotification]::Calls - $notifications) } | ConvertTo-Json -Depth 10))"
                    )
                    result = self.registry_driver(shell, install, project, env, body)
                    self.assertIn("PATH was changed since installation", result.stdout)
                    actual = json.loads(report.read_text(encoding="utf-8-sig"))
                    self.assertEqual(actual["final"], actual["edited"])
                    self.assertEqual(actual["writes"], 0)
                    self.assertEqual(actual["notifications"], 0)
                    self.assertFalse(install.exists())

    def test_raw_path_failed_publication_restores_the_exact_state_or_keeps_a_concurrent_edit(self):
        for shell in SHELLS:
            for scenario in ("rollback", "concurrent"):
                with self.subTest(shell=shell, scenario=scenario):
                    root, install, project, env = self.context(shell)
                    self.run_installer(shell, install, env)
                    before_files = snapshot(install)
                    report = root / (scenario + "-raw-path.json")
                    trigger = (
                        "[SyntheticBootstrapEnvironment]::FailPathOnce = $true; "
                        "[SyntheticEnvironmentNotification]::ReturnCode = 1460; " if scenario == "rollback"
                        else "[SyntheticRegistry]::ChangeOnRead = 2; "
                             "[SyntheticRegistry]::ConcurrentValue = '%USERPROFILE%\\tools;;C:\\concurrent-user-entry'; "
                    )
                    body = (
                        "[SyntheticRegistry]::Kind = 'ExpandString'; "
                        "[SyntheticRegistry]::Value = '%USERPROFILE%\\tools;;'; "
                        "$before = RawState; $message = $null; "
                        + trigger
                        + "try { & $bootstrap -DistributionDir $release -InstallDir $install -Upgrade -NoLaunch } "
                        "catch { $message = $_.Exception.Message }; "
                        f"[IO.File]::WriteAllText({quote(report)}, (@{{ before=$before; final=(RawState); "
                        "writes=[SyntheticRegistry]::Writes; error=$message; "
                        "notifications=[SyntheticEnvironmentNotification]::Calls; "
                        "notified_values=@([SyntheticEnvironmentNotification]::Values) } | ConvertTo-Json -Depth 10))"
                    )
                    result = self.registry_driver(shell, install, project, env, body)
                    actual = json.loads(report.read_text(encoding="utf-8-sig"))
                    self.assertTrue(actual["error"], "The publication must actually fail")
                    if scenario == "rollback":
                        self.assertIn("Synthetic PATH publication", actual["error"])
                        self.assertIn("WM_SETTINGCHANGE(Environment)", result.stdout)
                        self.assertIn("rollback restore", result.stdout)
                        self.assertEqual(actual["final"], actual["before"])
                        self.assertEqual(actual["writes"], 2)
                        self.assertEqual(actual["notifications"], 1)
                        self.assertEqual(actual["notified_values"], [actual["before"]["value"]])
                    else:
                        self.assertEqual(actual["final"], {
                            "exists": True, "kind": "ExpandString",
                            "value": r"%USERPROFILE%\tools;;C:\concurrent-user-entry",
                        })
                        self.assertEqual(actual["writes"], 0)
                        self.assertEqual(actual["notifications"], 0)
                    self.assertEqual(snapshot(install), before_files)
                    self.run_installer(shell, install, env, "-Uninstall", local=False)

    def test_raw_path_notifications_follow_commits_and_restores_only(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                report = root / "environment-notifications.json"
                body = (
                    "$before = RawState; $counts = @(); "
                    "$options = @{ DistributionDir=$release; InstallDir=$install; NoLaunch=$true; Verbose=$true }; "
                    "& $bootstrap @options -Mode core -DryRun | Out-Null; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -Mode core -NoPath; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -NoPath -Uninstall; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -Mode core; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -Verify; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -Upgrade; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -Uninstall; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    f"[IO.File]::WriteAllText({quote(report)}, (@{{ before=$before; final=(RawState); counts=$counts; "
                    "section=[SyntheticEnvironmentNotification]::LastSection; "
                    "timeout=[SyntheticEnvironmentNotification]::LastTimeout; "
                    "writes_at_notification=@([SyntheticEnvironmentNotification]::Writes) } | ConvertTo-Json -Depth 10))"
                )
                result = self.registry_driver(shell, install, project, env, body)
                actual = json.loads(report.read_text(encoding="utf-8-sig"))
                self.assertEqual(actual["counts"], [0, 0, 0, 1, 1, 1, 2])
                self.assertEqual(actual["section"], "Environment")
                self.assertEqual(actual["timeout"], 5000)
                self.assertEqual(actual["writes_at_notification"], [1, 2])
                self.assertEqual((result.stdout + result.stderr).count("WM_SETTINGCHANGE(Environment) completed"), 2)
                self.assertEqual(actual["final"], actual["before"])
                self.assertFalse(install.exists())

    def test_raw_path_notification_failure_warns_without_rollback_or_clobbering_new_edits(self):
        for shell in FIRST:
            for failure in ("timeout", "exception"):
                with self.subTest(shell=shell, failure=failure):
                    root, install, project, env = self.context(shell)
                    report = root / ("notification-" + failure + ".json")
                    condition = ("[SyntheticEnvironmentNotification]::ReturnCode = 1460; "
                                 if failure == "timeout" else "[SyntheticEnvironmentNotification]::Throw = $true; ")
                    body = (
                        condition
                        + "[SyntheticEnvironmentNotification]::ConcurrentValue = '%USERPROFILE%\\tools;C:\\synthetic-concurrent-edit'; "
                        "& $bootstrap -Mode core -DistributionDir $release -InstallDir $install "
                        "-NoLaunch -WarningAction Stop; "
                        f"$receipt = [IO.File]::ReadAllLines((Join-Path $install {quote(RECEIPT)})); "
                        f"[IO.File]::WriteAllText({quote(report)}, (@{{ final=(RawState); receipt=$receipt; "
                        "notifications=[SyntheticEnvironmentNotification]::Calls; writes=[SyntheticRegistry]::Writes } "
                        "| ConvertTo-Json -Depth 15)); "
                        "& $bootstrap -InstallDir $install -NoPath -Uninstall"
                    )
                    result = self.registry_driver(shell, install, project, env, body)
                    self.assertIn("WM_SETTINGCHANGE(Environment)", result.stdout)
                    self.assertIn("not rolled back", result.stdout.lower())
                    actual = json.loads(report.read_text(encoding="utf-8-sig"))
                    self.assertEqual(actual["final"]["value"], r"%USERPROFILE%\tools;C:\synthetic-concurrent-edit")
                    self.assertEqual(actual["writes"], 1)
                    self.assertEqual(actual["notifications"], 1)
                    self.assertIn("product\tcore", actual["receipt"])
                    self.assertFalse((install / RECEIPT).exists())

    def test_raw_path_dry_run_is_write_free(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                report = root / "path-plan.json"
                body = (
                    "[SyntheticRegistry]::Kind = 'ExpandString'; "
                    "[SyntheticRegistry]::Value = '%USERPROFILE%\\tools;;'; "
                    "$plan = & $bootstrap -DistributionDir $release -InstallDir $install -NoLaunch -DryRun | Out-String; "
                    f"[IO.File]::WriteAllText({quote(report)}, (@{{ plan=($plan | ConvertFrom-Json); "
                    "raw=(RawState); writes=[SyntheticRegistry]::Writes } | ConvertTo-Json -Depth 15))"
                )
                self.registry_driver(shell, install, project, env, body)
                actual = json.loads(report.read_text(encoding="utf-8-sig"))
                self.assertEqual(actual["writes"], 0)
                self.assertEqual(actual["plan"]["path_action"], "user PATH only; never the machine PATH")
                self.assertEqual(actual["raw"]["value"], r"%USERPROFILE%\tools;;")
                self.assertFalse(install.exists())
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_a_tampered_path_record_blocks_verify_upgrade_and_uninstall(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                body = "& $bootstrap -Mode core -DistributionDir $release -InstallDir $install -NoLaunch"
                self.registry_driver(shell, install, project, env, body)
                original = (install / RECEIPT).read_text(encoding="utf-8")
                for tampered, message in (
                    ("\n".join(line if not line.startswith("path\tafter") else line + r";C:\more"
                               for line in original.split("\n")), "does not describe adding"),
                    ("\n".join(line for line in original.split("\n") if not line.startswith("path\tbefore")),
                     "incomplete"),
                ):
                    (install / RECEIPT).write_text(tampered, encoding="utf-8", newline="\n")
                    before = snapshot(install)
                    for operation in ("-Verify", "-Upgrade", "-Uninstall"):
                        with self.subTest(operation=operation, message=message):
                            result = self.run_installer(shell, install, env, operation, success=False)
                            self.assertIn(message, result.stderr)
                            self.assertEqual(snapshot(install), before)
                (install / RECEIPT).write_text(original, encoding="utf-8", newline="\n")
                self.registry_driver(shell, install, project, env, "& $bootstrap -InstallDir $install -Uninstall")

    def legacy_install(self, install, mode="ui", path_change=None):
        """An installation made by the 0.3 installer, as it laid out files and its receipt."""
        legacy = {"midden.exe": self.probe, "LICENSE": b"Synthetic license.\n",
                  "THIRD_PARTY_NOTICES.txt": b"Old notices.\n"}
        if mode == "ui":
            legacy.update({"midden-ui.exe": self.probe, "NOTICE": b"Old notice.\n", "start.ps1": b"# Old launcher.\n",
                           "start.sh": b"# Old launcher.\n", "package-manifest.json": b"{}\n",
                           "bundles/article/SKILL.md": b"Old article guidance.\n",
                           "bundles/midden-shared/tools.md": b"Old tools guidance.\n"})
        for name, data in legacy.items():
            path = install / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        receipt = {"schema": "midden.bootstrap/v1", "mode": mode, "version": "0.3.1", "commit": "b" * 40,
                   "repository": "xibodev/midden", "install_dir": str(install),
                   "files": {name: digest(data) for name, data in legacy.items()}, "path_change": path_change}
        (install / LEGACY_RECEIPT).write_text(json.dumps(receipt), encoding="utf-8")
        return legacy

    def test_upgrade_replaces_an_installation_made_by_the_0_3_installer(self):
        for shell in FIRST:
            for mode in ("ui", "core"):
                with self.subTest(shell=shell, mode=mode):
                    _, install, project, env = self.context(shell)
                    if install.exists():
                        shutil.rmtree(install)
                    self.legacy_install(install, mode)
                    (install / "unowned.txt").write_bytes(b"Keep me.\n")
                    before = snapshot(install)
                    result = self.run_installer(shell, install, env, success=False)
                    self.assertIn("-Upgrade", result.stderr)
                    self.assertEqual(snapshot(install), before)
                    result = self.run_installer(shell, install, env, "-Verify", local=False)
                    self.assertIn("0.3 installer", result.stdout)
                    self.run_installer(shell, install, env, "-Upgrade")
                    products = ("core", "bundle", "app") if mode == "ui" else ("core",)
                    actual = files(install)
                    self.assertEqual(actual.pop("unowned.txt"), b"Keep me.\n")
                    self.assertEqual(set(actual), set(self.installed(*products)))
                    self.assertFalse((install / LEGACY_RECEIPT).exists())
                    self.assertFalse((install / "bundles").exists())
                    self.run_installer(shell, install, env, "-Verify", local=False)
                    self.run_installer(shell, install, env, "-Uninstall", local=False)
                    self.assertEqual({p.name for p in install.iterdir()}, {"unowned.txt"})

    def test_upgrade_carries_the_0_3_path_change_and_uninstall_restores_it(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                before = {"exists": True, "kind": "String", "value": r"C:\synthetic-existing-bin"}
                after = {"exists": True, "kind": "String", "value": r"C:\synthetic-existing-bin;" + str(install)}
                self.legacy_install(install, "core", {"schema": "midden.user-path/v1", "before": before, "after": after})
                report = root / "legacy-path.json"
                body = (
                    f"[SyntheticRegistry]::Value = {quote(after['value'])}; "
                    "& $bootstrap -DistributionDir $release -InstallDir $install -Upgrade -NoLaunch; "
                    f"$receipt = @([IO.File]::ReadAllLines((Join-Path $install {quote(RECEIPT)})) | Where-Object {{ $_.StartsWith('path') }}); "
                    "& $bootstrap -InstallDir $install -Uninstall; "
                    f"[IO.File]::WriteAllText({quote(report)}, (@{{ receipt=$receipt; final=(RawState) }} | ConvertTo-Json -Depth 10))"
                )
                self.registry_driver(shell, install, project, env, body)
                actual = json.loads(report.read_text(encoding="utf-8-sig"))
                self.assertEqual(actual["receipt"], [self.path_line("before", before), self.path_line("after", after)])
                self.assertEqual(actual["final"], before)
                self.assertFalse(install.exists())

    def test_a_0_3_cli_installation_is_refused_with_instructions(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                install.mkdir()
                (install / "midden-install-receipt.json").write_text("{}", encoding="utf-8")
                for operation in ((), ("-Upgrade",), ("-Uninstall",)):
                    result = self.run_installer(shell, install, env, *operation, success=False)
                    self.assertIn("0.3 installer", result.stderr)
                self.assertEqual({p.name for p in install.iterdir()}, {"midden-install-receipt.json"})

    def test_pandoc_must_match_its_pin_and_is_needed_only_by_the_app(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                original = (self.release / PANDOC_ASSET).read_bytes()
                (self.release / PANDOC_ASSET).write_bytes(original + b"tampered")
                result = self.run_installer(shell, install, env, success=False)
                self.assertIn("Pandoc archive checksum mismatch", result.stderr)
                self.assertFalse(install.exists())
                self.assertFalse(self.start_entry(env).exists())
                (self.release / PANDOC_ASSET).unlink()
                result = self.run_installer(shell, install, env, success=False)
                self.assertIn(f"needs {PANDOC_ASSET}", result.stderr)
                self.assertIn(PANDOC_URL, result.stderr)
                self.run_installer(shell, install, env, "-Mode", "bundle", "-Harness", "claude")
                (self.release / PANDOC_ASSET).write_bytes(original)
                self.run_installer(shell, install, env)
                self.assert_installed(install, "core", "bundle", "app")
                (self.release / PANDOC_ASSET).unlink()
                result = self.run_installer(shell, install, env, "-Upgrade")
                self.assertEqual((install / "app" / "tools" / "pandoc.exe").read_bytes(), self.pandoc["pandoc-3.12/pandoc.exe"])
                self.run_installer(shell, install, env, "-Uninstall", local=False)
                (self.release / PANDOC_ASSET).write_bytes(original)
                del self.pandoc["pandoc-3.12/COPYRIGHT.txt"]
                self.write_pandoc()
                self.write_metadata()
                result = self.run_installer(shell, install, env, success=False)
                self.assertIn("Pandoc archive lacks pandoc-3.12/COPYRIGHT.txt", result.stderr)
                self.assertFalse(install.exists())

    def test_a_start_entry_the_person_changed_is_kept(self):
        for shell in FIRST:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                self.run_installer(shell, install, env)
                start = self.start_entry(env)
                retarget = subprocess.run(
                    [shell, "-NoProfile", "-NonInteractive", "-Command",
                     f"$link = (New-Object -ComObject WScript.Shell).CreateShortcut({quote(start)}); "
                     "$link.TargetPath = $env:SystemRoot + '\\notepad.exe'; $link.Save()"],
                    capture_output=True, text=True, timeout=30,
                )
                self.assertEqual(retarget.returncode, 0, retarget.stdout + retarget.stderr)
                result = self.run_installer(shell, install, env, "-Verify", local=False, success=False)
                self.assertIn("changed or removed", result.stderr)
                self.assertIn("Midden.lnk", result.stderr)
                result = self.run_installer(shell, install, env, "-Upgrade")
                self.assertIn("Kept the Start menu entry", result.stdout)
                result = self.run_installer(shell, install, env, "-Uninstall", local=False)
                self.assertIn("Kept the Start menu entry", result.stdout)
                self.assertTrue(start.exists())
                self.assertFalse(install.exists())


if __name__ == "__main__":
    unittest.main()
