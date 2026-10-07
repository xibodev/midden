package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseVersionFlagDoesNotCreateUserState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_DATA_HOME", root)
	var output bytes.Buffer
	if err := runArgs([]string{"--version"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "midden-ui "+version+"\n" {
		t.Fatalf("unexpected version output: %q", output.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("passive version probe created user state", err)
	}
}

func TestDefaultLaunchUsesSiblingComponentsAndSeparateUserData(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "install")
	binary := filepath.Join(install, "midden-ui")
	kernel := filepath.Join(install, "app", executableName("compa-kernel"))
	tools := filepath.Join(install, "app", "tools")
	for _, dir := range []string{tools, filepath.Join(install, "skills")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{binary, kernel} {
		if err := os.WriteFile(file, []byte("synthetic executable"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(root, "user-data")
	opts, err := resolveLaunchPaths(Options{}, binary, data)
	if err != nil {
		t.Fatal(err)
	}
	realInstall, _ := filepath.EvalSymlinks(install)
	if opts.Data != data || opts.Install != realInstall || opts.Core != filepath.Join(realInstall, executableName("midden")) ||
		opts.Skills != filepath.Join(realInstall, "skills") || opts.Kernel != filepath.Join(realInstall, "app", executableName("compa-kernel")) ||
		opts.Tools != filepath.Join(realInstall, "app", "tools") {
		t.Fatalf("default launch did not use the installed layout: %+v", opts)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("path resolution unexpectedly created data")
	}
	if err := os.RemoveAll(filepath.Join(install, "app")); err != nil {
		t.Fatal(err)
	}
	bare, err := resolveLaunchPaths(Options{}, binary, data)
	if err != nil || bare.Kernel != "" || bare.Tools != "" {
		t.Fatalf("an App without a kernel or tools: %+v %v", bare, err)
	}
	if _, err := resolveLaunchPaths(Options{Kernel: filepath.Join(root, "missing")}, binary, data); err == nil {
		t.Fatal("a missing explicit kernel was accepted")
	}
	explicit, err := resolveLaunchPaths(Options{Data: filepath.Join(root, "elsewhere")}, binary, data)
	if err != nil || explicit.Data != filepath.Join(root, "elsewhere") {
		t.Fatal("an explicit data folder was not used as given", err)
	}
}

func TestKernelEnvironmentPutsTheAppsProgramsFirst(t *testing.T) {
	setup := kernelSetup{Home: "/data/kernel", Workspace: "/data/workspace", Skills: "/install/skills", Tools: "/install/app/tools", Install: "/install"}
	base := []string{"Path=/person/bin", "COMPA_HOME=/person/.compa", "COMPA_CONFIG=/person/config.json", "compa_gateway_port=1", "MIDDEN_HOME=/person/.midden", "OTHER=kept"}
	env := kernelEnv(base, setup, 18999, "token")
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, seen := values[strings.ToUpper(key)]; seen {
			t.Fatalf("%s is set twice: %v", key, env)
		}
		values[strings.ToUpper(key)] = value
	}
	separator := string(os.PathListSeparator)
	want := map[string]string{"PATH": "/install" + separator + "/install/app/tools" + separator + "/person/bin", "COMPA_HOME": "/data/kernel",
		"COMPA_AGENTS_DEFAULTS_WORKSPACE": "/data/workspace", "COMPA_GATEWAY_HOST": "127.0.0.1", "COMPA_GATEWAY_PORT": "18999",
		"COMPA_BUILTIN_SKILLS": "/install/skills", "COMPA_CHANNELS_WEB_TOKEN": "token", "MIDDEN_HOME": "/person/.midden", "OTHER": "kept"}
	for key, value := range want {
		if values[key] != value {
			t.Fatalf("%s = %q, want %q", key, values[key], value)
		}
	}
	if _, ok := values["COMPA_CONFIG"]; ok {
		t.Fatal("another Compa's settings reached the kernel")
	}
	pattern := regexp.MustCompile(values["COMPA_TOOLS_ALLOW_READ_PATHS"])
	for path, allowed := range map[string]bool{filepath.FromSlash("/install/skills"): true, filepath.FromSlash("/install/skills/midden-article/SKILL.md"): true,
		filepath.FromSlash("/install/skills-other/x"): false, filepath.FromSlash("/data/kernel/auth.json"): false} {
		if pattern.MatchString(filepath.Clean(path)) != allowed {
			t.Fatalf("read allowlist on %s = %v", path, !allowed)
		}
	}
	if strings.Contains(readOnlyPattern("/a,b"), ",") {
		t.Fatal("a comma in the skills path split the allowlist")
	}
}
func TestPlatformDataRootsStayOutsideVersionedInstallation(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	local := filepath.Join(root, "local")
	xdg := filepath.Join(root, "xdg")
	for _, tc := range []struct{ platform, local, xdg, want string }{
		{"windows", local, "", filepath.Join(local, "Midden")},
		{"windows", "", "", filepath.Join(home, "AppData", "Local", "Midden")},
		{"darwin", "", "", filepath.Join(home, "Library", "Application Support", "Midden")},
		{"linux", "", xdg, filepath.Join(xdg, "midden")},
		{"linux", "", "", filepath.Join(home, ".local", "share", "midden")},
	} {
		got, err := applicationDataRoot(tc.platform, home, tc.local, tc.xdg)
		if err != nil || got != tc.want {
			t.Fatalf("%s data root=%q err=%v want %q", tc.platform, got, err, tc.want)
		}
	}
	if _, err := applicationDataRoot("linux", home, "", "relative-data"); err == nil {
		t.Fatal("relative XDG data root was silently adopted")
	}
}
