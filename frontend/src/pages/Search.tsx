import React, { useEffect, useRef, useState } from "react";
import { backend, onEvent, type QueueTask, type SearchResult, type SearchGroup } from "../lib/api";
import { useToast } from "../App";
import { ManiculeMark } from "../components/icons";
import { SearchBookModal, type MergedEntry } from "../components/BookModal";
import { progressLabel } from "../lib/format";

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

interface StreamPayload {
  query: string;
  search_id: string;
  group?: SearchGroup;
  groups?: SearchGroup[];
}

export function SearchPage() {
  const toastCtx = useToast();
  const [query, setQuery] = useState("");
  const [merged, setMerged] = useState<MergedEntry[] | null>(null);
  const [groups, setGroups] = useState<SearchGroup[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [defaultSource, setDefaultSource] = useState<string>("");
  const [selected, setSelected] = useState<MergedEntry | null>(null);
  const [tasks, setTasks] = useState<QueueTask[]>([]);

  // refs mirror the search session so event handlers see current values
  // without re-subscribing
  const queryRef = useRef("");
  const searchSeq = useRef(0);
  const activeSearchId = useRef<string | null>(null);
  const groupsRef = useRef<SearchGroup[]>([]);

  useEffect(() => {
    backend.getSettings().then((s) => setDefaultSource(s?.default_source ?? "")).catch(() => {});
  }, []);

  // results stream in per source; the blocking searchAll resolve is the
  // final, authoritative reconcile (and the only path on cache hits)
  useEffect(() => {
    const offStart = onEvent("search:start", (p: StreamPayload) => {
      if (!p?.groups || p.query !== queryRef.current) return;
      activeSearchId.current = p.search_id;
      groupsRef.current = p.groups;
      setGroups(p.groups);
    });
    const offGroup = onEvent("search:group", (p: StreamPayload) => {
      if (!p?.group || p.search_id !== activeSearchId.current) return;
      const next = [...groupsRef.current];
      const i = next.findIndex((g) => g.source_id === p.group!.source_id);
      if (i >= 0) next[i] = p.group!;
      else next.push(p.group!);
      groupsRef.current = next;
      setGroups(next);
      setMerged(mergeGroups(next));
    });
    return () => { offStart(); offGroup(); };
  }, []);

  // download state, for the on-card progress/shelved chips
  useEffect(() => {
    backend.getQueue().then((t) => setTasks(t ?? [])).catch(() => {});
    return onEvent("queue:changed", (t) => setTasks(Array.isArray(t) ? t : []));
  }, []);

  const search = async () => {
    const q = query.trim();
    if (!q || busy) return;
    const seq = ++searchSeq.current;
    queryRef.current = q;
    activeSearchId.current = null;
    setBusy(true);
    try {
      const res = (await backend.searchAll(q)) ?? [];
      if (searchSeq.current !== seq) return; // a newer search took over
      activeSearchId.current = null;
      groupsRef.current = res;
      setGroups(res);
      setMerged(mergeGroups(res));
    } catch (e: any) {
      if (searchSeq.current === seq) {
        toastCtx.push("error", `Search failed: ${e?.message ?? e}`);
      }
    } finally {
      if (searchSeq.current === seq) setBusy(false);
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

  // the queue task tracking this entry's download, if any
  const taskFor = (entry: MergedEntry): QueueTask | undefined => {
    for (const r of entry.results) {
      const t = tasks.find((x) => x.source_id === r.source_id && x.result_id && x.result_id === r.id);
      if (t) return t;
    }
    return undefined;
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

      {!busy && merged === null && groups === null && (
        <div className="empty">
          <div className="big"><ManiculeMark size={46} /></div>
          <div className="empty-title">Point at something worth reading.</div>
          Project Gutenberg is live out of the box. Results are merged across catalogs —
          open one to choose where it comes from, and what lands in your library is cleaned for e-ink.
        </div>
      )}

      {!busy && merged !== null && merged.length === 0 && (
        <div className="empty">No enabled source returned anything. Check the strip below or add sources in Settings.</div>
      )}

      {groups !== null && groups.length > 0 && (
        <div className="source-strip">
          {groups.map((g) => (
            <span className={`pill ${g.state}`} key={g.source_id} title={g.message}>
              {g.source_name}: {g.state === "ok" ? `${g.results.length}` : stateWord(g.state, g.message)}
            </span>
          ))}
        </div>
      )}

      {merged !== null && merged.length > 0 && (
        <div className="cover-grid">
          {merged.map((e) => {
            const quick = pickDefault(e.results, defaultSource);
            const task = taskFor(e);
            const inFlight = task && (task.state === "queued" || task.state === "running");
            const shelved = task && (task.state === "done" || task.state === "duplicate");
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
                  {task && task.state === "done" && <span className="pill ok">✓ shelved</span>}
                  {task && task.state === "duplicate" && <span className="pill">already owned</span>}
                  {inFlight && (
                    <span className={`pill dl ${task.state === "queued" ? "dim" : ""}`}>
                      {task.state === "queued" ? "queued…" : `↓ ${progressLabel(task.bytes_done, task.bytes_total)}`}
                    </span>
                  )}
                  {!task && quick && quick.formats.some((f) => f.name.toUpperCase() === "EPUB") && (
                    <button
                      className="small fmt primary-quick"
                      title={`Quick-download EPUB from ${quick.source_name} — open the card to choose another`}
                      onClick={(ev) => { ev.stopPropagation(); void quickDownload(e); }}
                    >
                      +EPUB
                    </button>
                  )}
                  {!inFlight && !shelved && e.results.length > 1 && <span className="pill">{e.results.length} sources</span>}
                </div>
              </div>
            );
          })}
        </div>
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
