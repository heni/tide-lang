// Raku-inspired stress test #3: a multi-stage error-chain.
//
// Raku's
// [`S04-exceptions`](https://github.com/Raku/roast/tree/master/S04-exceptions)
// tests exercise `fail`, `try`, and `CATCH` against deeply nested
// call chains. Tide's `Result<T, E>` + prefix `try` covers the same
// ground at the type level. This file pushes a value through three
// stages, each able to fail with the same nominal error type, and
// reports either the final value or the **first** failure site.
//
// Forces: `Result<T, ParseError>` chaining through three calls, prefix
// `try` early-return, a single error type with a field carrying the
// failure context, and the `class implements error` shape so
// `parseErr.error()` is the printable rendering.

import fmt
import strconv
import strings

class ParseError implements error {
  let stage: string
  let detail: string

  error(): string {
    return "[" + stage + "] " + detail
  }
}

func parsePositive(s: string): Result<int, ParseError> {
  let trimmed = strings.trimSpace(s)
  if trimmed.len() == 0 {
    return Err(ParseError{ stage: "parsePositive", detail: "empty input" })
  }
  match strconv.atoi(trimmed) {
    Err(e) => Err(ParseError{ stage: "parsePositive", detail: e.error() }),
    Ok(n)  => {
      if n <= 0 {
        Err(ParseError{ stage: "parsePositive", detail: "non-positive: " + trimmed })
      } else {
        Ok(n)
      }
    },
  }
}

func dividend(n: int): Result<int, ParseError> {
  // Pretend stage two: take n, return n/2 if n is even, else fail.
  if n % 2 != 0 {
    return Err(ParseError{ stage: "dividend", detail: "odd: " + strconv.itoa(n) })
  }
  return Ok(n / 2)
}

func finalCheck(n: int): Result<int, ParseError> {
  if n > 1_000 {
    return Err(ParseError{ stage: "finalCheck", detail: "too large: " + strconv.itoa(n) })
  }
  return Ok(n)
}

func pipeline(raw: string): Result<int, ParseError> {
  let a = try parsePositive(raw)
  let b = try dividend(a)
  let c = try finalCheck(b)
  return Ok(c)
}

func main() {
  let inputs = ["42", "", "abc", "-3", "7", "1000000", "  10  "]
  for s in inputs {
    match pipeline(s) {
      Ok(n)  => fmt.println("ok: \"" + s + "\" -> " + strconv.itoa(n)),
      Err(e) => fmt.println("err: \"" + s + "\" -> " + e.error()),
    }
  }
}
