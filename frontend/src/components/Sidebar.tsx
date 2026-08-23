import React from "react";
import type { Page } from "../App";
import { ManiculeMark, SearchIcon, QueueIcon, LibraryIcon, DevicesIcon, SettingsIcon } from "./icons";

export function Sidebar(props: {
  page: Page;
  setPage: (p: Page) => void;
  queueCount: number;
  serverLine: string;
}) {
  const items: { id: Page; label: string; icon: React.ReactNode }[] = [
    { id: "search", label: "Search", icon: <SearchIcon /> },
    { id: "queue", label: "Queue", icon: <QueueIcon /> },
    { id: "library", label: "Library", icon: <LibraryIcon /> },
    { id: "devices", label: "Devices", icon: <DevicesIcon /> },
    { id: "settings", label: "Settings", icon: <SettingsIcon /> },
  ];
  const live = !!props.serverLine;
  return (
    <div className="sidebar">
      <div className="logo">
        <ManiculeMark size={34} />
        <span className="word">manicule</span>
      </div>
      {items.map((it) => (
        <div
          key={it.id}
          className={`nav-item ${props.page === it.id ? "active" : ""}`}
          onClick={() => props.setPage(it.id)}
        >
          <ManiculeMark size={22} className="nav-pointer" />
          {it.icon}
          {it.label}
          {it.id === "queue" && props.queueCount > 0 && (
            <span className="badge">{props.queueCount}</span>
          )}
        </div>
      ))}
      <div className="sidebar-footer">
        <div className={`lamp ${live ? "" : "off"}`}>
          <span className="flame">●</span>
          {live ? "OPDS live" : "OPDS off"}
        </div>
        {live && <div className="server-url">{props.serverLine.replace("OPDS live at ", "")}</div>}
        <div style={{ marginTop: 8 }}>MIT · no accounts · no telemetry</div>
      </div>
    </div>
  );
}
