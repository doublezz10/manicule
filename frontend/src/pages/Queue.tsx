import React, { useEffect, useState } from "react";
import { backend, onEvent, type QueueTask } from "../lib/api";
import { useToast } from "../App";

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
      <div style={{ display: "flex", alignItems: "center", marginBottom: 20 }}>
        <h1 style={{ margin: 0, flex: 1 }}>Queue</h1>
        <button onClick={() => backend.clearFinishedQueue()}>Clear finished</button>
      </div>

      {tasks.length === 0 && (
        <div className="empty">
          <div className="big">⇣</div>
          Nothing downloading.<br />Find a book on the Search page and hit +EPUB.
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
          </div>
          {(t.state === "queued" || t.state === "running") && (
            <button className="small" onClick={() => backend.cancelTask(t.id)}>Cancel</button>
          )}
        </div>
      ))}
    </>
  );
}
