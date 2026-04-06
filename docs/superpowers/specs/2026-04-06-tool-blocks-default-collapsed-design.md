# Tool Blocks Default Collapsed Design

**Date:** 2026-04-06

**Goal:** Make all tool blocks render collapsed by default in the session message view, including `Tool: Exec_command` and `Tool Output`, while preserving the existing click-to-expand behavior.

## Context

The current UI renders tool blocks expanded on first load through a single global client-side flag in `internal/api/routes.go`:

- `let collapseTools = false;`

Both direct function-call messages and tool blocks extracted from assistant message content already use this flag to decide whether to show the collapsed summary or expanded body. Existing toggle handlers and long-output `Show more` behavior are already implemented.

## Chosen Approach

Change the default value of `collapseTools` from `false` to `true`.

This keeps the implementation aligned with the existing design:

- One global default for all tool blocks
- No new persistence or settings UI
- No per-tool special cases
- No change to existing expand/collapse event handling

## Files

- Modify `internal/api/routes.go`
  - Flip the default `collapseTools` value to `true`
  - Update the nearby comment so it matches the new default behavior
- Modify `internal/api/routes_test.go`
  - Replace the test that asserts tool blocks open by default with one that asserts they are collapsed by default
  - Keep the assertion that long tool output still exposes the `Show more` toggle

## Validation

1. Run the targeted API tests covering `indexHTML`.
2. Confirm the updated test checks for `let collapseTools = true;`.
3. Confirm no changes are needed to toggle wiring or `Show more` output handling.

## Non-Goals

- Adding a user-visible preference for tool collapse state
- Persisting tool collapse state in `localStorage`
- Introducing per-tool or per-message default collapse rules
