// Showcase: Result / try / match. Sum-typed operators, errors as values.
// A Reverse-Polish-Notation evaluator: "3 4 + 2 *" -> 14.

import fmt
import os
import strconv
import strings

type Op = | Add | Sub | Mul | Div

func parseOp(token: string): Option<Op> {
  match token {
    "+" => Some(Add),
    "-" => Some(Sub),
    "*" => Some(Mul),
    "/" => Some(Div),
    _   => None,
  }
}

func apply(op: Op, a: float64, b: float64): Result<float64, error> {
  match op {
    Add => Ok(a + b),
    Sub => Ok(a - b),
    Mul => Ok(a * b),
    Div => if b == 0.0 {
             Err(error("division by zero"))
           } else {
             Ok(a / b)
           },
  }
}

func evalRPN(expr: string): Result<float64, error> {
  var stack = Stack<float64>{}
  for token in strings.fields(expr) {
    match parseOp(token) {
      Some(op) => {
        let b = try stack.pop()
        let a = try stack.pop()
        stack.push(try apply(op, a, b))
      },
      None => stack.push(try strconv.parseFloat(token, 64)),
    }
  }
  if stack.len() != 1 {
    return Err(error("expression did not reduce to a single value"))
  }
  return stack.pop()
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: rpn '<expression>'")
    os.exit(2)
  }
  match evalRPN(os.args[1]) {
    Ok(v)  => fmt.println(v),
    Err(e) => {
      fmt.println("error:", e)
      os.exit(1)
    },
  }
}
