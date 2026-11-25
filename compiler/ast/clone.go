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
