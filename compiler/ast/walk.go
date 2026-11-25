package ast

import "reflect"

var nodeType = reflect.TypeOf((*Node)(nil)).Elem()

// Calls [f] for [n] and every node inside it, parents first. Returning
// false from [f] skips the node's children.
func Inspect(n Node, f func(Node) bool) {
	if n == nil || reflect.ValueOf(n).IsNil() {
		return
	}
	if !f(n) {
		return
	}
	walkValue(reflect.ValueOf(n).Elem(), f)
}

func walkValue(v reflect.Value, f func(Node) bool) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				walkValue(v.Field(i), f)
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			walkValue(v.Index(i), f)
		}
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Type().Implements(nodeType) {
			Inspect(v.Interface().(Node), f)
			return
		}
		if v.Kind() == reflect.Ptr {
			walkValue(v.Elem(), f)
		}
	}
}
