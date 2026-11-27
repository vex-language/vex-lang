package types

import (
	"fmt"
	"maps"
	"slices"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// The module whose code may call the built-ins that convert values to and
// from its `Json` enum.
const jsonModule = "vex:json"

// Reports whether the struct listed `Codable`.
func (s *Struct) Codable() bool { return s.Decl != nil && declares(s.Decl.Interfaces, "Codable") }

// Reports whether the enum listed `Codable`.
func (e *Enum) Codable() bool { return e.Decl != nil && declares(e.Decl.Interfaces, "Codable") }

// Reports whether [t] is the `Json` enum of the JSON module, or its
// `JsonError` when [name] says so.
func jsonEnum(t Type, name string) bool {
	e, ok := t.(*Enum)
	return ok && e.Module != nil && e.Module.Path == jsonModule && e.Name == name
}

// Returns the struct's own `fn encode() -> json.Json`, or nil.
func (s *Struct) EncodeMethod() *Func { return encodeMethod(s.Methods) }

// Returns the struct's own `static fn decode(value: json.Json)`, or nil.
func (s *Struct) DecodeMethod() *Func { return decodeMethod(s.Methods, s) }

// Returns the enum's own `fn encode() -> json.Json`, or nil.
func (e *Enum) EncodeMethod() *Func { return encodeMethod(e.Methods) }

// Returns the enum's own `static fn decode(value: json.Json)`, or nil.
func (e *Enum) DecodeMethod() *Func { return decodeMethod(e.Methods, e) }

func encodeMethod(ms []*Func) *Func {
	m := memberOf(ms, "encode", ast.Method)
	if m == nil || m.Static || m.Async || m.Throws != nil || len(m.Params) != 0 || !jsonEnum(m.Result, "Json") {
		return nil
	}
	return m
}

func decodeMethod(ms []*Func, self Type) *Func {
	m := memberOf(ms, "decode", ast.Method)
	if m == nil || !m.Static || m.Async || len(m.Params) != 1 || !jsonEnum(m.Params[0].Type, "Json") ||
		!jsonEnum(m.Throws, "JsonError") || !Identical(m.Result, self) {
		return nil
	}
	return m
}

// Checks the `encode` and `decode` a Codable type [self] writes to replace
// the generated ones.
func (c *checker) checkCodableMethods(self Type, methods []*Func) {
	if m := memberOf(methods, "encode", ast.Method); m != nil && encodeMethod(methods) == nil {
		c.errorAt(m.Decl.Name, "write fn encode() -> json.Json", "encode must take no parameters and return json.Json, so JSON can use it")
	}
	if m := memberOf(methods, "decode", ast.Method); m != nil && decodeMethod(methods, self) == nil {
		c.errorAt(m.Decl.Name, fmt.Sprintf("write static fn decode(value: json.Json) throws(json.JsonError) -> %s", self),
			"decode must be static, take a json.Json and throw json.JsonError, so JSON can use it")
	}
}

// Reports whether values of [t] can be written as JSON and read back.
func codable(t Type) bool {
	switch t := t.(type) {
	case *Optional:
		return codable(t.Elem)
	case *List:
		return codable(t.Elem)
	case *Map:
		return t.Key == String && codable(t.Value)
	case *Struct:
		return t.Codable()
	case *Enum:
		return t.Codable()
	case *TypeParam:
		return t.Has("Codable")
	}
	return t == Int || t == Float || t == Bool || t == String
}

// Returns how a value of [t] could become Codable.
func codableHint(t Type) string {
	switch t := t.(type) {
	case *Struct, *Enum:
		return fmt.Sprintf("list Codable on %s", t)
	case *TypeParam:
		return fmt.Sprintf("require it: <%s: Codable>", t.Name)
	case *Map:
		if t.Key != String {
			return "JSON object keys are strings: use [string: " + t.Value.String() + "]"
		}
		return codableHint(t.Value)
	case *List:
		return codableHint(t.Elem)
	case *Optional:
		return codableHint(t.Elem)
	}
	return "Codable types are int, float, bool, string, lists, maps with string keys and optionals of those, and structs and enums that list Codable"
}

// Checks the structs and enums that list `Codable`: their fields must be
// Codable too. Generic types are checked on their templates.
func (c *checker) checkCodable() {
	structs := c.info.Structs
	for _, name := range slices.Sorted(maps.Keys(c.genStructs)) {
		if t := c.genStructs[name].tmpl; t != nil {
			structs = append(structs, t)
		}
	}
	for _, s := range structs {
		if s == c.info.Error || s.generic != nil && !s.abstract || !s.Codable() {
			continue
		}
		c.checkCodableFields(s.Decl.Name.Name, s.Fields, s.EncodeMethod() != nil && s.DecodeMethod() != nil)
		if s.generic == nil {
			c.checkCodableMethods(s, s.Methods)
		}
	}
	enums := c.info.Enums
	for _, name := range slices.Sorted(maps.Keys(c.genEnums)) {
		if t := c.genEnums[name].tmpl; t != nil {
			enums = append(enums, t)
		}
	}
	for _, e := range enums {
		if e.generic != nil && !e.abstract || !e.Codable() {
			continue
		}
		var fields []*Field
		for _, cs := range e.Cases {
			fields = append(fields, cs.Fields...)
		}
		c.checkCodableFields(e.Decl.Name.Name, fields, e.EncodeMethod() != nil && e.DecodeMethod() != nil)
		if e.generic == nil {
			c.checkCodableMethods(e, e.Methods)
		}
	}
}

// Checks that the [fields] of [name] are Codable, unless it writes its own
// encode and decode.
func (c *checker) checkCodableFields(name string, fields []*Field, own bool) {
	if own {
		return
	}
	for _, f := range fields {
		if f.Type == Invalid || codable(f.Type) {
			continue
		}
		where := ast.Node(f.Decl.Name)
		if f.Decl.Type != nil {
			where = f.Decl.Type
		}
		c.errorAt(where, codableHint(f.Type), "field '%s' is %s, which isn't Codable, so %s can't be Codable", f.Name, f.Type, name)
	}
}

// Checks `jsonOf(value)`, which gives [value] as a `Json`, and
// `jsonAs(json)`, which reads the type wanted from a `Json` and throws a
// `JsonError` when it doesn't match. Only the JSON module calls them.
func (c *checker) jsonBuiltin(e *ast.CallExpr, name string, want Type) Type {
	call := &Call{Kind: BuiltinCall, Builtin: name}
	c.info.Calls[e] = call
	json, jsonErr := c.enums["Json"], c.enums["JsonError"]
	if len(e.Args) != 1 || e.Args[0].Name != nil || json == nil || jsonErr == nil {
		c.checkArgs(e)
		c.errorAt(e.Fn, "", "%s takes 1 argument", name)
		return Invalid
	}
	arg := e.Args[0].Value
	call.Args = []ast.Expr{arg}
	if name == "jsonOf" {
		t := c.expr(arg, nil)
		if t != Invalid && !codable(t) {
			c.errorAt(arg, codableHint(t), "%s isn't Codable", t)
		}
		return json
	}
	c.assignable(json, c.expr(arg, json), arg, "argument")
	switch {
	case want == nil:
		c.errorAt(e, "", "jsonAs needs the type it reads")
		return Invalid
	case want != Invalid && !codable(want):
		c.errorAt(e, codableHint(want), "%s isn't Codable", want)
	}
	c.noteThrows(e, jsonErr, "jsonAs can throw, so the call needs try")
	return want
}
