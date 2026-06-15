// Parsing via the strconv.atoi binding and `try` error propagation.
import fmt
import strconv

func parseInt(s: string): Result<int, error> {
  let n = try strconv.atoi(s)
  return Ok(n)
}

func main() {
  match parseInt("42") {
    Ok(v)  => fmt.println("ok", v),
    Err(e) => fmt.println("err", e),
  }
  match parseInt("nope") {
    Ok(v)  => fmt.println("ok", v),
    Err(e) => fmt.println("err", e),
  }
}
