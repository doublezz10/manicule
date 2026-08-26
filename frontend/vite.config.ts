import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// wails3 task dev injects FRONTEND_DEVSERVER_URL so the desktop webview can
// point at this server during development.
export default defineConfig({
  plugins: [react(), devCovers()],
  clearScreen: false,
  server: {
    port: 5178,
    strictPort: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});

// Dev-only stand-in for the OPDS server's /cover/:id.jpg route, so the
// browser preview shows library covers without the Go backend running.
const COVER_BOOKS: Record<string, { title: string; author: string; cloth: string }> = {
  "1": { title: "The Picture of Dorian Gray", author: "Oscar Wilde", cloth: "#6d2f2a" },
  "2": { title: "Frankenstein", author: "M. W. Shelley", cloth: "#2f4a4d" },
  "3": { title: "Alice's Adventures in Wonderland", author: "Lewis Carroll", cloth: "#8a6d3b" },
  "4": { title: "Pride and Prejudice", author: "Jane Austen", cloth: "#3b4a6b" },
  "5": { title: "Moby Dick", author: "Herman Melville", cloth: "#274236" },
  "6": { title: "A Tale of Two Cities", author: "Charles Dickens", cloth: "#5a3a5e" },
};

function devCovers(): Plugin {
  return {
    name: "dev-covers",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/cover", (req, res, next) => {
        const id = decodeURIComponent((req.url ?? "/").replace(/^\/+/, "")).replace(/\.jpg$/, "");
        const book = COVER_BOOKS[id] ?? { title: "manicule", author: "preview", cloth: "#4a3b2c" };
        const words = book.title.split(" ");
        const lines: string[] = [];
        let cur = "";
        for (const w of words) {
          if ((cur + " " + w).trim().length > 13) { lines.push(cur.trim()); cur = w; }
          else cur += " " + w;
        }
        if (cur.trim()) lines.push(cur.trim());
        if (lines.length > 3) {
          lines.length = 3;
          lines[2] = lines[2].replace(/\s+\S*$/, "") + "…";
        }
        const firstBaseline = 148 - ((lines.length - 1) * 38) / 2;
        const titleSvg = lines.map((l, i) =>
          `<text x="100" y="${firstBaseline + i * 38}" class="t">${escapeXml(l)}</text>`).join("\n");
        const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="600" viewBox="0 0 200 300">
  <style>
    .t { font: 600 25px Georgia, serif; fill: #efe6d6; text-anchor: middle; }
    .a { font: italic 14px Georgia, serif; fill: #efe6d6; opacity: 0.75; text-anchor: middle; }
  </style>
  <rect width="200" height="300" fill="${book.cloth}"/>
  <rect x="0" y="0" width="200" height="300" fill="none" stroke="#000" stroke-opacity="0.25" stroke-width="6"/>
  <rect x="12" y="12" width="176" height="276" fill="none" stroke="#efe6d6" stroke-opacity="0.5" stroke-width="1.5"/>
  <rect x="16" y="16" width="168" height="268" fill="none" stroke="#efe6d6" stroke-opacity="0.25" stroke-width="0.75"/>
  ${titleSvg}
  <text x="100" y="252" class="a">${escapeXml(book.author)}</text>
  <g transform="translate(84,264) scale(0.5)" fill="#efe6d6" fill-opacity="0.55">
    <rect x="3" y="14" width="13" height="26" rx="4"/>
    <ellipse cx="26" cy="10" rx="6.5" ry="5"/>
    <path d="M27 9 H33 C40 9 43 12 44 17 L56 17 A4.75 4.75 0 0 1 56 26.5 L45 26.5 C44 30 43.5 33 42.5 36 Q41.5 41 36 41 L27 41 Q19 41 19 34 L19 16 Q19 9 27 9 Z"/>
  </g>
</svg>`;
        res.setHeader("Content-Type", "image/svg+xml");
        res.setHeader("Cache-Control", "no-store");
        res.end(svg);
      });
    },
  };
}

function escapeXml(s: string): string {
  return s.replace(/[<>&'"]/g, (c) =>
    ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", "'": "&apos;", '"': "&quot;" })[c] ?? c,
  );
}
