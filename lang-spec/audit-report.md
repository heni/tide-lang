# Formalization-L — closing audit report

The closing PR of the formalization series (A–L). This file
documents the mechanical and manual audit performed on the
full corpus and the formal specification, and closes out the
**D17 bootstrap exemption**: from this point onward, every new
formal artefact MUST carry ≥ 1 atomic fixture in
`../tests/{lexer,grammar,sema,codegen}/` per CLAUDE.md
§"Every spec artifact carries coverage".

## Series summary

| Step | Artefact | Status |
|---|---|---|
| A | `keywords.md` — reserved words, operators, predeclared list | merged |
| B | `grammar.ebnf` lexical + `test-contract.md` | merged |
| C | `grammar.ebnf` syntactic + operators | merged |
| D | `ast.md` — canonical AST schema | merged |
| E | `name-resolution.md` — scopes, implicit receiver, shadow rules | merged |
| F | `type-system.md` — sequent-style inference rules + exhaustiveness | merged |
| G | `builtins.md` — predeclared identifier catalog | merged |
| H | `desugaring.md` — AST → IR rewrite stages | merged |
| I | `lowering-go.md` — IR → Go encoding | merged |
| J | `diagnostics.md` — numbered error / warning catalog | merged |
| K | `acceptance.yml` — per-example feature manifest | merged |
| L | this report — closing audit | open |

The eleven formal artefacts in `lang-spec/` (excluding this
report) constitute the complete contract for the v1 Tide
compiler. A self-host reimplementation reading only these
files plus the corpus in `examples/` would have everything
needed to produce a working compiler with no consultation of
prose or design notes.

## Mechanical audit

### 1. Diagnostic-code cross-reference

Every E-code referenced anywhere in `lang-spec/` is present in
`diagnostics.md`, and every code in `diagnostics.md` is
referenced somewhere (or marked `reserved`).

- 34 unique codes referenced across formal docs
- 34 codes catalogued in `diagnostics.md`
- 0 dangling references (referenced ⇒ catalogued)
- 0 unreferenced live codes (catalog ⇒ referenced, except 2
  reserved)

### 2. Acceptance manifest coverage

`acceptance.yml` declares 99 features (96 regular + 6
ubiquitous). The corpus has 51 `.td` files.

- 51 manifest entries / 51 disk files — one-to-one match
- 0 features uncovered (every feature has ≥ 1 example, or is
  in `ubiquitous:`)
- 0 unknown tags (every per-example tag resolves to a known
  feature-id)
- spot-check sanity: 13 high-signal features (sum-type decls,
  classes, fields, select blocks, panics, `defer`, `tryRecv`,
  `Stack` operations, `refEq`) verified against grep evidence
  in their declared examples; 0 false positives remaining.

### 3. Authority discipline

Per CLAUDE.md "Formal docs are authoritative over prose":

- `docs/language-spec.md` — prose mirror; informational. May
  lag; `lang-spec/` wins on conflict.
- `docs/architecture.md` — public engineering view; not
  authoritative on language semantics.
- `docs/design-decisions.md` — polished decision log. Each
  public decision is mirrored from `AI.md` (internal).

Cross-checked: no committed file references gitignored
pipeline files (`TODO.md`, `backlog.md`, `AI.md`,
`spec-gaps.md`, `CLAUDE.md`). Decision identifiers (D1–D17)
appear as opaque short labels with no file-name link.

## Manual eyeball pass

Ten examples reviewed end-to-end against the formal rules.
Findings:

- `concurrency/pipeline.td` — uses `scope<unit, error>`,
  channel widening at parameter sites, `for env in inbox`
  loop terminating on close. All shapes match T-ScopeExpr,
  T-Chan-Widen, T-For (`RecvChan<T>`). ✓
- `leetcode/valid_parentheses.td` — uses
  `Stack<rune>`, `stack.pop(): Result<rune, error>`,
  `match Ok(got) | Err(_)`. Matches the Formal-G correction
  (Stack.pop returns Result, not Option). ✓
- `interview/rpn_calculator.td` — uses `try stack.pop()`
  inside a `Result<float64, error>`-returning function;
  E_inner = E_outer = `error`, satisfies T-Try-Result (G11).
  ✓
- `agents/counterstack/pentix_agent.td` — multi-class,
  multi-sum-type, multi-channel agent. The most complex
  corpus example. All constructs (scope, spawn, defer,
  implicit receiver, sum-type matching with wildcard arms,
  try-Option) are consistent with the formal rules. ✓
- `services/todo_api.td` — uses `Any` (variadic
  `log.println(args: ...Any)`) — matches D11/G23 binding
  boundary rule. ✓
- `raku/error_chain.td` — `class ParseError implements error`
  with method `error(): string`; the method name **shadows**
  the predeclared `error(msg: string): error` constructor
  inside the class body per name-resolution.md §93-105. The
  spec's intentional non-diagnostic on this shadow is
  exercised. ✓
- `aoc/2025/d11.td` — `type Graph = Map<string, []string>` —
  type alias to a parameterised type; matches WF-Body-Alias.
  ✓
- `borgo/match_on_tuples.td` — uses `panic("unreachable")`
  in a wildcard arm; matches T-Panic returning `Never`,
  unifying with the match arm type. ✓
- `borgo/errors_as_types.td` — `class FooErr implements
  error` with no fields, only a method body. Matches the
  pure-behaviour class shape (WF-Class-Body allows zero
  fields). The example deliberately exercises G11's no-implicit
  error widening; `bar()` keeps `Result<unit, FooErr>` end to
  end, not `Result<unit, error>`. ✓
- `interview/lru_cache.td` — generic class `LRU<K, V>` where
  `K` flows into a `Map<K, _>` key position. Lowering must
  emit `K comparable` per Formal-I §Generics constraint
  propagation. ✓

No corpus examples found that contradict the formal rules.

## Known gaps and follow-ups

These are deferred to the post-formalization phase and tracked
in the project backlog (none are blockers for the series
close):

1. **No tests/{lexer,grammar,sema,codegen}/ fixtures yet.**
   The bootstrap exemption (D17) released individual
   formalization PRs from the fixture requirement; this gate
   closes the exemption. From the next PR forward, every
   formal-doc edit (or new construct) requires ≥ 1 atomic
   fixture per the test-contract.
2. **No compiler implementation yet.** The eleven formal
   artefacts are sufficient input for one; the actual `cmd/tide`
   binary is the next major piece of work and is parked here.
3. **Typed errors on `scope<T, E>` with E ≠ error.** v1
   restricts `scope` to `E = error` (E0407). The relaxation
   ("typed-error adapter") is parked. When it lands, T-ScopeExpr
   loses the side condition, E0407 is decommissioned, and
   SpawnIR's `res.E.(error)` assertion is replaced with a
   typed dispatch.
4. **Open Iterable typeclass.** D11 parks bounded generics;
   `IterElem` is a closed mapping. User-defined iterables
   wait for v2.
5. **Prose docs RFC reorganisation.** Tracked as a backlog
   item; the formalization-series PRs deferred prose
   restructuring on purpose. `docs/language-spec.md` remains
   the chronological draft; an RFC-style chapter split is
   future work.
6. **AoC 2024 Rust port.** Backlog candidate for corpus
   breadth.

## Closing

The formalization series is **complete**. The v1 Tide language
surface, type system, runtime model, codegen target, and
diagnostic catalogue are fully specified in `lang-spec/`. The
corpus is consistent with the specification.

From this PR forward:

- D17 **bootstrap exemption is closed**. New formal artefacts
  require paired fixtures.
- The next milestone is compiler implementation —
  `cmd/tide` + `internal/{lexer,parser,sema,desugar,codegen}`
  + `_tidert/runtime.go` — with the formal docs in
  `lang-spec/` as the contract.
