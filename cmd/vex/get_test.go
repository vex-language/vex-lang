package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Creates a bare git repository holding [files], tagged [tag], and
// returns its file:// URL.
func gitRepo(t *testing.T, tag string, files map[string]string) string {
	t.Helper()
	base := t.TempDir()
	work, bare := filepath.Join(base, "work"), filepath.Join(base, "repo.git")
	writeProject(t, work, files)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", work},
		{"init", "-q", "--bare", bare},
		{"-C", work, "add", "-A"},
		{"-C", work, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "release"},
		{"-C", work, "tag", tag},
		{"-C", work, "push", "-q", bare, "HEAD:refs/heads/main", "refs/tags/" + tag},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return "file://" + filepath.ToSlash(bare)
}

func writeProject(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetAndRun(t *testing.T) {
	needCC(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(config, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VEX_CACHE", filepath.Join(t.TempDir(), "cache"))

	fmtURL := gitRepo(t, "v0.1.0", map[string]string{
		"package.toml": "[package]\nname = \"fmt\"\n",
		"fmt.vx":       "pub fn shout(s: string) -> string => s + \"!\"\n",
	})
	chartsURL := gitRepo(t, "v0.2.0", map[string]string{
		"package.toml":   "[package]\nname = \"charts\"\n\n[dependencies]\nfmt = { git = \"" + fmtURL + "\", version = \"0.1\" }\n",
		"charts.vx":      "import \"package:fmt\"\nimport \"line\"\n\npub fn title(s: string) -> string => fmt.shout(line.draw(s))\n",
		"line/line.vx":   "pub fn draw(s: string) -> string => \"~\" + s\n",
		"charts_test.vx": "not loaded\n",
	})
	app := t.TempDir()
	writeProject(t, app, map[string]string{
		"package.toml": "[package]\nname = \"app\"\n",
		"main.vx":      "import \"package:charts\"\nimport \"package:charts/line\"\n\nfn main() {\n    println(charts.title(\"sales\"))\n    println(line.draw(\"x\"))\n}\n",
	})
	wd, _ := os.Getwd()
	if err := os.Chdir(app); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	code, out, errOut := runCLI("get", chartsURL)
	if code != exitOK {
		t.Fatalf("get: code %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "Added charts 0.2.0\n") || !strings.HasSuffix(out, "Locked 2 packages in package.lock\n") {
		t.Errorf("get output %q", out)
	}
	code, out, errOut = runCLI("run")
	if code != exitOK || out != "~sales!\n~x\n" {
		t.Fatalf("run: code %d, stdout %q, stderr %q", code, out, errOut)
	}

	// A changed file in the cache stops the build.
	filepath.WalkDir(os.Getenv("VEX_CACHE"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "fmt.vx" {
			os.WriteFile(p, []byte("pub fn shout(s: string) -> string => s\n"), 0o644)
		}
		return nil
	})
	code, _, errOut = runCLI("check")
	if code != exitError || !strings.Contains(errOut, "doesn't match package.lock") {
		t.Errorf("tampered: code %d, stderr %q", code, errOut)
	}

	code, _, errOut = runCLI("get", "--path")
	if code != exitUsage {
		t.Errorf("get --path without a value: code %d, stderr %q", code, errOut)
	}
}

func TestPackageImportWithoutDependency(t *testing.T) {
	app := t.TempDir()
	writeProject(t, app, map[string]string{
		"package.toml": "[package]\nname = \"app\"\n",
		"main.vx":      "import \"package:ui\"\nfn main() {}\n",
	})
	code, _, errOut := runCLI("check", app)
	if code != exitError || !strings.Contains(errOut, "no package 'ui' in package.toml") || !strings.Contains(errOut, "vex get") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}
