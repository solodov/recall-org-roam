---
id: 20260426-191608-standalone-org-search-cli-indexing
title: Standalone org-search CLI indexing ID-based Org entries with file-granular Bleve search
status: done
created: 2026-04-26
updated: 2026-04-27
currentPhase: 
externalRef: 
origin: 
---

# Standalone org-search CLI indexing ID-based Org entries with file-granular Bleve search

## Outcome

`org-search` should be a standalone Go CLI organized around six clean seams: a repo-root Justfile build surface, a txtpb config contract, a Cobra command boundary, a discovery layer for reachable Org files, a `go-org` projection layer, and a Bleve-backed index lifecycle. The Justfile should own generation, build, test, and run workflows in the same spirit as `../shiny`, while the binary itself stays JSON-first and loads a small config proto instead of spreading settings across flags.

The control flow stays simple: the Justfile prepares generated code and build outputs; Cobra loads config and dispatches `rebuild`, `update-file`, and `search`; discovery resolves reachable `.org` files into canonical absolute identities; the parser adapter turns ID-bearing Org entries into index documents; and the index layer rebuilds the corpus or replaces one file’s documents by exact canonical path. Search remains a thin Bleve query pass-through and returns only `id` and `headline` in v1.

## Phases

- [x] 1. Establish a repo-level Justfile build and generation surface
- [x] 2. Define a simple txtpb config boundary backed by a proto schema
- [x] 3. Keep Cobra as a thin command boundary around application operations
- [x] 4. Define corpus discovery around reachable .org files and canonical identity
- [x] 5. Wrap go-org behind a projection layer that defines the indexed entry model
- [x] 6. Keep Bleve maintenance file-granular and treat the index as disposable cache state

## Phase Details

### Phase 1: Establish a repo-level Justfile build and generation surface

`org-search` should have one root `Justfile` that acts as the standard developer and automation entrypoint, similar to `../shiny`, so normal workflows do not depend on remembering `protoc`, `go build`, or package-specific test commands.

- Add stable recipes for `generate-proto`, `build`, `test`, and `run`. `build` and `test` should sequence proto generation so config schema changes cannot leave stale generated code in use.
- If formatting is part of the repo contract, expose it through a `lint` recipe rather than relying on raw `goimports` or `gofmt` usage.
- Keep the repo shape simple: one binary, one root `Justfile`, and one `dist/` output boundary. This project does not need a more layered wrapper/local Justfile split.

The maintainability win is that repo ergonomics become a first-class boundary: code generation and build orchestration live outside the application logic, but they stay standardized and predictable.

### Phase 2: Define a simple txtpb config boundary backed by a proto schema

The first durable operator input should be a small config proto, loaded from txtpb in the same style as `../shiny`. That gives the tool one explicit configuration contract before indexing logic or CLI options grow.

- Start with a narrow schema: required `notes_root` and optional `index_directory`.
- Keep the proto/codegen boundary explicit: schema under `proto/`, generated Go types in an internal generated package, and a runtime `Config` struct that hides protobuf details from the rest of the app.
- Normalize once at load time: trim strings, expand `~`, require absolute paths after normalization, and default `index_directory` under the `org-search` XDG data location when unset.
- Ship a small example txtpb so the config contract is reviewable without reading code.

This keeps configuration centralized and typed, rather than letting flags, defaults, and path handling drift across multiple layers.

### Phase 3: Keep Cobra as a thin command boundary around application operations

Cobra should organize commands and help text, but it should not become the application architecture. Each command should delegate to application-level operations that return structured results and errors.

- Keep the v1 command set as `rebuild`, `update-file`, and `search`.
- Make `--config` the shared top-level control, with command arguments focused only on the requested operation.
- Preserve machine-oriented JSON output by default so the CLI remains script-friendly even though Cobra is used for routing and flags.

The clean seam is that Cobra owns invocation and argument decoding, while config loading, discovery, indexing, and result shaping live below it as reusable services.

### Phase 4: Define corpus discovery around reachable .org files and canonical identity

Discovery should still define the corpus as every reachable `.org` file under the configured notes root, including files reached through symlinked directories or symlinked files. What matters is reachability from the configured root, not whether the resolved target stays inside that tree.

- Traverse from `notes_root`, follow symlinks, and include outside targets when they are reached by links under the root.
- Resolve each discovered file to one canonical absolute path and deduplicate aggressively enough to prevent loops and double-indexing from multiple symlink routes.
- Surface broken symlinks and unreadable paths as warnings when the operation can still proceed, so discovery stays explicit rather than silently lossy.
- Reuse the same canonicalization rules for `update-file` inputs so targeted updates and full rebuilds agree on file identity.

This keeps reachability and identity logic in one place instead of letting parser and index code invent their own path semantics.

### Phase 5: Wrap go-org behind a projection layer that defines the indexed entry model

The parser choice can now be concrete: use `github.com/niklasfasching/go-org`. But that dependency should stay boxed behind a projection layer so the rest of the system depends only on `org-search`’s own entry document contract.

- One indexed document should equal one real Org entry with an `ID` property.
- Entries without `ID` are ignored; synthetic IDs and file-derived IDs stay out of scope.
- Each projected document should contain the entry’s own `headline`, own `body`, and maintenance metadata such as canonical `path`, while excluding descendant subtree content from that entry’s body.
- Detect duplicate Org `ID` values across the reachable corpus before indexing so the system fails explicitly instead of letting Bleve overwrite one document with another.

This is the main semantic boundary of the tool: `go-org` parses source text, but `org-search` owns the stable meaning of an indexed entry.

### Phase 6: Keep Bleve maintenance file-granular and treat the index as disposable cache state

Index operations should remain deliberately simple: rebuild the whole corpus when needed, or replace one file’s full document set on `update-file`. That matches the source-of-truth boundary and avoids fragile incremental entry diffing.

- Use the Org `ID` as the Bleve document ID.
- Store canonical absolute `path` on every document and index it as an exact-match field so the index layer can replace or clean up all documents belonging to one file.
- `update-file` should delete all documents for one canonical path and then re-index that file’s current projected entries if it still exists.
- If the target file no longer exists, `update-file` should still succeed as cleanup for that path. Missing or corrupt index state should produce repair-oriented errors that point callers back to `rebuild` rather than silently rebuilding during `search`.

Validation should follow these seams rather than individual commands: config loading and normalization, proto generation workflow, discovery and symlink deduplication, projection correctness, duplicate-ID failure behavior, exact-path replacement, stale-file cleanup, and Bleve query pass-through.

The overall maintainability win is that each major dependency stays localized: the Justfile owns repo workflows, protobuf owns config shape, Cobra owns command routing, `go-org` owns Org parsing, and Bleve owns search storage. The rest of `org-search` stays defined by its own data model and operational rules.

## Plan Notes

## Summary

`org-search` should be a standalone Go CLI organized around six clean seams: a repo-root Justfile build surface, a txtpb config contract, a Cobra command boundary, a discovery layer for reachable Org files, a `go-org` projection layer, and a Bleve-backed index lifecycle. The Justfile should own generation, build, test, and run workflows in the same spirit as `../shiny`, while the binary itself stays JSON-first and loads a small config proto instead of spreading settings across flags.

The control flow stays simple: the Justfile prepares generated code and build outputs; Cobra loads config and dispatches `rebuild`, `update-file`, and `search`; discovery resolves reachable `.org` files into canonical absolute identities; the parser adapter turns ID-bearing Org entries into index documents; and the index layer rebuilds the corpus or replaces one file’s documents by exact canonical path. Search remains a thin Bleve query pass-through and returns only `id` and `headline` in v1.

## Implementation details

### 1. Establish a repo-level Justfile build and generation surface

`org-search` should have one root `Justfile` that acts as the standard developer and automation entrypoint, similar to `../shiny`, so normal workflows do not depend on remembering `protoc`, `go build`, or package-specific test commands.

- Add stable recipes for `generate-proto`, `build`, `test`, and `run`. `build` and `test` should sequence proto generation so config schema changes cannot leave stale generated code in use.
- If formatting is part of the repo contract, expose it through a `lint` recipe rather than relying on raw `goimports` or `gofmt` usage.
- Keep the repo shape simple: one binary, one root `Justfile`, and one `dist/` output boundary. This project does not need a more layered wrapper/local Justfile split.

The maintainability win is that repo ergonomics become a first-class boundary: code generation and build orchestration live outside the application logic, but they stay standardized and predictable.

### 2. Define a simple txtpb config boundary backed by a proto schema

The first durable operator input should be a small config proto, loaded from txtpb in the same style as `../shiny`. That gives the tool one explicit configuration contract before indexing logic or CLI options grow.

- Start with a narrow schema: required `notes_root` and optional `index_directory`.
- Keep the proto/codegen boundary explicit: schema under `proto/`, generated Go types in an internal generated package, and a runtime `Config` struct that hides protobuf details from the rest of the app.
- Normalize once at load time: trim strings, expand `~`, require absolute paths after normalization, and default `index_directory` under the `org-search` XDG data location when unset.
- Ship a small example txtpb so the config contract is reviewable without reading code.

This keeps configuration centralized and typed, rather than letting flags, defaults, and path handling drift across multiple layers.

### 3. Keep Cobra as a thin command boundary around application operations

Cobra should organize commands and help text, but it should not become the application architecture. Each command should delegate to application-level operations that return structured results and errors.

- Keep the v1 command set as `rebuild`, `update-file`, and `search`.
- Make `--config` the shared top-level control, with command arguments focused only on the requested operation.
- Preserve machine-oriented JSON output by default so the CLI remains script-friendly even though Cobra is used for routing and flags.

The clean seam is that Cobra owns invocation and argument decoding, while config loading, discovery, indexing, and result shaping live below it as reusable services.

### 4. Define corpus discovery around reachable `.org` files and canonical identity

Discovery should still define the corpus as every reachable `.org` file under the configured notes root, including files reached through symlinked directories or symlinked files. What matters is reachability from the configured root, not whether the resolved target stays inside that tree.

- Traverse from `notes_root`, follow symlinks, and include outside targets when they are reached by links under the root.
- Resolve each discovered file to one canonical absolute path and deduplicate aggressively enough to prevent loops and double-indexing from multiple symlink routes.
- Surface broken symlinks and unreadable paths as warnings when the operation can still proceed, so discovery stays explicit rather than silently lossy.
- Reuse the same canonicalization rules for `update-file` inputs so targeted updates and full rebuilds agree on file identity.

This keeps reachability and identity logic in one place instead of letting parser and index code invent their own path semantics.

### 5. Wrap `go-org` behind a projection layer that defines the indexed entry model

The parser choice can now be concrete: use `github.com/niklasfasching/go-org`. But that dependency should stay boxed behind a projection layer so the rest of the system depends only on `org-search`’s own entry document contract.

- One indexed document should equal one real Org entry with an `ID` property.
- Entries without `ID` are ignored; synthetic IDs and file-derived IDs stay out of scope.
- Each projected document should contain the entry’s own `headline`, own `body`, and maintenance metadata such as canonical `path`, while excluding descendant subtree content from that entry’s body.
- Detect duplicate Org `ID` values across the reachable corpus before indexing so the system fails explicitly instead of letting Bleve overwrite one document with another.

This is the main semantic boundary of the tool: `go-org` parses source text, but `org-search` owns the stable meaning of an indexed entry.

### 6. Keep Bleve maintenance file-granular and treat the index as disposable cache state

Index operations should remain deliberately simple: rebuild the whole corpus when needed, or replace one file’s full document set on `update-file`. That matches the source-of-truth boundary and avoids fragile incremental entry diffing.

- Use the Org `ID` as the Bleve document ID.
- Store canonical absolute `path` on every document and index it as an exact-match field so the index layer can replace or clean up all documents belonging to one file.
- `update-file` should delete all documents for one canonical path and then re-index that file’s current projected entries if it still exists.
- If the target file no longer exists, `update-file` should still succeed as cleanup for that path. Missing or corrupt index state should produce repair-oriented errors that point callers back to `rebuild` rather than silently rebuilding during `search`.

Validation should follow these seams rather than individual commands: config loading and normalization, proto generation workflow, discovery and symlink deduplication, projection correctness, duplicate-ID failure behavior, exact-path replacement, stale-file cleanup, and Bleve query pass-through.

The overall maintainability win is that each major dependency stays localized: the Justfile owns repo workflows, protobuf owns config shape, Cobra owns command routing, `go-org` owns Org parsing, and Bleve owns search storage. The rest of `org-search` stays defined by its own data model and operational rules.
