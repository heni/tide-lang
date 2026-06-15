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

// Borgo `bar` returns `Result<(), error>` and lets `?` widen `FooErr`
// to `error` along the way. Tide's `class FooErr implements error`
// establishes the *nominal* is-a relation just like Borgo's
// `impl (f: FooErr) { fn Error() }`, but v1 deliberately keeps
// strict on the `try` boundary: "No implicit error conversion in v1;
// the error types must match." So `bar` here returns
// `Result<unit, FooErr>` end to end — when v1's `try` learns to
// promote via an `implements error` relation, this function could
// return `Result<unit, error>` and the body wouldn't change.
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
