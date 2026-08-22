import React, { useEffect, useState } from "react";
import { backend, type SaveSettingsRequest, type SettingsShape } from "../lib/api";
import { useToast } from "../App";

const TIER2_IDS = ["annas-archive", "z-library", "libgen"];
const TIER2_NAMES: Record<string, string> = {
  "annas-archive": "Anna's Archive",
  "z-library": "Z-Library",
  libgen: "Library Genesis",
};
const TIER2_DISCLAIMER =
  "This source connects to a third-party website not affiliated with this app, which " +
  "bundles no content and no credentials. You are responsible for providing your own " +
  "account and for ensuring your use complies with applicable law.";

function Toggle(props: { on: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      className={`toggle ${props.on ? "on" : ""}`}
      onClick={() => props.onChange(!props.on)}
      aria-pressed={props.on}
    />
  );
}

export function SettingsPage() {
  const toastCtx = useToast();
  const [s, setS] = useState<SettingsShape | null>(null);
  const [tier2Acked, setTier2Acked] = useState(false);
  const [showMoreSources, setShowMoreSources] = useState(false);

  useEffect(() => { backend.getSettings().then(setS); }, []);

  if (!s) return <div className="empty">loading…</div>;

  const save = async (req: SaveSettingsRequest) => {
    try {
      const next = await backend.saveSettings(req);
      setS(next ?? s);
    } catch (e: any) {
      toastCtx.push("error", e?.message ?? String(e));
    }
  };

  return (
    <>
      <h1>Settings</h1>

      <div className="settings-section">
        <h2 style={{ marginTop: 0 }}>Library</h2>
        <div className="setting-row">
          <label>
            Library folder
            <span className="hint">{s.library_path || "not configured"}</span>
          </label>
          <button
            onClick={async () => {
              const dir = await backend.pickFolder("Choose your library folder");
              if (dir) save({ library_path: dir });
            }}
          >
            Change…
          </button>
        </div>
        <div className="setting-row">
          <label>Clean books for e-ink on import<span className="hint">Writes Title.clean.epub alongside the untouched original</span></label>
          <Toggle on={s.clean_on_import} onChange={(v) => save({ clean_on_import: v })} />
        </div>
        <div className="setting-row">
          <label>Watch folder<span className="hint">{s.watch_path ? `Copying new EPUBs from ${s.watch_path}` : "Off — pick a folder below"}</span></label>
          <Toggle on={s.watch_enabled} onChange={(v) => save({ watch_enabled: v })} />
        </div>
        {s.watch_enabled && (
          <div className="setting-row">
            <label>Watch folder location</label>
            <button
              onClick={async () => {
                const dir = await backend.pickFolder("Choose a folder to watch");
                if (dir) save({ watch_path: dir });
              }}
            >
              Choose…
            </button>
          </div>
        )}
        {s.watch_enabled && (
          <div className="setting-row">
            <label>Remove source file after successful import<span className="hint">Default off — copies only, originals stay</span></label>
            <Toggle on={s.delete_source_after_import} onChange={(v) => save({ delete_source_after_import: v })} />
          </div>
        )}
      </div>

      <div className="settings-section">
        <h2 style={{ marginTop: 0 }}>OPDS server</h2>
        <div className="setting-row">
          <label>Server running while app is open<span className="hint">Plain http on port {s.server_port} — https crashes e-ink readers</span></label>
          <Toggle on={s.server_enabled} onChange={(v) => save({ server_enabled: v })} />
        </div>
        <div className="setting-row">
          <label>Require PIN<span className="hint">Username is always “reader” — see Devices page</span></label>
          <Toggle on={s.auth_enabled} onChange={(v) => save({ auth_enabled: v })} />
        </div>
        <div className="setting-row">
          <label>Port</label>
          <input
            type="number"
            defaultValue={s.server_port}
            style={{ width: 110 }}
            onBlur={(e) => {
              const v = parseInt(e.target.value, 10);
              if (v && v !== s.server_port) save({ server_port: v });
            }}
          />
        </div>
        <div className="setting-row">
          <label>Launch at login<span className="hint">Keeps your OPDS feed available after reboot</span></label>
          <Toggle on={s.launch_at_login} onChange={(v) => save({ launch_at_login: v })} />
        </div>
      </div>

      <div className="settings-section">
        <h2 style={{ marginTop: 0 }}>Sources</h2>
        {["gutendex"].map((id) => (
          <div className="setting-row" key={id}>
            <label>Project Gutenberg<span className="hint">~75k public-domain titles · no account needed · Tier 1</span></label>
            <Toggle on={!!s.sources_enabled[id]} onChange={(v) => save({ sources_enabled: { [id]: v } })} />
          </div>
        ))}
        {["standardebooks"].map((id) => (
          <div className="setting-row" key={id}>
            <label>Standard Ebooks<span className="hint">~1k pristine classics · needs free Patrons Circle email (blank password) · Tier 1</span></label>
            <SEEmail
              onSave={(email) => save({ source_credentials: { standardebooks: { email } } })}
            />
          </div>
        ))}

        {!showMoreSources ? (
          <div className="setting-row">
            <label>More sources<span className="hint">Opt-in only. Bring your own account.</span></label>
            <button onClick={() => setShowMoreSources(true)}>Show</button>
          </div>
        ) : !tier2Acked && !s.tier2_acknowledged ? (
          <>
            <div className="disclaimer">{TIER2_DISCLAIMER}</div>
            <div className="setting-row">
              <label>I understand and want to enable these sources</label>
              <button
                className="primary"
                onClick={() => { setTier2Acked(true); save({ tier2_acknowledged: true }); }}
              >
                Acknowledge &amp; enable
              </button>
            </div>
          </>
        ) : (
          TIER2_IDS.map((id) => (
            <div className="setting-row" key={id}>
              <label>{TIER2_NAMES[id]}<span className="hint">Tier 2 · adapter lands in M2 — toggle reserved</span></label>
              <Toggle on={false} onChange={() => toastCtx.push("info", `${TIER2_NAMES[id]} support ships in milestone M2`)} />
            </div>
          ))
        )}
      </div>

      <div className="settings-section">
        <h2 style={{ marginTop: 0 }}>About</h2>
        <div className="setting-row">
          <label>manicule ☞<span className="hint">Free open-source software, MIT licensed. No accounts, no telemetry.</span></label>
          <a href="https://ko-fi.com/manicule" target="_blank" rel="noreferrer"><button>Ko-fi ☕</button></a>
        </div>
        <div className="setting-row">
          <label>Updates<span className="hint">Checks GitHub releases once, on click</span></label>
          <button
            onClick={async () => {
              const info = await backend.checkForUpdates();
              toastCtx.push(info.update ? "info" : "ok",
                info.update ? `New version available: ${info.latest}` : `You're up to date (${info.current})`);
            }}
          >
            Check for updates
          </button>
        </div>
      </div>
    </>
  );
}

function SEEmail(props: { onSave: (email: string) => void }) {
  const [email, setEmail] = useState("");
  return (
    <div style={{ display: "flex", gap: 8 }}>
      <input
        type="text"
        placeholder="patron email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        style={{ width: 190 }}
      />
      <button className="small primary" disabled={!email.trim()} onClick={() => props.onSave(email.trim())}>
        Save
      </button>
    </div>
  );
}
