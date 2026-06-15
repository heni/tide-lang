// Result-typed integer division with a typed error value.
import fmt

type DivError = { msg: string }

func safeDivide(a: int, b: int): Result<int, DivError> {
  if b == 0 {
    return Err(DivError{ msg: "division by zero" })
  }
  return Ok(a / b)
}

func main() {
  match safeDivide(10, 2) {
    Ok(v)  => fmt.println("ok", v),
    Err(e) => fmt.println("err", e.msg),
  }
  match safeDivide(10, 0) {
    Ok(v)  => fmt.println("ok", v),
    Err(e) => fmt.println("err", e.msg),
  }
}
