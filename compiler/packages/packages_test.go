package packages

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Prepares git and the package cache so tests run offline and leave the
// user's own settings alone.
func setup(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VEX_CACHE", filepath.Join(t.TempDir(), "cache"))
}

// A git repository for tests: a working copy and the bare repository it
// pushes its releases to, which packages are fetched from.
type repo struct {
	t    *testing.T
	work string
	bare string
}

func newRepo(t *testing.T, name string) *repo {
	t.Helper()
	base := t.TempDir()
	r := &repo{t: t, work: filepath.Join(base, "work"), bare: filepath.Join(base, name+".git")}
	r.git("", "init", "-q", "-b", "main", r.work)
	r.git("", "init", "-q", "--bare", r.bare)
	return r
}

func (r *repo) git(dir string, args ...string) {
	r.t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r *repo) url() string { return "file://" + filepath.ToSlash(r.bare) }

// Commits [files] and tags the commit [tag] in the bare repository.
func (r *repo) release(tag string, files map[string]string) {
	r.t.Helper()
	writeFiles(r.t, r.work, files)
	r.git(r.work, "add", "-A")
	r.git(r.work, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", tag)
	r.git(r.work, "tag", "-f", tag)
	r.git(r.work, "push", "-q", "-f", r.bare, "HEAD:refs/heads/main", "refs/tags/"+tag)
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Returns a project folder with [deps] as the lines of its
// [dependencies] section.
func newProject(t *testing.T, deps ...string) string {
	t.Helper()
	dir := t.TempDir()
	src := "[package]\nname = \"app\"\n"
	if len(deps) > 0 {
		src += "\n[dependencies]\n" + strings.Join(deps, "\n") + "\n"
	}
	writeFiles(t, dir, map[string]string{"package.toml": src, "main.vx": "fn main() {}\n"})
	return dir
}

func manifestFor(name string, deps ...string) string {
	src := "[package]\nname = \"" + name + "\"\n"
	if len(deps) > 0 {
		src += "\n[dependencies]\n" + strings.Join(deps, "\n") + "\n"
	}
	return src
}

func dep(key string, r *repo, version string) string {
	return key + ` = { git = "` + r.url() + `", version = "` + version + `" }`
}

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGet(t *testing.T) {
	setup(t)
	charts := newRepo(t, "charts")
	charts.release("v0.1.0", map[string]string{"package.toml": manifestFor("charts"), "charts.vx": "pub fn f() {}\n"})
	charts.release("v0.2.0", map[string]string{"charts.vx": "pub fn g() {}\n"})
	charts.release("not-a-version", nil)
	app := newProject(t)

	if err := Get(app, []string{charts.url()}, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, filepath.Join(app, "package.toml")), "\n[dependencies]\n"+dep("charts", charts, "0.2.0")+"\n"; !strings.HasSuffix(got, want) {
		t.Errorf("package.toml:\n%s\nwant it to end with:\n%s", got, want)
	}
	lock := read(t, filepath.Join(app, LockFile))
	entry := regexp.MustCompile(`\[\[package\]\]\nname = "charts"\ngit = "` + regexp.QuoteMeta(charts.url()) +
		`"\nversion = "0.2.0"\ncommit = "[0-9a-f]{40}"\nhash = "sha256:[0-9a-f]{64}"\n`)
	if !entry.MatchString(lock) {
		t.Errorf("lock file:\n%s", lock)
	}

	pkgs, err := Resolve(app, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	p := pkgs["charts"]
	if p == nil || p.Name != "charts" || !strings.HasSuffix(filepath.ToSlash(p.Dir), "charts@0.2.0") {
		t.Fatalf("packages %v", pkgs)
	}
	if got := read(t, filepath.Join(p.Dir, "charts.vx")); got != "pub fn g() {}\n" {
		t.Errorf("cached charts.vx %q", got)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".git")); err == nil {
		t.Errorf("the cache holds git's files")
	}

	// An older version, by prefix; vex get keeps the key and replaces the line.
	charts.release("v0.1.1", map[string]string{"charts.vx": "pub fn h() {}\n"})
	if err := Get(app, []string{charts.url() + "@0.1"}, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(app, "package.toml")); !strings.Contains(got, `version = "0.1.1" }`) || strings.Count(got, "charts =") != 1 {
		t.Errorf("package.toml:\n%s", got)
	}
	if err := Get(app, []string{charts.url() + "@0.9"}, "", io.Discard); err == nil || !strings.Contains(err.Error(), "has no version 0.9; its newest is 0.2.0") {
		t.Errorf("missing version: %v", err)
	}
}

func TestGetFromLocalPath(t *testing.T) {
	setup(t)
	lib := newRepo(t, "lib")
	lib.release("v1.0.0", map[string]string{"package.toml": manifestFor("lib"), "lib.vx": "pub fn f() {}\n"})
	app := newProject(t)
	if err := Get(app, []string{lib.bare + "@1.0.0"}, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if pkgs, err := Resolve(app, io.Discard); err != nil || pkgs["lib"] == nil {
		t.Fatalf("resolve: %v, %v", pkgs, err)
	}
}

func TestGetFolderPackage(t *testing.T) {
	setup(t)
	mono := newRepo(t, "packages")
	mono.release("camera/v0.2.0", map[string]string{"camera/package.toml": manifestFor("camera"), "camera/camera.vx": "pub fn snap() {}\n", "maps/package.toml": manifestFor("maps")})
	mono.release("maps/v0.5.0", nil)
	app := newProject(t)
	if err := Get(app, []string{mono.url()}, "camera", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(app, "package.toml")); !strings.Contains(got, `camera = { git = "`+mono.url()+`", path = "camera", version = "0.2.0" }`) {
		t.Errorf("package.toml:\n%s", got)
	}
	pkgs, err := Resolve(app, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pkgs["camera"].Dir, "camera.vx")); err != nil {
		t.Errorf("camera.vx isn't at the root of the cached package: %v", err)
	}
}

// Sets up fmt with several releases and charts, which needs fmt 0.1.1.
func transitive(t *testing.T) (fmtRepo, charts *repo) {
	fmtRepo = newRepo(t, "fmt")
	for _, v := range []string{"v0.1.0", "v0.1.1", "v0.1.2", "v1.0.0"} {
		fmtRepo.release(v, map[string]string{"package.toml": manifestFor("fmt"), "fmt.vx": "pub fn f() {} // " + v + "\n"})
	}
	charts = newRepo(t, "charts")
	charts.release("v0.1.0", map[string]string{"package.toml": manifestFor("charts", dep("fmt", fmtRepo, "0.1.1")), "charts.vx": "pub fn f() {}\n"})
	return fmtRepo, charts
}

func TestTransitive(t *testing.T) {
	setup(t)
	fmtRepo, charts := transitive(t)
	// The project accepts any fmt from 0.1.0 and charts needs 0.1.1, so
	// 0.1.1 is chosen: the oldest that meets both, not the newest.
	app := newProject(t, dep("charts", charts, "0.1"), dep("format", fmtRepo, "0.1"))
	if err := Get(app, nil, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	lock := read(t, filepath.Join(app, LockFile))
	if !strings.Contains(lock, "name = \"fmt\"\ngit = \""+fmtRepo.url()+"\"\nversion = \"0.1.1\"") || !strings.Contains(lock, "name = \"charts\"") {
		t.Errorf("lock file:\n%s", lock)
	}
	pkgs, err := Resolve(app, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if pkgs["charts"].Deps["fmt"] != pkgs["format"] || pkgs["format"].Name != "fmt" {
		t.Errorf("charts and the project don't share fmt: %+v", pkgs)
	}

	// Only the project's own requirement: still the oldest that meets it.
	app2 := newProject(t, dep("fmt", fmtRepo, "0.1"))
	if err := Get(app2, nil, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if lock := read(t, filepath.Join(app2, LockFile)); !strings.Contains(lock, `version = "0.1.0"`) {
		t.Errorf("lock file:\n%s", lock)
	}

	// Different major versions can't be used together.
	app3 := newProject(t, dep("charts", charts, "0.1"), dep("fmt", fmtRepo, "1.0"))
	err = Get(app3, nil, "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "can't be used together") {
		t.Errorf("major versions: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(app3, LockFile)); statErr == nil {
		t.Errorf("a lock file was written after an error")
	}
}

func TestTamperedCache(t *testing.T) {
	setup(t)
	lib := newRepo(t, "lib")
	lib.release("v0.1.0", map[string]string{"package.toml": manifestFor("lib"), "lib.vx": "pub fn f() {}\n"})
	app := newProject(t)
	if err := Get(app, []string{lib.url()}, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	pkgs, err := Resolve(app, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(pkgs["lib"].Dir, "lib.vx")
	os.WriteFile(file, []byte("pub fn evil() {}\n"), 0o644)
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "lib.git 0.1.0 doesn't match package.lock") {
		t.Errorf("changed file: %v", err)
	}

	// The tag now points to other code: downloading it again is rejected too.
	os.RemoveAll(pkgs["lib"].Dir)
	lib.release("v0.1.0", map[string]string{"lib.vx": "pub fn other() {}\n"})
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "tag v0.1.0 now points to other code") {
		t.Errorf("moved tag: %v", err)
	}
	if err := Get(app, nil, "", io.Discard); err == nil || !strings.Contains(err.Error(), "doesn't match package.lock") {
		t.Errorf("vex get with a moved tag: %v", err)
	}
}

func TestLockErrors(t *testing.T) {
	setup(t)
	fmtRepo, charts := transitive(t)
	app := newProject(t, dep("charts", charts, "0.1"))
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "there is no package.lock yet; run 'vex get'") {
		t.Errorf("no lock file: %v", err)
	}
	if err := Get(app, nil, "", io.Discard); err != nil {
		t.Fatal(err)
	}

	// A dependency added by hand, without running vex get.
	toml := filepath.Join(app, "package.toml")
	orig := read(t, toml)
	os.WriteFile(toml, []byte(orig+dep("extra", newRepo(t, "extra"), "1.0")+"\n"), 0o644)
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "the dependency 'extra' isn't in package.lock; run 'vex get' to add it") {
		t.Errorf("missing entry: %v", err)
	}

	// A requirement raised by hand.
	os.WriteFile(toml, []byte(orig+dep("fmt", fmtRepo, "0.1.2")+"\n"), 0o644)
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "package.toml asks for fmt 0.1.2, but package.lock has 0.1.1") {
		t.Errorf("newer requirement: %v", err)
	}

	// An indirect dependency missing from the lock file.
	os.WriteFile(toml, []byte(orig), 0o644)
	lock := filepath.Join(app, LockFile)
	text := read(t, lock)
	i := strings.Index(text, "\n[[package]]\nname = \"fmt\"")
	os.WriteFile(lock, []byte(text[:i]), 0o644)
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "'fmt', which charts depends on, isn't in package.lock") {
		t.Errorf("missing indirect entry: %v", err)
	}
}

func TestOfflineRebuild(t *testing.T) {
	setup(t)
	fmtRepo, charts := transitive(t)
	app := newProject(t, dep("charts", charts, "0.1"))
	if err := Get(app, nil, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	// Without the repositories, the cache is enough, for builds and for
	// vex get when every version is exact.
	os.RemoveAll(fmtRepo.bare)
	os.RemoveAll(charts.bare)
	if _, err := Resolve(app, io.Discard); err != nil {
		t.Fatal(err)
	}
	app2 := newProject(t, dep("charts", charts, "0.1.0"))
	if err := Get(app2, nil, "", io.Discard); err != nil {
		t.Fatal(err)
	}

	// A fresh clone of the project with an empty cache downloads what's missing.
	setup(t)
	if _, err := Resolve(app, io.Discard); err == nil || !strings.Contains(err.Error(), "can't download") {
		t.Errorf("download without the repository: %v", err)
	}
}

func TestDownloadMissing(t *testing.T) {
	setup(t)
	_, charts := transitive(t)
	app := newProject(t, dep("charts", charts, "0.1"))
	if err := Get(app, nil, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VEX_CACHE", filepath.Join(t.TempDir(), "fresh"))
	var log strings.Builder
	if _, err := Resolve(app, &log); err != nil {
		t.Fatal(err)
	}
	if got := log.String(); !strings.Contains(got, "Downloading "+charts.url()+" 0.1.0\n") || !strings.Contains(got, "fmt.git 0.1.1") {
		t.Errorf("log %q", got)
	}
}

func TestManifestErrors(t *testing.T) {
	tests := []struct{ dep, want string }{
		{`ui = "0.3"`, "the dependency 'ui' must be a table in braces"},
		{`ui = { version = "0.3" }`, "the dependency 'ui' needs the git repository it comes from"},
		{`ui = { git = "x", version = "latest" }`, `the dependency 'ui' needs a version such as "1.0" or "0.3.1"`},
		{`ui = { git = "x", version = "1", branch = "main" }`, "unknown key 'branch' in dependency 'ui'"},
		{`ui = { git = "x", path = "../y", version = "1" }`, "the path '../y' of 'ui' isn't a folder inside the repository"},
		{`fn = { git = "x", version = "1" }`, "'fn' isn't a valid package name"},
	}
	for _, tt := range tests {
		dir := newProject(t, tt.dep)
		_, err := readManifest(dir)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want %q", tt.dep, err, tt.want)
		}
	}
	dir := newProject(t, `ui = { git = "x", version = "1" }`, `ui = { git = "y", version = "1" }`)
	if _, err := readManifest(dir); err == nil || !strings.Contains(err.Error(), "'ui' is already a dependency on line 5") {
		t.Errorf("twice: %v", err)
	}
}

func TestVersions(t *testing.T) {
	for _, s := range []string{"1", "0.3", "v0.3.1", "10.20.30"} {
		if _, ok := parseVersion(s); !ok {
			t.Errorf("%s isn't parsed", s)
		}
	}
	for _, s := range []string{"", "1.2.3.4", "01.2", "1.-2", "1.x", "v", "1..2", "+1"} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("%q is parsed", s)
		}
	}
	v, _ := parseVersion("0.3.2")
	min, _ := parseVersion("0.3")
	if !v.satisfies(min) || !v.matches(min) {
		t.Errorf("0.3.2 should meet 0.3")
	}
	if w, _ := parseVersion("1.0.0"); w.satisfies(min) {
		t.Errorf("1.0.0 shouldn't meet 0.3")
	}
}

func TestSetDependency(t *testing.T) {
	d := &dependency{key: "ui", src: source{git: "https://example.com/ui"}, want: "0.3.0"}
	line := `ui = { git = "https://example.com/ui", version = "0.3.0" }`
	tests := []struct{ src, want string }{
		{"[package]\nname = \"app\"\n", "[package]\nname = \"app\"\n\n[dependencies]\n" + line + "\n"},
		{"[package]\nname = \"app\"\n\n[dependencies]\n# none yet\n\n[c]\nlibs = []\n",
			"[package]\nname = \"app\"\n\n[dependencies]\n# none yet\n" + line + "\n\n[c]\nlibs = []\n"},
		{"[dependencies]\nui = { git = \"old\", version = \"0.1\" } # pinned\nx = { git = \"x\", version = \"1\" }\n",
			"[dependencies]\n" + line + "\nx = { git = \"x\", version = \"1\" }\n"},
	}
	for _, tt := range tests {
		if got := setDependency(tt.src, d); got != tt.want {
			t.Errorf("\n got %q\nwant %q", got, tt.want)
		}
	}
}

func TestSourceID(t *testing.T) {
	tests := []struct {
		src  source
		want string
	}{
		{source{git: "https://github.com/ann/charts"}, "github.com/ann/charts"},
		{source{git: "https://github.com/ann/charts.git/"}, "github.com/ann/charts"},
		{source{git: "git@github.com:ann/charts.git"}, "github.com/ann/charts"},
		{source{git: "https://github.com/vex-lang/packages", path: "camera"}, "github.com/vex-lang/packages/camera"},
		{source{git: "file:///srv/git/lib.git"}, "local/srv/git/lib"},
		{source{git: "../lib"}, "local/home/lib"},
		{source{git: "https://evil.example/../../x"}, "evil.example/_/_/x"},
	}
	for _, tt := range tests {
		if got := tt.src.id("/home/app"); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestWindowsDrives(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters exist only on Windows")
	}
	for _, git := range []string{"file:///C:/git/lib.git", "file://C:/git/lib.git", `C:\git\lib.git`} {
		s := source{git: git}
		if got := s.id(`D:\app`); got != "local/C_/git/lib" {
			t.Errorf("%s: id %q", git, got)
		}
		if got := s.url(`D:\app`); got != "file:///C:/git/lib.git" && got != git {
			t.Errorf("%s: url %q", git, got)
		}
	}
}
