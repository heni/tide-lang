# Example conformance status

Generated from `examples/auto-status.json` by the `corpus-status` tool (`tools/corpus-status`) — do not edit by hand. Build and run the tool to refresh.

Two tracked metrics, each with a CI-enforced floor in `metric-floors.toml`:

- **build_ok — 46 / 56 examples build end-to-end** (floor 46).
- **diag_ok — 77 / 100 negative cases produce their expected diagnostic** (floor 70).

| Stage reached | Count |
|---|---|
| ✅ build (full pipeline) | 46 |
| emit / codegen fail | 1 |
| sema fail | 2 |
| parse fail | 7 |

## Per-example

| Example | Stage | First blocker |
|---|---|---|
| `examples/concurrency/concurrency/concurrency.td` | build | — |
| `examples/concurrency/pipeline/pipeline.td` | build | — |
| `examples/concurrency/rate_limited/rate_limited.td` | build | — |
| `examples/concurrency/select_showcase/select_showcase.td` | build | — |
| `examples/core-language/d01/d01.td` | build | — |
| `examples/core-language/d02/d02.td` | build | — |
| `examples/core-language/d03/d03.td` | build | — |
| `examples/core-language/d04/d04.td` | build | — |
| `examples/core-language/d05/d05.td` | build | — |
| `examples/core-language/d07/d07.td` | build | — |
| `examples/core-language/d08/d08.td` | build | — |
| `examples/core-language/d09/d09.td` | build | — |
| `examples/core-language/d11/d11.td` | build | — |
| `examples/core-language/deep_destructure/deep_destructure.td` | build | — |
| `examples/core-language/defer_demo/defer_demo.td` | build | — |
| `examples/core-language/fizzbuzz/fizzbuzz.td` | build | — |
| `examples/core-language/hello/hello.td` | build | — |
| `examples/core-language/interfaces/interfaces.td` | build | — |
| `examples/core-language/invert_binary_tree/invert_binary_tree.td` | build | — |
| `examples/core-language/match_on_tuples/match_on_tuples.td` | build | — |
| `examples/core-language/merge_intervals/merge_intervals.td` | build | — |
| `examples/core-language/p1033/p1033.td` | build | — |
| `examples/core-language/p1242/p1242.td` | build | — |
| `examples/core-language/p1335/p1335.td` | build | — |
| `examples/core-language/p1349/p1349.td` | build | — |
| `examples/core-language/p1404/p1404.td` | build | — |
| `examples/core-language/p1423/p1423.td` | build | — |
| `examples/core-language/p1605/p1605.td` | build | — |
| `examples/core-language/p1683/p1683.td` | build | — |
| `examples/core-language/p1786/p1786.td` | build | — |
| `examples/core-language/p1820/p1820.td` | build | — |
| `examples/core-language/reverse_linked_list/reverse_linked_list.td` | build | — |
| `examples/core-language/set_algebra/set_algebra.td` | build | — |
| `examples/core-language/two_sum/two_sum.td` | build | — |
| `examples/core-language/valid_parentheses/valid_parentheses.td` | build | — |
| `examples/ffi/config_reader/config_reader.td` | build | — |
| `examples/ffi/sum_numbers/sum_numbers.td` | build | — |
| `examples/modeling-errors/error_chain/error_chain.td` | build | — |
| `examples/modeling-errors/errors_as_types/errors_as_types.td` | build | — |
| `examples/modeling-errors/parse_int/parse_int.td` | build | — |
| `examples/modeling-errors/rpn_calculator/rpn_calculator.td` | build | — |
| `examples/modeling-errors/safe_divide/safe_divide.td` | build | — |
| `examples/modeling-errors/vending_machine/vending_machine.td` | build | — |
| `examples/stdlib-binding/config_loader/config_loader.td` | build | — |
| `examples/stdlib-binding/wc/wc.td` | build | — |
| `user_tests/leetcode_3131_idiomatic.td` | build | — |
| `examples/stdlib-binding/counterstack/pentix_agent.td` | emit | unknown failure |
| `examples/concurrency/worker_pool/worker_pool.td` | sema | error[E0207]: Wrong type arity on generic instantiation: Result expects 2 type arguments, got 0 |
| `examples/stdlib-binding/healthcheck_server/healthcheck_server.td` | sema | error[E0103]: Unknown name http |
| `examples/concurrency/graceful_server/graceful_server.td` | parse | error[E0112]: expected expression, got Punct ":" |
| `examples/concurrency/nested_scopes/nested_scopes.td` | parse | error[E0112]: expected Punct "{", got Punct "." |
| `examples/concurrency/parallel_fetcher/parallel_fetcher.td` | parse | error[E0112]: expected Punct "{", got Punct "." |
| `examples/concurrency/pubsub/pubsub.td` | parse | error[E0112]: mixed brace-literal entry kinds |
| `examples/core-language/lru_cache/lru_cache.td` | parse | error[E0112]: expected expression, got Punct "," |
| `examples/core-language/p1133/p1133.td` | parse | error[E0112]: expected parameter name |
| `examples/stdlib-binding/todo_api/todo_api.td` | parse | error[E0112]: mixed brace-literal entry kinds |

## Diagnostic-quality gaps

Negative cases whose `.expected` records the **ideal** user-facing diagnostic that the compiler does not yet emit (e.g. a parser message still leaking internal token-kind names). This is the backlog the `diag_ok` metric grows toward; closing a row means improving the diagnostic, not the test.

**23 of 100 cases fall short of the ideal.**

| Case | Ideal (`.expected`) | Actual |
|---|---|---|
| `examples/core-language/deep_destructure/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "cause" |
| `examples/core-language/fizzbuzz/errors/missing-brace.patch` | error[E0112]: expected `{` | error[E0112]: expected Punct "{", got Newline "" |
| `examples/core-language/fizzbuzz/errors/missing-in.patch` | error[E0112]: expected `in` | error[E0112]: expected Keyword "in", got IntLit "1" |
| `examples/core-language/invert_binary_tree/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "right" |
| `examples/core-language/merge_intervals/errors/organic-merge3-e0112.patch` | error[E0112]: expected a type | error[E0112]: expected type expression, got Newline "" |
| `examples/core-language/p1242/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "v" |
| `examples/core-language/p1335/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "e" |
| `examples/core-language/p1349/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "e" |
| `examples/core-language/p1404/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "e" |
| `examples/core-language/p1605/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "e" |
| `examples/core-language/p1683/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "e" |
| `examples/core-language/p1786/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "b" |
| `examples/core-language/p1820/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "b" |
| `examples/core-language/set_algebra/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "b" |
| `examples/core-language/two_sum/errors/missing-comma-args.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "i" |
| `examples/core-language/two_sum/errors/missing-in-for.patch` | error[E0112]: expected `in` | error[E0112]: expected Keyword "in", got Ident "nums" |
| `examples/modeling-errors/error_chain/errors/organic-dividend2-e0112.patch` | error[E0112]: expected an expression | error[E0112]: expected expression, got Punct "." |
| `examples/modeling-errors/parse_int/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "v" |
| `examples/modeling-errors/rpn_calculator/errors/organic-parseop2-e0112.patch` | error[E0112]: expected `(` | error[E0112]: expected Punct "(", got Punct "," |
| `examples/modeling-errors/safe_divide/errors/bare-record-literal.patch` | error[E0112]: expected an expression | error[E0112]: expected expression, got Punct ":" |
| `examples/modeling-errors/vending_machine/errors/organic-step2-e0112.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Punct ":" |
| `examples/stdlib-binding/config_loader/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got Ident "cfg" |
| `examples/stdlib-binding/wc/errors/missing-comma.patch` | error[E0112]: expected `)` | error[E0112]: expected Punct ")", got StringLit "\"\\n\"" |
