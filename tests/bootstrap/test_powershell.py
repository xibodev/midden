"""Standalone Windows bootstrap outcomes using only public synthetic releases."""

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
import threading
import unittest
import warnings
import zipfile


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "bootstrap" / "install.ps1"
SHELLS = [path for name in ("powershell", "pwsh") if (path := shutil.which(name))]
VERSION = "0.4.0"
COMMIT = "a" * 40
TARGET = "windows/amd64"
RECEIPT = ".midden-bootstrap-receipt.json"
LOCK = ".midden-bootstrap.lock"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def quote(value):
    return "'" + str(value).replace("'", "''") + "'"


def snapshot(root):
    return {
        path.relative_to(root).as_posix(): path.read_bytes() if path.is_file() else None
        for path in root.rglob("*")
    }


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
            Console.WriteLine("synthetic foreground UI completed"); return 0;
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
        string name;
        if (uri.AbsoluteUri == api) name = "latest.json";
        else if (uri.AbsoluteUri.StartsWith(prefix, StringComparison.Ordinal))
            name = uri.AbsoluteUri.Substring(prefix.Length);
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
            throw new Exception("Unexpected bootstrap environment lookup");
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


@unittest.skipUnless(os.name == "nt", "The PowerShell bootstrap installs Windows assets")
class PowerShellBootstrapTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not SHELLS or not shutil.which("powershell"):
            raise RuntimeError("Native Windows PowerShell is required for the probe fixture")
        cls.build = tempfile.TemporaryDirectory(prefix="midden-bootstrap-probe-")
        cls.addClassCleanup(cls.build.cleanup)
        cls.probe = native_probe(Path(cls.build.name))

    def setUp(self):
        parent = Path(tempfile.gettempdir()).resolve()
        self.assertNotEqual(parent, ROOT)
        self.assertNotIn(ROOT, parent.parents)
        self.temporary = tempfile.TemporaryDirectory(prefix="midden-bootstrap-test-", dir=parent)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.release = self.root / "reviewed release"
        self.release.mkdir()
        self.version = VERSION
        bundle = {}
        for outcome in ("investigation", "article", "presentation", "long-form"):
            bundle[f"bundles/{outcome}/SKILL.md"] = (
                f"---\nname: midden-{outcome}\ndescription: Synthetic guidance.\n---\n"
                "[Sources](../midden-shared/sources.md)\n"
            ).encode()
        bundle.update({
            "bundles/midden-shared/sources.md": b"Synthetic source guidance.\n",
            "bundles/midden-shared/tools.md": b"Synthetic dependency guidance.\n",
            "bundles/midden-shared/inspect_html.py": b"# Synthetic nonexecuted helper.\n",
            "bundles/midden-shared/html-inspection.md": b"Synthetic inspection guidance.\n",
            "bundles/presentation/templates/html.yaml": b"to: dzslides\n",
            "bundles/presentation/templates/slides.css": b"body { color: black; }\n",
            "bundles/long-form/templates/html.yaml": b"to: html5\n",
            "bundles/long-form/templates/epub.yaml": b"to: epub3\n",
            "bundles/article/template.md": b"Synthetic article template.\n",
        })
        self.products = {
            "core": {"midden.exe": self.probe, "LICENSE": b"Synthetic license.\n"},
            "ui": {
                **bundle, "midden.exe": self.probe, "midden-ui.exe": self.probe,
                "LICENSE": b"Synthetic license.\n", "NOTICE": b"Synthetic notice.\n",
                "start.ps1": b"# Synthetic launcher.\n",
                "start.sh": b"# Synthetic launcher.\n",
            },
            "bundle": {
                **bundle, "LICENSE": b"Synthetic license.\n",
                "install.ps1": (ROOT / "install.ps1").read_bytes(),
                "install.sh": (ROOT / "install.sh").read_bytes(),
                "installer/install.py": (ROOT / "installer" / "install.py").read_bytes(),
            },
        }
        entries = []
        for name, data in bundle.items():
            source = name[len("bundles/"):]
            if "/" not in source:
                continue
            first, rest = source.split("/", 1)
            destination = first if first == "midden-shared" else "midden-" + first
            entries.append({"source": source, "destination": destination + "/" + rest, "sha256": digest(data)})
        self.products["bundle"]["installer/bundle-manifest.json"] = json.dumps({
            "schema": "midden.bundle-files/v1", "files": entries,
        }).encode()
        self.write_release()

    def archive_name(self, product):
        suffix = "" if product == "bundle" else "_windows_amd64"
        return f"midden-{product}_{self.version}{suffix}.zip"

    def write_archive(self, product):
        with zipfile.ZipFile(self.release / self.archive_name(product), "w", zipfile.ZIP_DEFLATED) as archive:
            for name, data in self.products[product].items():
                entry = zipfile.ZipInfo(name)
                entry.filename = name
                entry.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(entry, data)

    def write_release(self):
        files = self.products["ui"]
        files.pop("package-manifest.json", None)
        files["package-manifest.json"] = json.dumps({
            "kind": "midden-ui-release", "version": self.version, "platform": TARGET,
            "source_commit": COMMIT, "kernel": "github.com/xibodev/compa v1.0.0",
            "files": {name: digest(data) for name, data in files.items()},
        }).encode()
        for product in self.products:
            self.write_archive(product)
        self.build_manifest = {
            "version": self.version, "commit": COMMIT, "targets": [TARGET],
            "products": ["core", "bundle", "ui"],
            "core_binaries": {TARGET: digest(self.products["core"]["midden.exe"])},
            "ui_binaries": {TARGET: digest(self.products["ui"]["midden-ui.exe"])},
            "archives": [self.archive_name(product) for product in self.products],
            "installers": ["install.ps1", "install.sh"],
        }
        self.write_metadata()

    def write_metadata(self):
        (self.release / "build-manifest.json").write_text(json.dumps(self.build_manifest), encoding="utf-8")
        lines = ["format\tmidden-release-v1", "version\t" + self.version, "commit\t" + COMMIT]
        for product, files in self.products.items():
            target = "universal" if product == "bundle" else TARGET
            lines.append(f"archive\t{product}\t{target}\t{self.archive_name(product)}")
            lines.extend(f"file\t{product}\t{target}\t{name}\t{digest(data)}" for name, data in files.items())
        (self.release / "manifest.tsv").write_text("\n".join(lines) + "\n", encoding="utf-8")
        (self.release / "install.ps1").write_bytes(SCRIPT.read_bytes() if SCRIPT.exists() else b"# pending bootstrap\n")
        (self.release / "install.sh").write_bytes(b"# synthetic Unix bootstrap\n")
        self.seal()

    def seal(self):
        names = self.build_manifest["archives"] + [
            "install.ps1", "install.sh", "build-manifest.json", "manifest.tsv"
        ]
        (self.release / "SHA256SUMS").write_text(
            "".join(digest((self.release / name).read_bytes()) + "  " + name + "\n" for name in sorted(names)),
            encoding="utf-8",
        )

    def prepare_cli_release(self):
        for product in ("core", "ui"):
            self.products[product]["THIRD_PARTY_NOTICES.txt"] = b"Synthetic dependency notices.\n"
        self.write_release()
        self.build_manifest["go"] = "go version go1.26.6 windows/amd64"
        self.write_metadata()
        for name in (self.archive_name("ui"), "install.ps1", "install.sh"):
            (self.release / name).unlink()

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
        isolated = self.root / "isolated-registry-bootstrap.txt"
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
            errors="replace", timeout=120,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def run_bootstrap(self, shell, install, project, env, *extra, success=True, pipeline=False,
                      launch=False, local=True):
        args = ["-InstallDir", str(install), "-ProjectDir", str(project), "-NoPath"]
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
                                encoding="utf-8", errors="replace", timeout=90)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def test_ui_clean_install_verify_and_uninstall_preserve_unowned_files_and_external_data(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                data = root / "application data"
                data.mkdir()
                (data / "retained.txt").write_text("Synthetic private state stays outside installation.\n")
                before_data = snapshot(data)
                result = self.run_bootstrap(shell, install, project, env)
                self.assertIn("Installed", result.stdout)
                self.assertEqual(
                    {name: data for name, data in snapshot(install).items() if data is not None and name != RECEIPT},
                    self.products["ui"],
                )
                self.assertTrue((install / RECEIPT).is_file())
                log = Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines()
                self.assertEqual(set(log), {"midden.exe|version", "midden-ui.exe|--version"})
                note = install / "bundles" / "article" / "unowned.md"
                note.write_text("Keep this local note.\n")
                before = snapshot(install)
                self.run_bootstrap(shell, install, project, env, "-Verify", local=False)
                self.assertEqual(snapshot(install), before)
                self.run_bootstrap(shell, install, project, env, "-Uninstall", local=False)
                self.assertEqual({p.name for p in install.rglob("*") if p.is_file()}, {"unowned.md"})
                self.assertEqual(note.read_text(), "Keep this local note.\n")
                self.assertEqual(snapshot(data), before_data)
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_core_mode_installs_only_core_without_python_node_or_go(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["PATH"] = str(Path(os.environ["SystemRoot"]) / "System32")
                env["PYTHON"] = "nonexistent-python-is-not-a-ui-or-core-prerequisite"
                self.run_bootstrap(shell, install, project, env, "-Mode", "core")
                self.assertEqual(set(snapshot(install)), set(self.products["core"]) | {RECEIPT})
                self.run_bootstrap(shell, install, project, env, "-Mode", "core", "-Verify", local=False)
                self.run_bootstrap(shell, install, project, env, "-Mode", "core", "-Uninstall", local=False)

    def test_dry_run_has_no_install_temp_or_execution_side_effects(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                before = snapshot(self.release)
                result = self.run_bootstrap(shell, install, project, env, "-DryRun")
                plan = json.loads(result.stdout)
                self.assertEqual(plan["mode"], "ui")
                self.assertEqual(plan["version"], VERSION)
                self.assertEqual(plan["install_dir"], str(install))
                self.assertTrue(plan["dry_run"])
                self.assertEqual({entry["path"] for entry in plan["destinations"]},
                                 {str(install / name) for name in self.products["ui"]} | {str(install / RECEIPT)})
                self.assertFalse(install.exists())
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
                self.assertEqual(snapshot(self.release), before)

    def test_piped_iex_defaults_to_ui_and_launches_foreground_with_no_open(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                result = self.run_bootstrap(shell, install, project, env, "-NoOpen", pipeline=True, launch=True)
                self.assertIn("synthetic foreground UI completed", result.stdout)
                self.assertEqual(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines()[-1],
                                 "midden-ui.exe|--no-open")
                self.run_bootstrap(shell, install, project, env, "-Uninstall", local=False)

    def test_upgrade_requires_unchanged_receipt_and_preserves_unowned_data(self):
        contexts = [(shell, *self.context(shell)) for shell in SHELLS]
        for shell, root, install, project, env in contexts:
            self.run_bootstrap(shell, install, project, env)
            (install / "unowned.txt").write_text("Keep synthetic unowned data.\n")
        self.version = "0.4.1"
        self.products["core"]["midden.exe"] += b"\nsynthetic second build\n"
        self.products["ui"]["midden.exe"] = self.products["core"]["midden.exe"]
        self.products["ui"]["midden-ui.exe"] += b"\nsynthetic second build\n"
        del self.products["ui"]["bundles/article/template.md"]
        self.products["ui"]["bundles/article/new.md"] = b"New synthetic guidance.\n"
        self.write_release()
        for shell, root, install, project, env in contexts:
            with self.subTest(shell=shell):
                env["MIDDEN_BOOTSTRAP_TEST_VERSION"] = self.version
                before = snapshot(install)
                self.run_bootstrap(shell, install, project, env, success=False)
                self.assertEqual(snapshot(install), before)
                self.run_bootstrap(shell, install, project, env, "-Upgrade")
                self.assertFalse((install / "bundles/article/template.md").exists())
                self.assertEqual((install / "bundles/article/new.md").read_bytes(), b"New synthetic guidance.\n")
                self.assertEqual((install / "unowned.txt").read_text(), "Keep synthetic unowned data.\n")
                self.run_bootstrap(shell, install, project, env, "-Verify", local=False)

    def test_modified_owned_file_blocks_verify_upgrade_uninstall(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                self.run_bootstrap(shell, install, project, env)
                (install / "NOTICE").write_text("Keep a synthetic local edit.\n")
                before = snapshot(install)
                Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).unlink()
                for operation in ("-Verify", "-Upgrade", "-Uninstall"):
                    result = self.run_bootstrap(shell, install, project, env, operation, success=False)
                    self.assertIn("modified", (result.stdout + result.stderr).lower())
                    self.assertEqual(snapshot(install), before)
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_unowned_collision_and_pending_lock_block_install_before_execution(self):
        for shell in SHELLS:
            _, install, project, env = self.context(shell)
            install.mkdir()
            for name in ("midden.exe", LOCK):
                with self.subTest(shell=shell, name=name):
                    collision = install / name
                    collision.write_bytes(self.probe if name == "midden.exe" else b"pending\n")
                    before = snapshot(install)
                    self.run_bootstrap(shell, install, project, env, success=False)
                    self.assertEqual(snapshot(install), before)
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                    collision.unlink()

    def test_corrupt_metadata_archive_and_binary_hash_never_execute(self):
        for name in ("build-manifest.json", "manifest.tsv", self.archive_name("ui")):
            original = (self.release / name).read_bytes()
            (self.release / name).write_bytes(original + b"synthetic corruption")
            for shell in SHELLS:
                with self.subTest(shell=shell, corruption=name):
                    _, install, project, env = self.context(shell)
                    result = self.run_bootstrap(shell, install, project, env, success=False)
                    self.assertIn("checksum", (result.stdout + result.stderr).lower())
                    self.assertFalse(install.exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
            (self.release / name).write_bytes(original)
        self.build_manifest["ui_binaries"][TARGET] = "0" * 64
        self.write_metadata()
        for shell in SHELLS:
            _, install, project, env = self.context(shell)
            self.run_bootstrap(shell, install, project, env, success=False)
            self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_wrong_version_source_commit_and_architecture_are_refused(self):
        for shell in SHELLS:
            _, install, project, env = self.context(shell)
            for options in (("-Version", "0.9.9"), ("-Repository", "owner/../other"),
                            ("-Upgrade", "-Uninstall")):
                with self.subTest(shell=shell, options=options):
                    self.run_bootstrap(shell, install, project, env, *options, success=False)
                    self.assertFalse(install.exists())
            foreign = dict(env, PROCESSOR_ARCHITECTURE="ARM64", PROCESSOR_ARCHITEW6432="ARM64")
            result = self.run_bootstrap(shell, install, project, foreign, success=False)
            self.assertIn("architecture", (result.stdout + result.stderr).lower())
        manifest = json.loads(self.products["ui"]["package-manifest.json"])
        manifest["source_commit"] = "b" * 40
        self.products["ui"]["package-manifest.json"] = json.dumps(manifest).encode()
        self.write_archive("ui")
        self.write_metadata()
        for shell in SHELLS:
            _, install, project, env = self.context(shell)
            result = self.run_bootstrap(shell, install, project, env, success=False)
            self.assertIn("source", (result.stdout + result.stderr).lower())
            self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_upgrade_and_uninstall_publication_failures_restore_old_receipt_and_files(self):
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
            self.run_bootstrap(shell, install, project, env)
        self.products["ui"]["NOTICE"] = b"New synthetic notice.\n"
        self.products["ui"]["midden-ui.exe"] += b"\nsecond synthetic build\n"
        del self.products["ui"]["bundles/article/template.md"]
        self.write_release()
        for shell, _, install, project, env in contexts:
            before = snapshot(install)
            handle = open_file(str(install / "NOTICE"), 0x80000000, 3, None, 3, 0x80, None)
            self.assertNotEqual(handle, wintypes.HANDLE(-1).value)
            try:
                for operation in ("-Upgrade", "-Uninstall"):
                    with self.subTest(shell=shell, operation=operation):
                        self.run_bootstrap(shell, install, project, env, operation, success=False)
                        self.assertEqual(snapshot(install), before)
                        self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
            finally:
                self.assertTrue(close(handle))
            self.run_bootstrap(shell, install, project, env, "-Verify", local=False)

    def test_unsafe_roots_and_linked_distribution_or_temporary_target_are_refused(self):
        for shell in SHELLS:
            root, install, project, env = self.context(shell)
            for target in (Path(env["USERPROFILE"]), Path(install.anchor),
                           Path(env["LOCALAPPDATA"]) / "Midden"):
                with self.subTest(shell=shell, target=target.name):
                    self.run_bootstrap(shell, target, project, env, "-DryRun", success=False)
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
                self.run_bootstrap(shell, alias, project, env, success=False)
                temp_env = dict(env, TEMP=str(alias), TMP=str(alias), TMPDIR=str(alias))
                self.run_bootstrap(shell, target, project, temp_env, success=False)
                self.assertEqual(snapshot(target), {})
                self.run_bootstrap(shell, install, project, temp_env)
                self.assertEqual(snapshot(target), {}, "External canonical scratch must be cleaned")
                self.run_bootstrap(shell, install, project, temp_env, "-Uninstall", local=False)
            finally:
                alias.rmdir()

    def test_relative_inputs_use_powershell_location_without_changing_process_directory(self):
        for shell in SHELLS:
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
                        f"-DistributionDir {quote(release_arg)} -ProjectDir {quote(project_arg)} "
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
                    self.assertEqual(report["plan"]["project_dir"], str(expected_project))
                    self.assertEqual(report["plan"]["version"], VERSION)
                    self.assertFalse(expected_install.exists())
                    self.assertFalse((process_directory / "relative install").exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
            with self.subTest(shell=shell, input="non-filesystem provider"):
                result = self.run_bootstrap(shell, "HKCU:\\Software", project, env, "-DryRun", success=False)
                self.assertIn("filesystem", result.stderr.lower())
            with self.subTest(shell=shell, input="relative explicit data root"):
                state = location / "relative state"
                state.mkdir()
                command = (
                    "$ErrorActionPreference = 'Stop'; "
                    f"Set-Location -LiteralPath {quote(location)}; "
                    f"[IO.Directory]::SetCurrentDirectory({quote(process_directory)}); "
                    f"& {quote(SCRIPT)} -InstallDir {quote(state)} -ProjectDir {quote(project)} "
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

    def test_install_root_cannot_overlap_default_or_explicit_core_and_ui_data(self):
        for shell in SHELLS:
            root, _, project, env = self.context(shell)
            home = Path(env["USERPROFILE"])
            default_core = home / ".midden"
            default_ui = home / "AppData" / "Local" / "Midden"
            alternate_local = root / "alternate local data"
            explicit_core = root / "explicit core data"
            explicit_kernel = root / "explicit kernel data"
            data_roots = (default_core, default_ui, alternate_local / "Midden", explicit_core, explicit_kernel)
            for path in data_roots:
                path.mkdir(parents=True, exist_ok=True)
                (path / "retained.txt").write_text("Synthetic retained data.\n")
            cases = (
                ("default core", default_core, env),
                ("default core child", default_core / "installation", env),
                ("default UI", default_ui, env),
                ("explicit core", explicit_core, dict(env, MIDDEN_HOME=str(explicit_core))),
                ("explicit UI", alternate_local / "Midden", dict(env, LOCALAPPDATA=str(alternate_local))),
                ("default UI with override", default_ui, dict(env, LOCALAPPDATA=str(alternate_local))),
                ("explicit kernel", explicit_kernel, dict(env, COMPA_HOME=str(explicit_kernel))),
            )
            for name, install, case_env in cases:
                with self.subTest(shell=shell, data_root=name):
                    before = {path: snapshot(path) for path in data_roots}
                    result = self.run_bootstrap(shell, install, project, case_env, "-DryRun", success=False)
                    self.assertIn("data", result.stderr.lower())
                    self.assertEqual({path: snapshot(path) for path in data_roots}, before)
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_default_data_junctions_cannot_hide_installation_overlap(self):
        for shell in SHELLS:
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
                        result = self.run_bootstrap(shell, target, project, env, "-DryRun", success=False)
                        self.assertIn("linked/reparse", result.stderr)
                        self.assertEqual(snapshot(target), before)
                        self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                    finally:
                        link.rmdir()

    def test_archive_size_limits_are_checked_before_extracting(self):
        archive = self.release / self.archive_name("ui")
        original = archive.read_bytes()
        changed = bytearray(original)
        central = changed.index(b"PK\x01\x02")
        struct.pack_into("<I", changed, central + 24, 256 * 1024 * 1024 + 1)
        archive.write_bytes(changed)
        self.seal()
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                result = self.run_bootstrap(shell, install, project, env, success=False)
                self.assertIn("size limit", (result.stdout + result.stderr).lower())
                self.assertFalse(install.exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
        archive.write_bytes(original)
        self.seal()
        (self.release / "SHA256SUMS").write_bytes(b"x" * (1024 * 1024 + 1))
        for shell in SHELLS:
            _, install, project, env = self.context(shell)
            result = self.run_bootstrap(shell, install, project, env, success=False)
            self.assertIn("size limit", (result.stdout + result.stderr).lower())

    def test_remote_latest_and_pinned_fork_download_only_selected_assets_without_credentials(self):
        transport_source = self.root / "SyntheticReleaseTransport.cs"
        transport_source.write_text(TRANSPORT, encoding="utf-8")
        (self.release / "latest.json").write_text(json.dumps({"tag_name": "v" + VERSION}), encoding="utf-8")
        for shell in SHELLS:
            for repository, pinned, mode in (("xibodev/midden", False, "ui"), ("example/synthetic-fork", True, "core")):
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
                    args = (f"-Mode {mode} -InstallDir {quote(install)} -ProjectDir {quote(project)} "
                            f"-Repository {quote(repository)} -NoPath -NoLaunch"
                            + (f" -Version {VERSION}" if pinned else ""))
                    result = subprocess.run(
                        [shell, "-NoProfile", "-NonInteractive", "-Command",
                         prefix + f"& {quote(SCRIPT)} {args}"],
                        cwd=self.root, env=env, capture_output=True, text=True, timeout=90,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    base = f"https://github.com/{repository}/releases/download/v{VERSION}/"
                    expected = ([] if pinned else [f"https://api.github.com/repos/{repository}/releases/latest"])
                    expected += [base + name for name in
                                 ("SHA256SUMS", "build-manifest.json", "manifest.tsv", self.archive_name(mode))]
                    self.assertEqual(log.read_text().splitlines(), expected)
                    self.run_bootstrap(shell, install, project, env, "-Mode", mode, "-Uninstall", local=False)

    def test_literal_irm_iex_defaults_install_and_launch_without_adjacent_files(self):
        source = self.root / "SyntheticDefaultTransport.cs"
        source.write_text(TRANSPORT, encoding="utf-8")
        (self.release / "latest.json").write_text(json.dumps({"tag_name": "v" + VERSION}), encoding="utf-8")
        script_bytes = self.isolated_registry_script().encode("utf-8")
        isolated = self.root / "literal-registry-bootstrap.txt"
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
                        cwd=root, env=env, capture_output=True, text=True, timeout=90,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn("synthetic foreground UI completed", result.stdout)
                    self.assertEqual(path_log.read_text().splitlines(),
                                     [r"C:\synthetic-existing-bin;" + str(install), r"C:\synthetic-existing-bin"])
                    self.assertEqual(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines()[-1],
                                     "midden-ui.exe|")
                    self.assertFalse((install / RECEIPT).exists())
                    self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=10)

    def test_cli_verify_and_uninstall_require_reviewed_matching_backend(self):
        self.prepare_cli_release()
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["PYTHON"] = sys.executable
                self.run_bootstrap(shell, install, project, env, "-Mode", "cli")
                before = snapshot(install)
                for operation in ("-Verify", "-Uninstall"):
                    result = self.run_bootstrap(shell, install, project, env, "-Mode", "cli", operation,
                                                local=False, success=False)
                    self.assertIn("reviewed", (result.stdout + result.stderr).lower())
                    self.assertEqual(snapshot(install), before)
                self.run_bootstrap(shell, install, project, env, "-Mode", "cli", "-Uninstall")

    def test_path_publication_failure_rolls_back_owned_files_receipt_and_simulated_user_path(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                self.run_bootstrap(shell, install, project, env)
                before = snapshot(install)
                log = root / "path-publication.log"
                command = (
                    f"[SyntheticBootstrapEnvironment]::PathLog = {quote(log)}; "
                    "[SyntheticBootstrapEnvironment]::FailPathOnce = $true; "
                    "& $bootstrap -DistributionDir $release -InstallDir $install "
                    "-ProjectDir $project -Upgrade -NoLaunch"
                )
                result = self.registry_driver(shell, install, project, env, command, success=False)
                self.assertIn("Synthetic PATH publication", result.stderr)
                self.assertEqual(snapshot(install), before)
                self.assertEqual(log.read_text().splitlines(),
                                 [r"C:\synthetic-existing-bin;" + str(install), r"C:\synthetic-existing-bin"])
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_raw_path_preserves_tokens_kind_and_absent_vs_empty_across_profiles(self):
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
                        "& $bootstrap -DistributionDir $release -InstallDir $install -ProjectDir $project -NoLaunch; "
                        f"$receipt = [IO.File]::ReadAllText((Join-Path $install {quote(RECEIPT)})) | ConvertFrom-Json; "
                        "$installed = RawState; "
                        f"$env:USERPROFILE = {quote(other)}; "
                        "& $bootstrap -InstallDir $install -ProjectDir $project -NoLaunch -Uninstall; "
                        f"[IO.File]::WriteAllText({quote(report)}, "
                        "(@{ before=$before; installed=$installed; restored=(RawState); ownership=$receipt.path_change } "
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
                    self.assertEqual(actual["ownership"], {
                        "schema": "midden.user-path/v1", "before": state, "after": expected_after,
                    })
                    self.assertFalse((install / RECEIPT).exists())

    def test_raw_path_rejects_unsupported_kind_before_extraction_or_execution(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                result = self.registry_driver(
                    shell, install, project, env,
                    "[SyntheticRegistry]::Kind = 'DWord'; "
                    "& $bootstrap -DistributionDir $release -InstallDir $install -ProjectDir $project -NoLaunch",
                    success=False,
                )
                self.assertIn("kind", result.stderr.lower())
                self.assertFalse(install.exists())
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_raw_path_concurrent_kind_or_text_edit_is_preserved_on_uninstall(self):
        for shell in SHELLS:
            for edit in ("kind", "text"):
                with self.subTest(shell=shell, edit=edit):
                    root, install, project, env = self.context(shell)
                    report = root / ("concurrent-" + edit + ".json")
                    mutation = ("[SyntheticRegistry]::Kind = 'String'; " if edit == "kind"
                                else "[SyntheticRegistry]::Value += ';C:\\synthetic-user-addition'; ")
                    body = (
                        "[SyntheticRegistry]::Kind = 'ExpandString'; "
                        "[SyntheticRegistry]::Value = '%USERPROFILE%\\tools'; "
                        "& $bootstrap -DistributionDir $release -InstallDir $install -ProjectDir $project -NoLaunch; "
                        + mutation
                        + "$edited = RawState; $writes = [SyntheticRegistry]::Writes; "
                        "$notifications = [SyntheticEnvironmentNotification]::Calls; "
                        "& $bootstrap -InstallDir $install -ProjectDir $project -Uninstall; "
                        f"[IO.File]::WriteAllText({quote(report)}, (@{{ edited=$edited; final=(RawState); "
                        "writes=([SyntheticRegistry]::Writes - $writes); "
                        "notifications=([SyntheticEnvironmentNotification]::Calls - $notifications) } | ConvertTo-Json -Depth 10))"
                    )
                    result = self.registry_driver(shell, install, project, env, body)
                    self.assertIn("PATH", result.stdout)
                    actual = json.loads(report.read_text(encoding="utf-8-sig"))
                    self.assertEqual(actual["final"], actual["edited"])
                    self.assertEqual(actual["writes"], 0)
                    self.assertEqual(actual["notifications"], 0)
                    self.assertFalse((install / RECEIPT).exists())

    def test_raw_path_failed_publication_restores_exact_state_or_preserves_concurrent_edit(self):
        for shell in SHELLS:
            for scenario in ("rollback", "concurrent"):
                with self.subTest(shell=shell, scenario=scenario):
                    root, install, project, env = self.context(shell)
                    self.run_bootstrap(shell, install, project, env)
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
                        + "try { & $bootstrap -DistributionDir $release -InstallDir $install -ProjectDir $project "
                        "-Upgrade -NoLaunch } catch { $message = $_.Exception.Message }; "
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
                    self.run_bootstrap(shell, install, project, env, "-Uninstall", local=False)

    def test_raw_path_notifications_follow_commits_restores_but_not_no_change_operations(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                report = root / "environment-notifications.json"
                body = (
                    "$before = RawState; $counts = @(); "
                    "$options = @{ Mode='core'; DistributionDir=$release; InstallDir=$install; "
                    "ProjectDir=$project; NoLaunch=$true; Verbose=$true }; "
                    "& $bootstrap @options -DryRun | Out-Null; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -NoPath; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options -NoPath -Uninstall; $counts += [SyntheticEnvironmentNotification]::Calls; "
                    "& $bootstrap @options; $counts += [SyntheticEnvironmentNotification]::Calls; "
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
                self.assertFalse((install / RECEIPT).exists())

    def test_raw_path_notification_failure_warns_without_rollback_or_clobbering_new_edits(self):
        for shell in SHELLS:
            for failure in ("timeout", "exception"):
                with self.subTest(shell=shell, failure=failure):
                    root, install, project, env = self.context(shell)
                    report = root / ("notification-" + failure + ".json")
                    condition = ("[SyntheticEnvironmentNotification]::ReturnCode = 1460; "
                                 if failure == "timeout" else "[SyntheticEnvironmentNotification]::Throw = $true; ")
                    body = (
                        condition
                        + "[SyntheticEnvironmentNotification]::ConcurrentValue = '%USERPROFILE%\\tools;C:\\synthetic-concurrent-edit'; "
                        "& $bootstrap -Mode core -DistributionDir $release -InstallDir $install -ProjectDir $project "
                        "-NoLaunch -WarningAction Stop; "
                        f"$receipt = [IO.File]::ReadAllText((Join-Path $install {quote(RECEIPT)})) | ConvertFrom-Json; "
                        f"[IO.File]::WriteAllText({quote(report)}, (@{{ final=(RawState); receipt=$receipt; "
                        "notifications=[SyntheticEnvironmentNotification]::Calls; writes=[SyntheticRegistry]::Writes } "
                        "| ConvertTo-Json -Depth 15)); "
                        "& $bootstrap -Mode core -InstallDir $install -ProjectDir $project -NoPath -Uninstall"
                    )
                    result = self.registry_driver(shell, install, project, env, body)
                    self.assertIn("WM_SETTINGCHANGE(Environment)", result.stdout)
                    self.assertIn("not rolled back", result.stdout.lower())
                    actual = json.loads(report.read_text(encoding="utf-8-sig"))
                    self.assertEqual(actual["final"]["value"], r"%USERPROFILE%\tools;C:\synthetic-concurrent-edit")
                    self.assertEqual(actual["writes"], 1)
                    self.assertEqual(actual["notifications"], 1)
                    self.assertEqual(actual["receipt"]["mode"], "core")
                    self.assertFalse((install / RECEIPT).exists())

    def test_raw_path_dry_run_is_write_free(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                report = root / "path-plan.json"
                body = (
                    "[SyntheticRegistry]::Kind = 'ExpandString'; "
                    "[SyntheticRegistry]::Value = '%USERPROFILE%\\tools;;'; "
                    "$plan = & $bootstrap -DistributionDir $release -InstallDir $install -ProjectDir $project "
                    "-NoLaunch -DryRun | Out-String; "
                    f"[IO.File]::WriteAllText({quote(report)}, (@{{ plan=($plan | ConvertFrom-Json); "
                    "raw=(RawState); writes=[SyntheticRegistry]::Writes } | ConvertTo-Json -Depth 15))"
                )
                self.registry_driver(shell, install, project, env, body)
                actual = json.loads(report.read_text(encoding="utf-8-sig"))
                self.assertEqual(actual["writes"], 0)
                self.assertEqual(actual["raw"]["value"], r"%USERPROFILE%\tools;;")
                self.assertFalse(install.exists())
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_path_receipt_without_schema_blocks_verify_upgrade_uninstall(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                self.run_bootstrap(shell, install, project, env)
                receipt_path = install / RECEIPT
                receipt = json.loads(receipt_path.read_text())
                receipt["path_change"] = {"before": r"C:\synthetic\tools", "after": r"C:\synthetic\tools;" + str(install)}
                receipt_path.write_text(json.dumps(receipt), encoding="utf-8")
                before = snapshot(install)
                Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).unlink()
                for operation in ("-Verify", "-Upgrade", "-Uninstall"):
                    result = self.run_bootstrap(shell, install, project, env, operation, success=False)
                    self.assertIn("path ownership inventory mismatch", (result.stdout + result.stderr).lower())
                    self.assertEqual(snapshot(install), before)
                self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_native_version_mismatch_leaves_no_installation_or_probe_scratch(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["MIDDEN_BOOTSTRAP_TEST_VERSION"] = "0.9.9"
                result = self.run_bootstrap(shell, install, project, env, success=False)
                self.assertIn("version", result.stderr.lower())
                self.assertFalse(install.exists())
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])

    def test_denied_native_probe_names_operation_and_error_without_installing_or_fallback(self):
        for shell in SHELLS:
            with self.subTest(shell=shell):
                _, install, project, env = self.context(shell)
                env["MIDDEN_BOOTSTRAP_TEST_DENY_UI_EXECUTE"] = "1"
                before_release = snapshot(self.release)
                result = self.run_bootstrap(shell, install, project, env, success=False)
                self.assertIn("Process.Start", result.stderr)
                self.assertIn("midden-ui.exe", result.stderr)
                self.assertIn("--version", result.stderr)
                self.assertIn("Win32=5", result.stderr)
                self.assertIn("application-control", result.stderr)
                self.assertFalse(install.exists())
                self.assertEqual(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).read_text().splitlines(),
                                 ["midden.exe|version"])
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])
                self.assertEqual(snapshot(self.release), before_release)

    def test_builder_third_party_notices_are_verified_installed_and_receipt_owned(self):
        notice = b"Synthetic dependency attribution; preserve with the distributed product.\n"
        for product in ("ui", "core"):
            self.products[product]["THIRD_PARTY_NOTICES.txt"] = notice
        self.write_release()
        for shell in SHELLS:
            for mode in ("ui", "core"):
                with self.subTest(shell=shell, mode=mode):
                    _, install, project, env = self.context(shell)
                    self.run_bootstrap(shell, install, project, env, "-Mode", mode)
                    self.assertEqual((install / "THIRD_PARTY_NOTICES.txt").read_bytes(), notice)
                    self.assertEqual(json.loads((install / RECEIPT).read_text())["files"]["THIRD_PARTY_NOTICES.txt"],
                                     digest(notice))
                    self.run_bootstrap(shell, install, project, env, "-Mode", mode, "-Verify", local=False)
                    self.run_bootstrap(shell, install, project, env, "-Mode", mode, "-Uninstall", local=False)
                    self.assertFalse((install / "THIRD_PARTY_NOTICES.txt").exists())

    def test_unsafe_archive_members_are_refused_even_when_checksums_match(self):
        original = dict(self.products["ui"])
        for name in ("../escape.txt", "C:/escape.txt", "bundles\\escape.txt", "bundles/article/NUL",
                     "NOTICE:stream", "bundles/ARTICLE/new.md", "NOTICE/child"):
            self.products["ui"] = {**original, name: b"Synthetic unsafe member.\n"}
            self.write_release()
            for shell in SHELLS:
                with self.subTest(shell=shell, member=name):
                    _, install, project, env = self.context(shell)
                    self.run_bootstrap(shell, install, project, env, success=False)
                    self.assertFalse(install.exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())
        self.products["ui"] = original
        for kind in ("duplicate", "symlink", "directory"):
            self.write_release()
            with zipfile.ZipFile(self.release / self.archive_name("ui"), "a") as archive:
                if kind == "duplicate":
                    with warnings.catch_warnings():
                        warnings.simplefilter("ignore", UserWarning)
                        archive.writestr("LICENSE", b"Synthetic duplicate.\n")
                else:
                    entry = zipfile.ZipInfo("link" if kind == "symlink" else "directory/")
                    entry.create_system = 3
                    entry.external_attr = ((stat.S_IFLNK if kind == "symlink" else stat.S_IFDIR) | 0o755) << 16
                    archive.writestr(entry, b"../outside")
            self.seal()
            for shell in SHELLS:
                with self.subTest(shell=shell, kind=kind):
                    _, install, project, env = self.context(shell)
                    self.run_bootstrap(shell, install, project, env, success=False)
                    self.assertFalse(install.exists())
                    self.assertFalse(Path(env["MIDDEN_BOOTSTRAP_TEST_LOG"]).exists())

    def test_cli_mode_delegates_to_verified_existing_python_installer(self):
        self.prepare_cli_release()
        metadata = ("build-manifest.json", "SHA256SUMS", "manifest.tsv")
        expected_metadata = {name: (self.release / name).read_bytes().hex() for name in metadata}
        expected_files = set(metadata) | {self.archive_name("core"), self.archive_name("bundle")}
        observer = self.root / "snapshot observer"
        observer.mkdir()
        (observer / "sitecustomize.py").write_text(
            "import json, os, sys\n"
            "from pathlib import Path\n"
            "if '--distribution-dir' in sys.argv:\n"
            "    directory = Path(sys.argv[sys.argv.index('--distribution-dir') + 1])\n"
            "    names = ('build-manifest.json', 'SHA256SUMS', 'manifest.tsv')\n"
            "    record = {'files': sorted(p.name for p in directory.iterdir()),\n"
            "              'metadata': {name: (directory / name).read_bytes().hex()\n"
            "                           for name in names if (directory / name).is_file()}}\n"
            "    with open(os.environ['MIDDEN_BOOTSTRAP_TEST_SNAPSHOT'], 'a', encoding='utf-8') as stream:\n"
            "        stream.write(json.dumps(record) + '\\n')\n",
            encoding="utf-8",
        )
        before_release = snapshot(self.release)
        for shell in SHELLS:
            with self.subTest(shell=shell):
                root, install, project, env = self.context(shell)
                observation = root / "snapshot-observations.jsonl"
                env.update(PYTHON=sys.executable, PYTHONPATH=str(observer), PYTHONNOUSERSITE="1",
                           PYTHONDONTWRITEBYTECODE="1", MIDDEN_BOOTSTRAP_TEST_SNAPSHOT=str(observation))
                self.run_bootstrap(shell, install, project, env, "-Mode", "cli", "-Host", "claude")
                self.assertTrue((project / ".claude/skills/midden-article/SKILL.md").is_file())
                self.assertTrue((install / "midden-install-receipt.json").is_file())
                self.assertFalse((install / RECEIPT).exists())
                self.run_bootstrap(shell, install, project, env, "-Mode", "cli", "-Host", "claude", "-Upgrade")
                self.run_bootstrap(shell, install, project, env, "-Mode", "cli", "-Host", "claude", "-Verify")
                self.run_bootstrap(shell, install, project, env, "-Mode", "cli", "-Host", "claude", "-Uninstall")
                self.assertFalse((install / "midden.exe").exists())
                self.assertFalse((project / ".midden/state").exists())
                observations = [json.loads(line) for line in observation.read_text().splitlines()]
                self.assertEqual(len(observations), 4)
                for record in observations:
                    self.assertEqual(set(record["files"]), expected_files)
                    self.assertEqual(record["metadata"], expected_metadata)
                self.assertEqual(snapshot(self.release), before_release)
                self.assertEqual(list(Path(env["TEMP"]).iterdir()), [])


if __name__ == "__main__":
    unittest.main()
