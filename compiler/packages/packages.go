// Package packages downloads the packages a project depends on from their
// git repositories into a shared cache, records their exact versions and
// content hashes in [LockFile], and checks them before every build.
package packages

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vex-language/vex-lang/compiler/modules"
)

// Finds, downloads and checks versions of packages for one project.
type resolver struct {
	// The project folder.
	root  string
	cache string
	log   io.Writer

	// The lock file as it was, by source id; nil when there is none.
	lock map[string]*lockEntry

	versions map[string][]version
	fetched  map[string]*fetched
}

// One version of a package, in the cache and checked.
type fetched struct {
	src      source
	version  version
	dir      string
	commit   string
	hash     string
	manifest *manifest
}

func newResolver(root string, log io.Writer) (*resolver, error) {
	cache, err := cacheDir()
	if err != nil {
		return nil, err
	}
	lock, err := readLock(root, root)
	if err != nil {
		return nil, err
	}
	return &resolver{root: root, cache: cache, log: log, lock: lock,
		versions: map[string][]version{}, fetched: map[string]*fetched{}}, nil
}

// Returns the tagged versions of [s], oldest first.
func (r *resolver) tags(s source) ([]version, error) {
	id := s.id(r.root)
	if vs, ok := r.versions[id]; ok {
		return vs, nil
	}
	vs, err := listVersions(s, r.root)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, fmt.Errorf("%s has no version tags; tag a release with: git tag %s", s, s.tag(version{0, 1, 0, 3}))
	}
	r.versions[id] = vs
	return vs, nil
}

// Returns the oldest version of [s] that meets [min]. A requirement with
// all three numbers is taken as it is, without asking the repository.
func (r *resolver) lowest(s source, min version) (version, error) {
	if min.parts == 3 {
		return min, nil
	}
	vs, err := r.tags(s)
	if err != nil {
		return version{}, err
	}
	for _, v := range vs {
		if v.satisfies(min) {
			return v, nil
		}
	}
	return version{}, fmt.Errorf("%s has no version %s or newer before %d.0.0; its newest is %s", s, min, min.major+1, vs[len(vs)-1])
}

// Returns version [v] of [s], downloading it into the cache when missing,
// and checks it against the lock file.
func (r *resolver) fetch(s source, v version) (*fetched, error) {
	id := s.id(r.root)
	key := id + "@" + v.String()
	if f := r.fetched[key]; f != nil {
		return f, nil
	}
	dir := filepath.Join(r.cache, filepath.FromSlash(key))
	commit := ""
	if _, err := os.Stat(dir); err == nil {
		data, _ := os.ReadFile(dir + ".commit")
		commit = strings.TrimSpace(string(data))
	} else {
		fmt.Fprintf(r.log, "Downloading %s %s\n", s, v)
		if err := os.MkdirAll(r.cache, 0o755); err != nil {
			return nil, err
		}
		if commit, err = download(s, v, r.root, dir, r.cache); err != nil {
			return nil, err
		}
		if err := writeFile(dir+".commit", commit+"\n"); err != nil {
			return nil, err
		}
	}
	hash, err := hashDir(dir)
	if err != nil {
		return nil, err
	}
	if e := r.lock[id]; e != nil && e.version == v {
		if e.hash != hash {
			return nil, fmt.Errorf("%s %s doesn't match %s:\n  locked hash: %s\n  found hash:  %s\n"+
				"Either the files in %s were changed, or tag %s now points to other code.\n"+
				"Delete that folder to download it again. If the tag really changed, check the new code, then remove its entry from %s and run 'vex get'.",
				s, v, LockFile, e.hash, hash, dir, s.tag(v), LockFile)
		}
		if commit == "" {
			commit = e.commit
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	f := &fetched{src: s, version: v, dir: dir, commit: commit, hash: hash, manifest: m}
	r.fetched[key] = f
	return f, nil
}

// A requirement on a package while choosing versions.
type requirement struct {
	src  source
	min  version
	want string

	// Who asks for it, for messages.
	from string
}

// Chooses one version of every package [m] depends on, directly or not:
// the oldest that meets every requirement on it (minimal version
// selection).
func (r *resolver) choose(m *manifest) ([]*fetched, error) {
	reqs := map[string]*requirement{}
	var order []string
	add := func(d *dependency, from string) error {
		id := d.src.id(r.root)
		cur := reqs[id]
		if cur == nil {
			reqs[id] = &requirement{d.src, d.min, d.want, from}
			order = append(order, id)
			return nil
		}
		if cur.min.major != d.min.major {
			return fmt.Errorf("%s needs %s %s, but %s needs %s.\nA build holds one version of each package, so these can't be used together.",
				cur.from, d.src, cur.want, from, d.want)
		}
		if cur.min.less(d.min) || cur.min == d.min && d.min.parts == 3 {
			cur.min, cur.want, cur.from = d.min, d.want, from
		}
		return nil
	}
	for _, d := range m.deps {
		if err := add(d, modules.ProjectFile); err != nil {
			return nil, err
		}
	}
	chosen := map[string]*fetched{}
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(order); i++ {
			req := reqs[order[i]]
			v, err := r.lowest(req.src, req.min)
			if err != nil {
				return nil, err
			}
			if f := chosen[order[i]]; f != nil && f.version == v {
				continue
			}
			f, err := r.fetch(req.src, v)
			if err != nil {
				return nil, err
			}
			chosen[order[i]] = f
			changed = true
			for _, d := range f.manifest.deps {
				if err := add(d, f.manifest.name+" "+v.String()); err != nil {
					return nil, err
				}
			}
		}
	}
	var out []*fetched
	for _, id := range order {
		out = append(out, chosen[id])
	}
	return out, nil
}

// Adds the packages in [args] to the project in folder [dir], then
// chooses the version of every dependency, downloads them, and writes
// [LockFile]. Each argument is a git repository with an optional
// `@version`; without one the newest tag is used. [path] is the folder
// inside those repositories, for repositories that hold several packages.
// Progress goes to [log].
func Get(dir string, args []string, path string, log io.Writer) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	if path != "" && !validPath(path) {
		return fmt.Errorf("the path '%s' isn't a folder inside the repository", path)
	}
	r, err := newResolver(dir, log)
	if err != nil {
		return err
	}
	text := m.file.Src
	for _, arg := range args {
		repo, want := arg, ""
		if i := strings.LastIndex(arg, "@"); i > strings.LastIndexAny(arg, "/:") {
			repo, want = arg[:i], arg[i+1:]
		}
		if repo == "" || strings.HasPrefix(repo, "-") {
			return fmt.Errorf("'%s' isn't a git repository", repo)
		}
		src := source{git: repo, path: path}
		v, err := r.pick(src, want)
		if err != nil {
			return err
		}
		f, err := r.fetch(src, v)
		if err != nil {
			return err
		}
		d := &dependency{key: f.manifest.name, src: src, want: v.String(), min: v}
		replaced := false
		for i, old := range m.deps {
			switch {
			case old.src.id(r.root) == src.id(r.root):
				d.key = old.key
				m.deps[i] = d
				replaced = true
			case old.key == d.key:
				return fmt.Errorf("%s already has a dependency named '%s', from %s.\nTo use both, add this one to %s by hand under another name:\n  %s",
					modules.ProjectFile, d.key, old.src, modules.ProjectFile, (&dependency{key: d.key + "2", src: src, want: d.want}).line())
			}
		}
		if !replaced {
			m.deps = append(m.deps, d)
		}
		text = setDependency(text, d)
		fmt.Fprintf(log, "Added %s %s\n", d.key, v)
	}
	chosen, err := r.choose(m)
	if err != nil {
		return err
	}
	var entries []*lockEntry
	for _, f := range chosen {
		entries = append(entries, &lockEntry{name: f.manifest.name, src: f.src, version: f.version, commit: f.commit, hash: f.hash})
	}
	if text != m.file.Src {
		if err := writeFile(filepath.Join(dir, modules.ProjectFile), text); err != nil {
			return err
		}
	}
	if err := writeLock(dir, entries); err != nil {
		return err
	}
	noun := "packages"
	if len(entries) == 1 {
		noun = "package"
	}
	fmt.Fprintf(log, "Locked %d %s in %s\n", len(entries), noun, LockFile)
	return nil
}

// Returns the version of [s] that `vex get` adds for [want]: the newest
// tag starting with [want], or the newest of all when it is empty.
func (r *resolver) pick(s source, want string) (version, error) {
	var prefix version
	if want != "" {
		var ok bool
		if prefix, ok = parseVersion(want); !ok {
			return version{}, fmt.Errorf("'%s' isn't a version; write it like 1.2.0 or 1.2", want)
		}
		if prefix.parts == 3 {
			return prefix, nil
		}
	}
	vs, err := r.tags(s)
	if err != nil {
		return version{}, err
	}
	for i := len(vs) - 1; i >= 0; i-- {
		if want == "" || vs[i].matches(prefix) {
			return vs[i], nil
		}
	}
	return version{}, fmt.Errorf("%s has no version %s; its newest is %s", s, want, vs[len(vs)-1])
}

// Returns the packages the project in folder [dir] depends on, by their
// key in its project file, after checking each against [LockFile]. Those
// missing from the cache are downloaded, with progress going to [log].
// Returns nil for a single file or a project without dependencies.
func Resolve(dir string, log io.Writer) (map[string]*modules.Package, error) {
	if info, err := os.Stat(filepath.Join(dir, modules.ProjectFile)); err != nil || info.IsDir() {
		return nil, nil
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	m, err := readManifest(dir)
	if err != nil || len(m.deps) == 0 {
		return nil, err
	}
	r, err := newResolver(dir, log)
	if err != nil {
		return nil, err
	}
	if r.lock == nil {
		return nil, fmt.Errorf("%s lists dependencies, but there is no %s yet; run 'vex get' to download them", modules.ProjectFile, LockFile)
	}
	pkgs := map[string]*modules.Package{}
	var deps func(m *manifest, owner string) (map[string]*modules.Package, error)
	deps = func(m *manifest, owner string) (map[string]*modules.Package, error) {
		out := map[string]*modules.Package{}
		for _, d := range m.deps {
			id := d.src.id(r.root)
			e := r.lock[id]
			if e == nil {
				if owner == "" {
					return nil, fmt.Errorf("the dependency '%s' isn't in %s; run 'vex get' to add it", d.key, LockFile)
				}
				return nil, fmt.Errorf("'%s', which %s depends on, isn't in %s; run 'vex get' to add it", d.key, owner, LockFile)
			}
			if !e.version.satisfies(d.min) {
				who := owner
				if who == "" {
					who = modules.ProjectFile
				}
				return nil, fmt.Errorf("%s asks for %s %s, but %s has %s; run 'vex get' to update it", who, d.key, d.want, LockFile, e.version)
			}
			if p := pkgs[id]; p != nil {
				out[d.key] = p
				continue
			}
			f, err := r.fetch(e.src, e.version)
			if err != nil {
				return nil, err
			}
			p := &modules.Package{Name: f.manifest.name, Dir: f.dir}
			pkgs[id] = p
			if p.Deps, err = deps(f.manifest, f.manifest.name); err != nil {
				return nil, err
			}
			out[d.key] = p
		}
		return out, nil
	}
	return deps(m, "")
}
