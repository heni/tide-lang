import fmt

// LeetCode 3131-style "Special letters": a letter is SPECIAL
// iff its uppercase form appears AFTER the last occurrence of
// its lowercase form. Index space 0..25 is shared between
// lower-case (c - 'a') and upper-case (c - 'A') so 'a' and 'A'
// refer to the same slot.
type Status =
  | Unknown    // letter unseen so far
  | Seen       // lower-case seen, no upper-case yet
  | Special    // upper-case seen after lower (the win condition)
  | Usual      // upper-case dominated the trace (no win)

// State transitions expressed as pure match expressions —
// each branch maps an input event (saw-lower / saw-upper) on a
// current Status to the next Status. No if/else, no magic ints.
func onLower(s: Status): Status {
    return match s {
        Special => Usual,    // demote — lower seen after upper
        Unknown => Seen,     // first sighting of this letter
        _ => s,              // Seen and Usual are sticky to lower
    }
}

func onUpper(s: Status): Status {
    return match s {
        Seen => Special,     // promote — upper followed lower
        Special => Special,  // sticky — repeated upper keeps win
        _ => Usual,           // Unknown / Usual settle on Usual
    }
}

func numberOfSpecialChars(word: string): int {
    var status = makeSlice<Status>(26)
    for c in word {
        if c >= 'a' && c <= 'z' {
            let key = c - 'a'
            status[key] = onLower(status[key])
        }
        if c >= 'A' && c <= 'Z' {
            let key = c - 'A'
            status[key] = onUpper(status[key])
        }
    }
    var count = 0
    for i in 0..26 {
        if status[i] == Special {
            count += 1
        }
    }
    return count
}

func main() {
    fmt.println(numberOfSpecialChars("aaAbcBC"))
    fmt.println(numberOfSpecialChars("abc"))
    fmt.println(numberOfSpecialChars("AbBCab"))
    fmt.println(numberOfSpecialChars("AbcbDBdD"))
    fmt.println(numberOfSpecialChars("dcbCC"))
}
