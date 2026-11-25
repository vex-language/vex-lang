package ast

import "reflect"

// Returns a deep copy of [n]. Nodes shared within [n] stay shared in the
// copy.
func Clone[T Node](n T) T {
	seen := map[uintptr]reflect.Value{}
	return clone(reflect.ValueOf(n), seen).Interface().(T)
}

func clone(v reflect.Value, seen map[uintptr]reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		if c, ok := seen[v.Pointer()]; ok {
			return c
		}
		c := reflect.New(v.Elem().Type())
		seen[v.Pointer()] = c
		c.Elem().Set(clone(v.Elem(), seen))
		return c
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		c := reflect.New(v.Type()).Elem()
		c.Set(clone(v.Elem(), seen))
		return c
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			c.Index(i).Set(clone(v.Index(i), seen))
		}
		return c
	case reflect.Struct:
		c := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			if c.Field(i).CanSet() {
				c.Field(i).Set(clone(v.Field(i), seen))
			}
		}
		return c
	}
	return v
}

var exprType = reflect.TypeFor[Expr]()

// Replaces every expression inside [n] with what [f] returns for it,
// innermost first.
func Rewrite(n Node, f func(Expr) Expr) {
	rewrite(reflect.ValueOf(n), f, map[uintptr]bool{})
}

func rewrite(v reflect.Value, f func(Expr) Expr, seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		rewrite(v.Elem(), f, seen)
	case reflect.Interface:
		if !v.IsNil() {
			rewrite(v.Elem(), f, seen)
		}
	case reflect.Slice:
		for i := range v.Len() {
			replace(v.Index(i), f, seen)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				replace(v.Field(i), f, seen)
			}
		}
	}
}

func replace(v reflect.Value, f func(Expr) Expr, seen map[uintptr]bool) {
	rewrite(v, f, seen)
	if v.Type() == exprType && !v.IsNil() {
		v.Set(reflect.ValueOf(f(v.Interface().(Expr))))
	}
}
