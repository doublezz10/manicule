import React, { useEffect, useState } from "react";
import QRCode from "qrcode";
import { backend, onEvent, type DeviceProgress, type DeviceState, type ServerStatus } from "../lib/api";
import { useToast } from "../App";
import { ManiculeMark } from "../components/icons";
import { formatBytes } from "../lib/format";

interface SendState {
  title: string;
  done: number;
  total: number;
  error?: string;
}

export function DevicesPage() {
  const toastCtx = useToast();
  const [st, setSt] = useState<DeviceState | null>(null);
  const [server, setServer] = useState<ServerStatus | null>(null);
  const [qr, setQr] = useState("");
  const [preview, setPreview] = useState("");
  const [sending, setSending] = useState<Record<number, SendState>>({});
  const [busy, setBusy] = useState(false);

  const refreshServer = () => backend.serverStatus().then(setServer);
  const refresh = () =>
    backend
      .deviceScan()
      .then((d) => setSt(d))
      .catch(() => {});

  useEffect(() => {
    refreshServer();
    refresh();
    backend.provisioningPreview().then(setPreview).catch(() => {});
    const offs = [
      onEvent("server:status", refreshServer),
      onEvent("device:changed", (d: DeviceState) => d && setSt(d)),
      onEvent("device:connected", (d: DeviceState) => d && setSt(d)),
      onEvent("device:disconnected", (d: DeviceState) => d && setSt(d)),
      onEvent("device:progress", (p: DeviceProgress) => {
        if (!p) return;
        setSending((prev) => {
          const next = { ...prev };
          if (p.error) {
            toastCtx.push("error", `${p.title}: ${p.error}`);
            delete next[p.book_id];
          } else if (p.done != null && p.total != null && p.done >= p.total) {
            delete next[p.book_id];
          } else {
            next[p.book_id] = { title: p.title, done: p.done ?? 0, total: p.total ?? 0 };
          }
          return next;
        });
      }),
    ];
    return () => offs.forEach((off) => off());
  }, []);

  useEffect(() => {
    if (!server?.running || !server.lan_url) return;
    QRCode.toDataURL(server.lan_url, { width: 220, margin: 1, color: { dark: "#1c1712", light: "#efe6d6" } })
      .then(setQr)
      .catch(() => setQr(""));
  }, [server?.running, server?.lan_url]);

  const connected = st?.phase === "connected";
  const sendCount = st?.missing.length ?? 0;

  const syncAll = async () => {
    setBusy(true);
    try {
      setSt(await backend.syncDevice());
      toastCtx.push("ok", "Device is up to date");
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    } finally {
      setBusy(false);
      setSending({});
    }
  };

  const sendOne = async (bookId: number, title: string) => {
    setSending((prev) => ({ ...prev, [bookId]: { title, done: 0, total: 0 } }));
    try {
      setSt(await backend.sendToDevice(bookId));
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    } finally {
      setSending((prev) => {
        const next = { ...prev };
        delete next[bookId];
        return next;
      });
    }
  };

  const removeOne = async (path: string) => {
    try {
      setSt(await backend.removeFromDevice([path]));
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    }
  };

  const provision = async () => {
    try {
      await backend.provisionDeviceOPDS();
      toastCtx.push("ok", "manicule catalog saved on the reader");
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    }
  };

  const senders = Object.values(sending);

  return (
    <>
      <div className="eyebrow">your reader <span className="dot">·</span> wifi sync, no cables</div>
      <h1>Devices</h1>

      {/* --- connection + sync -------------------------------------------- */}
      {!connected && (
        <div className="disclaimer" style={{ maxWidth: 760 }}>
          {st?.phase === "searching" || st === null ? (
            <>
              <div className="empty-title">Looking for your reader…</div>
              On the device, open <b>File Transfer</b> and join the same Wi-Fi as this Mac.
              manicule finds it automatically — leave that screen open while you sync.
            </>
          ) : (
            <>
              <div className="empty-title">No reader on the network.</div>
              Put the device in <b>File Transfer</b> mode (it answers only from that screen)
              and make sure it's on the same Wi-Fi. {st?.last_error ? `Last error: ${st.last_error}` : ""}
            </>
          )}
          <div style={{ marginTop: 12 }}>
            <button className="primary" onClick={() => void refresh()} disabled={busy}>
              Rescan now
            </button>
          </div>
        </div>
      )}

      {connected && st?.status && (
        <div className="device-hero">
          <div style={{ flex: 1 }}>
            <h2 style={{ marginTop: 0, display: "flex", alignItems: "center", gap: 10 }}>
              <ManiculeMark size={22} /> CrossPoint {st.status.device}
              <span className="pill ok">connected</span>
            </h2>
            <div style={{ color: "var(--text-dim)" }}>
              firmware {st.status.version} · {st.status.ip} ·{" "}
              {st.status.mode === "AP" ? "hotspot" : `${st.status.rssi} dBm`}
            </div>
            <div style={{ margin: "12px 0 4px", fontWeight: 550 }}>
              {st.on_device.length} on device
              {sendCount > 0 && <> · <span style={{ color: "var(--rubric-bright)" }}>{sendCount} ready to send</span></>}
              {(st.orphan?.length ?? 0) > 0 && <> · {st.orphan.length} not in library</>}
            </div>
            <div style={{ display: "flex", gap: 10, flexWrap: "wrap", marginTop: 10 }}>
              <button className="primary" onClick={() => void syncAll()} disabled={busy || sendCount === 0}>
                {busy ? "Syncing…" : sendCount > 0 ? `Sync ${sendCount} book${sendCount === 1 ? "" : "s"}` : "All synced"}
              </button>
              <button onClick={() => void refresh()} disabled={busy}>Rescan</button>
              <button onClick={() => void provision()} title="Save manicule's OPDS catalog into the reader's server list — no SD card needed">
                Install manicule catalog
              </button>
            </div>
          </div>
        </div>
      )}

      {senders.length > 0 && (
        <div style={{ margin: "18px 0" }}>
          {senders.map((s) => {
            const pct = s.total > 0 ? Math.min(100, Math.round((s.done / s.total) * 100)) : 0;
            return (
              <div className="queue-row" key={s.title}>
                <span className="state-chip running">sending</span>
                <div style={{ flex: 1 }}>
                  <div style={{ fontWeight: 550 }}>{s.title}</div>
                  <div className="queue-progress">
                    <div className="queue-progress-track">
                      <div className="queue-progress-bar" style={{ width: `${s.total > 0 ? pct : 15}%` }} />
                    </div>
                    <span className="queue-progress-label">
                      {s.total > 0 ? `${pct}% · ${formatBytes(s.done)} of ${formatBytes(s.total)}` : "…"}
                    </span>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {connected && (
        <>
          {st!.missing.length > 0 && (
            <>
              <h2>Ready to send</h2>
              {st!.missing.map((mi) => (
                <div className="queue-row" key={`m-${mi.book_id}`}>
                  <span className="state-chip queued">in library</span>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontWeight: 550 }}>{mi.title}</div>
                    <div style={{ color: "var(--text-dim)", fontSize: 12.5 }}>
                      {mi.author} · {mi.format}
                      {mi.remote_path ? ` → ${mi.remote_path}` : ""}
                    </div>
                  </div>
                  <button
                    className="small"
                    disabled={sending[mi.book_id] != null}
                    onClick={() => void sendOne(mi.book_id, mi.title)}
                  >
                    Send
                  </button>
                </div>
              ))}
            </>
          )}

          {st!.on_device.length > 0 && (
            <>
              <h2>On device</h2>
              {st!.on_device.map((mi) => (
                <div className="queue-row" key={`o-${mi.book_id}`}>
                  <span className="state-chip done">synced</span>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontWeight: 550 }}>{mi.title}</div>
                    <div style={{ color: "var(--text-dim)", fontSize: 12.5 }}>
                      {mi.author} · {mi.device_path}
                      {mi.device_size ? ` · ${formatBytes(mi.device_size)}` : ""}
                    </div>
                  </div>
                  <button className="small ghost" onClick={() => void removeOne(mi.device_path!)}>
                    Remove
                  </button>
                </div>
              ))}
            </>
          )}

          {(st!.orphan?.length ?? 0) > 0 && (
            <>
              <h2>On device, not in library</h2>
              {st!.orphan.map((f) => (
                <div className="queue-row" key={f.Path} style={{ opacity: 0.8 }}>
                  <span className="state-chip queued">unmatched</span>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontWeight: 550 }}>{f.Path}</div>
                    <div style={{ color: "var(--text-dim)", fontSize: 12.5 }}>{formatBytes(f.Size)}</div>
                  </div>
                  <button className="small ghost" onClick={() => void removeOne(f.Path)}>
                    Remove
                  </button>
                </div>
              ))}
            </>
          )}

          {st!.missing.length === 0 && st!.on_device.length === 0 && (st!.orphan?.length ?? 0) === 0 && (
            <div className="empty">
              <div className="big"><ManiculeMark size={40} /></div>
              <div className="empty-title">Card is clear.</div>
              Download a book and hit sync — it lands on the reader over Wi-Fi.
            </div>
          )}
        </>
      )}

      {/* --- OPDS pull flow ----------------------------------------------- */}
      <h2 style={{ marginTop: 34 }}>Browse from the reader (OPDS)</h2>
      {!server ? (
        <div className="empty">loading…</div>
      ) : (
        <>
          {!server.running && (
            <div className="disclaimer" style={{ maxWidth: 720 }}>
              The OPDS server isn't running. Check Settings → OPDS server, and make sure a
              library folder is configured.
            </div>
          )}
          <div className="device-hero">
            <div style={{ flex: 1 }}>
              <div style={{ color: "var(--text-dim)" }}>Username</div>
              <div className="url-line">{server.username}</div>
              <div style={{ color: "var(--text-dim)" }}>PIN (four taps on the device)</div>
              <div className="pin-display">{server.pin}</div>
              <div className="url-line" style={{ marginTop: 20 }}>{server.lan_url}</div>
              <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
                <button className="primary" onClick={() => navigator.clipboard.writeText(server.lan_url)}>Copy URL</button>
                <button onClick={async () => { await backend.regeneratePin(); refreshServer(); }}>New PIN</button>
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
          <p style={{ color: "var(--text-dim)", maxWidth: 720, lineHeight: 1.6 }}>
            The pull flow still works the way it always has: the reader browses your library at
            the address above. “Install manicule catalog” (above, when connected) writes these
            credentials onto the device over Wi-Fi — the SD-card provisioning file is the fallback.
          </p>
        </>
      )}
    </>
  );
}
