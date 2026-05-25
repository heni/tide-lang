# RFC-0000 — RFC process

| Field | Value |
|---|---|
| Number | 0000 |
| Status | accepted |
| Created | 2026-05-25 |
| Supersedes | — |
| Target | `docs/rfcs/` (this file is self-referential) |

## Summary

This RFC defines how RFCs work in the Tide project: when one is
required, what shape it takes, how it moves between statuses,
and who decides. It is intentionally lightweight — meant for a
pre-alpha project where one or two contributors are the entire
review surface — and will be extended when real precedents force
the question.

## Motivation

The formalization series (Formalization-A through Formalization-L)
established `lang-spec/` as the authoritative contract for the
current Tide compiler. From here on the language and compiler
will *change* — new features, relaxations of restrictions,
removals. Without a paper trail, these changes either land
silently or accumulate as undocumented drift. The RFC process
is the paper trail.

It is **not** a gatekeeping mechanism. The bar for a "good" RFC
is low: a couple of pages stating motivation, the proposed
change, and the paired edits in `lang-spec/`. The point is to
*record* a decision in a place that survives commit-message
archaeology.

## When an RFC is required

| Change | RFC required? |
|---|---|
| New construct in `lang-spec/` (keyword, type rule, builtin method, IR node, lowering case) | **yes** |
| Relaxation of an existing restriction (e.g., admitting `E ≠ error` in `scope`) | **yes** |
| Removal of an existing construct | **yes** |
| New diagnostic code | **yes** |
| New stdlib binding surface (a Go package not already bound) | **yes** |
| Fixing a sema bug — clarifying an existing rule | no |
| Typos, prose-only edits in `docs/` | no |
| Internal compiler refactor not touching `lang-spec/` | no |
| New example in `examples/` not requiring spec changes | no |

When in doubt, write the RFC. It costs less than the
post-merge "wait, why did we …" exchange.

## Document structure

Each RFC is a single Markdown file at
`docs/rfcs/<NNNN>-<kebab-name>.md`, where `NNNN` is the next
free four-digit number and `<kebab-name>` is a short slug.

```markdown
# RFC-NNNN — Title

| Field | Value |
|---|---|
| Number | NNNN |
| Status | draft \| accepted \| implemented \| superseded |
| Created | YYYY-MM-DD |
| Supersedes | — \| RFC-MMMM |
| Target | <which lang-spec/ files this RFC will touch> |

## Summary
A one-paragraph plain-language description of the change.

## Motivation
Why we want this. Concrete examples from the corpus or from
real implementation friction beat hypothetical scenarios.

## Design
The actual change. Sequents, grammar rules, signatures —
the technical contract, written so that the paired edit in
`lang-spec/` is mechanical from this text.

## Alternatives considered
The two or three other things we could have done, and why we
didn't. Even a one-liner per alternative is more useful than
omitting this section.

## Paired edits
A concrete list of `lang-spec/` files this RFC will touch on
acceptance — `type-system.md` adds rule T-Foo, `builtins.md`
gains method `m()`, `diagnostics.md` adds E0XYZ. Reviewers
check the paired edits exist when status moves to
`implemented`.

## Transition / compatibility
If the change is not strictly additive: who breaks, what does
their migration look like, is there a deprecation window.

## Open questions
Anything not decided. RFCs MAY be accepted with open questions
flagged, on the understanding that the questions are resolved
before `implemented`.
```

All template sections (Summary, Motivation, Design,
Alternatives, Paired edits, Transition, Open questions) are
**required**, but the body may be `None` or `Not applicable`
where genuinely so — declarative or purely-additive RFCs
will have `None` paired edits and `Not applicable`
transition sections, and that is fine.

Optional sections (rationale, performance notes, security
implications) are welcome but never required.

## Lifecycle

```
draft ──► accepted ──► implemented ──► superseded
```

The four states are linear; an RFC may carry open questions
through `accepted` and `implemented` (flagged in §"Open
questions"); `superseded` is reached only when a later RFC
sets its `Supersedes:` field to this one.

- **`draft`** — under discussion. RFC PR is open, review may
  request structural changes. Implementation has not started.
- **`accepted`** — the design is settled. Open questions, if
  any, are flagged in the doc but not blockers. Implementation
  may begin.
- **`implemented`** — the paired edits in `lang-spec/` have
  landed and (per the project's atomic-coverage rule) at least
  one atomic fixture exists in
  `tests/{lexer,grammar,sema,codegen}/`. The RFC is now a
  historical artefact, kept for the paper trail.
- **`superseded`** — a later RFC replaced this one. The
  superseding RFC sets its own `Supersedes:` field to this
  RFC's number. Both files stay in the directory.

Transitions are recorded by editing the `Status:` field in the
RFC and noting the date and superseding RFC (if any) in a
trailing "## History" subsection.

## Review

Each RFC PR runs the standard review-subagent pass. The
subagent's prompt begins with the project's required preamble
(the same boilerplate every PR review uses — it primes the
reviewer on the project's conventions). For RFC PRs
specifically the subagent checks:

- Document follows the structure above.
- `Target:` field lists realistic file targets.
- Paired-edit list maps 1:1 to spec files.
- Alternatives section is genuine (not a single placeholder).
- Transition section is present when the change is not
  strictly additive.

`draft → accepted` requires the user's explicit ok. `accepted →
implemented` does not — when the paired edits and fixtures land,
the status flips on the implementing PR.

## Relationship to other decision records

- **D-prefix identifiers** (`D1`…`D17`) reference foundational
  architectural decisions made during the project's
  formalization phase; they are short stable labels that may
  surface in RFC text and code comments. They capture the
  *historical* foundations of the current baseline — the
  "why is the language like this" answer — but not "what
  changed since". Future decisions go through this RFC process,
  not by appending to the D-series.
- **Internal pipeline notes** (working drafts, decision logs,
  contributor process) are gitignored. RFCs are the
  **public**, committed paper trail.
- **`lang-spec/`** is the contract. RFCs propose changes to it;
  accepted RFCs trigger paired edits there.

## Numbering and naming

- Numbers are monotonic four-digit decimal starting at 0000.
- A new RFC takes the next free number — no reservations, no
  gaps. Two PRs racing for the same number is a merge conflict
  resolved by re-numbering the later one.
- File names are `NNNN-<kebab-name>.md`. The slug is short and
  describes the change, not the motivation
  (`0002-iterable-typeclass.md`, not `0002-make-iter-better.md`).

## Open questions for this RFC

- Should an `RFC-0000 amendment` mechanism exist for tweaks
  that don't deserve their own number? *Deferred* — current
  bias is "just edit `0000-process.md` directly and call it
  out in the commit message".
- Should there be a stage between `draft` and `accepted` for
  experimental implementations? TC39 has Stages 0–4; we have
  draft → accepted → implemented. If a real proposal needs
  more granularity, add it then.
- Do removals of constructs need a deprecation window? The
  corpus is small; current bias is "fix the corpus in the same
  PR". Revisit when there is a second consumer.
