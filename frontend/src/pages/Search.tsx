import React, { useState } from "react";
import { backend, type SearchResult } from "../lib/api";
import { useToast } from "../App";
import { ManiculeMark } from "../components/icons";

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
  const [groups, setGroups] = useState<any[] | null>(null);
  const [busy, setBusy] = useState(false);

  const search = async () => {
    const q = query.trim();
    if (!q) return;
    setBusy(true);
    try {
      const res = await backend.searchAll(q);
      setGroups(res ?? []);
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

  const statePill = (state: string, message?: string) => (
    <span className={`pill ${state}`} title={message}>
      {state === "ok" ? `${""}` : ""}
      {state === "searching" ? "searching…" :
       state === "needs-auth" ? "needs account" :
       state === "disabled" ? "off" :
       state === "error" ? message ?? "error" : "ready"}
    </span>
  );

  return (
    <>
      <div className="eyebrow">catalogs <span className="dot">·</span> public first</div>
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
          {busy ? "…" : "Search"}
        </button>
      </div>

      {groups === null && (
        <div className="empty">
          <div className="big"><ManiculeMark size={46} /></div>
          <div className="empty-title">Point at something worth reading.</div>
          Project Gutenberg is live out of the box. Results are grouped by source,
          and what you download lands in your library, cleaned for e-ink.
        </div>
      )}

      {groups !== null && groups.length === 0 && (
        <div className="empty">No sources enabled. Turn some on in Settings.</div>
      )}

      {groups?.map((g) => (
        <div className="source-group" key={g.source_id}>
          <div className="source-group-head">
            {g.source_name}
            {g.state !== "ok" ? statePill(g.state, g.message) : (
              <span className="pill ok">{g.results.length} result{g.results.length === 1 ? "" : "s"}</span>
            )}
            {g.state === "needs-auth" && (
              <span style={{ color: "var(--text-dim)", fontWeight: 400 }}>— add your credentials in Settings</span>
            )}
          </div>
          {(g.state === "ok" || g.state === "searching") && g.results.length > 0 && (
            <div className="cover-grid">
              {g.results.map((r: SearchResult) => (
                <div className="cover-card" key={`${g.source_id}-${r.id}`}>
                  <Cover src={r.cover_url} alt={r.title} />
                  <div className="cover-title">{r.title}</div>
                  <div className="cover-author">{r.authors.join(", ")}</div>
                  <div className="card-actions">
                    {r.formats.slice(0, 2).map((f) => (
                      <button key={f.name} className="small primary" onClick={() => download(r, f.name)}>
                        +{f.name}
                      </button>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </>
  );
}
