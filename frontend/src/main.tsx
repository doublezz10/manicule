import React from "react";
import { createRoot } from "react-dom/client";
import App from "./App";

// Self-hosted type system — Fraunces (display), Alegreya Sans (body),
// IBM Plex Mono (data/labels). Bundled locally, so the app never phones home.
import "@fontsource/fraunces/600.css";
import "@fontsource/fraunces/600-italic.css";
import "@fontsource/fraunces/700.css";
import "@fontsource/alegreya-sans/400.css";
import "@fontsource/alegreya-sans/500.css";
import "@fontsource/alegreya-sans/700.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
