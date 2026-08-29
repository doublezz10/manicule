import React, { useEffect, useState } from "react";
import { backend, onEvent } from "../lib/api";
import { useToast } from "../App";
import { ManiculeMark } from "../components/icons";
import { LibraryBookModal } from "../components/BookModal";

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
    year?: number;
    subjects?: string[];
    decade?: string;
    cover_path?: string;
    added_at: string;
  };
  files: BookFileT[];
}

export function LibraryPage() {
  const toastCtx = useToast();
  const [books, setBooks] = useState<BookT[]>([]);
  const [genres, setGenres] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState("recent");
  const [genreFilter, setGenreFilter] = useState<string | null>(null);
  const [port, setPort] = useState(8787);
  const [detail, setDetail] = useState<BookT | null>(null);
  const [onDevice, setOnDevice] = useState<Set<number>>(new Set());

  useEffect(() => {
    backend.serverStatus().then((st) => st?.port && setPort(st.port));
    backend.genres?.().then((g) => setGenres(g ?? [])).catch(() => {});
    // last known sync state — in-memory, never touches the network
    backend.deviceState().then((d) => {
      if (d?.phase === "connected") setOnDevice(new Set(d.on_device.map((m) => m.book_id)));
    }).catch(() => {});
  }, []);

  const refresh = async () => {
    try {
      let res;
      if (genreFilter) {
        res = await backend.listByGenre(genreFilter, sort, 0);
      } else {
        res = await backend.listLibrary(query, sort, 0);
      }
      setBooks(Array.isArray(res) ? res : []);
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    }
  };

  useEffect(() => { refresh(); }, [query, sort, genreFilter]);
  useEffect(() => onEvent("library:changed", () => {
    refresh();
    backend.genres?.().then((g) => setGenres(g ?? [])).catch(() => {});
  }), []);
  useEffect(() => onEvent("device:changed", (d: any) => {
    setOnDevice(d?.phase === "connected" ? new Set<number>(d.on_device.map((m: any) => m.book_id)) : new Set());
  }), []);

  return (
    <>
      <div className="eyebrow">
        your collection
        {books.length > 0 && (<><span className="dot">·</span>{books.length} book{books.length === 1 ? "" : "s"}</>)}
      </div>
      <div className="page-head">
        <h1>Library</h1>
        <input
          type="text"
          placeholder="Search your library…"
          value={query}
          onChange={(e) => { setQuery(e.target.value); setGenreFilter(null); }}
          style={{ width: 300 }}
        />
        <select value={sort} onChange={(e) => setSort(e.target.value)}>
          <option value="recent">Recent</option>
          <option value="title">Title</option>
          <option value="author">Author</option>
          <option value="year">Year</option>
          <option value="decade">Decade</option>
          <option value="genre">Genre</option>
        </select>
        <button onClick={() => backend.importFiles().catch((e) => toastCtx.push("error", e?.message ?? String(e)))}>
          Import…
        </button>
      </div>

      {genres.length > 0 && (
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 18 }}>
          <span
            className={`pill ${!genreFilter ? "ok" : ""}`}
            style={{ cursor: "pointer" }}
            onClick={() => setGenreFilter(null)}
          >
            All
          </span>
          {genres.map((g) => (
            <span
              key={g}
              className={`pill ${genreFilter === g ? "ok" : ""}`}
              style={{ cursor: "pointer" }}
              onClick={() => { setGenreFilter(genreFilter === g ? null : g); setQuery(""); }}
            >
              {g}
            </span>
          ))}
        </div>
      )}

      {books.length === 0 && (
        <div className="empty">
          <div className="big"><ManiculeMark size={46} /></div>
          <div className="empty-title">Every library starts at zero.</div>
          Download something from Search, or drop EPUBs into the watch folder (Settings).
        </div>
      )}

      <div className="cover-grid">
        {books.map((b) => {
          const author = b.book.authors.filter((a) => a && a !== "Unknown").join(", ");
          return (
            <div
              className="cover-card clickable"
              key={b.book.id}
              role="button"
              tabIndex={0}
              aria-label={`Details for ${b.book.title}`}
              onClick={() => setDetail(b)}
              onKeyDown={(e) => e.key === "Enter" && setDetail(b)}
            >
              <Cover id={b.book.id} hasCover={!!b.book.cover_path} title={b.book.title} port={port} />
              <div className="cover-title">{b.book.title}</div>
              {author && <div className="cover-author">{author}</div>}
              <div className="cover-meta">
                {onDevice.has(b.book.id) && <span className="pill ok">on device</span>}
                {b.book.year ? <span className="pill">{b.book.year}</span> : null}
                {b.book.subjects?.[0] ? <span className="pill">{b.book.subjects[0]}</span> : null}
              </div>
            </div>
          );
        })}
      </div>

      {detail && (
        <LibraryBookModal
          book={detail.book}
          files={detail.files}
          port={port}
          onClose={() => setDetail(null)}
          onDownloadFile={(fileId) =>
            backend
              .openExternal(`http://localhost:${port}/download/${detail.book.id}/${fileId}`)
              .catch((e) => toastCtx.push("error", e?.message ?? String(e)))
          }
          onRevertClean={async () => {
            await backend.revertClean(detail.book.id);
            toastCtx.push("ok", "Cleaned copy removed — original untouched");
            setDetail(null);
            refresh();
          }}
          onDelete={async () => {
            if (!confirm(`Move "${detail.book.title}" to trash?`)) return;
            await backend.deleteBook(detail.book.id);
            setDetail(null);
            refresh();
          }}
        />
      )}
    </>
  );
}

function Cover(props: { id: number; hasCover: boolean; title: string; port: number }) {
  const [failed, setFailed] = useState(false);
  if (!props.hasCover || failed) {
    return <div className="cover-fallback"><ManiculeMark size={30} /></div>;
  }
  return (
    <img
      className="cover-img"
      src={`http://localhost:${props.port}/cover/${props.id}.jpg`}
      alt={props.title}
      onError={() => setFailed(true)}
    />
  );
}
