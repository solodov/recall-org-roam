---
id: 20260428-143000-org-index-recall-provider-transition
title: org-search transition to org-recall-index recall provider
status: done
created: 2026-04-28
updated: 2026-04-30
currentPhase: 
externalRef: 
origin: 
---

# org-search transition to org-recall-index recall provider

## Outcome

`org-search` became `org-recall-index`, the Org-specific indexer and provider for `recall`.

`org-recall-index` owns Org corpus discovery, Org projection, Bleve index maintenance, save-time file updates, and the Recall `SearchProvider` implementation. Recall owns universal search UX, provider orchestration, result blending, grouping layout, selector filtering, opener selection, and terminal rendering.

The migration intentionally kept no compatibility shims: no `org-search` binary alias, wrapper command, deprecated config path, or transitional Elisp fallback. Internal callers and integration points moved directly to `org-recall-index`.

## Current Recall provider contract

`org-recall-index recall-provider` serves the protobuf `recall.search.v1.SearchProvider` service using the Recall SDK and supports both stdio RPC paths:

- `/recall.search.v1.SearchProvider/Search`
- `/recall.search.v1.SearchProvider/ListCapabilities`

Search requests use Recall’s selector API:

- `query` carries provider-native Org query text.
- `limit` remains a soft provider-local cap; absent or zero means no caller cap.
- `selector_hints` are advisory optimization inputs. Recall still applies authoritative selector filtering.

The provider advertises one provider-local search surface:

- `entry:content` — Org entry headlines, outlines, tags, and body text.

Every returned hit sets `selector: "entry:content"`. The configured Recall provider id is not included in provider-local selectors.

Org hits map into Recall data as:

- `id`: Org entry ID.
- `selector`: `entry:content`.
- `title`: cleaned Org headline.
- `snippet`: omitted until the index exposes useful match context.
- `targets`: first target is the `org-protocol://roam-node?...` URI; include a typed file target when available.
- `group`: file-based grouping keyed by canonical path with a typed file target.
- `score`: omitted unless the search layer exposes Bleve scores cleanly.

## Completed phases

- [x] Add Recall SDK provider integration while the repository was still named `org-search`.
- [x] Rename the package and binary to `org-recall-index` with no shims.
- [x] Move human search rendering responsibility into Recall.
- [x] Update the provider to Recall’s selector-based SearchProvider API.

## End state

- `org-recall-index` indexes Org data and implements Recall’s `SearchProvider` contract.
- Recall searches across corpora and renders results.
- Elisp integration updates the Org index on save and invokes `org-recall-index` directly.
