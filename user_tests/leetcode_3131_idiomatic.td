import fmt

// LeetCode 3131-style "Special letters": a letter is SPECIAL
// iff its uppercase form appears AFTER the last occurrence of
// its lowercase form. The state machine per letter tracks
// where in that order we are. Index space 0..25 is shared
// between lower-case (c - 'a') and upper-case (c - 'A') so 'a'
// and 'A' refer to the same slot.
type Status =
  | Unknown    // letter unseen so far
  | Seen       // lower-case seen, no upper-case yet
  | Special    // upper-case seen after lower (the win condition)
  | Usual      // upper-case dominated the trace (no win)

func numberOfSpecialChars(word: string): int {
    var status = makeSlice<Status>(26)

    for c in word {
        if c >= 'a' && c <= 'z' {
            let key = c - 'a'
            if status[key] == Special {
                status[key] = Usual
            } else if status[key] == Unknown {
                status[key] = Seen
            }
        }
        if c >= 'A' && c <= 'Z' {
            let key = c - 'A'
            if status[key] == Seen {
                status[key] = Special
            } else if status[key] == Unknown || status[key] == Usual {
                status[key] = Usual
            }
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
