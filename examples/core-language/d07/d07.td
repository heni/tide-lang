// Advent of Code 2025, Day 7 — https://adventofcode.com/2025/day/7
//
// A 1-D wavefront through a maze of `S` (source), `^` (splitter), and
// other cells. For each row: an `S` adds a beam with timeline 1; a `^`
// at a column that already has a live beam splits it into two beams
// at the neighbouring columns, distributes the timeline, zeroes the
// original, and counts one split. Beams persist across rows. Report
// the total splits and the surviving total timeline.
//
// Forces: `Set<int>` for the live beam positions, slice
// index-assignment `s[i] = v`, rune iteration with
// byte index `for (pos, c) in row`, `.copy()` on slices.

import fmt
import os
import strings

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d07 <input>")
    os.exit(2)
  }
  match os.readFile(os.args[1]) {
    Err(e)   => { fmt.println("read error:", e); os.exit(1) },
    Ok(data) => run(strings.fromBytes(data)),
  }
}

func run(text: string) {
  var lines: []string = []string{}
  for ln in strings.split(text, "\n") {
    if ln.len() > 0 { lines = lines.push(ln) }
  }
  if lines.len() == 0 {
    fmt.println("empty input")
    return
  }
  let width = lines[0].len()

  var beams: Set<int> = Set<int>{}
  var timelines: []int = []int{}
  for _ in 0..width {
    timelines = timelines.push(0)
  }
  var splits = 0

  for ln in lines {
    var nBeams: Set<int> = Set<int>{}
    var nTimelines = timelines.copy()

    // Maze input is ASCII, so byte indices and column positions
    // coincide. (Non-ASCII would require .runes() and rune-ordinal
    // indexing — irrelevant here.)
    for (pos, c) in ln {
      if c == 'S' {
        nBeams.add(pos)
        nTimelines[pos] = 1
      } else if c == '^' && beams.has(pos) {
        nBeams.add(pos - 1)
        nBeams.add(pos + 1)
        nTimelines[pos + 1] = nTimelines[pos + 1] + timelines[pos]
        nTimelines[pos - 1] = nTimelines[pos - 1] + timelines[pos]
        nTimelines[pos] = 0
        splits = splits + 1
        beams.delete(pos)
      }
    }
    for b in nBeams {
      beams.add(b)
    }
    timelines = nTimelines
  }

  var total = 0
  for v in timelines {
    total = total + v
  }
  fmt.println("Splits:    ", splits)
  fmt.println("Timelines: ", total)
}
