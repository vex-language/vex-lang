# Vex Syntax

Vex aims to be readable at first sight for anyone who knows Dart, Swift, Kotlin, Go or TypeScript.

Principles:
- **Familiar:** `let` / `var`, `fn`, `name: Type`, and `if` / `for` without parentheses.
- **Low noise:** no semicolons, no `self.` inside methods.
- **One way to create things:** `User(name: "Ann")` and `Text("Hi")` look the same.
- **Readable calls:** named and default arguments.
- **Interpolation:** `"Hi {name}"` instead of `"Hi " + name`.
- **Helpful errors** that point at the problem and suggest a fix.

## Basics

```swift
// `let` can't change, `var` can.
let name = "Vex"            // inferred: string
var count = 0               // inferred: int
let price: float = 9.99     // explicit type

println("Hello, {name}! Count is {count + 1}")
```

A new line ends a statement. A line ending with an operator, `,` or an opening bracket continues on the next line, and so does a line that starts with `.member`, which keeps chains readable:

```swift
Text("Count: {count}")
    .size(24)
    .color(blue)
```

## Literals and comments

```swift
let count = 1_000_000        // `_` separates digits
let mask = 0xFF              // also 0b1010 and 0o17
let ratio = 2.5e-3
let text = "Tab\tquote\" brace\{ smile \u{1F600}"

/// Doc comment for the next declaration.
/* Block comments /* can nest */ too. */
```

Escapes: `\n` `\t` `\r` `\\` `\"` `\{` `\}` and `\u{...}`. Strings stay on one line.

## Functions

```swift
fn add(a: int, b: int) -> int {
    return a + b
}

/// One-line function.
fn square(x: int) -> int => x * x

/// Default and named parameters.
fn greet(name: string, greeting: string = "Hi") {
    println("{greeting}, {name}!")
}

greet("Sam")                       // Hi, Sam!
greet("Sam", greeting: "Welcome")  // Welcome, Sam!
```

## Control flow

```swift
if count > 10 {
    println("big")
} else if count > 5 {
    println("medium")
} else {
    println("small")
}

for i in 0..5 { println(i) }               // 0 to 4
for i in 1..=5 { println(i) }              // 1 to 5
for fruit in fruits { println(fruit) }
for i, fruit in fruits { println("{i}: {fruit}") }

while running { tick() }
```

`else` must be on the same line as the closing `}`.

A loop over a list visits the items it had when the loop started: items pushed inside the loop aren't visited, and the loop ends early if items are removed.

Conditions don't need parentheses, and lines don't need a `;` at the end. The compiler warns about both, as it does about other habits from C-style languages, but still builds the program.

## Lists

```swift
var nums = [1, 2, 3]
nums.push(4)
println(nums.len)        // 4
println(nums[0])         // 1
println(nums)            // [1, 2, 3, 4]

let empty: [string] = []
```

## Maps

```swift
var ages = ["ann": 31, "bob": 25]
ages["cat"] = 40               // add or replace
let a = ages["ann"]            // int?, nil when the key is missing
let z = ages["zoe"] ?? 0
if let b = ages["bob"] { println(b) }
ages.remove("bob")             // returns the removed value, an int?
println(ages.len, ages.contains("cat"), ages.keys, ages.values)

for name, age in ages { println(name, age) }
for name in ages { println(name) }

let empty: [string: int] = [:]
```

- Loops visit entries in the order their keys were first added. A loop sees the entries that existed when it started; entries removed during the loop are skipped.
- Keys can be `int`, `string`, `bool` or an enum whose cases carry no values. Values can be any type except an optional, since a missing key already reads as `nil`.
- Maps are shared references, like lists and structs.
- `m[k] += 1` is an error, since the key may be missing. Write `m[k] = (m[k] ?? 0) + 1`.

## Structs

```swift
struct User {
    name: string
    age: int = 18

    fn greet() {
        println("Hi, I'm {name}")
    }

    fn isAdult() -> bool => age >= 18

    static fn guest() -> User {
        return User(name: "Guest")
    }
}

let u = User(name: "Ann", age: 30)
u.greet()
println(u.isAdult())          // true
println(User.guest())         // User(name: "Guest", age: 18)
```

Fields and methods are reachable inside methods without `self.`; `self` is available when needed.

## Generics

Functions and structs can take type parameters in `< >`. The types are usually worked out from the arguments, or from the type the result is stored in; they can also be written at the call.

```swift
fn first<T>(xs: [T]) -> T? {
    if xs.len == 0 { return nil }
    return xs[0]
}

struct Box<T> {
    value: T
    fn get() -> T => value
}

let n = first([3, 1, 2])          // T is int
let b = Box(value: "hi")          // Box<string>
let s = Stack<string>()           // written out: nothing to infer from
let e: [int] = empty()            // T from the type it is stored in
let x = first<int>([])
```

A type parameter can require what the generic code needs from it, with `&` between several:

| Constraint | Allows | Met by |
|---|---|---|
| `Equatable` | `==`, `!=`, `list.contains` | int, float, bool, string, enums whose cases carry no values |
| `Comparable` | `<`, `<=`, `>`, `>=`, and `==` | int, float, string |
| `Hashable` | map keys, and `==` | int, bool, string, enums whose cases carry no values |

```swift
fn largest<T: Comparable>(xs: [T]) -> T? { ... }
fn count<T: Hashable>(xs: [T]) -> [T: int] { ... }
struct Index<K: Hashable & Comparable, V> { ... }
```

- Generic code is checked once, with `T` standing for any type that meets its constraints, so `a > b` on a plain `T` is an error that names the missing constraint.
- Each use with new types compiles to its own copy, specialised for those types. Generic code runs as fast as code written for one type.
- Type arguments in a call touch the name and the `(`: `first<int>(xs)`. With spaces, `<` and `>` are comparisons.
- Not yet: methods with their own type parameters, generic enums, and using a generic function as a value (wrap it in a closure instead).

## Closures

A closure is a function without a name, written like a function declaration. Function types are written `fn(int) -> bool`, or `fn()` for a function that takes and returns nothing.

```swift
let double = fn(x: int) -> int => x * 2
let greet = fn(name: string) {
    println("Hi, {name}")
}

fn apply(f: fn(int) -> int, x: int) -> int => f(x)

apply(double, 5)               // named functions are values too
apply(fn(x) => x + 1, 5)       // parameter types come from fn(int) -> int
```

- Parameter types can be left out where the expected function type gives them; otherwise write them.
- The result type is taken from `-> T`, the expected type, the `=>` expression, or the first `return`.
- Closures capture variables by reference: changing a captured `var` inside the closure changes it outside too, and the variable lives as long as the closure.
- Each loop iteration has its own loop variable, so closures made in a loop each keep their own value.
- Call function values with unnamed arguments: `f(1, 2)`.

**Trailing blocks.** A `{ ... }` block after a call is passed as its last argument, which must have type `fn()`:

```swift
fn repeat(times: int, action: fn()) {
    for i in 0..times { action() }
}

var count = 0
repeat(3) { count += 1 }       // count is 3

struct Button {
    title: string
    onTap: fn()
}
let add = Button(title: "Add") { count += 1 }
add.onTap()
```

**Lists.** `filter` keeps the items a function accepts; `map` turns each item into a new value.

```swift
let evens = nums.filter(fn(x) => x % 2 == 0)
let labels = nums.map(fn(x) => "#{x}")      // [string]
```

## Optionals

A type ending in `?` holds either a value or `nil`. Values of every other type always exist, so there are no null crashes: Vex makes you check before using an optional.

```swift
struct User {
    name: string
    manager: User? = nil
}

fn findUser(id: int) -> User? {
    if id == 1 { return User(name: "Ann") }
    return nil
}

let user = findUser(1)                    // User?
let age: int? = 31                        // a plain value becomes an optional where one is expected
var picked: User? = nil                   // nil needs a known type
```

Ways to get the value out:

```swift
if let u = findUser(1) {                  // u is a User inside the block
    println(u.name)
} else {
    println("not found")
}

fn greet(id: int) {
    guard let u = findUser(id) else {     // the else block must return, break, continue or panic
        return
    }
    println("hi {u.name}")                // u is a User from here on
}

let name = findUser(2)?.name ?? "guest"   // ?. gives nil if the left side is nil; ?? gives a default
let boss = user?.manager?.name            // string?: each step after an optional needs ?.
user?.greet()                             // runs only when user isn't nil

if user == nil { ... }                    // optionals compare only with nil
```

- `x ?? fallback` only runs `fallback` when `x` is nil. Chains like `a ?? b ?? 0` try each in turn.
- `user.name` on a `User?` is an error. Use `user?.name`, `if let` or `guard let`.
- There's no force unwrap: every optional is checked.
- `?` comes last in a type: `[int?]` is a list of optional ints, and `[int]?` is an optional list. Write `(fn() -> int)?` for an optional function that returns a value; `fn()?` needs no parentheses.
- Optionals print as their value or `nil`.

## Enums and match

An enum is a value that is one of several cases. Cases are lowercase, and each may carry values, named like parameters. Enums can have methods, just like structs.

```swift
enum Shape {
    circle(radius: float)
    rect(width: float, height: float)
    empty

    fn area() -> float => match self {
        circle(r) => 3.14 * r * r
        rect(w, h) => w * h
        empty => 0.0
    }

    static fn square(side: float) -> Shape => .rect(width: side, height: side)
}

enum Light { red, yellow, green }

let a = Shape.circle(radius: 2)
let b: Shape = .rect(width: 2, height: 3)    // the enum name can be left out
let c = Shape.empty                          // cases without values aren't called
println(a.area(), b)                         // 12.56 Shape.rect(width: 2.0, height: 3.0)
```

- `.case` works wherever the enum is expected: a typed `let`, an argument, a `return`, a list of that enum, or a `==` comparison.
- The values a case carries are read with `match`, not with `.`.
- Enum values never change after they are created.
- `==` and `!=` work on enums whose cases carry no values, like `Light`.

**match** picks the first arm whose pattern fits and runs it. It works on enums, `int`, `bool` and `string`.

```swift
match shape {
    circle(r) => println("circle of radius {r}")
    rect(w, _) => {                    // _ ignores a value
        println("rect {w} wide")
    }
    empty => return
}

let message = match code {             // a match can be a value
    200 => "ok"
    404 => "not found"
    _ => "error {code}"                // _ matches anything
}
```

- A match must handle every case, or end with `_`. On `int` and `string` it always needs `_`.
- Bindings follow the order of the case's values: `rect(w, h)`.
- Used as a value, every arm needs an expression of the same type; an arm that calls `panic(...)` is allowed too.
- Used as a statement, an arm can be a block or a single statement like `return`, `break` or `count += 1`.

## Errors

A function that can fail declares `throws` before its result type, and `throw` stops it with an error. Every call to it needs `try`, so each line that may fail is visible.

```swift
fn parsePort(s: string) throws -> int {
    if s == "" {
        throw Error("empty port")            // the built-in Error has a message
    }
    let n = int(s)
    if n < 1 || n > 65535 {
        throw Error("port {n} out of range")
    }
    return n
}

fn loadPort(path: string) throws -> int {
    let text = try readConfig(path)          // try passes the error up to the caller
    return try parsePort(text)
}
```

Three ways to handle an error:

```swift
let port = try parsePort(s)                  // pass it up: the enclosing function must throw

let port = try parsePort(s) catch e {        // handle it here; e is the error
    println("bad port: {e.message}")
    8080                                     // the last line gives the value
}

let port = try? parsePort(s) ?? 8080         // try? gives nil instead of the error
```

**Typed errors.** `throws(E)` names an enum, so callers can `match` every kind of failure:

```swift
enum LoadError {
    notFound(path: string)
    denied
}

fn open(path: string) throws(LoadError) -> File {
    throw .notFound(path: path)
}

let file = try open(path) catch e {
    match e {
        notFound(p) => File.empty(p)
        denied => File.empty("locked")
    }
}
```

- `try` covers every throwing call in the expression after it: `try save(load(path))`. Nothing after a failing call runs.
- A `catch` block that gives a value ends with an expression of the try's type, or leaves with `return`, `break`, `continue`, `throw` or `panic(...)`. Without a value it is a statement: `try save(x) catch e { println(e.message) }`.
- A typed error passed up through a plain `throws` function becomes an `Error` whose message describes it, such as `LoadError.notFound(path: "a.txt")`. Passing it up through a function that throws a different enum is an error: catch it there.
- A catch receives the enum when every call in the try throws the same enum, and `Error` otherwise.
- Closures and function types can throw too: `fn(int) throws -> int`. A closure passed where a throwing function is expected may throw.
- `fn main() throws` lets errors reach the top: the program prints `error: <message>` and exits with code 1.
- Errors are passed back like return values, not by unwinding the stack, so code that doesn't throw pays nothing and a `try` costs one check after each call.

## Built-ins

| Name | Description |
|---|---|
| `print(a, b, ...)` / `println(...)` | Print values separated by spaces |
| `input(prompt)` | Read a line from standard input; the prompt is optional |
| `int(x)`, `float(x)`, `str(x)` | Convert between types; `int` and `float` ignore spaces around the number |
| `panic(msg)`, `assert(cond, msg)` | Stop the program with a message; `assert`'s message is optional |
| `list.len`, `.push(x)`, `.pop()`, `.contains(x)`, `.filter(f)`, `.map(f)` | List members |
| `map.len`, `.keys`, `.values`, `.contains(k)`, `.remove(k)` | Map members |
| `s.len`, `.upper()`, `.lower()`, `.contains(x)`, `.split(sep)` | String members. `len` counts Unicode code points, so an emoji with a modifier counts as more than one; `upper` and `lower` change only the letters A–Z for now |

## Types

The types are `int` (64-bit), `float` (64-bit), `bool`, `string`, lists `[T]`, maps `[K: V]`, structs, enums, function types `fn(T) -> R` and optionals `T?`.

- `let x = ...` takes its type from the value. An empty list or map needs the type written: `let xs: [int] = []`, `let m: [string: int] = [:]`.
- Whole-number literals adapt to `float` where one is expected: `let price: float = 10`, `price * 2`.
- Int arithmetic that overflows stops the program instead of giving a wrong number: `9223372036854775807 + 1` is an error. When wrap-around is wanted, as in hashes and random number generators, use `&+`, `&-` and `&*` (and `&+=`, `&-=`, `&*=`), which work only on `int`:

  ```swift
  var h = 5381
  for c in codes { h = h &* 33 &+ c }
  ```

  The checks are cheap. In a tight loop where they cost speed, the wrapping operators remove them.
- `int` and `float` never mix silently: `count * price` is an error until one side is converted with `float(count)` or `int(price)`.
- Strings join with `+` only to other strings. Use interpolation for anything else: `"{count} items"`.
- `==` and `!=` compare `int`, `float`, `bool`, `string` and enums whose cases carry no values; `<`, `<=`, `>`, `>=` also work on these except `bool`.
- Conditions must be `bool`: write `if xs.len > 0`, not `if xs`.
- Structs are shared references: after `let b = a`, changing `b.name` also changes `a.name`. `let` stops the name being reassigned, not the fields being changed.
- A name can be declared again in an inner block, which hides the outer one; declaring it twice in the same block is an error.

Run `vex check file.vx` to see type errors without building.

## Memory

Memory is managed automatically by a garbage collector: objects nobody can reach any more are freed, including ones that refer to each other in a cycle. There is nothing to free by hand and no `weak` keyword. See [design/memory.md](design/memory.md).

## Runtime errors

Mistakes that can only be found while the program runs stop it with a message and the source line, and exit code 1:

```
error: index 5 is out of range for a list of length 3
 --> main.vx:12
```

These cover list indexes out of range, `pop()` on an empty list, division by zero, integer overflow, `int(...)` or `float(...)` of text that isn't a number, `panic(...)` and failed `assert(...)`. Recursion that never stops ends with `error: stack overflow` instead of a crash. Printing a struct that refers back to itself shows the repeat as `Name(...)`.

## Planned

### Mobile UI (v0.3+)

The UI framework is a separate package built from general language features, with no UI keywords in the compiler. A sketch, not approved syntax (see [design/architecture.md](design/architecture.md)):

```swift
import "ui"

struct Counter: View {
    let count = State(0)

    fn body() -> View => Column(spacing: 12) {
        Text("Count: {count.value}").size(24)
        Button("Add") { count.value += 1 }
    }
}

fn main() {
    app(Counter())
}
```

## Error messages

```
error: can't add int and string
  --> main.vx:4:15
   |
 4 |     let x = count + "items"
   |                   ^ did you mean "{count} items"?
```
