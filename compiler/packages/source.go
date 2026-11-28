package packages

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Where a package comes from: a git repository, and the folder inside it
// for repositories that hold several packages.
type source struct {
	git  string
	path string
}

// Returns the repository and folder without the URL scheme, such as
// `github.com/ann/charts`, which names the package in the cache. Local
// repositories are named by their absolute path under `local/`.
func (s source) id(root string) string {
	repo := strings.TrimSuffix(strings.TrimRight(s.git, "/"), ".git")
	if u, err := url.Parse(repo); err == nil && u.Scheme != "" && u.Scheme != "file" && u.Host != "" {
		repo = u.Host + u.Path
	} else if i := strings.Index(repo, "@"); i >= 0 && strings.Contains(repo[i:], ":") && !strings.Contains(repo, "://") {
		// An scp-like address such as git@github.com:ann/charts.
		repo = strings.Replace(repo[i+1:], ":", "/", 1)
	} else {
		repo = "local/" + strings.TrimPrefix(filepath.ToSlash(localPath(repo, root)), "/")
	}
	if s.path != "" {
		repo += "/" + s.path
	}
	return safePath.ReplaceAllString(repo, "_")
}

var safePath = regexp.MustCompile(`[^A-Za-z0-9._~+/-]|\.\.+`)

// Returns the folder of a local repository [repo], a `file://` URL or a
// path relative to [root].
func localPath(repo, root string) string {
	if strings.HasPrefix(repo, "file://") {
		if u, err := url.Parse(repo); err == nil {
			// On Windows both file:///C:/git and file://C:/git name C:\git.
			p := u.Path
			if filepath.VolumeName(u.Host) != "" {
				p = u.Host + p
			} else if len(p) > 1 && filepath.VolumeName(p[1:]) != "" {
				p = p[1:]
			}
			return filepath.FromSlash(p)
		}
	}
	if filepath.IsAbs(repo) {
		return repo
	}
	return filepath.Join(root, repo)
}

// Returns the address to give git for the repository.
func (s source) url(root string) string {
	if strings.Contains(s.git, "://") || strings.Contains(s.git, "@") {
		return s.git
	}
	p := filepath.ToSlash(localPath(s.git, root))
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// Returns the tag that marks version [v], such as `v0.3.1`, or
// `camera/v0.2.0` for a package in a folder.
func (s source) tag(v version) string {
	if s.path != "" {
		return s.path + "/v" + v.String()
	}
	return "v" + v.String()
}

// Returns the name of the package for messages.
func (s source) String() string {
	if s.path != "" {
		return s.git + " (" + s.path + ")"
	}
	return s.git
}

// Returns the folder that holds downloaded packages: `$VEX_CACHE`, or
// `~/.vex/pkg`.
func cacheDir() (string, error) {
	if dir := os.Getenv("VEX_CACHE"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("can't find the package cache: %v; set VEX_CACHE", err)
	}
	return filepath.Join(home, ".vex", "pkg"), nil
}

// Runs git with [args] and returns what it printed.
func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https:http:ssh:git:file")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			return "", errors.New("vex get needs git; install it and try again")
		}
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return out.String(), nil
}

// Returns the versions tagged in the repository of [s], oldest first.
func listVersions(s source, root string) ([]version, error) {
	out, err := git("ls-remote", "--tags", "--refs", "--", s.url(root))
	if err != nil {
		return nil, fmt.Errorf("can't read the versions of %s:\n%s", s, err)
	}
	prefix := "refs/tags/" + s.tag(version{})
	prefix = prefix[:len(prefix)-len("0.0.0")]
	var vs []version
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(ref, prefix) {
			continue
		}
		if v, ok := parseVersion(ref[len(prefix):]); ok && v.parts == 3 {
			vs = append(vs, v)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].less(vs[j]) })
	return vs, nil
}

// Downloads version [v] of [s] into folder [dest], which must not exist,
// and returns the commit it came from. [tmp] is a folder on the same disk
// for the work.
func download(s source, v version, root, dest, tmp string) (string, error) {
	work, err := os.MkdirTemp(tmp, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	clone := filepath.Join(work, "repo")
	if _, err := git("-c", "advice.detachedHead=false", "clone", "--quiet", "--depth", "1", "--branch", s.tag(v), "--", s.url(root), clone); err != nil {
		return "", fmt.Errorf("can't download %s %s:\n%s", s, v, err)
	}
	commit, err := git("-C", clone, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	from := filepath.Join(clone, filepath.FromSlash(s.path))
	if _, err := os.Stat(filepath.Join(from, "package.toml")); err != nil {
		where := "the root of the repository"
		if s.path != "" {
			where = "folder '" + s.path + "'"
		}
		return "", fmt.Errorf("%s %s has no package.toml in %s", s, v, where)
	}
	pkg := filepath.Join(work, "pkg")
	if err := copyTree(from, pkg); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(pkg, dest); err != nil {
		if _, statErr := os.Stat(dest); statErr == nil {
			// Downloaded meanwhile by another build.
			return strings.TrimSpace(commit), nil
		}
		return "", err
	}
	return strings.TrimSpace(commit), nil
}

// Copies the files in folder [from] to [to], leaving out git's own files
// and symbolic links.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		target := filepath.Join(to, rel)
		switch {
		case d.Name() == ".git" && p != from:
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// Returns the content hash of the files in folder [dir]: the SHA-256 of a
// list with the SHA-256 and path of each file, sorted by path.
func hashDir(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	sum := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(sum, "%x  %s\n", sha256.Sum256(data), f)
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil)), nil
}
