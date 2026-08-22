# manicule

☞ **manicule** — the pointing hand from manuscript margins. "Look here."

One window for your e-reader's whole pipeline: search legal book catalogs → one-click download →
auto-cleaned local library → built-in OPDS server your CrossPoint / Xteink X3 reader browses and
pulls from over WiFi.

Built to replace the librarr + Calibre two-app shuffle with a single free, open-source desktop app.

## Status

**In active development.** See [x3-companion-app-spec.md](./x3-companion-app-spec.md) for the full
product & engineering spec and roadmap. Unsigned development builds; notarized installers land
before public launch.

## Highlights

- **Federated search** — Tier 1 legal catalogs on by default (Standard Ebooks, Project Gutenberg);
  results grouped per source, no fake unified ranking. Your own library is searched from the same
  bar via SQLite full-text search.
- **One-click download** into a managed library filed `Author/Title`, with duplicate detection.
- **E-ink cleaning pipeline** — a native Go port of bigbag's optimizer recipe: strips floats/flex/
  grid/fixed CSS + embedded fonts, converts images to grayscale for e-ink panels, injects sane CSS.
  Your original EPUB stays untouched (`Book.clean.epub` sits alongside it).
- **Built-in OPDS server** — OPDS 1.2 over plain http (https OOM-crashes e-ink readers), port 8787,
  auth on by default with tiny credentials: username `reader`, a 4-character PIN shown huge beside
  a QR code. Four taps on the device keyboard, done.
- **Endpoint fleet** — source domains live in a community registry with automatic failover and
  background re-probing, so one dead mirror never breaks a source.

## Stack

[Wails v3](https://v3.wails.io/) (Go backend) + React + TypeScript frontend.
Pure-Go SQLite (`modernc.org/sqlite` — no CGO for the database). MIT licensed. No accounts,
no telemetry, no paid anything.

## Development

Requirements: Go 1.27+, Node 22+, Wails v3 CLI.

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
wails3 doctor        # check platform dependencies
wails3 task dev      # run with live reload
wails3 task build    # produce build/bin/*
go test ./...        # backend tests
```

## Positioning

manicule bundles no content and no credentials. Tier 1 sources are free, legal catalogs.
Additional sources are opt-in only: you acknowledge a disclaimer and supply your own account.
You are responsible for ensuring your use complies with applicable law.

Remote access is user-managed Tailscale — deliberately not a product feature.
