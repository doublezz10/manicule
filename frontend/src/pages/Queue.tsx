import React, { useEffect, useState } from "react";
import { backend, onEvent, type QueueTask } from "../lib/api";
import { useToast } from "../App";
import { ManiculeMark } from "../components/icons";
import { formatBytes } from "../lib/format";

function ProgressRow({ t }: { t: QueueTask }) {
  const pct = t.bytes_total > 0 ? Math.min(100, Math.round((t.bytes_done / t.bytes_total) * 100)) : 0;
  return (
    <div className="queue-progress">
      <div className="queue-progress-track">
        <div className="queue-progress-bar" style={{ width: `${t.bytes_total > 0 ? pct : 15}%` }} />
      </div>
      <span className="queue-progress-label">
        {t.bytes_total > 0 ? `${pct}% · ${formatBytes(t.bytes_done)} of ${formatBytes(t.bytes_total)}` : formatBytes(t.bytes_done) || "…"}
      </span>
    </div>
  );
}

export function QueuePage() {
  const toastCtx = useToast();
  const [tasks, setTasks] = useState<QueueTask[]>([]);

  const refresh = () => backend.getQueue().then((t) => setTasks(t ?? [])).catch(() => {});
  useEffect(() => {
    refresh();
    return onEvent("queue:changed", (t) => setTasks(Array.isArray(t) ? t : []));
  }, []);

  return (
    <>
      <div className="eyebrow">
        downloads
        {tasks.length > 0 && (<><span className="dot">·</span>{tasks.length} in flight</>)}
      </div>
      <div className="page-head">
        <h1>Queue</h1>
        <button onClick={() => backend.clearFinishedQueue()}>Clear finished</button>
      </div>

      {tasks.length === 0 && (
        <div className="empty">
          <div className="big"><ManiculeMark size={46} /></div>
          <div className="empty-title">Nothing in flight.</div>
          Find a book on the Search page and hit +EPUB.
        </div>
      )}

      {tasks.map((t) => (
        <div className="queue-row" key={t.id}>
          <span className={`state-chip ${t.state}`}>
            {t.state === "queued" ? "queued…" :
             t.state === "running" ? "downloading" :
             t.state === "done" ? "✓ in library" :
             t.state === "duplicate" ? "already owned" : "failed"}
          </span>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 550 }}>{t.title}</div>
            <div style={{ color: "var(--text-dim)", fontSize: 12.5 }}>
              {t.authors.join(", ") || t.source_id} · {t.format_name}
              {t.error ? ` — ${t.error}` : ""}
            </div>
            {t.state === "running" && <ProgressRow t={t} />}
            {t.state === "done" && t.bytes_total > 0 && (
              <div style={{ color: "var(--text-dim)", fontSize: 12 }}>{formatBytes(t.bytes_total)}</div>
            )}
          </div>
          {(t.state === "queued" || t.state === "running") && (
            <button className="small" onClick={() => backend.cancelTask(t.id)}>Cancel</button>
          )}
        </div>
      ))}
    </>
  );
}
