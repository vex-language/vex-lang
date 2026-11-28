package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The name of the file that records the exact version of every dependency.
const LockFile = "package.lock"

// The exact version of one dependency, as recorded in the lock file.
type lockEntry struct {
	// The name the package gives itself.
	name    string
	src     source
	version version
	commit  string
	hash    string
}

// Reads the lock file in [dir], returning its entries by source id, or nil
// when there is none.
func readLock(dir, root string) (map[string]*lockEntry, error) {
	p := filepath.Join(dir, LockFile)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries := map[string]*lockEntry{}
	var cur *lockEntry
	finish := func(line int) error {
		if cur == nil {
			return nil
		}
		if cur.src.git == "" || strings.HasPrefix(cur.src.git, "-") || cur.src.path != "" && !validPath(cur.src.path) || cur.hash == "" || cur.version.parts != 3 {
			return fmt.Errorf("%s:%d: the entry above is incomplete; run 'vex get' to write the lock file again", p, line)
		}
		entries[cur.src.id(root)] = cur
		return nil
	}
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(stripComment(strings.TrimSuffix(raw, "\r")))
		switch {
		case line == "":
			continue
		case line == "[[package]]":
			if err := finish(i + 1); err != nil {
				return nil, err
			}
			cur = &lockEntry{}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		s, _, okVal := cutString(strings.TrimSpace(val))
		if !ok || !okVal || cur == nil {
			return nil, fmt.Errorf("%s:%d: can't read this line; run 'vex get' to write the lock file again", p, i+1)
		}
		switch strings.TrimSpace(key) {
		case "name":
			cur.name = s
		case "git":
			cur.src.git = s
		case "path":
			cur.src.path = s
		case "version":
			cur.version, _ = parseVersion(s)
		case "commit":
			cur.commit = s
		case "hash":
			cur.hash = s
		}
	}
	if err := finish(len(lines)); err != nil {
		return nil, err
	}
	return entries, nil
}

// Writes the lock file in [dir] with [entries], sorted by name and source.
func writeLock(dir string, entries []*lockEntry) error {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.name != b.name {
			return a.name < b.name
		}
		return a.src.String() < b.src.String()
	})
	var b strings.Builder
	b.WriteString("# Written by vex get; don't edit it by hand. Commit it with the project.\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "\n[[package]]\nname = %s\ngit = %s\n", quote(e.name), quote(e.src.git))
		if e.src.path != "" {
			fmt.Fprintf(&b, "path = %s\n", quote(e.src.path))
		}
		fmt.Fprintf(&b, "version = %s\ncommit = %s\nhash = %s\n", quote(e.version.String()), quote(e.commit), quote(e.hash))
	}
	return writeFile(filepath.Join(dir, LockFile), b.String())
}

// Writes [text] to the file at [p] in one step, so a failed write leaves
// the old file.
func writeFile(p, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+"-")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		mode = info.Mode().Perm()
	}
	os.Chmod(tmp.Name(), mode)
	return os.Rename(tmp.Name(), p)
}
