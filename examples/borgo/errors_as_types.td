// Port of Borgo's `errors-as-custom-types.brg` codegen-emit test.
// Source: https://github.com/borgo-lang/borgo/blob/main/compiler/test/snapshot/codegen-emit/errors-as-custom-types.exp
//
// Borgo and Tide both let a user-defined struct/class implement an
// error-shaped interface. The mechanics differ in the obvious ways:
//   * Borgo's `impl (f: FooErr) { fn Error() -> string { ... } }`
//     adds a method to a struct after the fact. Tide attaches methods
//     inline on the class.
//   * Borgo uses the `?` postfix for early-return on `Result`; Tide
//     uses the `try` prefix.
//   * Tide's `error` interface is built-in (one method `error():
//     string`), bound to Go's `error` so `Result<T, error>` returns
//     map straight through the binding boundary.

import fmt

class FooErr implements error {
  error(): string { return "b" }
}

func foo(): Result<unit, FooErr> {
  return Ok(())
}

// Borgo `bar` widens FooErr to `error` automatically. In Tide the
// generic `error` interface and the specific `FooErr` are nominal —
// a single try-propagate doesn't auto-widen, so an explicit `match`
// converts on the boundary. The cleaner alternative is to return
// `Result<unit, FooErr>` all the way up, which we do here.
func bar(): Result<unit, FooErr> {
  let x = try foo()
  return Ok(x)
}

func main() {
  let x = foo()
  let y = bar()
  match x {
    Ok(_)  => fmt.println("x: Ok"),
    Err(e) => fmt.println("x: Err", e.error()),
  }
  match y {
    Ok(_)  => fmt.println("y: Ok"),
    Err(e) => fmt.println("y: Err", e.error()),
  }
}
