import React, { useState } from "react";
import { backend, type SettingsShape } from "../lib/api";

export function Wizard(props: { onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [libraryPath, setLibraryPath] = useState("");
  const [launchAtLogin, setLaunchAtLogin] = useState(true);
  const [busy, setBusy] = useState(false);

  const browse = async () => {
    const dir = await backend.pickFolder("Choose or create your library folder");
    if (dir) setLibraryPath(dir);
  };

  const finish = async () => {
    if (!libraryPath) return;
    setBusy(true);
    try {
      await backend.completeWizard(libraryPath, launchAtLogin);
      props.onDone();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="wizard">
      <div className="wizard-card">
        <h1><span style={{ color: "var(--accent)" }}>☞</span> Welcome to manicule</h1>
        <div className="wizard-steps">
          {[0, 1, 2].map((i) => (
            <div key={i} className={`wizard-step-dot ${step >= i ? "active" : ""}`} />
          ))}
        </div>

        {step === 0 && (
          <>
            <h2 style={{ marginTop: 0 }}>Where do your books live?</h2>
            <p style={{ color: "var(--text-dim)", lineHeight: 1.6 }}>
              Pick a folder for your library. manicule files books as
              <b> Author/Title</b>, keeps the original file sacred, and stores a
              cleaned e-reader copy alongside it.
            </p>
            <div className="path-row">
              <input
                type="text"
                placeholder="Choose a folder…"
                value={libraryPath}
                onChange={(e) => setLibraryPath(e.target.value)}
                readOnly
              />
              <button onClick={browse}>Browse…</button>
            </div>
            <div className="wizard-actions">
              <button className="primary" disabled={!libraryPath} onClick={() => setStep(1)}>
                Continue
              </button>
            </div>
          </>
        )}

        {step === 1 && (
          <>
            <h2 style={{ marginTop: 0 }}>Sources</h2>
            <p style={{ color: "var(--text-dim)", lineHeight: 1.6 }}>
              <b style={{ color: "var(--text)" }}>Project Gutenberg</b> (~75,000 free,
              public-domain books) is on and ready right now — no account needed.
            </p>
            <p style={{ color: "var(--text-dim)", lineHeight: 1.6 }}>
              <b style={{ color: "var(--text)" }}>Standard Ebooks</b> offers ~1,000
              beautifully produced classics. Their catalog now requires a free
              Patrons Circle email — add yours any time in Settings.
            </p>
            <div className="disclaimer">
              More sources (Anna's Archive, Z-Library, LibGen) are opt-in only and live
              behind Settings → More sources. They connect to third-party websites not
              affiliated with manicule, which bundles no content and no credentials. You
              supply your own account and are responsible for complying with applicable law.
            </div>
            <div className="wizard-actions">
              <button onClick={() => setStep(0)}>Back</button>
              <button className="primary" onClick={() => setStep(2)}>Continue</button>
            </div>
          </>
        )}

        {step === 2 && (
          <>
            <h2 style={{ marginTop: 0 }}>Almost done</h2>
            <label className="checkline">
              <input
                type="checkbox"
                checked={launchAtLogin}
                onChange={(e) => setLaunchAtLogin(e.target.checked)}
              />
              Launch manicule at login (keeps your OPDS server available)
            </label>
            <p style={{ color: "var(--text-dim)", lineHeight: 1.6 }}>
              After this we'll start the built-in OPDS server. On your reader you'll add
              one server — username <b>reader</b>, a 4-character PIN shown on the Devices
              page — then browse and pull your whole library over WiFi.
            </p>
            <div className="wizard-actions">
              <button onClick={() => setStep(1)}>Back</button>
              <button className="primary" disabled={busy || !libraryPath} onClick={finish}>
                {busy ? "Setting up…" : "Done"}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
