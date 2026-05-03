# recall-org-roam

`recall-org-roam` is the Org/Org-roam search provider for [Recall](https://github.com/solodov/recall). It scans a local Org notes tree, projects ID-backed Org entries into a disposable Bleve index, and serves those entries through Recall's `SearchProvider` protocol.

The boundary is intentional:

- `recall-org-roam` owns Org corpus discovery, Org parsing, entry projection, index maintenance, Emacs save-time sync, and the provider RPC implementation.
- Recall owns the universal search UX, provider orchestration, selector filtering, result blending, grouping, opener selection, and terminal rendering.

## What it indexes

The index is built from `.org` files under the configured notes root. Entries are keyed by Org `ID` properties and include searchable headline, outline, tag, scheduling, category, style, and body-text data. A `tags.org` file at the notes root can provide tag-group expansion, and configured directory names can be excluded from discovery.

Search results are returned to Recall as `entry:content` results. Each result displays its cleaned outline under a file group, with an `org-protocol://roam-node?...` open target for Org-roam and a file target when the source file is known.

## Installation

Install the provider binary with Go:

```sh
go install github.com/solodov/recall-org-roam/cmd/recall-org-roam@latest
```

Make sure the Go binary directory, usually `$(go env GOPATH)/bin` or `GOBIN`, is on `PATH` so Recall and Emacs can launch `recall-org-roam`.

## Configuration

By default the CLI reads `~/.config/recall-org-roam/config.txtpb`, or `$XDG_CONFIG_HOME/recall-org-roam/config.txtpb` when `XDG_CONFIG_HOME` is set.

```protobuf
notes_root: "~/org"
excluded_directory_names: "excluded-one"
excluded_directory_names: "excluded-two"
```

`notes_root` is required. `index_directory` is optional and defaults to a safe XDG cache location for `recall-org-roam`.

## Recall integration

After installing the `recall-org-roam` binary, add it as a stdio provider in your Recall config:

```protobuf
providers {
  id: "org"
  enabled: true
  weight: 1.0
  timeout_ms: 1500
  default_limit: 30
  stdio {
    command: "recall-org-roam"
    args: "recall-provider"
  }
}
```

Recall launches the provider process over stdio and calls the `recall.search.v1.SearchProvider` RPCs. `recall-org-roam` advertises the provider-local `entry:content` surface; Recall applies its normal provider selection, ranking, rendering, and open-target behavior around those results.

## Emacs integration

`emacs/recall-org-roam.el` keeps the external index current while editing Org files. Enable `recall-org-roam-mode` to run asynchronous file updates after Org saves, and use the package commands for manual rebuilds or diagnostics.

With Emacs 29+ `package-vc`, install the package directly from GitHub:

```elisp
(require 'package-vc)
(unless (package-installed-p 'recall-org-roam)
  (package-vc-install
   '(recall-org-roam
     :url "https://github.com/solodov/recall-org-roam.git"
     :lisp-dir "emacs")))
(require 'recall-org-roam)
(recall-org-roam-mode 1)
```

The Emacs package updates the index only; interactive search stays in Recall.

## Development

Use the repo `Justfile` for local workflows:

- `just build` builds the Go binary and byte-compiles the Emacs package.
- `just test` runs Go and Emacs tests.
- `just install` installs the built binary into the Go binary directory.

Additional design notes and completed implementation plans live in `docs/`.
