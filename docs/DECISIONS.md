# Decisions & findings during the build

Log of choices made while implementing `x3-companion-app-spec.md`, newest last.
Anything that contradicts the spec is recorded here until the spec is amended.

## 2026-08-21 — Standard Ebooks OPDS feed is no longer anonymous (verified live)

The spec assumed Tier 1 = "Standard Ebooks, no auth — trivial". Reality: SE moved
the full OPDS catalog behind their **Patrons Circle**; access = patron **email +
blank password** over HTTP Basic. Only the New Releases RSS/Atom feed remains
anonymous, and it isn't searchable.

Consequences:

- The SE adapter takes an optional user email (`Settings → Sources → Standard Ebooks`)
  and reports a `needs-auth` status pill until provided.
- Gutendex (verified live, fully anonymous) carries the zero-account first-run path.
- Action item before public launch: apply for manicule under SE's open-source
  project access program so we can document first-class support.

## 2026-08-21 — Library layout: DB and covers live inside the library folder

Everything (SQLite DB, FTS index, covers) lives under `<LibraryRoot>/.manicule/`
and all file paths are stored relative to the library root, so moving or syncing
the library folder keeps it intact.

## 2026-08-21 — Covers are served without OPDS auth

`/cover/{id}.jpg` on the OPDS server is exempt from Basic auth so the app's own
cover grid renders in the webview. Everything else (feeds, downloads) stays
behind `reader`/PIN when auth is on.

## 2026-08-21 — Trash, never rm

Book deletion moves files to OS trash (macOS `~/.Trash`, Linux XDG Trash,
Windows fallback `.manicule-trash`) with a copy+remove cross-volume fallback.
A hidden library-local `.manicule-trash` is the last resort — user data is
always recoverable.

## 2026-08-21 — Tray scope for v1

Spec sketches "tray showing OPDS URL + QR + big PIN". v1 ships the tray label
with the LAN URL and PIN plus a menu (copy URL, toggle PIN auth, quit); the QR
lives on the Devices page where there's room to scan it. Full QR-in-tray can
come later if wanted.

## 2026-08-21 — Ko-fi

`FUNDING.yml` points at `ko-fi: manicule`; About panel links the same page.
Update once the final Ko-fi username is confirmed.
