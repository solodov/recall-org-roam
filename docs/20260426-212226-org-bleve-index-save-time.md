---
id: 20260426-212226-org-bleve-index-save-time
title: org-bleve-index save-time Bleve sync with quiet updates and actionable diagnostics
status: done
created: 2026-04-27
updated: 2026-04-27
currentPhase: 
externalRef: 
origin: 
---

# org-bleve-index save-time Bleve sync with quiet updates and actionable diagnostics

## Outcome

After this change, `org-bleve-index` should be a maintenance-only Emacs package that keeps the external Bleve index current without teaching Emacs any index policy. The control flow should stay simple: any file-backed `org-mode` buffer can trigger a save-time sync, the package calls `org-search update-file --json ...` asynchronously, and `org-search` decides whether that path updates the index, cleans up a missing file, or is a no-op because it falls outside the configured corpus. The txtpb config and the `org-search` binary remain the only source of truth for corpus membership, canonical path rules, duplicate-ID validation, and index lifecycle.

The Emacs side should remain intentionally thin. `org-bleve-index` installs the save hook, manages background processes, parses JSON responses, and shows failures in an operator-friendly way. It should not provide search UX, maintain its own view of the corpus, or reimplement file identity rules. Successful updates and structured skips should stay quiet by default; failures should surface as a minibuffer summary plus a dedicated diagnostics buffer. Manual rebuild remains the repair path, and the package docstring should describe both the normal save-driven workflow and the known v1 limitations around rename and delete events.

## Phases

- [x] 1. Establish an editor-safe update-file --json contract in org-search
- [x] 2. Introduce org-bleve-index as a maintenance-only Emacs package
- [x] 3. Use broad save-hook coverage with asynchronous per-file process management
- [x] 4. Surface failures as diagnostics while keeping success and skips quiet
- [x] 5. Document v1 lifecycle limits and preserve a clean future extension seam

## Phase Details

### Phase 1: Establish an editor-safe update-file --json contract in org-search

The main boundary to tighten is the per-file sync command that Emacs will call on every save. `update-file` should become the stable automation surface for editor integrations, with result shapes that make Emacs simple rather than forcing Lisp code to reverse-engineer outcomes from exit codes and stderr text.

- `update-file` should return structured outcomes for at least the meaningful editor cases: updated, cleanup of a missing file, and skipped because the target path is outside the configured corpus.
- Corpus membership must be decided inside `org-search`, not in Elisp. That keeps the config file and the CLI as the only source of truth for notes-root reachability, symlink rules, and canonical path identity.
- Duplicate-ID failures should stay structured and complete, including every conflicting occurrence, so Emacs can present a grouped fix list without text scraping.
- `rebuild --json` should remain the stable coarse-grained repair path for full recrawls and recovery when incremental updates are insufficient.

This phase keeps the editor-facing contract narrow and durable: Emacs gets one dependable command to call, and `org-search` keeps ownership of indexing semantics.

### Phase 2: Introduce org-bleve-index as a maintenance-only Emacs package

The Emacs integration should be packaged as `org-bleve-index`, with a surface that reflects its narrow job: keep an external Bleve index fresh for Org files. It should not imply or grow into a search frontend.

- The package should expose a global minor mode, `org-bleve-index-mode`, that enables autosync behavior.
- Customization should stay small and explicit: the `org-search` binary path, an optional config path override, whether autosync is enabled, and where diagnostics are reported.
- Interactive commands should remain operational rather than exploratory: update the current buffer, rebuild the index, and possibly open the diagnostics buffer. Search commands stay out of scope.
- The package docstring should be the primary integration guide and should explain installation, setup, save-hook behavior, manual rebuild, and the fact that the package maintains an external index but does not provide search functionality itself.

This keeps the Emacs boundary easy to review: one package, one mode, a few operational commands, and no leakage of search or corpus logic into Lisp.

### Phase 3: Use broad save-hook coverage with asynchronous per-file process management

The save path should favor correctness and simplicity over clever prefiltering. The package should hook broadly into file-backed Org buffers and let the CLI decide whether the saved file matters to the configured index.

- `org-bleve-index-mode` should attach to file-backed `org-mode` buffers and trigger work from `after-save-hook`.
- The package should call `org-search update-file --json` asynchronously so normal editing is never blocked on indexing.
- Process ownership should be per file path, not per buffer object. One in-flight process per file is enough, with one queued rerun when another save lands before the current update finishes.
- The package should not precompute corpus membership. A broad hook plus a structured CLI skip result is the intended clean boundary.

This phase localizes runtime control flow inside the editor integration without undermining the CLI’s role as the policy owner.

### Phase 4: Surface failures as diagnostics while keeping success and skips quiet

Because the package exists only to keep the index up to date, the operator experience should emphasize actionable failures rather than normal successful operation. The default experience should be “save and move on.”

- Successful updates should be silent by default.
- Structured skip results for files outside the corpus should also be silent by default, since broad hook coverage is intentional.
- Failures should produce a brief minibuffer summary and a dedicated diagnostics buffer, with duplicate-ID errors rendered as grouped entries showing the conflicting IDs, paths, and headlines.
- Missing binary, unreadable config, malformed JSON, and repair-oriented CLI failures should use the same diagnostics path so setup and runtime problems are easy to distinguish and fix.

This keeps the integration operationally friendly: routine saves are quiet, but index health issues become visible in a consistent, readable form.

### Phase 5: Document v1 lifecycle limits and preserve a clean future extension seam

The v1 integration should explicitly acknowledge that save hooks cover content edits well but do not perfectly track every filesystem lifecycle event. Those limits should be documented rather than papered over with fragile partial heuristics in Emacs.

- The package docstring should note that rename and delete workflows are only partially covered in v1 and that manual rebuild is the intended repair path when the index may have drifted.
- The planned save-driven contract is still valid: ordinary edits are incremental, missing-file cleanup can happen through CLI-owned logic, and full rebuild remains available when needed.
- Future work can add better rename/delete handling through explicit commands, editor hooks for rename-like operations, or broader filesystem observation, but that should remain a later seam rather than a hidden requirement for the first version.
- The naming boundary should also remain explicit: the Emacs package is `org-bleve-index`, while the CLI binary remains `org-search` for now. That separation is acceptable as long as the docstring makes the relationship clear.

This phase keeps the current design honest and maintainable: the v1 workflow is clear, its limitations are documented, and future improvements have a defined place to land.

## Plan Notes

## Summary

After this change, `org-bleve-index` should be a maintenance-only Emacs package that keeps the external Bleve index current without teaching Emacs any index policy. The control flow should stay simple: any file-backed `org-mode` buffer can trigger a save-time sync, the package calls `org-search update-file --json ...` asynchronously, and `org-search` decides whether that path updates the index, cleans up a missing file, or is a no-op because it falls outside the configured corpus. The txtpb config and the `org-search` binary remain the only source of truth for corpus membership, canonical path rules, duplicate-ID validation, and index lifecycle.

The Emacs side should remain intentionally thin. `org-bleve-index` installs the save hook, manages background processes, parses JSON responses, and shows failures in an operator-friendly way. It should not provide search UX, maintain its own view of the corpus, or reimplement file identity rules. Successful updates and structured skips should stay quiet by default; failures should surface as a minibuffer summary plus a dedicated diagnostics buffer. Manual rebuild remains the repair path, and the package docstring should describe both the normal save-driven workflow and the known v1 limitations around rename and delete events.

## Implementation details

### Phase 1: Establish an editor-safe `update-file --json` contract in `org-search`

The main boundary to tighten is the per-file sync command that Emacs will call on every save. `update-file` should become the stable automation surface for editor integrations, with result shapes that make Emacs simple rather than forcing Lisp code to reverse-engineer outcomes from exit codes and stderr text.

- `update-file` should return structured outcomes for at least the meaningful editor cases: updated, cleanup of a missing file, and skipped because the target path is outside the configured corpus.
- Corpus membership must be decided inside `org-search`, not in Elisp. That keeps the config file and the CLI as the only source of truth for notes-root reachability, symlink rules, and canonical path identity.
- Duplicate-ID failures should stay structured and complete, including every conflicting occurrence, so Emacs can present a grouped fix list without text scraping.
- `rebuild --json` should remain the stable coarse-grained repair path for full recrawls and recovery when incremental updates are insufficient.

This phase keeps the editor-facing contract narrow and durable: Emacs gets one dependable command to call, and `org-search` keeps ownership of indexing semantics.

### Phase 2: Introduce `org-bleve-index` as a maintenance-only Emacs package

The Emacs integration should be packaged as `org-bleve-index`, with a surface that reflects its narrow job: keep an external Bleve index fresh for Org files. It should not imply or grow into a search frontend.

- The package should expose a global minor mode, `org-bleve-index-mode`, that enables autosync behavior.
- Customization should stay small and explicit: the `org-search` binary path, an optional config path override, whether autosync is enabled, and where diagnostics are reported.
- Interactive commands should remain operational rather than exploratory: update the current buffer, rebuild the index, and possibly open the diagnostics buffer. Search commands stay out of scope.
- The package docstring should be the primary integration guide and should explain installation, setup, save-hook behavior, manual rebuild, and the fact that the package maintains an external index but does not provide search functionality itself.

This keeps the Emacs boundary easy to review: one package, one mode, a few operational commands, and no leakage of search or corpus logic into Lisp.

### Phase 3: Use broad save-hook coverage with asynchronous per-file process management

The save path should favor correctness and simplicity over clever prefiltering. The package should hook broadly into file-backed Org buffers and let the CLI decide whether the saved file matters to the configured index.

- `org-bleve-index-mode` should attach to file-backed `org-mode` buffers and trigger work from `after-save-hook`.
- The package should call `org-search update-file --json` asynchronously so normal editing is never blocked on indexing.
- Process ownership should be per file path, not per buffer object. One in-flight process per file is enough, with one queued rerun when another save lands before the current update finishes.
- The package should not precompute corpus membership. A broad hook plus a structured CLI skip result is the intended clean boundary.

This phase localizes runtime control flow inside the editor integration without undermining the CLI’s role as the policy owner.

### Phase 4: Surface failures as diagnostics while keeping success and skips quiet

Because the package exists only to keep the index up to date, the operator experience should emphasize actionable failures rather than normal successful operation. The default experience should be “save and move on.”

- Successful updates should be silent by default.
- Structured skip results for files outside the corpus should also be silent by default, since broad hook coverage is intentional.
- Failures should produce a brief minibuffer summary and a dedicated diagnostics buffer, with duplicate-ID errors rendered as grouped entries showing the conflicting IDs, paths, and headlines.
- Missing binary, unreadable config, malformed JSON, and repair-oriented CLI failures should use the same diagnostics path so setup and runtime problems are easy to distinguish and fix.

This keeps the integration operationally friendly: routine saves are quiet, but index health issues become visible in a consistent, readable form.

### Phase 5: Document v1 lifecycle limits and preserve a clean future extension seam

The v1 integration should explicitly acknowledge that save hooks cover content edits well but do not perfectly track every filesystem lifecycle event. Those limits should be documented rather than papered over with fragile partial heuristics in Emacs.

- The package docstring should note that rename and delete workflows are only partially covered in v1 and that manual rebuild is the intended repair path when the index may have drifted.
- The planned save-driven contract is still valid: ordinary edits are incremental, missing-file cleanup can happen through CLI-owned logic, and full rebuild remains available when needed.
- Future work can add better rename/delete handling through explicit commands, editor hooks for rename-like operations, or broader filesystem observation, but that should remain a later seam rather than a hidden requirement for the first version.
- The naming boundary should also remain explicit: the Emacs package is `org-bleve-index`, while the CLI binary remains `org-search` for now. That separation is acceptable as long as the docstring makes the relationship clear.

This phase keeps the current design honest and maintainable: the v1 workflow is clear, its limitations are documented, and future improvements have a defined place to land.
