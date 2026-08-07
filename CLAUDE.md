# Scratchpad — Agent Operating Conventions

This module is being improved via a coordinated multi-agent effort against
`docs/improvement-plan.md`. These conventions keep every agent's work atomic
and merge-safe. **Follow them exactly.**

## The per-commit green gate

Before **every** commit, all of the following must pass:

```sh
gofmt -l .                 # must print nothing
go build ./...
go vet ./...
go test -count=1 ./...     # unit tests are self-contained; no Chrome needed
```

If any step fails, fix your code before committing. Never commit a red tree.
`internal/protocol/testdata/*.golden.json` files must be regenerated whenever
you change a serialized type (`go test ./internal/protocol/ -update` — see
`internal/protocol/*_test.go` for the update mechanism).

## Commit rules

- One **atomic** commit per logical change. Never bundle unrelated edits.
- Conventional format: `<type>(<scope>): <subject>` — e.g. `feat(middleware): implement request-id middleware`. Types: `feat`, `fix`, `refactor`, `test`, `docs`, `perf`, `chore`.
- Footer on every commit:
  ```
  Co-Authored-By: Claude <noreply@anthropic.com>
  ```
- Never amend an existing commit; add a new one.
- Check `git status --porcelain` after committing — the tree must be clean.

## File-ownership rules (conflict-avoidance contract)

These hot files are owned by exactly **one agent per wave**. Do not edit a file
you were not assigned unless the current state makes it unavoidable, and never
reformat code outside your own additions.

1. `internal/protocol/types.go` — one agent per wave; **additive-only** edits (new constants/fields, never renames). Regenerate goldens after touching it.
2. `internal/server/websocket.go` — one owner per wave. Build on the reader-goroutine loop; never reintroduce a blocking inline `ReadMessage` loop.
3. `internal/mcp/server.go` — after the descriptor-driven registration exists, **only append** new tool rows / per-domain files (`tools_network.go`, `tools_download.go`, …); do not rewrite `RegisterTools`.
4. `internal/browser/engine.go` — one agent per wave may change `Observe()`; all observe rework goes in `internal/browser/observe.go`.
5. `internal/browser/actions.go` — one owner per wave; add your own `case` and your own helper file, never reformat others' cases.
6. `internal/api/router.go` — one owner per wave; at most one route + one self-contained handler file per agent.
7. `internal/engine/engine.go` + `factory.go` + `testhelpers.go` — any interface-signature change must be a **single commit** spanning the interface and all implementations (browser, android, MemoryEngine).
8. `internal/protocol/engine.go` — legacy unused interface; never touch it.

## Scope guard

Work **only** on the improvement-plan items assigned to you. Do not fix
unrelated bugs or refactor code you did not add — note them in your final
report instead.

## Checkpoint convention

After each wave the coordinator writes `checkpoints/wave-NN.md` (items
completed, commit hashes, verification results) and updates per-item progress
markers in `docs/improvement-plan.md`, then commits it. Update progress
markers in `docs/improvement-plan.md` for the items you complete during your
run.
