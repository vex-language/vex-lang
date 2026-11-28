package types

import "github.com/vex-language/vex-lang/compiler/ast"

// A list or map literal with a body, or a builder block, being checked.
type build struct {
	lit ast.Expr

	// Whether the elements are map entries. Until [decided] is set, only
	// spreads have been seen, and the first spread decides.
	isMap, decided bool

	// The element type of a list, or the key and value types of a map;
	// nil until known.
	elem, key, value Type
	count            int
}

// Checks a literal whose elements are the statements of [body], or a
// builder block, against [want].
func (c *checker) collection(lit ast.Expr, body *ast.Block, want Type, isMap bool) Type {
	b := &build{lit: lit, isMap: isMap, decided: isMap || hasValue(body)}
	switch w := want.(type) {
	case *List:
		if !b.isMap {
			b.elem, b.decided = w.Elem, true
		}
	case *Map:
		if b.isMap || !b.decided {
			b.key, b.value, b.isMap, b.decided = w.Key, w.Value, true, true
		}
	}
	c.buildBlock(body, b)
	if b.isMap {
		if b.key == nil || b.value == nil {
			if b.count == 0 {
				c.errorAt(lit, "give the type, e.g. let m: [string: int] = [:]", "can't tell the type of this map")
			}
			return Invalid
		}
		return &Map{Key: b.key, Value: b.value}
	}
	if b.elem == nil {
		if b.count == 0 {
			c.errorAt(lit, "give the type, e.g. let xs: [int] = []", "can't tell the type of this list")
		}
		return Invalid
	}
	return &List{Elem: b.elem}
}

// Reports whether [s] holds an element that isn't a spread.
func hasValue(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.Block:
		for _, st := range s.Stmts {
			if hasValue(st) {
				return true
			}
		}
	case *ast.ExprStmt:
		_, spread := s.X.(*ast.SpreadExpr)
		return !spread
	case *ast.IfStmt:
		return hasValue(s.Then) || s.Else != nil && hasValue(s.Else)
	case *ast.ForStmt:
		return hasValue(s.Body)
	}
	return false
}

func (c *checker) buildBlock(blk *ast.Block, b *build) {
	c.push()
	for _, s := range blk.Stmts {
		c.buildStmt(s, b)
	}
	c.pop()
}

// Checks one statement of a builder block or literal body: an element,
// `let`, `var`, `if` or `for`.
func (c *checker) buildStmt(s ast.Stmt, b *build) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		c.info.Elements[s] = b.lit
		c.element(s.X, b)
	case *ast.LetStmt:
		c.stmt(s)
	case *ast.Block:
		c.buildBlock(s, b)
	case *ast.IfStmt:
		if s.Bind != nil {
			elem := c.unwrapped(c.expr(s.Cond, nil), s.Cond, "if let")
			c.push()
			c.declare(s.Bind, elem, false, LocalVar)
			c.buildBlock(s.Then, b)
			c.pop()
		} else {
			c.cond(s.Cond, "if")
			c.buildBlock(s.Then, b)
		}
		if s.Else != nil {
			c.buildStmt(s.Else, b)
		}
	case *ast.ForStmt:
		c.forLoop(s, func() { c.buildBlock(s.Body, b) })
	default:
		c.errorAt(s, "move it out of the block, or into a closure", "a builder block can only hold elements, let, var, if and for")
	}
}

// Checks the element [x] and adds its type to [b].
func (c *checker) element(x ast.Expr, b *build) {
	b.count++
	switch x := x.(type) {
	case *ast.SpreadExpr:
		c.spread(x, b)
		return
	case *ast.MapEntry:
		c.info.Types[x] = Void
		if !b.isMap {
			c.expr(x.Key, nil)
			c.expr(x.Value, nil)
			c.errorAt(x, "a list holds values, a map holds key: value pairs", "unexpected 'key: value' in a list")
			return
		}
		kt := c.elemValue(x.Key, b.key)
		c.entryType(&b.key, kt, x.Key, "map key")
		if b.key != nil && b.key != Invalid && !mapKeyType(b.key) {
			c.errorAt(x.Key, keyHint(b.key), "%s can't be a map key", b.key)
			b.key = Invalid
		}
		vt := c.elemValue(x.Value, b.value)
		if _, ok := vt.(*Optional); ok && b.value == nil {
			c.errorAt(x.Value, "leave the key out instead of storing nil", "map values can't be optional, since a missing key already reads as nil")
			vt = Invalid
		}
		c.entryType(&b.value, vt, x.Value, "map value")
		return
	}
	t := c.elemValue(x, b.elem)
	c.entryType(&b.elem, t, x, "list element")
}

// Checks a value, key or map value [x] of a literal against [want], and
// returns its type: for `?v`, the type inside the optional v.
func (c *checker) elemValue(x ast.Expr, want Type) Type {
	e, ok := x.(*ast.OptionalElem)
	if !ok {
		return c.expr(x, want)
	}
	_, optWant := want.(*Optional)
	if want != nil && want != Invalid {
		want = optionalOf(want)
	}
	c.expr(e.X, want)
	t := c.info.Types[e.X]
	c.info.Types[e] = t
	if o, ok := t.(*Optional); ok {
		if optWant {
			return t
		}
		return o.Elem
	}
	if t != Invalid && t != Void {
		name := "it"
		if id, ok := e.X.(*ast.Ident); ok {
			name = id.Name
		}
		c.errorAt(e, name+" isn't optional, so drop the ?", "'?' needs an optional value, found %s", t)
	}
	return t
}

// Records [t] as the type in [slot], or checks it against the type
// already there.
func (c *checker) entryType(slot *Type, t Type, n ast.Expr, what string) {
	switch {
	case t == Void:
		c.errorAt(n, "each line of a builder block adds its value; move calls without one out of the block", "this expression has no value to add")
	case *slot == nil:
		*slot = t
	case !assignableType(*slot, t) && *slot != Invalid && t != Invalid:
		c.mismatch(n, *slot, t, what)
	}
}

// Checks `...xs` or `...?xs`, which adds every element of a list or entry
// of a map.
func (c *checker) spread(x *ast.SpreadExpr, b *build) {
	var want Type
	if b.decided {
		if b.isMap && b.key != nil && b.value != nil {
			want = &Map{Key: b.key, Value: b.value}
		} else if !b.isMap && b.elem != nil {
			want = &List{Elem: b.elem}
		}
		if want != nil && x.Optional {
			want = &Optional{Elem: want}
		}
	}
	t := c.expr(x.X, want)
	c.info.Types[x] = t
	inner := t
	if o, ok := t.(*Optional); ok {
		if !x.Optional {
			c.errorAt(x.X, "write ...? to add nothing when it is nil", "this %s may be nil", o.Elem)
			return
		}
		inner = o.Elem
	} else if x.Optional && t != Invalid {
		c.errorAt(x, "write ... for a value that can't be nil", "'...?' needs an optional, found %s", t)
	}
	switch it := inner.(type) {
	case *List:
		if !b.decided {
			b.isMap, b.decided = false, true
		}
		if b.isMap {
			c.errorAt(x.X, "", "can't add the elements of a list to a map")
			return
		}
		c.spreadType(&b.elem, it.Elem, x.X)
	case *Map:
		if !b.decided {
			b.isMap, b.decided = true, true
		}
		if !b.isMap {
			c.errorAt(x.X, "add its keys or values: ...m.keys", "can't add the entries of a map to a list")
			return
		}
		c.spreadType(&b.key, it.Key, x.X)
		c.spreadType(&b.value, it.Value, x.X)
	default:
		if inner != Invalid {
			c.errorAt(x.X, "", "'...' needs a list or a map, found %s", inner)
		}
	}
}

// Like [checker.entryType], for the element, key or value type [t] of a
// spread collection.
func (c *checker) spreadType(slot *Type, t Type, n ast.Expr) {
	switch {
	case *slot == nil:
		*slot = t
	case *slot == Invalid || t == Invalid:
	case !assignableType(*slot, t) && !wrappable(*slot, t):
		c.errorAt(n, "", "can't add elements of type %s where %s is expected", t, *slot)
	}
}

// Reports whether a value of type [t] can be stored as a present value of
// the optional [want].
func wrappable(want, t Type) bool {
	o, ok := want.(*Optional)
	return ok && assignableType(o.Elem, t)
}
