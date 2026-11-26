package tests

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/parser"
	"github.com/irshadio/vex-lang/compiler/types"
)

var mapCount = flag.Int("maps", 6, "number of random map programs TestMapOperations runs")

// Runs random sequences of map operations and compares the output with a
// model of the same map in Go. The key ranges are small, so keys are
// removed and added again often, and loops change the map they visit.
func TestMapOperations(t *testing.T) {
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	seeds := make([]int64, *mapCount)
	for i := range seeds {
		seeds[i] = int64(i + 1)
	}
	if *genSeed != 0 {
		seeds = []int64{*genSeed}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			t.Parallel()
			src, want := mapProgram(seed)
			dir := t.TempDir()
			file := diag.NewFile("main.vx", src)
			tree, err := parser.Parse(file)
			if err != nil {
				t.Fatalf("doesn't parse:\n%v\nprogram: %s", err, keep(t, src))
			}
			info, err := types.Check(file, tree)
			if err != nil {
				t.Fatalf("doesn't type check:\n%v\nprogram: %s", err, keep(t, src))
			}
			exe := filepath.Join(dir, "main")
			if err := cc.Compile(codegen.Generate(info, "main.vx"), exe); err != nil {
				t.Fatal(err)
			}
			for _, env := range [][]string{nil, {"VEX_GC_STRESS=3", "VEX_GC_VERIFY=1"}} {
				cmd := exec.Command(exe)
				cmd.Env = append(os.Environ(), env...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				got, err := cmd.Output()
				if err != nil {
					t.Fatalf("%s %v\n%s\nprogram: %s", env, err, stderr.String(), keep(t, src))
				}
				if string(got) != want {
					t.Fatalf("%s output differs at %s\nprogram: %s", env, firstDiff(string(got), want), keep(t, src))
				}
			}
		})
	}
}

type mapEntry struct {
	key   int
	value int
	seq   int
	alive bool
}

// An insertion-ordered map of int keys. String-keyed maps use the same
// model with keys shown as "k<n>".
type mapModel struct {
	str     bool
	entries []*mapEntry
	index   map[int]*mapEntry
	seq     int
}

func newMapModel(str bool) *mapModel { return &mapModel{str: str, index: map[int]*mapEntry{}} }

func (m *mapModel) set(k, v int) {
	if e := m.index[k]; e != nil {
		e.value = v
		return
	}
	m.seq++
	e := &mapEntry{key: k, value: v, seq: m.seq, alive: true}
	m.entries = append(m.entries, e)
	m.index[k] = e
}

func (m *mapModel) remove(k int) (int, bool) {
	e := m.index[k]
	if e == nil {
		return 0, false
	}
	e.alive = false
	delete(m.index, k)
	return e.value, true
}

func (m *mapModel) live() []*mapEntry {
	var out []*mapEntry
	for _, e := range m.entries {
		if e.alive {
			out = append(out, e)
		}
	}
	return out
}

// Returns [k] as Vex prints it inside a map or list.
func (m *mapModel) repr(k int) string {
	if m.str {
		return fmt.Sprintf("%q", fmt.Sprintf("k%d", k))
	}
	return fmt.Sprint(k)
}

// Returns [k] as Vex prints it on its own.
func (m *mapModel) show(k int) string {
	if m.str {
		return fmt.Sprintf("k%d", k)
	}
	return fmt.Sprint(k)
}

func (m *mapModel) String() string {
	live := m.live()
	if len(live) == 0 {
		return "[:]"
	}
	var parts []string
	for _, e := range live {
		parts = append(parts, m.repr(e.key)+": "+fmt.Sprint(e.value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func optString(v int, ok bool) string {
	if !ok {
		return "nil"
	}
	return fmt.Sprint(v)
}

// Returns a program of random map operations and its expected output.
func mapProgram(seed int64) (string, string) {
	r := rand.New(rand.NewSource(seed))
	var src, out strings.Builder
	src.WriteString("fn main() {\n    var a: [int: int] = [:]\n    var b: [string: int] = [:]\n")
	models := map[string]*mapModel{"a": newMapModel(false), "b": newMapModel(true)}
	ranges := []int{8, 64, 500}
	line := func(format string, args ...any) { fmt.Fprintf(&src, "    "+format+"\n", args...) }
	for range 400 {
		name := "a"
		if r.Intn(2) == 0 {
			name = "b"
		}
		m := models[name]
		size := ranges[r.Intn(len(ranges))]
		// Returns the Vex source of key [k], or of an int expression [x]
		// used as a key.
		key := func(k int) string {
			if m.str {
				return fmt.Sprintf(`"k%d"`, k)
			}
			return fmt.Sprint(k)
		}
		keyOf := func(x string) string {
			if m.str {
				return `"k{` + x + `}"`
			}
			return x
		}
		k, v := r.Intn(size), r.Intn(1000)
		switch op := r.Intn(20); {
		case op < 6:
			line("%s[%s] = %d", name, key(k), v)
			m.set(k, v)
		case op < 9:
			line("println(%s[%s])", name, key(k))
			e := m.index[k]
			fmt.Fprintln(&out, optString(func() (int, bool) {
				if e == nil {
					return 0, false
				}
				return e.value, true
			}()))
		case op < 11:
			line("println(%s.remove(%s))", name, key(k))
			fmt.Fprintln(&out, optString(m.remove(k)))
		case op < 12:
			line("println(%s.contains(%s), %s.len)", name, key(k), name)
			fmt.Fprintln(&out, m.index[k] != nil, len(m.index))
		case op < 13:
			line("println(%s)", name)
			fmt.Fprintln(&out, m)
		case op < 14:
			line("println(%s.keys, %s.values)", name, name)
			var ks, vs []string
			for _, e := range m.live() {
				ks = append(ks, m.repr(e.key))
				vs = append(vs, fmt.Sprint(e.value))
			}
			fmt.Fprintf(&out, "[%s] [%s]\n", strings.Join(ks, ", "), strings.Join(vs, ", "))
		case op < 16:
			n, mul := r.Intn(2*size)+1, r.Intn(50)+1
			line("for i in 0..%d {", n)
			line("    %s[%s] = i", name, keyOf(fmt.Sprintf("i * %d %% %d", mul, size)))
			line("}")
			for i := range n {
				m.set(i*mul%size, i)
			}
		case op < 17:
			n, every := r.Intn(2*size)+1, r.Intn(4)+1
			line("for i in 0..%d {", n)
			line("    if i %% %d == 0 {", every)
			line("        %s.remove(%s)", name, keyOf("i"))
			line("    }")
			line("}")
			for i := range n {
				if i%every == 0 {
					m.remove(i)
				}
			}
		case op < 19:
			// A loop that removes entries, adds new ones (which can grow and
			// compact the map) and may stop early.
			stop, d, mul, e := r.Intn(9), r.Intn(size), r.Intn(5)+1, r.Intn(size)
			line("for k, v in %s {", name)
			line("    if v %% 9 == %d {", stop)
			line("        break")
			line("    }")
			line(`    print(k, v, "")`)
			line("    %s.remove(%s)", name, keyOf(fmt.Sprintf("(v + %d) %% %d", d, size)))
			line("    %s[%s] = v + 1", name, keyOf(fmt.Sprintf("(v * %d + %d) %% %d", mul, e, size*2)))
			line("}")
			line("println()")
			limit := m.seq + 1
			for i := 0; i < len(m.entries); i++ {
				ent := m.entries[i]
				if !ent.alive || ent.seq >= limit {
					continue
				}
				if ent.value%9 == stop {
					break
				}
				fmt.Fprintf(&out, "%s %d ", m.show(ent.key), ent.value)
				val := ent.value
				m.remove((val + d) % size)
				m.set((val*mul+e)%(size*2), val+1)
			}
			fmt.Fprintln(&out)
		default:
			ks := r.Perm(size)[:min(size, r.Intn(4))]
			var parts []string
			fresh := newMapModel(m.str)
			for _, k := range ks {
				v := r.Intn(100)
				parts = append(parts, key(k)+": "+fmt.Sprint(v))
				fresh.set(k, v)
			}
			lit := "[:]"
			if len(parts) > 0 {
				lit = "[" + strings.Join(parts, ", ") + "]"
			}
			line("%s = %s", name, lit)
			models[name] = fresh
		}
	}
	src.WriteString("}\n")
	return src.String(), out.String()
}
