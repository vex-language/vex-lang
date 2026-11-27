package types

import (
	"fmt"
	"math"
	"strconv"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/lexer"
)

// Reports whether values of [t] can be map keys.
func mapKeyType(t Type) bool {
	switch t := t.(type) {
	case *Enum:
		return t.Simple() || t.Hashable()
	case *Struct:
		return t.Hashable()
	case *TypeParam:
		return t.Has("Hashable")
	case *Record:
		for _, f := range t.FieldTypes() {
			if !mapKeyType(f) {
				return false
			}
		}
		return true
	}
	return t == Int || t == Bool || t == String
}

func (c *checker) mapType(te *ast.MapType) Type {
	key, value := c.resolveType(te.Key), c.resolveType(te.Value)
	if key != Invalid && !mapKeyType(key) {
		c.errorAt(te.Key, keyHint(key), "%s can't be a map key", key)
		key = Invalid
	}
	if _, ok := value.(*Optional); ok {
		c.errorAt(te.Value, "remove the key instead of storing nil", "map values can't be optional, since a missing key already reads as nil")
		value = Invalid
	}
	return &Map{Key: key, Value: value}
}

// Checks [key] as a key of [m].
func (c *checker) mapKey(key ast.Expr, m *Map) {
	t := c.expr(key, m.Key)
	if m.Key != Invalid {
		c.assignable(m.Key, t, key, "map key")
	}
}

// Checks `m[key]` as the target of [s], returning the value type, or nil
// after an error.
func (c *checker) mapTarget(x *ast.IndexExpr, m *Map, s *ast.AssignStmt) Type {
	c.mapKey(x.Index, m)
	c.info.Types[x] = m.Value
	if s.Op == lexer.Assign && isNil(s.Value) {
		c.errorAt(s.Value, "remove the key instead: m.remove(key)", "a map entry can't be set to nil")
		return nil
	}
	if s.Op != lexer.Assign {
		c.expr(s.Value, nil)
		c.errorAt(s, "write m[k] = (m[k] ?? 0) + 1", "the key may be missing, so %s can't be used on a map entry", s.Op)
		return nil
	}
	return m.Value
}

func (c *checker) mapLit(e *ast.MapLit, want Type) Type {
	var key, value Type
	if m, ok := want.(*Map); ok {
		key, value = m.Key, m.Value
	}
	if len(e.Keys) == 0 {
		if key == nil {
			c.errorAt(e, "give the type, e.g. let m: [string: int] = [:]", "can't tell the type of an empty map")
			return Invalid
		}
		return &Map{Key: key, Value: value}
	}
	seen := map[string]int{}
	for i, k := range e.Keys {
		kt := c.expr(k, key)
		if key == nil || key == Invalid {
			key = kt
			if key != Invalid && !mapKeyType(key) {
				c.errorAt(k, keyHint(key), "%s can't be a map key", key)
			}
		} else if !Identical(key, kt) {
			c.mismatch(k, key, kt, fmt.Sprintf("key %d", i+1))
		}
		if lit := literalKey(k); lit != "" {
			if line, dup := seen[lit]; dup {
				c.errorAt(k, "", "key %s is already in this map on line %d", c.source(k), line)
			}
			seen[lit] = k.Pos().Line
		}
		vt := c.expr(e.Values[i], value)
		if vt == Void {
			c.errorAt(e.Values[i], "", "this expression has no value")
			continue
		}
		if value == nil || value == Invalid {
			value = vt
			if _, ok := vt.(*Optional); ok {
				c.errorAt(e.Values[i], "leave the key out instead of storing nil", "map values can't be optional, since a missing key already reads as nil")
			}
		} else if !assignableType(value, vt) {
			c.mismatch(e.Values[i], value, vt, fmt.Sprintf("value %d", i+1))
		}
	}
	return &Map{Key: key, Value: value}
}

// Returns a hint for a method other languages give maps, or "".
func mapMethodHint(recv Type, name string) string {
	if _, ok := recv.(*Map); !ok {
		return ""
	}
	switch name {
	case "get":
		return "read a key with m[key], which gives nil when it is missing"
	case "has", "containsKey", "hasKey":
		return "use .contains(key)"
	case "delete", "removeKey":
		return "use .remove(key)"
	case "set", "put", "insert":
		return "write m[key] = value"
	}
	return ""
}

// Returns the source of [k] when it is a literal, so repeated keys can be
// found, or "".
func literalKey(k ast.Expr) string {
	switch k := k.(type) {
	case *ast.StringLit:
		return fmt.Sprintf("%q", k.Value)
	case *ast.IntLit:
		// Keys written differently, such as 10 and 0b1010, are the same.
		if v, err := strconv.ParseInt(k.Value, 0, 64); err == nil {
			return strconv.FormatInt(v, 10)
		}
		return k.Value
	case *ast.UnaryExpr:
		if IsMinInt(k) {
			return strconv.FormatInt(math.MinInt64, 10)
		}
		if lit, ok := k.X.(*ast.IntLit); ok && k.Op == lexer.Minus {
			if v, err := strconv.ParseInt(lit.Value, 0, 64); err == nil {
				return strconv.FormatInt(-v, 10)
			}
		}
	case *ast.BoolLit:
		return fmt.Sprint(k.Value)
	}
	return ""
}
