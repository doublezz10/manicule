import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import { backend, onEvent, type QueueTask } from "./lib/api";
import { Sidebar } from "./components/Sidebar";
import { Wizard } from "./components/Wizard";
import { ManiculeMark } from "./components/icons";
import { SearchPage } from "./pages/Search";
import { QueuePage } from "./pages/Queue";
import { LibraryPage } from "./pages/Library";
import { DevicesPage } from "./pages/Devices";
import { SettingsPage } from "./pages/Settings";

export interface Toast {
  id: number;
  kind: "ok" | "error" | "info";
  text: string;
}

interface ToastCtx {
  push: (kind: Toast["kind"], text: string) => void;
}

const ToastContext = createContext<ToastCtx>({ push: () => {} });
export const useToast = () => useContext(ToastContext);

export type Page = "search" | "queue" | "library" | "devices" | "settings";

export default function App() {
  const [page, setPage] = useState<Page>("search");
  const [wizardDone, setWizardDone] = useState<boolean | null>(null);
  const [queueCount, setQueueCount] = useState(0);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [serverLine, setServerLine] = useState("");

  const push = useCallback((kind: Toast["kind"], text: string) => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, kind, text }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4200);
  }, []);

  useEffect(() => {
    backend.getSettings().then((s) => setWizardDone(s?.wizard_done ?? false)).catch(() => setWizardDone(false));
    const offQueue = onEvent("queue:changed", (tasks: QueueTask[]) => {
      if (Array.isArray(tasks)) {
        const active = tasks.filter((t) => t.state === "queued" || t.state === "running").length;
        setQueueCount(active);
      }
    });
    const offLib = onEvent("library:changed", () => {
      push("ok", "Library updated");
    });
    const offErr = onEvent("app:error", (msg: any) => {
      if (typeof msg === "string") push("error", msg);
    });
    return () => { offQueue(); offLib(); offErr(); };
  }, []);

  useEffect(() => {
    let alive = true;
    backend.serverStatus().then((st) => {
      if (alive && st?.running) setServerLine(`OPDS live at ${st.lan_url}`);
    });
    return () => { alive = false; };
  }, [page]);

  if (wizardDone === null) {
    return (
      <div className="loading-screen">
        <ManiculeMark size={56} />
        <div>opening the study…</div>
      </div>
    );
  }

  if (!wizardDone) {
    return (
      <Wizard
        onDone={() => {
          setWizardDone(true);
          push("ok", "Welcome to manicule — your reader is ready to pull");
        }}
      />
    );
  }

  return (
    <ToastContext.Provider value={{ push }}>
      <div className="layout">
        <Sidebar page={page} setPage={setPage} queueCount={queueCount} serverLine={serverLine} />
        <div className="main" key={page}>
          <div className="page-enter">
            {page === "search" && <SearchPage />}
            {page === "queue" && <QueuePage />}
            {page === "library" && <LibraryPage />}
            {page === "devices" && <DevicesPage />}
            {page === "settings" && <SettingsPage />}
          </div>
        </div>
      </div>
      <div className="toasts">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.kind}`}>{t.text}</div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
