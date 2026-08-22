import React, { useEffect, useState } from "react";
import { backend, onEvent } from "../lib/api";
import { useToast } from "../App";

interface BookFileT {
  id: number;
  format: string;
  path: string;
  size_bytes: number;
  is_original: boolean;
}

interface BookT {
  book: {
    id: number;
    title: string;
    authors: string[];
    cover_path?: string;
    added_at: string;
  };
  files: BookFileT[];
}

export function LibraryPage() {
  const toastCtx = useToast();
  const [books, setBooks] = useState<BookT[]>([]);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState("recent");
  const [port, setPort] = useState(8787);

  useEffect(() => {
    backend.serverStatus().then((st) => st?.port && setPort(st.port));
  }, []);

  const refresh = async () => {
    try {
      const res = await backend.listLibrary(query, sort, 0);
      setBooks(Array.isArray(res) ? res : []);
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    }
  };

  useEffect(() => { refresh(); }, [query, sort]);
  useEffect(() => onEvent("library:changed", refresh), []);

  return (
    <>
      <div style={{ display: "flex", gap: 10, alignItems: "center", marginBottom: 20 }}>
        <h1 style={{ margin: 0, flex: 1 }}>Library</h1>
        <input
          type="text"
          placeholder="Search your library…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ width: 300 }}
        />
        <select value={sort} onChange={(e) => setSort(e.target.value)}>
          <option value="recent">Recent</option>
          <option value="title">Title</option>
          <option value="author">Author</option>
        </select>
        <button onClick={() => backend.importFiles().catch((e) => toastCtx.push("error", e?.message ?? String(e)))}>
          Import…
        </button>
      </div>

      {books.length === 0 && (
        <div className="empty">
          <div className="big">📚</div>
          Your library is empty.<br />
          Download something from Search, or drop EPUBs into the watch folder (Settings).
        </div>
      )}

      <div className="cover-grid">
        {books.map((b) => (
          <div className="cover-card" key={b.book.id}>
            <Cover id={b.book.id} hasCover={!!b.book.cover_path} title={b.book.title} port={port} />
            <div className="cover-title">{b.book.title}</div>
            <div className="cover-author">{b.book.authors.join(", ")}</div>
            <div className="card-actions">
              {b.files.filter((f) => f.is_original).map((f) => (
                <a key={f.id} href={`http://localhost:${port}/download/${b.book.id}/${f.id}`} target="_blank" rel="noreferrer">
                  <button className="small">{f.format}</button>
                </a>
              ))}
              {b.files.some((f) => !f.is_original) && (
                <span className="pill ok" title="A cleaned copy sits alongside the original">cleaned</span>
              )}
              <button
                className="small"
                title="Delete the derived clean file; the master is untouched"
                onClick={async () => {
                  await backend.revertClean(b.book.id);
                  toastCtx.push("ok", "Cleaned copy removed — original untouched");
                }}
              >
                revert clean
              </button>
              <button
                className="small"
                title="Move to OS trash"
                onClick={async () => {
                  if (!confirm(`Move "${b.book.title}" to trash?`)) return;
                  await backend.deleteBook(b.book.id);
                  refresh();
                }}
              >
                delete
              </button>
            </div>
          </div>
        ))}
      </div>
    </>
  );
}

function Cover(props: { id: number; hasCover: boolean; title: string; port: number }) {
  if (!props.hasCover) {
    return <div className="cover-img" style={{ display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-dim)", fontSize: 30 }}>☞</div>;
  }
  return (
    <img
      className="cover-img"
      src={`http://localhost:${props.port}/cover/${props.id}.jpg`}
      alt={props.title}
      onError={(ev: React.SyntheticEvent<HTMLImageElement>) => { ev.currentTarget.style.visibility = "hidden"; }}
    />
  );
}
