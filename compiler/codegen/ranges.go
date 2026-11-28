package codegen

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/types"
)

// The values an int can take, from lo to hi inclusive.
type interval struct{ lo, hi int64 }

var anyInt = interval{math.MinInt64, math.MaxInt64}

// What is known about the ints and list lengths of the function being
// generated, at the statement being generated. It lets the code leave out
// overflow and bounds checks that can't fail.
//
// Facts come from declarations, `for` ranges and conditions, and are
// dropped when a variable is assigned. Before a statement holding a loop
// or a branch, the facts about every variable it assigns are dropped, so
// nothing learned on one path or in one pass is used on another.
type ranges struct {
	info *types.Info
	body ast.Node

	vals map[*types.Var]interval
	// The lengths of lists that keep their length: declared with a list
	// literal and then only indexed, looped over or asked for their length.
	lens map[*types.Var]interval
	// Whether each list variable keeps its length, worked out when first
	// asked.
	keeps map[*types.Var]bool
}

func newRanges(info *types.Info, body ast.Node) *ranges {
	return &ranges{info: info, body: body, vals: map[*types.Var]interval{}, lens: map[*types.Var]interval{}, keeps: map[*types.Var]bool{}}
}

// Returns the variable [x] names, when it is one whose value only the
// function's own statements change.
func (r *ranges) local(x ast.Expr) *types.Var {
	id, ok := x.(*ast.Ident)
	if !ok {
		return nil
	}
	v, ok := r.info.Uses[id].(*types.Var)
	if !ok || v.Boxed() || v.Kind == types.SelfVar {
		return nil
	}
	return v
}

// Returns the values the int expression [e] can take.
func (r *ranges) of(e ast.Expr) interval {
	switch e := e.(type) {
	case *ast.IntLit:
		n, err := strconv.ParseInt(strings.ReplaceAll(e.Value, "_", ""), 0, 64)
		if err == nil {
			return interval{n, n}
		}
	case *ast.Ident:
		if v := r.local(e); v != nil {
			if iv, ok := r.vals[v]; ok {
				return iv
			}
		}
	case *ast.UnaryExpr:
		if e.Op == lexer.Minus {
			x := r.of(e.X)
			if x.lo != math.MinInt64 {
				return interval{-x.hi, -x.lo}
			}
		}
	case *ast.BinaryExpr:
		x, y := r.of(e.X), r.of(e.Y)
		switch e.Op {
		case lexer.Amp:
			// Masking with a value that can't be negative can't exceed it.
			switch {
			case x.lo >= 0 && y.lo >= 0:
				return interval{0, min(x.hi, y.hi)}
			case x.lo >= 0:
				return interval{0, x.hi}
			case y.lo >= 0:
				return interval{0, y.hi}
			}
		case lexer.Shr:
			if x.lo >= 0 && y.lo >= 0 && y.hi <= 63 {
				return interval{x.lo >> y.hi, x.hi >> y.lo}
			}
		}
		if iv, ok := combine(e.Op, x, y); ok {
			return iv
		}
	case *ast.MemberExpr:
		if e.Name.Name == "len" {
			if v := r.local(e.X); v != nil {
				if n, ok := r.lens[v]; ok {
					return n
				}
			}
			switch r.info.Types[e.X].(type) {
			case *types.List, *types.Map, *types.Set:
				return interval{0, math.MaxInt64}
			}
		}
	}
	return anyInt
}

// Returns the values [a] [op] [b] can take, and whether it can't overflow.
func combine(op lexer.Kind, a, b interval) (interval, bool) {
	var f func(x, y *big.Int) *big.Int
	switch op {
	case lexer.Plus:
		f = func(x, y *big.Int) *big.Int { return new(big.Int).Add(x, y) }
	case lexer.Minus:
		f = func(x, y *big.Int) *big.Int { return new(big.Int).Sub(x, y) }
	case lexer.Star:
		f = func(x, y *big.Int) *big.Int { return new(big.Int).Mul(x, y) }
	default:
		return anyInt, false
	}
	if op == lexer.Minus {
		b = interval{b.hi, b.lo}
	}
	lo, hi := big.NewInt(a.lo), big.NewInt(a.hi)
	blo, bhi := big.NewInt(b.lo), big.NewInt(b.hi)
	ends := []*big.Int{f(lo, blo), f(lo, bhi), f(hi, blo), f(hi, bhi)}
	min, max := ends[0], ends[0]
	for _, x := range ends[1:] {
		if x.Cmp(min) < 0 {
			min = x
		}
		if x.Cmp(max) > 0 {
			max = x
		}
	}
	if !min.IsInt64() || !max.IsInt64() {
		return anyInt, false
	}
	return interval{min.Int64(), max.Int64()}, true
}

// Reports whether [x] [op] [y] on ints can't overflow, or for a shift,
// whether [y] is always in 0 to 63.
func (r *ranges) safe(op lexer.Kind, x, y ast.Expr) bool {
	if op == lexer.Shl || op == lexer.Shr {
		n := r.of(y)
		return n.lo >= 0 && n.hi <= 63
	}
	_, ok := combine(op, r.of(x), r.of(y))
	return ok
}

// Reports whether [list][index] is always in bounds.
func (r *ranges) inBounds(list, index ast.Expr) bool {
	v := r.local(list)
	if v == nil {
		return false
	}
	n, ok := r.lens[v]
	if !ok {
		return false
	}
	i := r.of(index)
	return i.lo >= 0 && i.hi < n.lo
}

// Records what the declaration of [v] with [value] tells.
func (r *ranges) declare(v *types.Var, value ast.Expr) {
	if v == nil || v.Boxed() {
		return
	}
	switch r.info.Types[value].(type) {
	case *types.List:
		if n, ok := r.length(value); ok && r.keepsLength(v) {
			r.lens[v] = n
		}
		return
	}
	if r.info.Types[value] == types.Int {
		r.vals[v] = r.of(value)
	}
}

// Returns the length of the list literal [e], when it is known.
func (r *ranges) length(e ast.Expr) (interval, bool) {
	lit, ok := e.(*ast.ListLit)
	if !ok {
		return interval{}, false
	}
	if lit.Body == nil {
		return interval{int64(len(lit.Elems)), int64(len(lit.Elems))}, true
	}
	// A `for` over a range adding one element each pass.
	if len(lit.Body.Stmts) != 1 {
		return interval{}, false
	}
	fs, ok := lit.Body.Stmts[0].(*ast.ForStmt)
	if !ok || len(fs.Body.Stmts) != 1 {
		return interval{}, false
	}
	rg, ok := fs.Iter.(*ast.RangeExpr)
	if !ok {
		return interval{}, false
	}
	es, ok := fs.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || r.info.Elements[es] != lit {
		return interval{}, false
	}
	switch es.X.(type) {
	case *ast.SpreadExpr, *ast.OptionalElem:
		return interval{}, false
	}
	hi := rg.Hi
	n, ok := combine(lexer.Minus, r.of(hi), r.of(rg.Lo))
	if !ok {
		return interval{}, false
	}
	if rg.Inclusive {
		if n, ok = combine(lexer.Plus, n, interval{1, 1}); !ok {
			return interval{}, false
		}
	}
	if n.lo < 0 {
		n.lo = 0
	}
	if n.hi < 0 {
		n.hi = 0
	}
	return n, true
}

// Reports whether the list variable [v] keeps its length: it is never
// assigned, and each use of it indexes it, loops over it or asks for its
// length.
func (r *ranges) keepsLength(v *types.Var) bool {
	if keeps, ok := r.keeps[v]; ok {
		return keeps
	}
	allowed := map[*ast.Ident]bool{}
	ast.Inspect(r.body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IndexExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				allowed[id] = true
			}
		case *ast.MemberExpr:
			if id, ok := n.X.(*ast.Ident); ok && n.Name.Name == "len" {
				allowed[id] = true
			}
		case *ast.ForStmt:
			if id, ok := n.Iter.(*ast.Ident); ok {
				allowed[id] = true
			}
		}
		return true
	})
	keeps := true
	ast.Inspect(r.body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if id, ok := n.Target.(*ast.Ident); ok && r.info.Uses[id] == v {
				keeps = false
			}
		case *ast.Ident:
			if r.info.Uses[n] == v && !allowed[n] {
				keeps = false
			}
		}
		return keeps
	})
	r.keeps[v] = keeps
	return keeps
}

// Returns the variables [n] assigns, as targets of assignments.
func (r *ranges) assigned(n ast.Node) map[*types.Var]bool {
	vs := map[*types.Var]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		if s, ok := n.(*ast.AssignStmt); ok {
			if id, ok := s.Target.(*ast.Ident); ok {
				if v, ok := r.info.Uses[id].(*types.Var); ok {
					vs[v] = true
				}
			}
		}
		return true
	})
	return vs
}

// Drops the facts about the variables [n] assigns.
func (r *ranges) forget(n ast.Node) {
	for v := range r.assigned(n) {
		delete(r.vals, v)
	}
}

// Drops the facts about the variables [loop] assigns before its body runs,
// keeping a bound for a variable that only grows or only shrinks: one
// assigned only by += or -= of an amount that can't be negative.
func (r *ranges) enterLoop(loop ast.Node) {
	before := map[*types.Var]interval{}
	for v, iv := range r.vals {
		before[v] = iv
	}
	r.forget(loop)
	grows, shrinks := map[*types.Var]bool{}, map[*types.Var]bool{}
	for v := range r.assigned(loop) {
		grows[v], shrinks[v] = true, true
	}
	ast.Inspect(loop, func(n ast.Node) bool {
		s, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		id, ok := s.Target.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := r.info.Uses[id].(*types.Var)
		if !ok {
			return true
		}
		step := r.of(s.Value)
		if s.Op != lexer.PlusAssign || step.lo < 0 {
			grows[v] = false
		}
		if s.Op != lexer.MinusAssign || step.lo < 0 {
			shrinks[v] = false
		}
		return true
	})
	for v := range r.assigned(loop) {
		iv, ok := before[v]
		switch {
		case !ok:
		case grows[v]:
			r.vals[v] = interval{iv.lo, math.MaxInt64}
		case shrinks[v]:
			r.vals[v] = interval{math.MinInt64, iv.hi}
		}
	}
}

// Narrows the facts by what the condition [c] being true tells.
func (r *ranges) assume(c ast.Expr) {
	b, ok := c.(*ast.BinaryExpr)
	if !ok {
		return
	}
	if b.Op == lexer.AndAnd {
		r.assume(b.X)
		r.assume(b.Y)
		return
	}
	if r.info.Types[b.X] != types.Int || r.info.Types[b.Y] != types.Int {
		return
	}
	r.bound(b.Op, b.X, b.Y)
	if flipped, ok := flips[b.Op]; ok {
		r.bound(flipped, b.Y, b.X)
	}
}

var flips = map[lexer.Kind]lexer.Kind{lexer.Lt: lexer.Gt, lexer.LtEq: lexer.GtEq, lexer.Gt: lexer.Lt, lexer.GtEq: lexer.LtEq, lexer.Eq: lexer.Eq}

// Narrows the facts about [x], when it is a variable, by x [op] y.
func (r *ranges) bound(op lexer.Kind, x, y ast.Expr) {
	v := r.local(x)
	if v == nil {
		return
	}
	iv, ok := r.vals[v]
	if !ok {
		iv = anyInt
	}
	other := r.of(y)
	switch op {
	case lexer.Lt:
		if other.hi == math.MinInt64 {
			return
		}
		iv.hi = min(iv.hi, other.hi-1)
	case lexer.LtEq:
		iv.hi = min(iv.hi, other.hi)
	case lexer.Gt:
		if other.lo == math.MaxInt64 {
			return
		}
		iv.lo = max(iv.lo, other.lo+1)
	case lexer.GtEq:
		iv.lo = max(iv.lo, other.lo)
	case lexer.Eq:
		iv.lo, iv.hi = max(iv.lo, other.lo), min(iv.hi, other.hi)
	default:
		return
	}
	if iv.lo > iv.hi {
		// Can't happen on this path; keep what was known.
		return
	}
	r.vals[v] = iv
}

// Returns a copy of the facts about ints, to restore after a branch.
func (r *ranges) save() map[*types.Var]interval {
	c := make(map[*types.Var]interval, len(r.vals))
	for v, iv := range r.vals {
		c[v] = iv
	}
	return c
}
