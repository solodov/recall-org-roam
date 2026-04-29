---
id: 20260428-143000-org-index-recall-provider-transition
title: org-search transition to org-index recall provider
status: done
created: 2026-04-28
updated: 2026-04-29
currentPhase: 
externalRef: 
origin: 
---

# org-search transition to org-index recall provider

## Outcome

`org-search` should become the Org-specific indexer and provider for `recall`, with the durable name `org-recall-index`.

`org-index` is not a good target because it already exists and is too generic. `org-recall-index` makes the boundary explicit:

- `org-recall-index` owns Org corpus discovery, Org projection, Bleve index maintenance, save-time file updates, and Recall `SearchProvider` implementation.
- `recall` owns universal search UX, provider orchestration, result blending, grouping layout, and terminal rendering.
- The Recall protobuf/SDK dependency comes from `github.com/solodov/recall`, accessed through the private Git repo.

The migration does not need compatibility shims. We should not keep an `org-search` binary alias, wrapper command, deprecated config path, or transitional Elisp fallback. Once the rename phase happens, internal callers and integration points move directly to `org-recall-index`.

Phase 1 adds Recall provider integration while the repository is still named `org-search`, so the Recall boundary can be proven before the rename. Phase 2 renames the package, binary, config defaults, docs, and Elisp integration directly to `org-recall-index`. Phase 3 removes direct human search rendering once Recall owns result presentation.

## Phases

- [x] 1. Add Recall SDK provider integration behind the existing org-search CLI
- [x] 2. Rename the package and binary to org-recall-index with no shims
- [x] 3. Move human rendering responsibility into Recall

## Phase Details

### Phase 1: Add Recall SDK provider integration behind the existing org-search CLI

Phase 1 proves the new architecture without mixing provider integration with the package rename.

`org-search` should gain a Recall-compatible provider mode while existing indexing behavior remains stable. The important boundary is that this package starts serving Recall provider requests using Recall’s SDK/protobuf types, while the current CLI continues to exist only because the rename has not happened yet.

- Add Recall as a Go dependency from `github.com/solodov/recall`, using its SDK/protobuf package rather than redefining SearchProvider messages locally.
- Add a provider command or mode such as `org-search recall-provider` that implements Recall’s process transport for `recall.search.v1.SearchProvider.Search`.
- Read one Recall `SearchRequest` from stdin using the SDK-supported encoding, run the existing Org query path with `query` and `limit`, and write one `SearchResponse` to stdout.
- Keep provider diagnostics on stderr so stdout remains reserved for the protobuf response.
- Map Org hits into Recall hits:
  - `id`: Org entry ID.
  - `kind`: `org_entry`.
  - `title`: cleaned Org headline.
  - `snippet`: omit initially unless the index exposes useful match context.
  - `uris`: first URI is the org-roam node URI; include a file URI when available.
  - `group`: file-based grouping keyed by canonical path with a file URI.
  - `score`: include only if the current search layer exposes Bleve scores cleanly.
- Preserve `rebuild`, `update-file`, direct `search`, and Emacs save-time behavior during this phase so the provider integration can be validated independently.
- Add tests around provider request decoding, response encoding, hit mapping, empty results, malformed input, and provider error paths.

The temporary overlap is intentional: direct `org-search search` remains available only until Recall rendering is ready and the rename phase completes. This is not a long-term compatibility promise.

### Phase 2: Rename the package and binary to org-recall-index with no shims

Phase 2 establishes the durable identity and updates all internal integration points directly. There should be no compatibility shim for the old `org-search` name.

The package should be renamed to `org-recall-index` because it communicates the tool’s actual role: an Org index provider for Recall.

This phase should update naming consistently across the system:

- Go module name and import paths.
- Binary name and dist artifact.
- Cobra command names and help text.
- Config paths and default data/index locations.
- Documentation and examples.
- Recall provider configuration examples.
- Emacs package/function/variable names where they refer to the executable or integration identity.
- Save-time hook shell-outs that currently invoke `org-search`.

No `org-search` binary alias, command shim, deprecated config fallback, or Elisp compatibility wrapper should be kept. Callers should be migrated directly to `org-recall-index`.

The rename should not change indexing semantics. It should keep the command focused on indexing and provider responsibilities: rebuild the Org index, update one file, and serve Recall provider requests.

### Phase 3: Move human rendering responsibility into Recall

Once Recall can render named URIs and groups well enough, this package should stop owning human search presentation.

`org-recall-index` should return structured provider results and let Recall render them. The Org-specific UX is preserved through data, not terminal-specific code:

- Org entry titles link through the first `org-protocol://roam-node?...` URI.
- File grouping comes from Recall `group` data keyed by canonical file path.
- File headings link through `file://` group URIs.
- Source/kind context and secondary actions are rendered by Recall’s generic renderer.

At that point, direct human-oriented `search` output should be removed rather than shimmed or deprecated. The durable user-facing search command should be `recall`; `org-recall-index` remains the indexing and provider backend.

The end state is clean:

- `org-recall-index` indexes Org data and implements Recall’s SearchProvider contract.
- Recall searches across corpora and renders results.
- Elisp integration updates the Org index on save and invokes `org-recall-index` directly.

## Plan Notes

## Summary

`org-search` should become the Org-specific indexer and provider for `recall`, with the durable name `org-recall-index`.

`org-index` is not a good target because it already exists and is too generic. `org-recall-index` makes the boundary explicit:

- `org-recall-index` owns Org corpus discovery, Org projection, Bleve index maintenance, save-time file updates, and Recall `SearchProvider` implementation.
- `recall` owns universal search UX, provider orchestration, result blending, grouping layout, and terminal rendering.
- The Recall protobuf/SDK dependency comes from `github.com/solodov/recall`, accessed through the private Git repo.

The migration does not need compatibility shims. We should not keep an `org-search` binary alias, wrapper command, deprecated config path, or transitional Elisp fallback. Once the rename phase happens, internal callers and integration points move directly to `org-recall-index`.

Phase 1 adds Recall provider integration while the repository is still named `org-search`, so the Recall boundary can be proven before the rename. Phase 2 renames the package, binary, config defaults, docs, and Elisp integration directly to `org-recall-index`. Phase 3 removes direct human search rendering once Recall owns result presentation.

## Implementation details

### Phase 1: Add Recall SDK provider integration behind the existing org-search CLI

Phase 1 proves the new architecture without mixing provider integration with the package rename.

`org-search` should gain a Recall-compatible provider mode while existing indexing behavior remains stable. The important boundary is that this package starts serving Recall provider requests using Recall’s SDK/protobuf types, while the current CLI continues to exist only because the rename has not happened yet.

- Add Recall as a Go dependency from `github.com/solodov/recall`, using its SDK/protobuf package rather than redefining SearchProvider messages locally.
- Add a provider command or mode such as `org-search recall-provider` that implements Recall’s process transport for `recall.search.v1.SearchProvider.Search`.
- Read one Recall `SearchRequest` from stdin using the SDK-supported encoding, run the existing Org query path with `query` and `limit`, and write one `SearchResponse` to stdout.
- Keep provider diagnostics on stderr so stdout remains reserved for the protobuf response.
- Map Org hits into Recall hits:
  - `id`: Org entry ID.
  - `kind`: `org_entry`.
  - `title`: cleaned Org headline.
  - `snippet`: omit initially unless the index exposes useful match context.
  - `uris`: first URI is the org-roam node URI; include a file URI when available.
  - `group`: file-based grouping keyed by canonical path with a file URI.
  - `score`: include only if the current search layer exposes Bleve scores cleanly.
- Preserve `rebuild`, `update-file`, direct `search`, and Emacs save-time behavior during this phase so the provider integration can be validated independently.
- Add tests around provider request decoding, response encoding, hit mapping, empty results, malformed input, and provider error paths.

The temporary overlap is intentional: direct `org-search search` remains available only until Recall rendering is ready and the rename phase completes. This is not a long-term compatibility promise.

### Phase 2: Rename the package and binary to org-recall-index with no shims

Phase 2 establishes the durable identity and updates all internal integration points directly. There should be no compatibility shim for the old `org-search` name.

The package should be renamed to `org-recall-index` because it communicates the tool’s actual role: an Org index provider for Recall.

This phase should update naming consistently across the system:

- Go module name and import paths.
- Binary name and dist artifact.
- Cobra command names and help text.
- Config paths and default data/index locations.
- Documentation and examples.
- Recall provider configuration examples.
- Emacs package/function/variable names where they refer to the executable or integration identity.
- Save-time hook shell-outs that currently invoke `org-search`.

No `org-search` binary alias, command shim, deprecated config fallback, or Elisp compatibility wrapper should be kept. Callers should be migrated directly to `org-recall-index`.

The rename should not change indexing semantics. It should keep the command focused on indexing and provider responsibilities: rebuild the Org index, update one file, and serve Recall provider requests.

### Phase 3: Move human rendering responsibility into Recall

Once Recall can render named URIs and groups well enough, this package should stop owning human search presentation.

`org-recall-index` should return structured provider results and let Recall render them. The Org-specific UX is preserved through data, not terminal-specific code:

- Org entry titles link through the first `org-protocol://roam-node?...` URI.
- File grouping comes from Recall `group` data keyed by canonical file path.
- File headings link through `file://` group URIs.
- Source/kind context and secondary actions are rendered by Recall’s generic renderer.

At that point, direct human-oriented `search` output should be removed rather than shimmed or deprecated. The durable user-facing search command should be `recall`; `org-recall-index` remains the indexing and provider backend.

The end state is clean:

- `org-recall-index` indexes Org data and implements Recall’s SearchProvider contract.
- Recall searches across corpora and renders results.
- Elisp integration updates the Org index on save and invokes `org-recall-index` directly.
