# Manicule — product & engineering spec

☞ **Status: ALIGNED** (2026-08-21, after full design grill — every open decision resolved).
Build deferred until the Xteink X3 arrives; this document is the build-from artifact.
Related: `rd-aa-downloader-spec.md` (queued Real-Debrid glue; eventual candidate for absorption
into the AA adapter — standalone script remains the stopgap).

## 1. One-liner

A free, open-source desktop app that replaces the librarr + Calibre two-app setup with **one
window**: search your sources → one-click download → auto-cleaned local library → built-in OPDS
server that CrossPoint/X3 readers browse and pull from over WiFi. First-run under two minutes;
device setup = scan nothing, type four characters.

## 2. Locked decisions (design grill, 2026-08-21)

| Decision | Call |
|---|---|
| Name | **manicule** — the ☞ pointing-hand symbol from manuscript margins ("look here"). Near-empty GitHub namespace (15 hits, none in ebooks). Logo = ☞, free |
| License | **MIT** |
| Funding | None. Free OSS forever; optional Ko-fi link + `FUNDING.yml` (README badge + About panel only) |
| Stack | **Wails v3 (Go backend) + React + TypeScript frontend**. Pure-Go SQLite (`modernc.org/sqlite`, librarr's driver — no CGO), `net/http` + `goquery`, Go stdlib `image` + `imaging` for cleaning |
| Platforms | **Tri-platform CI builds day one** (macOS/Windows/Linux), unsigned during development; Apple Developer ($99/yr) + notarization added **before public r/xteinkereader launch**. Positioning: macOS-first, Windows/Linux community-supported |
| Remote access | User-managed Tailscale — explicitly not a product feature |
| Source tiers | **Tier 1 legal catalogs ON by default** (Standard Ebooks, Project Gutenberg via Gutendex; Open Library joins M1 as metadata layer); **Tier 2 opt-in only** (explicit toggle + acknowledged disclaimer + user credentials), isolated in `sources_optin/` module, extractable to companion repo under pressure |
| Search model | Federated fan-out to enabled sources in parallel; results **grouped by source** with filter chips (no fake unified ranking); own library searched same bar via SQLite FTS, flagged "In library" |

## 3. Why build it (verified 2026-08-21)

**No exact match exists** (nothing combines shadow-library search → managed library → built-in
OPDS in an installable GUI): librarr 257★ (Docker web server, nearest neighbor; AA scraper broke
Aug 21 — DDoS-Guard JS challenge), Shelfwright 17★ (Windows GUI, SSH-push instead of OPDS),
bindery 401★ / Chaptarr 333★ (indexer-sourcing only), Grimmory 4,005★ / BookOrbit 2,675★ (pure
OPDS servers, zero acquisition), XTLibre 76★ (XTC + OPDS + push, no acquisition), plus 1–9★
micro-bridges proving people hand-stitch these exact pieces.

Demand: r/xteinkereader = 40,433 subs, ~40 posts/day; Dec-2025 thread describes this product
verbatim; Aug-2026 automation thread invokes the \*arr analogy over five-app workflows. Wedge =
the non-technical majority nobody serves.

## 4. Device constraints (verified — firmware source read where noted)

1. Serve OPDS over **plain http:// only — https OOM-crashes the device**.
2. ~~No in-catalog search~~ **CORRECTED by source read**: CrossPoint `develop` has
   `BrowserState::SEARCH_INPUT` + search button in the OPDS browser — issue #2107 appears stale.
   `/opds/search` + OpenSearch therefore matter **more**, not less. Verify on device day one.
3. **Auth is fully supported device-side**: `OpdsServer{name, url, username, password}` per saved
   server (max 8), stored XOR-obfuscated in `/.crosspoint/opds.json` on SD; requests send creds
   (`HttpDownloader::fetchUrl(url, stream, username, password)`). Entry is via on-device
   virtual keyboard (`KeyboardEntryActivity`) — workable, painful for long strings → keep creds tiny.
4. The store loader accepts legacy plaintext passwords and re-obfuscates on save → **zero-typing
   provisioning path**: our app can generate `/.crosspoint/opds.json` for a one-time SD-card drop.
   Verify plaintext-import behavior at build.
5. Issue #2522 requests optimize-on-OPDS-download → our dual-format entries answer it first.
6. X3 conversion profile = **528×792** portrait (not marketed 480×800); XTCH (2-bit) suits the
   4-level grayscale panel.

## 5. Architecture

### 5.1 Sources — two tiers
Trait per source: `search` / `getLinks` / `download`.
- **Tier 1 (default-on)**: Standard Ebooks (official OPDS feed, ~1k pristine titles), Project
  Gutenberg via **Gutendex** JSON API + direct gutenberg.org file URLs (~70k; note: geo-blocks
  Germany — future fleet citizen), Open Library in M1 (metadata/covers primary, acquisition of
  scanned copies de-prioritized).
- **Tier 2 (opt-in)**: Z-Library (eAPI login flow, SSO session survival across mirrors),
  Anna's Archive (donator-key API preferred, scrape fallback; future RD unrestrict), LibGen
  (mirror scraping). Enable flow = Settings toggle → disclaimer → own credentials. Disclaimer
  draft: *"This source connects to a third-party website not affiliated with this app, which
  bundles no content and no credentials. You are responsible for providing your own account and
  for ensuring your use complies with applicable law."*

### 5.2 Endpoint fleet subsystem (the moat)
Registry = `sources.json` committed in-repo; fetch chain at startup: **live raw.githubusercontent →
disk cache → snapshot embedded at build time** (Go `embed`). Probing: async at launch (search never
blocks against last-known-good domains), re-probe on failure, ~30-min background refresh. Failover
applies at link-selection; failed downloads auto-retry next candidate — no mid-download swapping.
Manual personal-domain override field; errors surface only after whole fleet fails (+ "Test
connections" button); Tor/onion transport toggle shipped dark in v1.

### 5.3 Library store
SQLite + FTS5; files filed `Author/Title`; dedupe = normalized title+author hash → **skip-and-notify
toast** (no modals), per-book Replace/Keep-both available later. Watch-folder: **copy-only** ingest,
optional "remove source file after successful import & cleaning" toggle (**default off**).
Remove-from-library → OS trash, never rm.

### 5.4 Cleaning pipeline (Calibre absorption)
Native Go e-ink cleaner replicating bigbag's optimizer recipe: strip floats/flex/grid/fixed CSS +
embedded fonts; images → grayscale baseline JPEG at max width; flatten alpha; drop SVG/WebP/TIFF;
inject sane CSS. Auto-runs post-download (toggleable). **Originals sacred**: master EPUB untouched,
derivative stored alongside (`Book.clean.epub`, Calibre-multi-format style); per-book revert always
possible. Lightweight structural validation (zip integrity, XHTML well-formedness, image decodability)
instead of Java epubcheck. Odd-format conversion (MOBI/AZW3→EPUB): detect-and-shell Calibre's
`ebook-convert` when present. Kobo DRM out of scope.

### 5.5 Built-in OPDS server
OPDS 1.2, http-only, bind-all interfaces, **port 8787** (8080 = Calibre collision, 5050 = librarr),
page size 20. **Auth ON by default with tiny creds**: username fixed `reader`, password = 4-char PIN
displayed huge in-app beside the QR (four taps on the device keyboard, verified supported by §4.3);
one-click disable toggle. Feeds: root, /newest, /by-author, /by-title, /search + OpenSearch.
Acquisition entries expose EPUB master (+ derived XTCH once M3a lands). Server runs while app is
open; **launch-at-login pre-checked** in wizard. M1 nicety: generate `/.crosspoint/opds.json`
for zero-typing SD-card provisioning (§4.4).

### 5.6 Phase 2 (ordered)
- **M3a — XTCH dual-format FIRST**: bigbag CLI as sidecar (verify license before bundling;
  pluggable for cr2xt when its CLI lands), X3 profile 528×792; serves every user through existing
  OPDS flow; answers issue #2522.
- **M3b — Send-to-device push second**: UDP discovery + CrossPoint WebSocket upload endpoints
  (XTLibre proves feasibility); flashier demo, thinner value — hence second.

## 6. UX sketch

Single window: sidebar (Search · Queue · Library · Devices · Settings), cover-grid results,
per-source status pills, tray showing "OPDS live at http://<ip>:8787/opds" + QR + big-PIN display.
First-run wizard: pick library folder → sources screen (Tier 1 live immediately; Tier 2 collapsed
behind "More sources" + disclaimer) → launch-at-login checkbox → done. Target < 2 min to first
device pull, achievable with zero accounts because Tier 1 ships live.

## 7. Milestones

- **M0 (weekend 1):** Wails scaffold + Tier 1 adapters (no auth — trivial) → working legit
  search→download demo end-to-end.
- **M1 (wks 2–4, MVP):** library store + watch-folder + cleaning pass + OPDS server + QR/PIN +
  settings + Open Library metadata. Dogfood on own X3 (verify device auth + search behavior).
- **M2:** Tier 2 adapters behind opt-in flow, endpoint-fleet prober, enrichment polish,
  packaging/auto-update/signing (buy Apple dev here, before any public post).
- **M3a:** XTCH dual-format. **M3b:** device discovery/push.
- **Launch:** r/xteinkereader framed clean ("companion app — works great with Gutenberg/Standard
  Ebooks; bring your own accounts for more"), crosspoint-reader Discussions, MobileRead.

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Source-breakage treadmill | Adapter isolation + fleet prober + crowd registry; donator-key APIs preferred over scraping |
| DMCA/repo takedown | Two-tier sources; Tier-2 extractable from `sources_optin/`; neutral name; personal-use framing |
| Scope creep into \*arr bloat | Non-goals below are load-bearing |
| librarr converges on desktop | Speed + X3-native moats (fleet prober, tiny-cred auth UX, dual-format, push) |
| Wails v3 auto-update immaturity | v1 ships in-app "check releases" link; auto-update is post-launch polish |

## 9. Explicit non-goals

Paid anything; hosted services; DRM removal; audiobook/manga management (v1); mobile apps;
replacing readers — Manicule does the plumbing, the reader does the reading.
