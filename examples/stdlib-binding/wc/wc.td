// A simplified word-count CLI: counts lines, words, bytes for each given
// file, then a "total" line if more than one file. No flags — `wc` proper
// has -l/-w/-c switches; we keep this minimal and demonstrate the file/IO
// and CLI surface.
//
// Forces: `os` (args, exit), `io` / file read via `os.readFile`,
// `strings` for splitting, `strconv` for output formatting,
// `Result`/`try` over file errors, structured exit codes.

import fmt
import os
import strings

type Counts = {
  lines: int
  words: int
  bytes: int
}

func count(data: []byte): Counts {
  let text = strings.fromBytes(data)
  return Counts{
    lines: strings.count(text, "\n"),
    words: strings.fields(text).len(),
    bytes: data.len(),
  }
}

func render(c: Counts, name: string): string {
  return fmt.sprintf("%8d %8d %8d %s", c.lines, c.words, c.bytes, name)
}

func main() {
  let args = os.args
  if args.len() < 2 {
    fmt.println("usage: wc <file>...")
    os.exit(2)
  }

  var total = Counts{ lines: 0, words: 0, bytes: 0 }
  var anyError = false

  for i in 1..args.len() {
    let path = args[i]
    match os.readFile(path) {
      Err(e) => {
        fmt.println("wc:", path + ":", e)
        anyError = true
      },
      Ok(data) => {
        let c = count(data)
        fmt.println(render(c, path))
        total = Counts{
          lines: total.lines + c.lines,
          words: total.words + c.words,
          bytes: total.bytes + c.bytes,
        }
      },
    }
  }

  if args.len() > 2 {
    fmt.println(render(total, "total"))
  }

  if anyError { os.exit(1) }
}
