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

Escapes: `\n` `\t` `\r` `\\` `\"` `\{` `\}` and `\u{...}`. A `"..."` string stays on one line.

A multi-line string starts with `"""` at the end of a line and ends with `"""` on its own line, as in Swift. The indentation of the closing `"""` is removed from every line, so the string can be indented with the code around it:

```swift
fn help() -> string {
    return """
        Usage: vex <command>
          run    build and run "{name}"
        """
}
// "Usage: vex <command>\n  run    build and run \"app\""
```

- The text starts on the line after the opening `"""`, and the line break before the closing `"""` isn't part of the string.
- Every line must be indented at least as much as the closing `"""`; blank lines may be shorter.
- `"` and `""` need no escape inside; write `\"""` for three quotes.
- A `\` at the end of a line joins it to the next line.
- Escapes and `{interpolation}` work as in other strings. Windows line endings become `\n`.

A raw string, written `r"..."` or `r"""..."""` as in Dart, keeps `\` and `{` as plain text, for patterns, paths and JSON:

```swift
let number = r"\d+\.\d+"
let folder = r"C:\Users\ann"
let body = r"""
    {"name": "{not interpolated}"}
    """
```

A raw `r"..."` string can't contain `"`; use `r"""..."""` for that.

## Constants

`const` declares a value fixed when the program is compiled. The type is inferred, or written after the name.

```swift
const maxUsers = 100
const timeout: float = 2.5
pub const appName = "vex"                  // usable from other modules
const greeting = "hello from {appName}"
const limit = maxUsers * 2 + 1
const origin = (x: 0, y: 0)
const defaultLight = Light.red
const primes = [2, 3, 5, 7]
const codes = ["ok": 200, "missing": 404]

struct Circle {
    static const pi = 3.14159              // Circle.pi, or pi inside its methods
    radius: float
    fn area() -> float => pi * radius * radius
}

match code {
    okCode => "ok"                         // a constant as a pattern
    _ => "other"
}

var mine = primes.copy()                   // a changeable copy
mine.push(11)
```

- A constant's value is made of literals, other constants, operators, string interpolation, records, enum cases, and lists and maps of those. It can't call functions or use `if`, `for` or `...` inside a list.
- `int`, `bool` and `string` constants are worked out by the compiler and written into the code where they are used. Overflow and division by zero are compile errors.
- Lists, maps and other values are built once when the program starts, before `main`, and shared by every isolate.
- Constant lists and maps can't change: `primes.push(11)` is a compile error, and changing one through another name fails when the program runs. Use `.copy()` for a changeable copy.
- Constants can be declared in any order; one that depends on itself is an error.
- In a pattern, a name that refers to a constant matches values equal to it, unless a local variable has the same name.
- Generic structs and enums can't have constants.

## Top-level and static variables

`let` and `var` outside functions declare variables the whole program shares, and `static let` and `static var` declare them on a struct or enum. Unlike constants, their starting value can be anything, function calls included.

```swift
var visits = 0
let config = loadConfig()                  // runs the first time config is used
pub var theme = Theme(name: "light")       // other modules can read and change it

struct Router {
    static var stack: [Page] = []
    static fn push(p: Page) { stack.push(p) }   // Router.stack outside the struct
}

fn visit() -> int {
    visits += 1
    return visits
}
```

- Each variable is set up the first time it is used, not before `main`, so unused ones cost nothing and the order of declarations doesn't matter. A starting value that ends up using its own variable stops the program with an error.
- Each isolate has its own copy, set up on its first use there. Changes in one isolate are never seen by another, so no locks are needed.
- `let` variables can't be assigned again, but a list or map they hold can still change, as with a local `let`.
- A starting value can't use `await` or pass errors up with `try`; use `try?` or `try ... catch`.
- Names in patterns bind new variables even when a top-level variable has the same name; only constants are compared.
- Generic structs and enums can't have static variables, and neither can extensions of built-in types.

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

repeat {
    line = readLine()
} while line != "quit"                    // runs at least once
```

`else` must be on the same line as the closing `}`, and so must the `while` of `repeat { } while`.

**Named loops.** A name before a loop lets `break` and `continue` leave an outer loop from inside an inner one.

```swift
outer: for row in grid {
    for cell in row {
        if cell < 0 { continue outer }   // next row
        if cell == target { break outer } // stop both loops
    }
}
```

A name can't repeat the name of a loop around it, and closures start without any loop names.

**`defer`.** A `defer` block runs when the block it is in ends, however it ends: at its last line, or through `return`, `throw`, `break`, `continue` or an error passed up by `try`. Several deferred blocks run last first.

```swift
fn copy(from: string, to: string) throws {
    let src = try open(from)
    defer { src.close() }
    let dst = try open(to)
    defer { dst.close() }                // closes before src
    try dst.write(src.read())
}

for job in jobs {
    defer { println("{job} done") }      // after every pass, even one cut short by continue
    if job.skip { continue }
    job.run()
}
```

- A deferred block runs only if the `defer` line was reached.
- It sees the variables declared before it, with the values they have when it runs.
- It can't `return` or `throw`, pass up an error with `try`, or `break` or `continue` a loop outside it. Errors inside it are handled with `try?` or `try ... catch`.

**`if` as a value.** An `if` with an `else` gives a value, so it works anywhere an expression does. Vex has no `? :` operator.

```swift
let color = if selected { blue } else { gray }
let size = if n > 100 { "large" } else if n > 10 { "medium" } else { "small" }
let label = if let name = user.name { "Hi {name}" } else { "Welcome" }
println("{count} {if count == 1 { "item" } else { "items" }}")
return if s case circle(r) { 3.14 * r * r } else { 0.0 }
```

- Every branch holds a single expression of the same type, and `else` is required. A branch holding only another `if` gives that `if`'s value.
- A branch may be `panic(...)` instead of a value.
- With a declared type, branches convert to it: `let n: int? = if ok { 5 } else { nil }`. Without one, `if ok { 5 } else { nil }` is an error, since `nil` doesn't fit the `int` the first branch gives.
- Only the branch taken runs.
- The `if` ends at its last `}`, so in `1 + if ok { 2 } else { 3 } * 4` the `* 4` applies to the value of the whole `if`.

A loop over a list visits the items it had when the loop started: items pushed inside the loop aren't visited, and the loop ends early if items are removed.

Conditions don't need parentheses, and lines don't need a `;` at the end. The compiler warns about both, as it does about other habits from C-style languages, but still builds the program.

## Lists

```swift
var nums = [1, 2, 3]
nums.push(4)
println(nums.len)        // 4
println(nums[0])         // 1
println(nums)            // [1, 2, 3, 4]
let snapshot = nums.copy()   // a separate list with the same elements

let empty: [string] = []
```

**Working with lists.**

```swift
if tasks.isEmpty { return }
let next = tasks.first ?? defaultTask      // first and last are optionals
tasks.sort(by: fn(a, b) => a.priority < b.priority)
let names = users.map(fn(u) => u.name).sorted().join(", ")
let total = prices.sum()
let adults = people.filter(fn(p) => p.age >= 18)
let firstAdmin = users.find(fn(u) => u.admin)        // User?
let words = lines.flatMap(fn(l) => l.split(" "))
let letters = words.reduce(0, fn(n, w) => n + w.len)
```

| Member | Result |
|---|---|
| `.len`, `.isEmpty`, `.first`, `.last` | Properties; `first` and `last` are `nil` for an empty list |
| `.push(x)`, `.pop()`, `.insert(x, at: i)`, `.remove(at: i)`, `.removeWhere(f)`, `.clear()` | Change the list in place; `remove` and `pop` give the element removed |
| `.sort()`, `.sort(by: f)` | Sort in place. `sort()` needs elements that `<` orders: numbers, strings, enums whose cases carry no values, and structs that define `operator <`. `by:` takes whether `a` goes before `b`. Sorting is stable: equal elements keep their order |
| `.sorted()`, `.sorted(by: f)`, `.reversed()`, `.slice(from, to)`, `.copy()` | New lists; the original is unchanged. `slice` takes the elements from `from` up to `to` |
| `.contains(x)`, `.indexOf(x)` | Whether `x` is in the list, and the index of the first match as an `int?`; elements need `==` |
| `.find(f)`, `.any(f)`, `.all(f)` | The first element `f` accepts, as an optional; whether any or every element passes |
| `.filter(f)`, `.map(f)`, `.flatMap(f)` | New lists: the elements `f` accepts; what `f` returns for each; the lists `f` returns, joined |
| `.reduce(initial, f)` | Combines the elements, starting from `initial`: `xs.reduce(0, fn(sum, x) => sum + x)` |
| `.min()`, `.max()`, `.sum()` | The smallest and largest element as optionals; the total of an `[int]` or `[float]`, which stops the program if an int total overflows |
| `.join(separator)` | The elements as text, as `str` shows them, with the separator between; it may be left out |

- An index out of range stops the program, for `insert`, `remove` and `slice` as for `xs[i]`.
- Changing the list from inside the function passed to `sort(by:)` or `removeWhere` stops the program. `filter`, `map`, `find` and the others visit the elements there were at the start, as a `for` loop does.

**Collection `if`, `for` and spread.** Inside a list or map literal, `if` and `for` produce elements and `...` adds every element of another collection, as in Dart:

```swift
let items = [
    header,
    if loggedIn profile else loginButton,
    for u in users u.name,
    for x in nums if x > 0 x * 2,
    ...extras,
    ...?maybeMore,                 // nothing when maybeMore is nil
]
let scores = ["ann": 3, if bonus "bob": 5, ...others]
```

- The element follows the condition or loop directly, without braces. `if` and `for` nest, and `else if` chains work.
- A `(`, `[` or `-` after a space starts the element: in `[if wide [1, 2]]` the list is the element, and in `[if neg -1]` the element is `-1`. Without the space, `xs[0]` and `f(x)` stay part of the condition.
- `if let n = maybe n` works too.
- Spreading a map into a map sets its entries in order, so a later key replaces an earlier one.

## Maps

```swift
var ages = ["ann": 31, "bob": 25]
ages["cat"] = 40               // add or replace
let a = ages["ann"]            // int?, nil when the key is missing
let z = ages["zoe"] ?? 0
if let b = ages["bob"] { println(b) }
ages.remove("bob")             // returns the removed value, an int?
let backup = ages.copy()       // a separate map with the same entries
println(ages.len, ages.contains("cat"), ages.keys, ages.values)

for name, age in ages { println(name, age) }
for name in ages { println(name) }

let empty: [string: int] = [:]
```

- Loops visit entries in the order their keys were first added. A loop sees the entries that existed when it started; entries removed during the loop are skipped.
- Keys can be `int`, `string`, `bool`, an enum whose cases carry no values, a record of key types, or a struct or enum that lists `Hashable` (see [Equality and hashing](#equality-and-hashing)). Values can be any type except an optional, since a missing key already reads as `nil`.
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

**Getters and setters.** A getter is read like a field and a setter runs when the property is assigned, as in Dart:

```swift
struct Slider {
    _value: float = 0

    get value -> float => _value
    set value(v: float) {
        _value = math.clamp(v, 0.0, 1.0)
        redraw()
    }
    get percent -> int {
        return int(_value * 100.0)
    }
}

slider.value = 2.0           // runs the setter: stored as 1.0
slider.value += 0.1          // reads with the getter, then sets
println(slider.percent)      // no ()
```

- A getter has a result type and no parameters; a setter takes exactly one parameter, the new value, of the getter's type.
- A property with only a getter is read-only; one with only a setter can't be read.
- `pub` makes each one visible to other modules on its own. `get` and `set` are names only where they start a getter or setter, so fields and methods may still be called `get`.
- Interfaces can require getters and setters (see [Interfaces](#interfaces)); enums can have getters but not setters, since their values never change.

**didSet.** A block after a field runs after each assignment to it, with the previous value in `oldValue`, as in Swift:

```swift
struct Counter {
    count: int = 0 {
        didSet {
            if count != oldValue {
                redraw()
            }
        }
    }
}

counter.count += 1           // runs didSet
```

- It runs for every assignment, `x.count = ...` and `count = ...` in methods alike, but not when the struct is created.
- Assigning the field inside its own `didSet` doesn't run it again.
- Changing what a field refers to, such as pushing to a list field, isn't an assignment and doesn't run it.

**Printing.** A struct or enum shows as its name and fields, such as `User(name: "Ann", age: 30)`. A `toString()` method replaces that, as in Dart and Kotlin, wherever the value is shown: `println`, `str`, interpolation, and inside lists, maps, optionals and other structs.

```swift
struct Money {
    cents: int

    fn toString() -> string => "${cents / 100}.{cents % 100}"
}

let m = Money(cents: 1250)
println(m)                   // $12.50
println("cost: {m}")         // cost: $12.50
println([m, m])              // [$12.50, $12.50]
```

- `toString` must be written `fn toString() -> string`: no parameters, not static, async or throwing. An extension can add it too.
- Showing `self` inside `toString` calls it again without end; show the fields instead.

**Operators.** A struct defines an operator like a method, as in Dart: `self` is the left operand and the parameter the right one.

```swift
struct Vec {
    x: float
    y: float

    operator +(other: Vec) -> Vec => Vec(x: x + other.x, y: y + other.y)
    operator -() -> Vec => Vec(x: -x, y: -y)
    operator *(k: float) -> Vec => Vec(x: x * k, y: y * k)
    operator ==(other: Vec) -> bool => x == other.x && y == other.y
}

struct Version {
    major: int
    minor: int

    operator ==(other: Version) -> bool => major == other.major && minor == other.minor
    operator <(other: Version) -> bool =>
        major < other.major || (major == other.major && minor < other.minor)
}

struct Grid {
    cells: [int]
    width: int

    operator [](at: (int, int)) -> int => cells[at.$2 * width + at.$1]
    operator []=(at: (int, int), value: int) {
        cells[at.$2 * width + at.$1] = value
    }
}

let v = a + b * 2.0
v += a                        // v = v + a
println(v1 >= v2)             // from <
grid[(1, 0)] = 5
grid[(1, 0)] += 1             // reads with [], then sets with []=
```

- The operators are `+ - * / %`, unary `-` (no parameter), `==`, `<`, `[]` and `[]=`. The right operand of the arithmetic ones and the index can have any type.
- `!=` is the opposite of `==`, and `>`, `<=` and `>=` come from `<`, so they can't be defined themselves.
- `==` and `<` take the struct itself and return `bool`. A struct with `==` is `Equatable`, so it works with `contains` and in records compared with `==`; one with `<` and `==` is also `Comparable`. To compare all fields without writing `==`, list `Equatable` instead.
- A compound assignment such as `+=` uses the operator, which must return the struct. Operands run left to right, as for other operators.
- Operators can't be static, generic, async or throwing, and their parameters have no defaults. `pub` makes one visible to other modules, and extensions of structs can add them. Enums, interfaces and built-in types can't define operators yet.

## Equality and hashing

Structs are compared by reference until they ask for more. Listing `Equatable` gives a struct or enum an `==` that compares every field; `Hashable` adds a hash, so its values can be map keys.

```swift
struct Point: Hashable {
    x: int
    y: int
}

let a = Point(x: 1, y: 2)
let b = Point(x: 1, y: 2)
println(a == b)                    // true: same fields
println(a === b)                   // false: two separate structs
var names = [a: "home"]
println(names[b] ?? "?")           // home

enum Key: Hashable {
    id(n: int)
    name(s: string)
}

struct Email: Hashable {
    address: string
    operator ==(other: Email) -> bool => address.lower() == other.address.lower()
    fn hash() -> int => hash(address.lower())
}

println([1, 2] == [1, 2], ["a": [1]] == ["a": [1]])   // true true
let grid: [(int, int): string] = [(0, 0): "origin"]
```

- `Equatable` needs every field to have `==`, and `Hashable` needs every field to be hashable, or a `float`. A generic struct lists the constraint on its parameter: `struct Box<T: Equatable>: Equatable`.
- A struct that writes its own `operator ==` and lists `Hashable` must also write `fn hash() -> int`, so that equal values hash the same. `hash` takes no parameters.
- `hash(a, b, ...)` combines the hashes of its arguments into an `int`. Inside a type whose `hash` method takes parameters, `hash(x)` calls that method.
- Lists compare element by element, maps entry by entry whatever their order, optionals by their value or both `nil`, and records field by field. Each works when its elements have `==`.
- `===` and `!==` tell whether two references are the same object: structs, lists, maps, interface values, C handles and enums whose cases carry values. They don't apply to ints, strings and other values.
- Records of key types are hashable, so they can be map keys without declaring anything.

## Generics

Functions, structs, enums and methods can take type parameters in `< >`. The types are usually worked out from the arguments, or from the type the result is stored in; they can also be written at the call.

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

A type parameter can require what the generic code needs from it: an [interface](#interfaces), or one of the built-in constraints below, with `&` between several:

| Constraint | Allows | Met by |
|---|---|---|
| `Equatable` | `==`, `!=`, `list.contains` | int, float, bool, string, enums whose cases carry no values, structs that define `operator ==`, structs and enums that list `Equatable` or `Hashable`, and lists, maps, optionals and records of these |
| `Comparable` | `<`, `<=`, `>`, `>=`, and `==` | int, float, string, enums whose cases carry no values, structs that define `operator <` and `operator ==` |
| `Hashable` | map keys, and `==` | int, bool, string, enums whose cases carry no values, records of these, and structs and enums that list `Hashable` |
| an interface, such as `Shape` | its methods | structs that implement it, and the interface itself |

```swift
fn largest<T: Comparable>(xs: [T]) -> T? { ... }
fn count<T: Hashable>(xs: [T]) -> [T: int] { ... }
struct Index<K: Hashable & Comparable, V> { ... }
```

- Generic code is checked once, with `T` standing for any type that meets its constraints, so `a > b` on a plain `T` is an error that names the missing constraint.
- Each use with new types compiles to its own copy, specialised for those types. Generic code runs as fast as code written for one type.
- Type arguments in a call touch the name and the `(`: `first<int>(xs)`. With spaces, `<` and `>` are comparisons.

**Generic enums.** The type arguments come from the case's values or from the expected type; write them out only when neither says, as in `Option<int>.none`:

```swift
enum Result<T> {
    ok(value: T)
    err(message: string)
}

fn parse(s: string) -> Result<int> {
    if s == "" { return .err("empty") }     // T from the result type
    return .ok(int(s))
}

let r = Result.ok(5)                         // Result<int>
let e = Result<string>.err("bad")
```

**Generic methods.** A method can declare its own type parameters after its name, worked out at each call like a generic function's:

```swift
struct Box<T> {
    item: T
    fn map<U>(f: fn(T) -> U) -> Box<U> => Box(item: f(item))
}

let b = Box(item: 2).map(fn(x) => "#{x}")    // Box<string>
let c = Box(item: 2).map<float>(fn(x) => float(x))
```

**Generic functions as values.** A generic function can be stored or passed once its types are known: from the expected function type, or written out.

```swift
let f: fn([int]) -> int? = first             // T from the type
let g = first<string>
let heads = groups.map(first<string>)        // groups: [[string]]
```

- Not yet: generic methods in interfaces.

## Interfaces

An interface lists methods and properties. A struct or enum names the interfaces it implements after a `:`, and the compiler checks there that it has every member with the right types.

```swift
interface Shape {
    fn area() -> float
    fn name() -> string => "shape"             // a default
    fn summary() -> string => "{name()}: {area()}"
}

struct Circle: Shape, Resizable {
    radius: float
    fn area() -> float => 3.14 * radius * radius
    fn name() -> string => "circle"            // replaces the default
    fn resize(by: float) { radius = radius * by }
}

let shapes: [Shape] = [Circle(radius: 1), Rect(width: 2, height: 3)]
for s in shapes { println(s.summary()) }

fn largest<T: Shape>(items: [T]) -> T? { ... }
```

- A struct can be used wherever an interface it implements is expected: in a `[Shape]`, a `Shape` parameter or a `Shape?`. Without a written type, values must have the same type, so write `let shapes: [Shape] = [...]` for a mixed list.
- Methods with a body are defaults. A struct that leaves one out gets it, and a default calls the struct's own versions of the other methods.
- Calls on an interface value go through a table of the struct's methods, which is as fast as Go's interfaces. In generic code like `largest<T: Shape>`, each type gets its own copy and calls go straight to its methods.
- Interface values print as the value inside them. They can't be compared with `==`, but `===` tells whether two hold the same object.

**Extending.** An interface can extend others after a `:`. It requires their members too, and its values can be used as any of them.

```swift
interface Drawable { fn draw() -> string }
interface Shape: Drawable {
    fn area() -> float
    fn describe() -> string => "{area()}: {draw()}"
}

struct Square: Shape { ... }                 // needs draw() and area()
let d: Drawable = Square(side: 2)            // a Shape is a Drawable
```

**Properties.** `get name -> T` requires a getter and `set name(value: T)` a setter. A struct can provide either with a getter or setter, or with a stored field of that type.

```swift
interface Sized {
    get size -> int
}

struct Text: Sized {
    value: string
    get size -> int => value.len             // a getter
}

struct Block: Sized {
    size: int                                // a field also counts
}

fn total(xs: [Sized]) -> int { ... x.size ... }
```

**Enums** implement interfaces the same way, usually with a `match` on `self`. Enums can have getters, but not setters, since their values never change.

```swift
enum Suit: Drawable {
    hearts
    spades
    fn draw() -> string => match self {
        hearts => "♥"
        spades => "♠"
    }
}

let items: [Drawable] = [Suit.hearts, Square(side: 2)]
```

**Generic interfaces** take type parameters like structs. A type parameter's constraint can name itself, as in `T: Ordered<T>`:

```swift
interface Store<T> {
    fn get(key: string) -> T?
    fn put(key: string, value: T)
    fn getOr(key: string, fallback: T) -> T => get(key) ?? fallback
}

interface Ordered<T> { fn compare(other: T) -> int }

struct Cache: Store<int> { ... }
struct MapStore<T>: Store<T> { ... }
struct Money: Ordered<Money> { ... }

fn largest<T: Ordered<T>>(xs: [T]) -> T? { ... }
```

- Not yet: generic methods in interfaces, and static members.

**Type tests.** `is` asks whether an interface value holds a given struct, enum or interface, and `as?` gives it as that type, or `nil`:

```swift
fn render(view: View) -> string {
    if view is Text {
        return view.content                 // view is a Text here
    }
    if view is Image && view.width > 0 { ... }
    if let b = view as? Button { b.press() }
    let width = (view as? Image)?.width ?? 0
    return match view {
        Button(:label) => "button {label}"  // a struct pattern tests the type too
        t: Text => t.content                // binds the whole value as a Text
        _ => "other"
    }
}
```

- After `x is T`, `x` has type `T` in the `if` block, the rest of an `&&` chain, an `if` value's branch and a `while` body. This needs a name that can't change: a `let`, a parameter, a loop variable or a pattern name. A `var` keeps its type; bind it with `if let t = x as? T`.
- The value tested must be an interface (unwrap an optional first), and the type must be able to hold it: a struct or enum that implements the interface, or another interface.
- Interfaces are open, so a `match` on one needs a `_` arm.
- Vex has no `as` without `?`, as it has no force unwrap.
- A test is one comparison: each struct and enum value carries a pointer to its type's method tables.

## Extensions

`extension` adds methods, getters and static members to a type that already exists, as in Swift, including built-in types and types from other modules:

```swift
extension string {
    fn shout() -> string => upper() + "!"
    get isBlank -> bool => trim().len == 0
}

extension Point {
    fn distance(to: Point) -> float { ... }
    static fn origin() -> Point => Point(x: 0, y: 0)
    static const dimensions = 2
}

println("hi".shout())          // HI!
println(Point.origin())
```

Inside an extension, the value is `self`, and its members can be used without `self.`, as in a struct: `upper()` means `self.upper()`.

- Structs and enums, generic ones included, get methods, getters, setters, static methods and constants. An extension of a generic type names it without type arguments, and its members use the type's own parameters: `extension Box { fn get() -> T => value }`.
- `int`, `float`, `bool`, `string`, lists and maps get methods and getters. Lists and maps are extended for one element type, `extension [int]`, or for all, with type parameters and constraints:

  ```swift
  extension<T: Comparable> [T] {
      fn largest() -> T? { ... }
  }
  extension<K: Hashable, V> [K: V] {
      get size -> int => len
  }
  ```

- An extension for one type, such as `[int]`, is chosen before one for all lists.
- Extensions can't add fields, and can't change what a type already has: a member with the same name as an existing one is an error. A method an extension adds can meet an interface the struct already lists after `:`.
- An extension works in its own module. In other modules, its `pub` members work in files that import the extension's module; `pub extension` makes every member public.
- Interfaces, optionals, records and function types can't be extended yet.

## Modules

A folder is a module: its `.vx` files share their names and use each other without imports. A project is a folder with a `package.toml`, and its top folder is the root module, which holds `main`.

```
store/
  package.toml
  main.vx            // fn main() and the rest of the root module
  totals.vx
  models/
    item.vx          // module "models"
    cart.vx
  format/
    receipt.vx       // module "format"
```

```toml
[package]
name = "store"
version = "0.1.0"
```

Imports go at the top of a file and belong to that file:

```swift
import "vex:math"             // the standard library
import "models"               // a folder of this project, relative to this file
import "util" as u            // renamed with as

fn main() {
    let cart = models.Cart(owner: "Ana")
    println(math.max(3, 9), u.twice(4))
}
```

Names from another module are written through the module's name, which is the last part of the path: `models.Cart`, `math.max`, `models.Kind.tool`, `models.Box<int>`. A name is only visible outside its module when it is marked `pub`:

```swift
// models/cart.vx
pub struct Cart {
    pub owner: string
    items: [Item] = []                   // only models can read it

    pub fn add(item: Item) { items.push(item) }
    pub static fn empty() -> Cart => Cart(owner: "nobody")
    fn audit() { }                      // private
}

pub enum Kind { tool, paper }          // cases are public with the enum
pub interface Shape { fn area() -> int }
fn helper() { }                         // private to models
```

- `pub` goes on functions, structs, enums, interfaces, fields and methods. Enum cases and interface methods are public whenever their enum or interface is, and so is a method a struct writes for one of its interfaces.
- Another module can only create a struct whose private fields all have defaults. Otherwise give it a `pub static fn` that creates it.
- `vex:` modules come with the compiler:
  - `vex:math`: `abs`, `min`, `max`, `clamp`, `pow` and `gcd` for integers and comparable values; `sqrt`, `cbrt`, `powf`, `exp`, `log`, `log2`, `log10`, `sin`, `cos`, `tan`, `asin`, `acos`, `atan`, `atan2`, `hypot`, `floor`, `ceil`, `round`, `trunc`, `absf` and `pi()` for floats
  - `vex:strings`: `repeat`, `join`, `padLeft`, `padRight`
  - `vex:time`: `nanos()` for measuring, `unix()` for the date, `sleep(ms)`, a future that completes after `ms` milliseconds, and `timeout(future, ms)` (see [Cancellation](#cancellation-and-timeouts))
  - `vex:fs`: `read`, `write`, `append`, `exists`, `isDir`, `size`, `remove`, `makeDir`, `list`; files are text, and failures throw an `Error` naming the path and the reason
  - `vex:isolate`: `run` and `spawn`, which start work on other cores (see Isolates), and `cores()`
  - `vex:os`: `args()`, `env`, `setEnv`, `exit`, `cwd`, `run` (a shell command, printing to the terminal; returns its exit status) and `output` (returns what it printed; throws on a non-zero status), and `target` and `arch`, the system the program is built for (see [Platform conditionals](#platform-conditionals))

```swift
import "vex:fs"
import "vex:os"

fn main() throws {
    let path = os.args()[0]
    let text = try fs.read(path)
    try fs.write(path + ".bak", text)
    println(try fs.list("."))
}
```

`vex run app.vx a b` passes `a` and `b` to the program; write `vex run -- a b` to run the project in the current folder with arguments.
- Modules may import each other. A name that is already an import, such as a variable called `math`, is an error, so `math.max` always means the module.
- `vex run` with no file runs the project in the current folder, and `vex run store` runs the one in `store/`. A single file can import too: `vex run hello.vx`.
- `package:` imports come with the package manager (V-121).

## C functions

`extern fn` declares a function written in C. It has no body, and `as "name"` calls it under a different C name. It can be `pub`, so a module can wrap C code behind Vex names, the way `vex:math` does.

```swift
extern fn strlen(s: string) -> int
extern fn getenv(name: string) -> string?
extern fn sqrt(x: float) -> float
extern fn next() -> int as "counter_next"

println(strlen("hello"), getenv("HOME") ?? "no home")
println(["a", "bb"].map(strlen))          // a C function is a value like any other
```

| Vex | C |
|---|---|
| `int` | `int64_t` (`long` on 64-bit systems, not `int`) |
| `float` | `double` |
| `bool` | `bool` |
| `string` | `const char *`: a NUL-terminated copy, valid during the call |
| `string?` | `const char *` that may be `NULL` (`nil`) |
| `int8` ... `int64`, `uint8` ... `uint64` | `int8_t` ... `uint64_t`; `int` in Vex code |
| `float32`, `float64` | `float`, `double`; `float` in Vex code |
| an `extern type`, such as `Db` | a pointer; `Db?` may be `NULL` |
| `fn(...)` | a function pointer and its `void *` context: two C parameters |
| no result | `void` |

- A string result is copied into a Vex string, and `NULL` gives `nil` for `string?` (or `""` for `string`).
- No header is needed: the compiler writes the declaration from the Vex signature. It must match the C function, as in C itself. A C function that returns `int` must be declared without a result, or wrapped in C, since only the low 32 bits of the result are set.
- Well-known math functions such as `sqrt` and `sin` compile to the C compiler's built-ins, so they run as fast as in C.
- Extern functions can't be generic, throw, or be methods.
- Sized numbers convert at the call: an `int` passed as `int32` keeps its low 32 bits, as a C cast does. They can only be used in `extern fn` and `export fn` signatures. Everywhere else, Vex has one `int` and one `float`.

**C pointer types.** `extern type` declares a type for pointers that C code owns, such as a `sqlite3 *` or a `UIView *`. Vex passes them around but never looks inside, so a `Db` can't be passed where a `View` is expected. `as "name"` gives the C name used when one is printed.

```swift
extern type Db as "sqlite3"
extern fn db_open(path: string) -> Db?       // NULL gives nil
extern fn db_close(db: Db)

if let db = db_open("app.db") {
    println(db)                              // <sqlite3 0x6000...>
    db_close(db)
}
```

Handles can be compared with `==` and kept in fields, lists and optionals. They can't be created in Vex.

**Callbacks.** A `fn(...)` parameter passes a Vex closure to C as two C parameters: a function pointer that takes a `void *` context first, then the context. Callback parameters may be `int`, `float`, `bool`, `string`, `string?` and extern types, using the C types in the table above. The result may be `int`, `float`, `bool` or an extern type.

```swift
// C: void on_tap(void *view, void (*cb)(void *ctx, int64_t count), void *ctx);
extern fn on_tap(view: View, handler: fn(int))

var taps = 0
on_tap(button, fn(n: int) { taps = taps + n })
```

C may keep the context and call it later. The closure stays alive until C passes the context to `vex_release`, which it declares as `void vex_release(void *ctx);`. Callbacks must be called on the thread that runs the Vex program.

**Exported functions.** `export fn` makes a top-level function callable from C under its own name, with the C types from the table above. C code declares it itself, e.g. `int64_t vex_frame(double t);`.

```swift
export fn vex_frame(t: float) -> int {
    render(t)
    return 0
}
```

Exported functions can't be generic or throw, and can't return strings yet. C can call them while the Vex program runs: from code that Vex called, or from a callback.

A project adds its own C code in `package.toml`:

```toml
[c]
sources = ["native/sqlite3.c", "native/glue.m"]   # .c and .m files, or built .o and .a
libs = ["z"]                                       # linked as -lz
frameworks = ["Foundation"]                        # linked as -framework Foundation
flags = ["-DSQLITE_OMIT_LOAD_EXTENSION", "-Inative"]
```

`.m` files are Objective-C, compiled with automatic reference counting (`-fobjc-arc`). Together with `frameworks`, this is how Vex code reaches Apple's APIs. Frameworks can only be linked on macOS.

Paths are relative to the project. Flags may start with `-D`, `-U`, `-I`, `-L`, `-O`, `-f`, `-W`, `-m` or `-std=`.

C code for one system goes in `[c.macos]`, `[c.linux]` or `[c.windows]`, with the same keys. Its lists are added to those of `[c]` when building for that system:

```toml
[c]
sources = ["native/common.c"]

[c.macos]
sources = ["native/clipboard_mac.m"]
frameworks = ["AppKit"]

[c.linux]
sources = ["native/clipboard_x11.c"]
libs = ["X11"]
```

### Platform conditionals

`os.target` and `os.arch` are constants naming the system the program is built for, as the enums `os.Target` (`macos`, `linux`, `windows`) and `os.Arch` (`arm64`, `x64`). An `if` or `match` that tests them compiles only the branch for that system:

```swift
import "vex:os"

fn open(path: string) throws {
    if os.target == .macos {
        try os.run("open {path}")
    } else if os.target == .windows {
        try os.run("start {path}")
    } else {
        try os.run("xdg-open {path}")
    }
}

let separator = match os.target {
    windows => "\\"
    _ => "/"
}
```

- Every branch is still type-checked, so mistakes in code for other systems show up on yours. Only the C for the branch taken is generated, so a branch may call a C function that exists on one system only.
- This works for any condition made of constants: `const verbose = false` turns `if verbose { ... }` into nothing.
- The program is built for the system the compiler runs on.

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

**Local functions.** A function can be declared inside another, as in Swift, Kotlin and Dart. It sees the variables around it, like a closure, and can call itself.

```swift
fn countFiles(root: Dir) -> int {
    var total = 0

    fn visit(dir: Dir) {
        total += dir.files.len
        for sub in dir.subdirs {
            visit(sub)              // recursive
        }
    }

    visit(root)
    return total
}
```

- A local function is declared before it is used, as in Kotlin; one declared further down can't be called yet.
- It is a value like a closure: pass it as `xs.map(double)`, and call it with unnamed arguments. Its parameters can't have default values, and it can't have type parameters yet.
- `async`, `throws` and `=>` bodies work as at the top level.

**Tear-offs.** A method named without a call is a function value, as in Dart. It keeps the value it was torn off, even if the variable changes later. Static methods, enum cases that carry values and constructors work the same way; a constructor is written `Type.new`.

```swift
let greetings = names.map(host.greet)    // fn(string) -> string, bound to host
let users = names.map(User.new)          // fn(string) -> User
let shapes = radii.map(Shape.circle)     // fn(float) -> Shape
let origin = Point.origin                // fn() -> Point
let shout = "hi".upper                   // fn() -> string
let add = items.push                     // fn(int)

let make: fn(int) -> Box<int> = Box.new  // generic types take their type arguments from the expected type
```

- Methods of structs, enums and interfaces, extension methods and built-in members such as `upper` and `push` can be torn off. Inside a struct or enum, a method named alone is torn off `self`, or the type when static: `names.map(greet)`.
- `x?.method` is nil when `x` is nil, and otherwise a function bound to the value inside.
- A tear-off has the method's parameters and result, and throws what the method throws. Default arguments don't carry over: the function takes every parameter.
- A method must be torn off a value: `User.greet` is an error. Async and generic methods, and `map`, can't be torn off yet; call them in a closure.

**Trailing blocks.** A `{ ... }` block after a call is passed as its last argument, which must be a function or a list (see builder blocks below). A call with only a trailing block can leave out the `()`: `Column { ... }`, `xs.map { ... }`.

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

let doubled = xs.map { x in x * 2 }         // parameters before in
scores.sort { a, b in a > b }
let adults = people.filter { it.age >= 18 } // one parameter can stay unnamed: it
let names = people.map { p in
    let first = p.name.split(" ")[0]
    first.upper()                            // the last expression is the value
}
```

- A block names its parameters before `in`, on the line of the `{`: `{ a, b in ... }`. Their types come from the function the call expects.
- A block for a function of one parameter can leave it unnamed and call it `it`, as in Kotlin. A variable named `it` that is already in scope wins; name the parameter then.
- When the function returns a value, the block's last expression is that value. `return` works too.
- In the head of `if`, `while`, `for` and `match`, a `{` starts the body, so a call there needs parentheses: `if xs.any(fn(x) => x > 1) { ... }`.

**Builder blocks.** When the last parameter is a list, a trailing block builds that list: each line is one element. `let`, `var`, `if`, `for` and `...` work inside, with braces as in ordinary code:

```swift
struct Column: View {
    spacing: int = 0
    children: [View]
}

Column(spacing: 12) {
    Text("Hello")
    let count = items.len
    if count > 0 {
        Text("{count} items")
    } else {
        Button("Add") { add() }
    }
    for item in items {
        Row(item.name)
    }
    ...footer
}
```

- Each line must give an element of the list type; a call without a value, such as `println(...)`, is an error inside the block. Other statements (`return`, assignments, `while`) belong outside it or in a closure.
- Builder blocks nest, and closures inside them, such as a button's `onTap`, are ordinary closures.

**Lists.** `filter` keeps the items a function accepts; `map` turns each item into a new value.

```swift
let evens = nums.filter { it % 2 == 0 }
let labels = nums.map { "#{it}" }      // [string]
```

## Records

A record groups a few values without declaring a struct, so a function can return several things. Types and values are written as in Dart 3: positional fields in parentheses, named fields in braces in the type.

```swift
fn divmod(a: int, b: int) -> (int, int) {
    return (a / b, a % b)
}

fn bounds(xs: [int]) -> ({int min, int max}) {
    return (min: xs[0], max: xs[xs.len - 1])
}

let r = divmod(17, 5)
println(r)                     // (3, 2)
println(r.$1, r.$2)            // positional fields count from $1

let b = bounds([1, 5, 9])
println(b.max)                 // 9

let (q, rem) = divmod(9, 4)    // take a record apart
let (:min, :max) = b           // a named field into a variable of the same name
var (min: lo, max: _) = b      // into another name; _ skips a field

for (name, score) in [("ann", 3), ("bob", 5)] {
    println(name, score)
}
```

- A record can mix both kinds: `(int, {string name})`, written `(3, name: "vex")`. A record with one positional field is written `(int,)` and `(1,)`; `(x)` is just `x` in parentheses.
- Records are values. Two records are equal with `==` when their fields are, and the order of named fields doesn't matter: `(a: 1, b: 2) == (b: 2, a: 1)`.
- The type is the shape: any `(int, string)` is the same type as any other, with no declaration.
- Fields can't be changed; make a new record instead.
- A pattern names every field of the record, using `_` for the ones it doesn't need. Patterns nest, and work in `match` and `if case` too (see [Patterns](#patterns)).
- Records print like their values, with named fields in alphabetical order: `(3, color: Color.green, name: "vex")`.
- Records work with generics (`fn swap<A, B>(p: (A, B)) -> (B, A)`), optionals, lists, `contains`, and isolates. They can't be map keys yet.

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

while let job = queue.next() {            // loops until the value is nil
    run(job)
}

let name = findUser(2)?.name ?? "guest"   // ?. gives nil if the left side is nil; ?? gives a default
let boss = user?.manager?.name            // string?: each step after an optional needs ?.
user?.greet()                             // runs only when user isn't nil

if user == nil { ... }
if id == 3 { ... }                        // true only when id isn't nil and is 3
```

**Several conditions.** `if`, `while` and `guard` take conditions and bindings separated by commas, as in Swift. They are checked left to right, each name is visible to the conditions after it, and the block runs only when all hold.

```swift
if let user, let email = user.email, email.contains("@") {
    send(email)                           // if let user is short for if let user = user
}

guard let id = int.parse(text), id > 0 else {
    return                                // id is an int from here on
}
guard ready else { return }               // guard works with a plain condition too

while let job = queue.next(), !stopped {
    run(job)
}
```

**Leaving early.** The right side of `??` can be `return`, `throw`, `break` or `continue`, which runs when the left side is nil, as Kotlin's `?: return`:

```swift
let text = fs.read(path) ?? throw Error("missing {path}")
let port = int.parse(text) ?? return 80

for line in lines {
    let n = int.parse(line) ?? continue
    total += n
}
```

- `x ?? fallback` only runs `fallback` when `x` is nil. Chains like `a ?? b ?? 0` try each in turn.
- A comma condition can follow a line break: `if let a = x,` with the next condition on the next line.
- `if value case pattern` can't take more conditions after a comma; add them with `when`.
- The names a `guard` binds can't be used in its `else` block, which must leave.
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
- `==` and `!=` work on enums whose cases carry no values, like `Light`, and on enums that list `Equatable` or `Hashable`, which compare the case and then its values.

**match** picks the first arm whose pattern fits and runs it. It works on any value: enums, numbers, `bool`, `string`, records, structs and lists.

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

- A match must handle every case, or end with `_`. On `int`, `float` and `string` it always needs `_`.
- Bindings follow the order of the case's values, `rect(w, h)`, or name them: `rect(height: h)`.
- Used as a value, every arm needs an expression of the same type; an arm that calls `panic(...)` is allowed too.
- Used as a statement, an arm can be a block or a single statement like `return`, `break` or `count += 1`.

## Patterns

Patterns test a value's shape and take it apart in one step. They nest anywhere, and work in `match` arms, `if case`, `let`, `var` and `for`.

```swift
let grade = match score {
    90..=100 => "A"                    // ranges, like in for loops
    80..90 => "B"
    > 100 => "bonus"                   // comparisons
    _ => "C"
}

match code {
    200 || 201 || 204 => "ok"          // alternatives
    _ => "fail"
}

match p {
    Point(x: 0, y: 0) => "origin"      // struct patterns name their fields
    Point(x: 0, :y) => "on y at {y}"   // :y binds the field y
    Point(:x, :y) when x == y => "diagonal"
    _ => "elsewhere"
}

match xs {
    [] => "empty"
    [one] => "just {one}"
    [first, ...rest] => "{first} then {rest.len} more"
}

match shape {
    circle(Point(x: 0, y: 0), r) => "centred"
    circle(radius: r) when r > 10 => "big"   // when adds a condition
    _ => "other"
}

if shape case circle(r) when r > 1 {       // test one pattern
    println("radius {r}")
}

let ((a, b), Point(:x, :y)) = (pair, point)
for Point(:x, :y) in points {
    println(x + y)
}
```

| Pattern | Matches |
|---|---|
| `_` | anything |
| `name` | anything, bound to `name`; an enum case such as `empty` when the value is that enum |
| `0`, `-1.5`, `"yes"`, `true` | that value |
| `1..=9`, `0..10` | an `int` or `float` in the range |
| `> 100`, `<= 0` | an `int`, `float` or `string` that compares so |
| `circle(r)`, `rect(height: h)` | that enum case, its values matching the inner patterns |
| `Point(x: 0, :y)` | a struct whose named fields match; others are ignored. On an interface value, it also tests that the value is a `Point` |
| `t: Text` | an interface value that is a `Text`, bound to `t` as one |
| `(a, b)`, `(:min, max: _)` | a record, every field given |
| `[a, b]`, `[first, ...rest]`, `[..., last]` | a list of that length, or at least that long with `...` |
| `a \|\| b` | either pattern; each binds the same names |

- A match must cover every value, nested shapes included: the compiler names what's missing, such as `match doesn't handle '(_, false)'`. Arms with `when` don't count toward that.
- An arm that earlier arms already cover is an error.
- `let`, `var` and `for` patterns must match every value; use `match` or `if case` for ones that can fail.
- Names bound by `match` and `if case` can't be changed; `var` patterns make changeable variables.
- `...rest` copies the remaining elements into a new list.

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
- Closures and function types can throw too: `fn(int) throws -> int`. A closure passed where a throwing function is expected may throw. Async ones are written `async fn(int) throws -> int`.
- `fn main() throws` lets errors reach the top: the program prints `error: <message>` and exits with code 1.
- Errors are passed back like return values, not by unwinding the stack, so code that doesn't throw pays nothing and a `try` costs one check after each call.

## Async

An `async fn` can wait for other work with `await` without blocking the program: while it waits, other tasks and timers run. Its result type is the plain value, as in Dart.

```swift
import "vex:time"

async fn fetch(url: string) throws -> string {
    await time.sleep(100)                    // pauses this function only
    return "page of {url}"
}

async fn main() throws {
    let page = try await fetch("a.com")      // await gives the value; try handles the error
    println(page)
}
```

Calling an async function without `await` starts it right away and gives a `Future<T>`, so several can run at once:

```swift
let a = fetch("a.com")                       // Future<string, Error>; both are running now
let b = fetch("b.com")
let pages = try await all([a, b])            // [string], in the order given
let first = try await any([fetch("x.com"), fetch("y.com")])   // the first to finish
```

- `Future<T>` holds a value of `T` once it is done; `Future<T, E>` can also fail with `E`, which is `Error` or an enum. A future can be stored, passed and awaited any number of times.
- `await` works on a call of an async function, which runs it here, or on any future. Code that can fail needs `try`, `try?` or `try ... catch` around the await, as with other calls.
- `all(futures)` gives a future of every value, failing at the first error; with `Future<void>` it gives `Future<void>`. `any(futures)` gives the first to finish.
- `async fn main()` runs as the first task. The program ends when main is done and nothing else is pending: tasks still running keep it alive, and so do the timers they wait for. A plain `fn main()` can start tasks too, and they run after it returns.
- `run(future)` waits for a future from code that isn't async, running the other tasks meanwhile: `let page = try run(fetch("a.com"))`. Async functions use `await` instead.
- Methods, static methods and interface methods can be async; `extern fn` and `export fn` can't be. A struct method that implements an async interface method must be async too.
- Each task has its own small stack, so an `await` deep inside nested calls costs no more than one at the top, and code that doesn't await pays nothing.
- Apps with their own run loop, such as iOS and Android, call the C function `int64_t vex_poll(void)` from it. It runs everything that is ready and returns the milliseconds until the next timer, or -1 when nothing is pending.
- `all`, `any` and `run` are ordinary names: a function of the program with the same name replaces them.

**Async closures and function values.** `async fn(T) -> R` is the type of an async function used as a value. Calling such a value works like calling an async function: `await` gives the result, and a call without `await` gives a future.

```swift
let load: async fn(string) throws -> string = fetch     // an async function as a value
let page = try await load("a.com")
let later = load("b.com")                                // Future<string, Error>

let save = async fn() {
    await api.post(form)
}

fn button(label: string, action: async fn()) { ... }
button("Save") {
    await save()                // the block is async, since button expects an async fn
}

interface Repo {
    async fn find(id: int) -> User?
}
let user = await repo.find(7)
let tear = repo.find            // async fn(int) -> User?
```

- A closure is async when written `async fn(...)`, or when it is passed or assigned where an async function is expected, such as a trailing block. Only async closures can `await`.
- A plain function can be used where an async one is expected; it simply never waits. An async function can't be used where a plain one is expected.
- Async methods can be torn off like other methods, and `isolate.run` and `isolate.spawn` take async closures and functions, running them on the isolate's own loop.

**Cancellation and timeouts.** `f.cancel()` stops the task behind a future. A timeout gives `nil` when the time runs out, and cancels the late task.

```swift
let req = fetch(url)
req.cancel()                    // fetch stops at its next await
println(req.isCancelled)        // true

let page = await time.timeout(fetch(url), 5000) ?? "offline"

async fn loadScreen() {
    let a = fetch("a")          // started by loadScreen
    let b = fetch("b")
    show(await a, await b)
}
let screen = loadScreen()
screen.cancel()                 // cancels a and b too
```

- A cancelled task stops at its next `await` that waits for a future or a timer; the code after it never runs. Work that never awaits runs to its end.
- A task waiting for a cancelled future stops too, so cancellation reaches the code that needed the result. In code that isn't async, `run` of a cancelled future stops the program with an error; check `f.isCancelled` first.
- Cancelling a task also cancels every task it started that is still running, and theirs in turn.
- `time.timeout(f, ms)` is a future of the value as an optional: the value when `f` finishes in time, otherwise `nil`. Errors from `f` still need `try`. For a `Future<void>` it gives whether `f` finished. A future whose value is already optional can't be timed out, since `nil` would be ambiguous.
- Cancelling a future that is already done does nothing. A future from `isolate.run` can be cancelled, which drops its result, but the other isolate finishes its work.

**Streams.** A `Stream<T>` gives values over time. An `async fn` that returns a stream gives each value with `yield`, and `for await` reads them.

```swift
async fn ticks(n: int) -> Stream<int> {
    for i in 0..n {
        await time.sleep(100)
        yield i
    }
}

for await t in ticks(3) {
    println(t)                  // 0, 1, 2
}

let taps = StreamController<Point>()
button.onTap { p in taps.add(p) }
for await p in taps.stream {    // ends after taps.close()
    draw(p)
}
```

- Calling a stream function gives its stream at once, without `await`. Its body starts when the stream is first read, and pauses at each `yield` until the reader takes the value.
- `for await` works only in async code. Leaving the loop early, with `break`, `return` or an error, stops the stream: a stream function stops at its next `yield` or `await`.
- A stream function ends its stream by finishing its body or with a bare `return`. It can't `throw` yet; catch errors inside it, or yield a result value.
- A `StreamController<T>` makes a stream that other code fills: `add(value)` queues a value, `close()` ends the stream once the queued values are read, and `isClosed` tells whether it was closed. Adding to a closed controller is a runtime error.
- A stream has one reader at a time. Streams belong to the isolate that made them, so channels can't carry them.

## Isolates

Isolates run code in parallel on other cores. Each has its own memory and its own event loop, so there are no locks and no data races: values pass between isolates as copies.

```swift
import "vex:isolate"

async fn main() {
    let photos = load()
    // runs on another core; photos is copied there
    let thumbs = await isolate.run(fn() -> [Image] {
        return photos.map(fn(p) => shrink(p))
    })
    println(thumbs.len)
}
```

`isolate.run(f)` gives a `Future<T>` of what `f` returns, or `Future<T, E>` when it throws. It takes a closure or the name of a top-level function with no parameters. What the closure uses is copied when it starts, and its result is copied back.

**Channels** carry messages between isolates that keep running. `isolate.spawn(f)` starts an isolate that runs until `f` and its tasks are done:

```swift
fn worker(jobs: Channel<int>, out: Channel<string>) {
    while let n = jobs.receiveSync() {      // waits; nil once jobs is closed and empty
        out.send("done {n}")
    }
    out.close()
}

async fn main() {
    let jobs = Channel<int>()
    let out = Channel<string>()
    isolate.spawn(fn() { worker(jobs, out) })
    jobs.send(1)
    jobs.send(2)
    jobs.close()
    while let msg = await out.receive() {   // string?: nil once out is closed and empty
        println(msg)
    }
}
```

- `send(x)` copies `x` into the channel and never waits. Sending on a closed channel stops the program.
- `await receive()` waits without blocking the isolate: its other tasks keep running. `receiveSync()` blocks the whole isolate, which suits workers that do nothing else.
- `close()` lets receivers finish: messages already sent are still delivered, then each receive gives nil.
- A channel is shared, not copied: every isolate that has it sees the same messages. Several workers can receive from one channel, and each message goes to one of them.
- Ints, floats, bools, strings, lists, maps, optionals, structs and enums are copied, deeply. Objects shared or in cycles stay shared and cyclic in the copy. Functions, futures, C handles and interface values can't be copied, and using them in an isolate's closure, its result or a channel is a compile-time error.
- The program ends when `main` and its tasks are done, even if spawned isolates are still running. Keep `main` waiting on a channel when it needs their results.
- `isolate.cores()` gives the number of processor cores, for sizing a pool of workers.
- The memory an isolate used is freed when it ends. A channel's own memory is kept until the program ends.

## Tests

A `test` block names a test and holds its code. `vex test` runs every test of the project, or of one file; other builds leave tests out.

```swift
// math_test.vx
test "adds two numbers" {
    expect(add(2, 3) == 5)
}

test "parses a list" throws {
    let xs = try parse("[1, 2]")
    expect(xs.len == 2, "two items")
}
```

```text
$ vex test
✓ adds two numbers
✗ parses a list
  math_test.vx:7: expect(xs.len == 2)
    two items
    left:  3
    right: 2

2 tests: 1 passed, 1 failed
```

- Tests go in any file, or in files ending in `_test.vx`, which only `vex test` reads. Both see the private declarations of their module. Tests of imported modules don't run.
- `expect(cond)` reports a failure and lets the test go on, so one run shows every problem. When `cond` compares two values (`==`, `!=`, `<`, `<=`, `>`, `>=`), the failure shows both; an optional second argument adds a message. `expect` only works in tests.
- A test with `throws` may use `try`; an error passed up out of it fails the test.
- Each test runs in its own process, so a runtime error or crash fails that test and the others still run. `vex test -filter parse` runs only the tests whose names contain `parse`.
- A test can't await; use `run(future)` to wait for async work.
- The exit status is 1 when a test fails.

## Built-ins

| Name | Description |
|---|---|
| `print(a, b, ...)` / `println(...)` | Print values separated by spaces |
| `input(prompt)` | Read a line from standard input; the prompt is optional |
| `int(x)`, `float(x)`, `str(x)` | Convert between types; `int` and `float` ignore spaces around the number, and stop the program if the text isn't one |
| `int.parse(s)`, `float.parse(s)` | Parse text that may not be a number, giving `int?` and `float?`: `int.parse(field.text) ?? 0`. `int.parse(s, radix: 16)` reads other bases from 2 to 36 |
| `str(n, radix: 16)` | An int in another base from 2 to 36, with lowercase letters: `str(255, radix: 16)` is `"ff"` |
| `int.max`, `int.min`, `float.infinity`, `float.nan` | Limits and special values; constants can use them |
| `x.isNaN`, `x.isFinite`, `x.toFixed(2)` | Float members. `toFixed` gives text with exactly that many digits after the point, from 0 to 20, rounding halves away from zero as in Dart: `2.5.toFixed(0)` is `"3"` |
| `panic(msg)`, `assert(cond, msg)` | Stop the program with a message; `assert`'s message is optional |
| `expect(cond, msg)` | In tests, report `cond` as failed without stopping the test; see [Tests](#tests) |
| `list.len`, `.push(x)`, `.sort()`, `.filter(f)`, `.map(f)`, `.join(sep)` and the rest | List members; see [Lists](#lists) |
| `map.len`, `.keys`, `.values`, `.contains(k)`, `.remove(k)` | Map members |
| `s.len`, `.upper()`, `.lower()`, `.contains(x)`, `.split(sep)` | String members. `len` counts Unicode code points, so an emoji with a modifier counts as more than one; `upper` and `lower` change only the letters A–Z for now |
| `.replace(old, with)`, `.trim()`, `.trimStart()`, `.trimEnd()` | New strings: every `old` replaced; spaces, tabs and line endings removed from the ends |
| `.startsWith(x)`, `.endsWith(x)`, `.indexOf(x)` | `indexOf` gives an `int?`: the index of the first match, or `nil` |
| `.substring(from, to)`, `.chars()`, `.lines()` | The characters from `from` up to `to`; each character; each line without its line ending. Indexes count characters, as `len` does, and an index out of range stops the program |

## Types

The types are `int` (64-bit), `float` (64-bit), `bool`, `string`, lists `[T]`, maps `[K: V]`, structs, enums, function types `fn(T) -> R` and `async fn(T) -> R`, optionals `T?`, records `(int, string)` and futures `Future<T>`.

- `let x = ...` takes its type from the value. An empty list or map needs the type written: `let xs: [int] = []`, `let m: [string: int] = [:]`.
- Whole-number literals adapt to `float` where one is expected: `let price: float = 10`, `price * 2`.
- Int arithmetic that overflows stops the program instead of giving a wrong number: `9223372036854775807 + 1` is an error. When wrap-around is wanted, as in hashes and random number generators, use `&+`, `&-` and `&*` (and `&+=`, `&-=`, `&*=`), which work only on `int`:

  ```swift
  var h = 5381
  for c in codes { h = h &* 33 &+ c }
  ```

  The checks are cheap. In a tight loop where they cost speed, the wrapping operators remove them.
- The bitwise operators `&` `|` `^` `~` `<<` `>>` (and `&=`, `|=`, `^=`, `<<=`, `>>=`) work only on `int`, for colors, option masks, flags and hashing:

  ```swift
  let red = (argb >> 16) & 0xFF
  flags |= HIDDEN
  if flags & SELECTED != 0 { ... }
  ```

  As in Swift, `<<` and `>>` bind tightest, `&` binds like `*` and `|` and `^` like `+`, all tighter than comparisons, so `flags & SELECTED != 0` reads as written. Bits shifted out of the top are lost, `>>` keeps the sign, and a shift by less than 0 or more than 63 stops the program.
- `int` and `float` never mix silently: `count * price` is an error until one side is converted with `float(count)` or `int(price)`.
- Strings join with `+` only to other strings. Use interpolation for anything else: `"{count} items"`.
- `==` and `!=` compare `int`, `float`, `bool`, `string`, enums whose cases carry no values, records, and structs that define `operator ==`. `<`, `<=`, `>`, `>=` work on `int`, `float`, `string`, those enums, and structs that define `operator <`. Enum cases are ordered as they are declared, so in `enum Level { low, high }`, `Level.low < Level.high`.
- Conditions must be `bool`: write `if xs.len > 0`, not `if xs`.
- Structs are shared references: after `let b = a`, changing `b.name` also changes `a.name`. `let` stops the name being reassigned, not the fields being changed.
- A name can be declared again in an inner block, which hides the outer one; declaring it twice in the same block is an error.

Run `vex check file.vx` to see type errors without building.

### Type aliases

`typedef` gives a long type a short name, as in Dart. The alias is the same type as the one it names, so values pass between them freely:

```swift
typedef Json = [string: string?]
typedef Handler = fn(Event) throws -> bool
pub typedef Point = ({float x, float y})

fn on(handler: Handler) { ... }
let p: Point = (x: 1.0, y: 2.0)
```

An alias can take type parameters, with constraints, like a struct:

```swift
typedef Pair<T> = (T, T)
typedef Cache<K: Hashable> = [K: Future<string>]

let p: Pair<int> = (1, 2)
var cache: Cache<string> = [:]
```

- An alias of a struct creates values and reaches static members: after `typedef Pt = Point`, `Pt(x: 1, y: 2)` and `Pt.origin()` work. An alias of an enum reaches its cases: `Lv.high`.
- An alias of an interface can be listed after `:` and used as a constraint: `struct Square: S`, `fn f<T: S>(x: T)`.
- `pub typedef` makes the alias usable from other modules as `shapes.Point`.
- Aliases are declared at the top level of a file, and can't refer to themselves; use a struct or enum for a type that contains itself.
- An alias doesn't make a new type: `typedef UserId = int` still accepts any `int`. Distinct wrapper types are planned as extension types.

## Memory

Memory is managed automatically by a garbage collector: objects nobody can reach any more are freed, including ones that refer to each other in a cycle. There is nothing to free by hand and no `weak` keyword. See [design/memory.md](design/memory.md).

## Runtime errors

Mistakes that can only be found while the program runs stop it with a message and the source line, and exit code 1:

```
error: index 5 is out of range for a list of length 3
 --> main.vx:12
```

These cover list indexes out of range, `pop()` on an empty list, a list changed while it is being sorted, division by zero, integer overflow, shifts by less than 0 or more than 63, a radix outside 2 to 36, `int(...)` or `float(...)` of text that isn't a number, `panic(...)` and failed `assert(...)`. Recursion that never stops ends with `error: stack overflow` instead of a crash. Printing a struct that refers back to itself shows the repeat as `Name(...)`.

## Planned

### Mobile UI (v0.3+)

The UI framework is a separate package built from general language features, with no UI keywords in the compiler. A sketch, not approved syntax (see [design/architecture.md](design/architecture.md)):

```swift
import "package:ui"

struct Counter: View {
    count: State<int> = State(0)

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

Warnings point out code that is probably a mistake but still builds:

```
warning: 'total' is never used
  --> main.vx:3:9
   |
 3 |     let total = sum(xs)
   |         ^^^^^ use it, remove it, or name it _total
```

- A local variable that is never used. Starting its name with `_` says it is meant to be unused; a `catch` can leave its name out.
- Code after a `return`, `throw`, `break`, `continue`, `panic` or endless loop, which can never run.

Features from other languages that Vex lacks get an error that names them and shows what to write instead: `x!` (use `??`, `if let` or `guard let`), `x++` (`x += 1`), `do`/`while` (`repeat { } while`), `switch` (`match`) and `class` (`struct`).
