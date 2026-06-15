// LeetCode 20. Valid Parentheses.
// Return true iff every opener has a matching, correctly-nested closer.
//
// Forces: a generic stack, runes (strings iterate as Unicode code
// points), match on character literals.

import fmt
import os

func opener(c: rune): Option<rune> {
  match c {
    ')' => Some('('),
    ']' => Some('['),
    '}' => Some('{'),
    _   => None,
  }
}

func isOpener(c: rune): bool {
  match c {
    '(' | '[' | '{' => true,
    _               => false,
  }
}

func isValid(s: string): bool {
  var stack = Stack<rune>{}
  for c in s {
    if isOpener(c) {
      stack.push(c)
    } else {
      match opener(c) {
        Some(want) => match stack.pop() {
          Ok(got)  => if got != want { return false },
          Err(_)   => return false,           // empty stack — closer with no opener
        },
        None => {},                            // not a bracket char; ignore
      }
    }
  }
  return stack.len() == 0
}

func main() {
  let cases = ["()", "()[]{}", "(]", "([)]", "{[]}", ""]
  for s in cases {
    fmt.println(s, "->", isValid(s))
  }
  os.exit(0)
}
