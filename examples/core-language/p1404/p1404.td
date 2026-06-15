// Timus 1404 — Easy to Hack! (Caesar-style decoding, byte arithmetic)
// http://acm.timus.ru/problem.aspx?space=1&num=1404
//
// Decode a ciphertext where each character was shifted by 5 + sum of
// preceding plaintext characters. Reverse:
//   plain[0]   = (cipher[0] - 'a' + 21) % 26 + 'a'   // -5 mod 26
//   plain[i]   = (cipher[i] - cipher[i-1] + 26) % 26 + 'a'
//
// Forces: `s.bytes()` to view a string as `[]byte`, byte arithmetic
// over a mutable `[]byte` accumulator, primitive `byte(n)` conversions
// for integer constants in mixed expressions (G49),
// `strings.fromBytes` for the round-trip.

import fmt
import os
import strings

func decode(s: string): string {
  let codes = s.bytes()
  let a = byte('a')
  let out = makeSlice<byte>(codes.len())
  out[0] = ((codes[0] - a + byte(21)) % byte(26)) + a
  for i in 1..codes.len() {
    out[i] = ((codes[i] - codes[i - 1] + byte(26)) % byte(26)) + a
  }
  return strings.fromBytes(out)
}

func main() {
  match fmt.scan<string>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(s)  => fmt.println(decode(s)),
  }
}
