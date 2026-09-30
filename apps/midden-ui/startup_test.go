package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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
	binary := filepath.Join(root, "install", "midden-ui")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("synthetic executable location"), 0600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "user-data")
	opts, create, err := resolveLaunchPaths(Options{}, binary, data)
	if err != nil {
		t.Fatal(err)
	}
	core := "midden"
	if runtime.GOOS == "windows" {
		core += ".exe"
	}
	if !create || opts.Workspace != filepath.Join(data, "workspace") || opts.State != filepath.Join(data, "host-state") {
		t.Fatalf("default launch did not separate writable data from installation: %+v", opts)
	}
	realBinary, _ := filepath.EvalSymlinks(binary)
	if opts.Core != filepath.Join(filepath.Dir(realBinary), core) || opts.Bundle != filepath.Join(filepath.Dir(realBinary), "bundles") {
		t.Fatal("default launch would resolve stale components from PATH or cwd")
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("path resolution unexpectedly created data")
	}
}

func TestExplicitWorkspaceRetainsItsExistingImplicitStateBinding(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "midden-ui")
	if err := os.WriteFile(binary, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "project")
	opts, create, err := resolveLaunchPaths(Options{Workspace: workspace}, binary, filepath.Join(root, "default-data"))
	if err != nil || create || opts.State != filepath.Join(workspace, ".midden-ui") {
		t.Fatal("an existing explicit-workspace launch silently moved its state", err)
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
