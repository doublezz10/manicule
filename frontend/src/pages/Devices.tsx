import React, { useEffect, useState } from "react";
import QRCode from "qrcode";
import { backend, onEvent, type ServerStatus } from "../lib/api";

export function DevicesPage() {
  const [st, setSt] = useState<ServerStatus | null>(null);
  const [qr, setQr] = useState("");
  const [preview, setPreview] = useState("");

  const refresh = () => backend.serverStatus().then(setSt);
  useEffect(() => {
    refresh();
    backend.provisioningPreview().then(setPreview).catch(() => {});
    return onEvent("server:status", refresh);
  }, []);

  useEffect(() => {
    if (!st?.running || !st.lan_url) return;
    QRCode.toDataURL(st.lan_url, { width: 220, margin: 1, color: { dark: "#1c1712", light: "#efe6d6" } })
      .then(setQr)
      .catch(() => setQr(""));
  }, [st?.running, st?.lan_url]);

  if (!st) return <div className="empty">loading…</div>;

  return (
    <>
      <div className="eyebrow">opds <span className="dot">·</span> your books over wifi</div>
      <h1>Devices</h1>
      {!st.running && (
        <div className="disclaimer" style={{ maxWidth: 720 }}>
          The OPDS server isn't running. Check Settings → OPDS server, and make sure a
          library folder is configured.
        </div>
      )}

      <div className="device-hero">
        <div style={{ flex: 1 }}>
          <h2 style={{ marginTop: 0 }}>Your reader's credentials</h2>
          <div style={{ color: "var(--text-dim)" }}>Username</div>
          <div className="url-line">{st.username}</div>
          <div style={{ color: "var(--text-dim)" }}>PIN (four taps on the device)</div>
          <div className="pin-display">{st.pin}</div>
          <div className="url-line" style={{ marginTop: 20 }}>{st.lan_url}</div>
          <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
            <button className="primary" onClick={() => navigator.clipboard.writeText(st.lan_url)}>Copy URL</button>
            <button onClick={async () => { await backend.regeneratePin(); refresh(); }}>New PIN</button>
            <button
              onClick={async () => {
                const dest = await backend.saveProvisioningFile();
                if (dest) alert(`Saved.\n\nDrop this file onto the device's SD card at /.crosspoint/opds.json — the reader imports it with zero typing.`);
              }}
            >
              Save provisioning file…
            </button>
          </div>
        </div>

        {qr && (
          <div className="qr-box">
            <img src={qr} alt="OPDS URL QR code" />
            <div style={{ color: "var(--text-dim)", fontSize: 12, textAlign: "center", marginTop: 8 }}>
              scan to copy<br />the feed address
            </div>
          </div>
        )}
      </div>

      <h2>Zero-typing setup (SD card)</h2>
      <p style={{ color: "var(--text-dim)", maxWidth: 720, lineHeight: 1.6 }}>
        CrossPoint readers import saved OPDS servers from <b>/.crosspoint/opds.json</b>.
        Tap “Save provisioning file…”, write it to your SD card's root as
        <b> /.crosspoint/opds.json</b>, reinsert — done, no keyboard needed.
        The file contains your feed URL and current PIN in plain text; treat the card like a password note.
      </p>
      <pre className="code-block">
        {preview}
      </pre>
    </>
  );
}
