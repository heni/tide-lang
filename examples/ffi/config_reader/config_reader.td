// Binds a third-party Go module (example.com/tidekv, vendored) through
// the FFI and reads a config — proving generated Tide code can depend on
// a non-stdlib Go library hermetically (ffi.md §"Dependency model").
import fmt

extern func value(data: string, key: string): string @go("example.com/tidekv.Value")

extern func count(data: string): int @go("example.com/tidekv.Count")

func main() {
  let config = "name = tide\nversion = 0.1\nbackend = go\n# a comment\n"
  fmt.println(value(config, "name"))
  fmt.println(value(config, "backend"))
  fmt.println(count(config))
}
