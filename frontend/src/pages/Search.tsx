import React, { useEffect, useState } from "react";
import { backend, type SearchResult, type SearchGroup } from "../lib/api";
import { useToast } from "../App";
import { ManiculeMark } from "../components/icons";
import { SearchBookModal, type MergedEntry } from "../components/BookModal";

/** Mirror of norm.Key's text rules — enough agreement to merge catalog hits. */
function normText(s: string): string {
  return s
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .trim();
}
const normKey = (title: string, author: string) => `${normText(title)}|${normText(author)}`;

function mergeGroups(groups: SearchGroup[]): MergedEntry[] {
  const byKey = new Map<string, MergedEntry>();
  for (const g of groups) {
    if (g.state !== "ok") continue;
    for (const r of g.results) {
      const key = normKey(r.title, r.authors[0] ?? "");
      let e = byKey.get(key);
      if (!e) {
        e = {
          key,
          title: r.title,
          authors: r.authors,
          year: r.year,
          language: r.language,
          coverUrl: r.cover_url,
          description: r.description,
          results: [],
        };
        byKey.set(key, e);
      }
      // prefer the richest fields across duplicates
      if (!e.coverUrl && r.cover_url) e.coverUrl = r.cover_url;
      if (!e.description && r.description) e.description = r.description;
      if (!e.year && r.year) e.year = r.year;
      e.results.push(r);
    }
  }
  return [...byKey.values()];
}

/** The result a quick +EPUB click should use: preferred source first, then
 *  whichever catalog can supply an EPUB. */
export function pickDefault(results: SearchResult[], defaultSource?: string): SearchResult | null {
  const pref = defaultSource ? results.find((r) => r.source_id === defaultSource) : undefined;
  const pool = pref ? [pref, ...results.filter((r) => r !== pref)] : results;
  for (const r of pool) {
    if (r.formats.some((f) => f.name.toUpperCase() === "EPUB")) return r;
  }
  return pool[0] ?? null;
}

function Cover(props: { src?: string; alt: string }) {
  const [failed, setFailed] = useState(false);
  if (!props.src || failed) {
    return <div className="cover-fallback"><ManiculeMark size={30} /></div>;
  }
  return (
    <img
      className="cover-img"
      src={props.src}
      alt={props.alt}
      referrerPolicy="no-referrer"
      onError={() => setFailed(true)}
    />
  );
}

export function SearchPage() {
  const toastCtx = useToast();
  const [query, setQuery] = useState("");
  const [merged, setMerged] = useState<MergedEntry[] | null>(null);
  const [groupStates, setGroupStates] = useState<SearchGroup[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [defaultSource, setDefaultSource] = useState<string>("");
  const [selected, setSelected] = useState<MergedEntry | null>(null);

  useEffect(() => {
    backend.getSettings().then((s) => setDefaultSource(s?.default_source ?? "")).catch(() => {});
  }, []);

  const search = async () => {
    const q = query.trim();
    if (!q) return;
    setBusy(true);
    try {
      const res = (await backend.searchAll(q)) ?? [];
      setGroupStates(res);
      setMerged(mergeGroups(res));
    } catch (e: any) {
      toastCtx.push("error", `Search failed: ${e?.message ?? e}`);
    } finally {
      setBusy(false);
    }
  };

  const download = async (r: SearchResult, formatName?: string) => {
    const fmt = formatName ?? r.formats[0]?.name;
    if (!fmt) {
      toastCtx.push("error", "No downloadable format");
      return;
    }
    try {
      await backend.download(r, fmt);
      toastCtx.push("ok", `Queued "${r.title}" (${fmt})`);
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    }
  };

  const quickDownload = async (entry: MergedEntry) => {
    const r = pickDefault(entry.results, defaultSource);
    if (r) await download(r, "EPUB");
  };

  const stateWord = (state: string, message?: string) =>
    state === "searching" ? "searching…" :
    state === "needs-auth" ? "needs account" :
    state === "disabled" ? "off" :
    state === "error" ? message ?? "error" : "ready";

  return (
    <>
      <div className="eyebrow">catalogs <span className="dot">·</span> merged across sources</div>
      <h1>Search</h1>
      <div className="search-bar">
        <input
          type="text"
          placeholder="Title, author…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && search()}
          autoFocus
        />
        <button className="primary" onClick={search} disabled={busy}>
          {busy ? (
            <span className="busy-dots" role="status" aria-label="Searching">
              <i /><i /><i />
            </span>
          ) : (
            "Search"
          )}
        </button>
      </div>

      {merged === null && (
        <div className="empty">
          <div className="big"><ManiculeMark size={46} /></div>
          <div className="empty-title">Point at something worth reading.</div>
          Project Gutenberg is live out of the box. Results are merged across catalogs —
          open one to choose where it comes from, and what lands in your library is cleaned for e-ink.
        </div>
      )}

      {merged !== null && merged.length === 0 && (
        <div className="empty">No enabled source returned anything. Check the strip below or add sources in Settings.</div>
      )}

      {merged !== null && merged.length > 0 && (
        <>
          {groupStates && groupStates.length > 0 && (
            <div className="source-strip">
              {groupStates.map((g) => (
                <span className={`pill ${g.state}`} key={g.source_id} title={g.message}>
                  {g.source_name}: {g.state === "ok" ? `${g.results.length}` : stateWord(g.state, g.message)}
                </span>
              ))}
            </div>
          )}
          <div className="cover-grid">
            {merged.map((e) => {
              const quick = pickDefault(e.results, defaultSource);
              return (
                <div
                  className="cover-card clickable"
                  key={e.key}
                  onClick={() => setSelected(e)}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(ev) => ev.key === "Enter" && setSelected(e)}
                >
                  <Cover src={e.coverUrl} alt={e.title} />
                  <div className="cover-title">{e.title}</div>
                  <div className="cover-author">{e.authors.join(", ")}</div>
                  <div className="card-actions">
                    {quick && quick.formats.some((f) => f.name.toUpperCase() === "EPUB") && (
                      <button
                        className="small fmt primary-quick"
                        title={`Quick-download EPUB from ${quick.source_name} — open the card to choose another`}
                        onClick={(ev) => { ev.stopPropagation(); void quickDownload(e); }}
                      >
                        +EPUB
                      </button>
                    )}
                    {e.results.length > 1 && <span className="pill">{e.results.length} sources</span>}
                  </div>
                </div>
              );
            })}
          </div>
        </>
      )}

      {selected && (
        <SearchBookModal
          entry={selected}
          onClose={() => setSelected(null)}
          onDownload={(r, fmt) => void download(r, fmt)}
        />
      )}
    </>
  );
}
