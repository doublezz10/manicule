import React, { useEffect, useState } from "react";
import { ManiculeMark } from "./icons";
import { backend, type SearchResult } from "../lib/api";

/** A search hit merged across catalogs that carry the same book. */
export interface MergedEntry {
  key: string;
  title: string;
  authors: string[];
  year?: string;
  language?: string;
  coverUrl?: string;
  description?: string;
  results: SearchResult[];
}

function Modal(props: { onClose: () => void; children: React.ReactNode; label: string }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") props.onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [props.onClose]);

  return (
    <div className="modal-backdrop" onClick={props.onClose}>
      <div
        className="modal-card page-enter"
        role="dialog"
        aria-modal="true"
        aria-label={props.label}
        onClick={(e) => e.stopPropagation()}
      >
        <button className="modal-close" aria-label="Close" onClick={props.onClose}>
          ×
        </button>
        {props.children}
      </div>
    </div>
  );
}

function CoverArt(props: { src?: string; alt: string }) {
  return props.src ? (
    <img className="modal-cover" src={props.src} alt={props.alt} referrerPolicy="no-referrer" />
  ) : (
    <div className="modal-cover modal-cover-fallback">
      <ManiculeMark size={44} />
    </div>
  );
}

/** One catalog's format buttons, with sizes probed lazily when unknown. */
function FormatButtons(props: {
  result: SearchResult;
  onDownload: (r: SearchResult, formatName: string) => void;
}) {
  const [probed, setProbed] = useState<Record<string, number>>({});
  const unknown = props.result.formats.filter((f) => !f.size && !(f.name in probed));

  useEffect(() => {
    if (unknown.length === 0) return;
    let alive = true;
    for (const f of unknown) {
      backend
        .probeFileSize(f.url)
        .then((bytes) => {
          if (alive && bytes > 0) setProbed((p) => ({ ...p, [f.name]: bytes }));
        })
        .catch(() => {}); // no size available — button just omits it
    }
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.result.source_id, props.result.id]);

  const sizeLabel = (f: (typeof props.result.formats)[number]) => {
    const bytes = f.size || probed[f.name];
    return bytes ? ` · ${Math.round(bytes / 1024 / 102.4) / 10} MB` : "";
  };

  return (
    <div className="card-actions">
      {props.result.formats.map((f) => (
        <button key={f.name} className="small fmt" onClick={() => props.onDownload(props.result, f.name)}>
          +{f.name}
          {sizeLabel(f)}
        </button>
      ))}
    </div>
  );
}

/** Detail view for a merged set of catalog hits: metadata + per-source links. */
export function SearchBookModal(props: {
  entry: MergedEntry;
  onClose: () => void;
  onDownload: (r: SearchResult, formatName: string) => void;
}) {
  const { entry } = props;
  const withFormats = entry.results.filter((r) => r.formats.length > 0);

  // backfill a missing blurb from Open Library on open; failures stay quiet
  const [backfill, setBackfill] = useState<"idle" | "loading" | "ok">("idle");
  const [backfillText, setBackfillText] = useState("");
  useEffect(() => {
    if (entry.description) return;
    let alive = true;
    setBackfill("loading");
    backend
      .workDescription(entry.title, entry.authors)
      .then((d) => {
        if (!alive) return;
        if (d) {
          setBackfillText(d);
          setBackfill("ok");
        } else {
          setBackfill("idle");
        }
      })
      .catch(() => alive && setBackfill("idle"));
    return () => {
      alive = false;
    };
  }, [entry.key, entry.description, entry.title, entry.authors]);
  const desc = entry.description ?? (backfill === "ok" ? backfillText : "");

  return (
    <Modal onClose={props.onClose} label={`Details for ${entry.title}`}>
      <div className="modal-grid">
        <div>
          <CoverArt src={entry.coverUrl} alt={entry.title} />
        </div>
        <div className="modal-info">
          <h2 className="book-title">{entry.title}</h2>
          <div className="book-author">{entry.authors.join(", ")}</div>
          <div className="cover-meta" style={{ margin: "10px 0 4px" }}>
            {entry.year ? <span className="pill">{entry.year}</span> : null}
            {entry.language ? <span className="pill">{entry.language}</span> : null}
            {withFormats.map((r) => (
              <span className="pill ok" key={r.source_id}>{r.source_name}</span>
            ))}
          </div>
          {desc ? (
            <p className="book-desc">{desc}</p>
          ) : backfill === "loading" ? (
            <p className="book-desc desc-loading">fetching a description…</p>
          ) : null}
          <div className="get-block">
            {withFormats.map((r) => (
              <div className="src-row" key={r.source_id}>
                <div className="src-name">{r.source_name}</div>
                <FormatButtons result={r} onDownload={props.onDownload} />
              </div>
            ))}
            {withFormats.length === 0 && (
              <div className="src-name">no direct downloads for this title</div>
            )}
          </div>
        </div>
      </div>
    </Modal>
  );
}

/** Detail view for a book already in the library. */
export function LibraryBookModal(props: {
  book: {
    id: number;
    title: string;
    authors: string[];
    year?: number;
    subjects?: string[];
    decade?: string;
    cover_path?: string;
    description?: string;
    added_at: string;
  };
  files: { id: number; format: string; size_bytes: number; is_original: boolean }[];
  port: number;
  onClose: () => void;
  onDownloadFile: (fileId: number) => void;
  onRevertClean: () => void;
  onDelete: () => void;
}) {
  const b = props.book;
  const originals = props.files.filter((f) => f.is_original);
  const cleaned = props.files.some((f) => !f.is_original);
  const added = new Date(b.added_at).toLocaleDateString(undefined, { dateStyle: "long" });

  // stored blurb first; otherwise backfill once and the backend persists it
  const [blurb, setBlurb] = useState(b.description ?? "");
  const [loadingBlurb, setLoadingBlurb] = useState(!b.description);
  useEffect(() => {
    if (b.description) return;
    let alive = true;
    backend
      .bookBlurb(b.id)
      .then((d) => alive && (setBlurb(d), setLoadingBlurb(false)))
      .catch(() => alive && setLoadingBlurb(false));
    return () => {
      alive = false;
    };
  }, [b.id, b.description]);

  return (
    <Modal onClose={props.onClose} label={`Details for ${b.title}`}>
      <div className="modal-grid">
        <div>
          <CoverArt src={b.cover_path ? `http://localhost:${props.port}/cover/${b.id}.jpg` : undefined} alt={b.title} />
        </div>
        <div className="modal-info">
          <h2 className="book-title">{b.title}</h2>
          <div className="book-author">{b.authors.filter((a) => a && a !== "Unknown").join(", ") || "Author unknown"}</div>
          <div className="cover-meta" style={{ margin: "10px 0 4px" }}>
            {b.year ? <span className="pill">{b.year}</span> : null}
            {b.decade ? <span className="pill">{b.decade}</span> : null}
            {(b.subjects ?? []).slice(0, 3).map((s) => (
              <span className="pill" key={s}>{s}</span>
            ))}
          </div>
          {blurb ? (
            <p className="book-desc">{blurb}</p>
          ) : loadingBlurb ? (
            <p className="book-desc desc-loading">fetching a description…</p>
          ) : null}
          <div className="get-block">
            <div className="src-row">
              <div className="src-name">Files{cleaned ? " · cleaned copy kept alongside original" : ""}</div>
              <div className="card-actions">
                {originals.map((f) => (
                  <button key={f.id} className="small fmt" onClick={() => props.onDownloadFile(f.id)}>
                    Save {f.format} · {Math.round(f.size_bytes / 1024 / 102.4) / 10} MB
                  </button>
                ))}
                {!originals.length && <span className="pill">no files</span>}
              </div>
            </div>
            <div className="src-row">
              <div className="src-name">Shelved {added}</div>
              <div className="card-actions">
                {cleaned && (
                  <button
                    className="small ghost"
                    title="Delete the derived clean file; the master is untouched"
                    onClick={props.onRevertClean}
                  >
                    revert clean
                  </button>
                )}
                <button className="small ghost" title="Move to OS trash" onClick={props.onDelete}>
                  delete
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}
