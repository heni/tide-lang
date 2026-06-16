// Extract every integer from a string with a Go regexp and sum them —
// exercising the FFI end to end: an opaque *regexp.Regexp handle, a
// method returning []string, and the strconv.Atoi (T,error)->Result lift.
import fmt

extern type Regexp @go("regexp")

extern func mustCompile(expr: string): Regexp @go("regexp.MustCompile")

extern impl Regexp {
  findAll(s: string, n: int): []string @go("FindAllString")
}

extern func atoi(s: string): Result<int, error> @go("strconv.Atoi")

func main() {
  let re = mustCompile("[0-9]+")
  let nums = re.findAll("a12 b3 c100 d5", -1)
  var total = 0
  for n in nums {
    match atoi(n) {
      Ok(v)  => total = total + v,
      Err(_) => total = total,
    }
  }
  fmt.println(total)
}
