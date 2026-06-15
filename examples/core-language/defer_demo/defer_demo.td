// Port of Borgo's `defer-statements.brg` codegen-emit test.
// Source: https://github.com/borgo-lang/borgo/blob/main/compiler/test/snapshot/codegen-emit/defer-statements.exp
//
// `defer` is identical in Borgo and Tide: function-scoped, LIFO,
// adopted directly from Go. The expected output is:
//
//   first
//   second
//   defer 2
//   defer 1

import fmt

func main() {
  fmt.println("first")
  defer fmt.println("defer 1")

  fmt.println("second")
  defer (func() { fmt.println("defer 2") })()
}
