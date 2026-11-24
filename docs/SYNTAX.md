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

## Lists

```swift
var nums = [1, 2, 3]
nums.push(4)
println(nums.len)        // 4
println(nums[0])         // 1
println(nums)            // [1, 2, 3, 4]

let empty: [string] = []
```

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

## Built-ins

| Name | Description |
|---|---|
| `print(a, b, ...)` / `println(...)` | Print values separated by spaces |
| `input(prompt)` | Read a line from standard input; the prompt is optional |
| `int(x)`, `float(x)`, `str(x)` | Convert between types |
| `panic(msg)`, `assert(cond, msg)` | Stop the program with a message; `assert`'s message is optional |
| `list.len`, `.push(x)`, `.pop()`, `.contains(x)` | List members |
| `s.len`, `.upper()`, `.lower()`, `.contains(x)`, `.split(sep)` | String members |

## Types

The types are `int` (64-bit), `float` (64-bit), `bool`, `string`, lists `[T]` and structs.

- `let x = ...` takes its type from the value. An empty list needs the type written: `let xs: [int] = []`.
- Whole-number literals adapt to `float` where one is expected: `let price: float = 10`, `price * 2`.
- `int` and `float` never mix silently: `count * price` is an error until one side is converted with `float(count)` or `int(price)`.
- Strings join with `+` only to other strings. Use interpolation for anything else: `"{count} items"`.
- `==` and `!=` compare `int`, `float`, `bool` and `string`; `<`, `<=`, `>`, `>=` also work on these except `bool`.
- Conditions must be `bool`: write `if xs.len > 0`, not `if xs`.
- Structs are shared references: after `let b = a`, changing `b.name` also changes `a.name`. `let` stops the name being reassigned, not the fields being changed.
- A name can be declared again in an inner block, which hides the outer one; declaring it twice in the same block is an error.

Run `vex check file.vx` to see type errors without building.

## Runtime errors

Mistakes that can only be found while the program runs stop it with a message and the source line, and exit code 1:

```
error: index 5 is out of range for a list of length 3
 --> main.vx:12
```

These cover list indexes out of range, `pop()` on an empty list, division by zero, `int(...)` or `float(...)` of text that isn't a number, `panic(...)` and failed `assert(...)`. Int arithmetic wraps around on overflow.

## Planned

### Optionals and errors (v0.2)

```swift
fn findUser(id: int) -> User? { ... }

if let user = findUser(5) {
    println(user.name)
}

fn load(path: string) -> string throws { ... }
let text = try load("data.txt")
```

### Mobile UI (v0.3+)

```swift
component Counter {
    state count = 0

    view {
        Column(spacing: 12) {
            Text("Count: {count}").size(24)
            Button("Add") { count += 1 }
        }
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
