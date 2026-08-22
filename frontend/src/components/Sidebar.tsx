import React from "react";
import type { Page } from "../App";

export function Sidebar(props: {
  page: Page;
  setPage: (p: Page) => void;
  queueCount: number;
  serverLine: string;
}) {
  const items: { id: Page; label: string; icon: string }[] = [
    { id: "search", label: "Search", icon: "🔍" },
    { id: "queue", label: "Queue", icon: "⇣" },
    { id: "library", label: "Library", icon: "📚" },
    { id: "devices", label: "Devices", icon: "📡" },
    { id: "settings", label: "Settings", icon: "⚙" },
  ];
  return (
    <div className="sidebar">
      <div className="logo"><span className="hand">☞</span> manicule</div>
      {items.map((it) => (
        <div
          key={it.id}
          className={`nav-item ${props.page === it.id ? "active" : ""}`}
          onClick={() => props.setPage(it.id)}
        >
          <span>{it.icon}</span>
          {it.label}
          {it.id === "queue" && props.queueCount > 0 && (
            <span className="badge">{props.queueCount}</span>
          )}
        </div>
      ))}
      <div className="sidebar-footer">
        {props.serverLine ? (
          <>
            <div className="opds-pill">● OPDS live</div>
            <div style={{ wordBreak: "break-all" }}>{props.serverLine}</div>
          </>
        ) : (
          <div>OPDS off</div>
        )}
        <div style={{ marginTop: 8 }}>MIT · free forever</div>
      </div>
    </div>
  );
}
