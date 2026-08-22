import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// wails3 task dev injects FRONTEND_DEVSERVER_URL so the desktop webview can
// point at this server during development.
export default defineConfig({
  plugins: [react()],
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
