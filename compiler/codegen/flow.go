package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// A block or loop around the statement being generated. A jump out of it
// first runs the block's deferred blocks, last first, or stops the
// loop's stream.
type frame struct {
	defers []*ast.DeferStmt
	loop   *loopInfo
}

// A loop being generated.
type loopInfo struct {
	// The loop's name, or empty.
	label string
	id    int

	// The stream a for await loop reads, or empty.
	stream string

	// Set once a labelled break or continue jumps to it.
	breaks, conts bool
}

// Returns the body of the loop [s].
func loopBody(s ast.Stmt) *ast.Block {
	switch s := s.(type) {
	case *ast.ForStmt:
		return s.Body
	case *ast.WhileStmt:
		return s.Body
	case *ast.RepeatStmt:
		return s.Body
	}
	return nil
}

// Returns the loop whose body is [b], recording it on first use.
func (g *gen) loopOf(b *ast.Block) *loopInfo {
	if g.loopBodies == nil {
		g.loopBodies = map[*ast.Block]*loopInfo{}
	}
	if info := g.loopBodies[b]; info != nil {
		return info
	}
	g.nloops++
	info := &loopInfo{id: g.nloops}
	g.loopBodies[b] = info
	return info
}

// Writes [stmts] as a block of their own, then [tail], if set, and then
// the blocks they deferred, last first.
func (g *gen) stmtsIn(stmts []ast.Stmt, tail func()) {
	f := &frame{}
	g.frames = append(g.frames, f)
	for _, s := range stmts {
		g.stmt(s)
	}
	if tail != nil {
		tail()
	}
	g.frames = g.frames[:len(g.frames)-1]
	g.runDefers(f)
}

// Writes the blocks [f] deferred, last first.
func (g *gen) runDefers(f *frame) {
	for i := len(f.defers) - 1; i >= 0; i-- {
		g.block("", f.defers[i].Body)
	}
}

// Reports whether a return here must do work first.
func (g *gen) hasCleanup() bool {
	for _, f := range g.frames {
		if len(f.defers) > 0 || f.loop != nil && f.loop.stream != "" {
			return true
		}
	}
	return false
}

// Returns the C code that leaves the frames from index [from] up: their
// deferred blocks run and their streams stop.
func (g *gen) unwind(from int) string {
	outer, ind := g.b, g.ind
	var b strings.Builder
	g.b, g.ind = &b, 0
	saved := g.frames
	// The jump leaves, so what the deferred blocks assign tells nothing
	// about the code after it.
	if g.rng != nil {
		facts := g.rng.save()
		defer func() { g.rng.vals = facts }()
	}
	for i := len(saved) - 1; i >= from; i-- {
		f := saved[i]
		// A deferred block is generated where the frames around it
		// still apply, so its own loops work.
		g.frames = saved[:i:i]
		g.runDefers(f)
		if f.loop != nil && f.loop.stream != "" {
			g.line("vx_stream_stop(%s);", f.loop.stream)
		}
	}
	g.frames = saved
	g.b, g.ind = outer, ind
	return strings.TrimSpace(strings.ReplaceAll(b.String(), "\n", " ")) + " "
}

// Returns the C code a return or throw runs first, leaving every frame.
func (g *gen) leaving() string {
	if !g.hasCleanup() {
		return ""
	}
	return g.unwind(0)
}

// Writes `break` or `continue`, with the loop [name] names, if any.
func (g *gen) jump(name *ast.Ident, cont bool) {
	t := len(g.frames) - 1
	for ; t >= 0; t-- {
		if l := g.frames[t].loop; l != nil && (name == nil || l.label == name.Name) {
			break
		}
	}
	if t < 0 {
		// Inside a loop the frames don't know about, as in a builder block.
		if cont {
			g.line("continue;")
		} else {
			g.line("break;")
		}
		return
	}
	info := g.frames[t].loop
	pre := ""
	if t+1 < len(g.frames) {
		pre = g.unwind(t + 1)
	}
	switch {
	case name == nil && cont:
		g.line("%scontinue;", pre)
	case name == nil:
		g.line("%sbreak;", pre)
	case cont:
		info.conts = true
		g.line("%sgoto cont_%d;", pre, info.id)
	default:
		info.breaks = true
		if info.stream != "" {
			pre += fmt.Sprintf("vx_stream_stop(%s); ", info.stream)
		}
		g.line("%sgoto brk_%d;", pre, info.id)
	}
}
