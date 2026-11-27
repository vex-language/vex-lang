package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// A named loop around the statement being checked.
type label struct {
	name string

	// The loop depth inside the loop.
	depth int
}

// Checks `break` or `continue`, written [word], with the loop it names,
// if any.
func (c *checker) jump(name *ast.Ident, word string, s ast.Node) {
	if c.loops == 0 {
		if c.inDefer {
			c.errorAt(s, "a defer block runs as its block ends, so it can't leave a loop around it", "%s can only be used inside a loop", word)
			return
		}
		c.errorAt(s, "", "%s can only be used inside a loop", word)
		return
	}
	if name == nil {
		return
	}
	for i := len(c.labels) - 1; i >= 0; i-- {
		if c.labels[i].name == name.Name {
			return
		}
	}
	c.errorAt(name, "name the loop: "+name.Name+": for ...", "no loop around this is named '%s'", name.Name)
}

// Checks a loop with a name.
func (c *checker) labeled(s *ast.LabeledStmt) {
	for _, l := range c.labels {
		if l.name == s.Label.Name {
			c.errorAt(s.Label, "give the inner loop another name", "a loop around this one is already named '%s'", s.Label.Name)
		}
	}
	c.labels = append(c.labels, label{name: s.Label.Name, depth: c.loops + 1})
	c.stmt(s.Loop)
	c.labels = c.labels[:len(c.labels)-1]
}

// Checks `defer { ... }`. Its block can't leave with return, throw, or a
// break or continue of a loop around it.
func (c *checker) deferStmt(s *ast.DeferStmt) {
	loops, labels, inDefer := c.loops, c.labels, c.inDefer
	c.loops, c.labels, c.inDefer = 0, nil, true
	c.stmt(s.Body)
	c.loops, c.labels, c.inDefer = loops, labels, inDefer
}
