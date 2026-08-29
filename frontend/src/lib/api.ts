// Typed access to the generated Wails bindings + event helpers.

import * as api from "../../bindings/github.com/doublezz10/manicule/app/manicule";
import { Events } from "@wailsio/runtime";

export type { Settings } from "../../bindings/github.com/doublezz10/manicule/internal/config/models";
export type { Book, BookFile } from "../../bindings/github.com/doublezz10/manicule/internal/library/models";

export interface ResultFormat {
  name: string;
  url: string;
  size?: number;
}

export interface SearchResult {
  source_id: string;
  source_name: string;
  id: string;
  title: string;
  authors: string[];
  language?: string;
  description?: string;
  cover_url?: string;
  year?: string;
  formats: ResultFormat[];
}

export interface SearchGroup {
  source_id: string;
  source_name: string;
  state: "ok" | "needs-auth" | "error" | "disabled" | "searching";
  message?: string;
  results: SearchResult[];
}

export interface QueueTask {
  id: string;
  source_id: string;
  result_id?: string;
  title: string;
  authors: string[];
  cover_url?: string;
  format_name: string;
  state: "queued" | "running" | "done" | "failed" | "duplicate";
  error?: string;
  book_id?: number;
  bytes_done: number;
  bytes_total: number; // 0 = unknown
  added_at: string;
}

export interface ServerStatus {
  running: boolean;
  port: number;
  url: string;
  lan_url: string;
  pin: string;
  auth_enabled: boolean;
  username: string;
}

export interface DeviceMatch {
  book_id: number;
  title: string;
  author: string;
  format: string;
  device_path?: string;
  device_size?: number;
  remote_path: string;
}

export interface DeviceFile {
  Path: string;
  Size: number;
}

export interface DeviceStatus {
  version: string;
  ip: string;
  mode: string; // "STA" (joined wifi) | "AP" (hotspot)
  rssi: number;
  freeHeap: number;
  uptime: number;
  device: string; // "X3" | "X4"
}

export interface DeviceState {
  phase: "searching" | "connected" | "offline";
  status?: DeviceStatus;
  on_device: DeviceMatch[];
  missing: DeviceMatch[];
  orphan: DeviceFile[];
  last_error?: string;
}

export interface DeviceProgress {
  book_id: number;
  title: string;
  done?: number;
  total?: number;
  error?: string;
}

export interface UpdateInfo {
  current: string;
  latest?: string;
  update: boolean;
  url: string;
}

export interface SaveSettingsRequest {
  library_path?: string;
  sources_enabled?: Record<string, boolean>;
  tier2_acknowledged?: boolean;
  source_credentials?: Record<string, Record<string, string>>;
  clean_on_import?: boolean;
  image_max_width?: number;
  delete_source_after_import?: boolean;
  watch_enabled?: boolean;
  watch_path?: string;
  server_enabled?: boolean;
  server_port?: number;
  auth_enabled?: boolean;
  launch_at_login?: boolean;
  fleet_override_source?: string;
  fleet_override_url?: string;
  filing_mode?: string;
  default_source?: string;
  auto_send_device?: boolean;
}

const wailsBackend = {
  searchAll: (query: string) => api.SearchAll(query) as Promise<SearchGroup[]>,
  download: (result: SearchResult, formatName: string) =>
    // round-trip through plain objects; the runtime serializes class instances fine
    api.Download(result as never, formatName) as Promise<QueueTask>,
  getQueue: () => api.GetQueue() as Promise<QueueTask[]>,
  cancelTask: (id: string) => api.CancelTask(id),
  clearFinishedQueue: () => api.ClearFinishedQueue(),
  listLibrary: (query: string, sort: string, page: number) =>
    api.ListLibrary(query, sort, page) as Promise<any>,
  listByGenre: (genre: string, sort: string, page: number) =>
    api.ListByGenre(genre, sort, page) as Promise<any>,
  genres: () => api.Genres() as Promise<string[]>,
  getBook: (id: number) => api.GetBook(id) as Promise<any>,
  countBooks: () => api.CountBooks() as Promise<number>,
  deleteBook: (id: number) => api.DeleteBook(id),
  revertClean: (id: number) => api.RevertClean(id),
  importFiles: () => api.ImportFiles(),
  pickFolder: (title: string) => api.PickFolder(title) as Promise<string>,
  openExternal: (url: string) => api.OpenExternal(url) as Promise<void>,
  workDescription: (title: string, authors: string[]) => api.WorkDescription(title, authors) as Promise<string>,
  bookBlurb: (id: number) => api.BookBlurb(id) as Promise<string>,
  probeFileSize: (url: string) => api.ProbeFileSize(url) as Promise<number>,
  getSettings: () => api.GetSettings() as Promise<SettingsShape>,
  saveSettings: (req: SaveSettingsRequest) =>
    api.SaveSettings(req as never) as Promise<SettingsShape>,
  completeWizard: (libraryPath: string, launchAtLogin: boolean) =>
    api.CompleteWizard(libraryPath, launchAtLogin) as Promise<SettingsShape>,
  serverStatus: () => api.ServerStatus() as Promise<ServerStatus>,
  restartServer: () => api.RestartServer(),
  deviceScan: () => api.DeviceScan() as Promise<DeviceState>,
  deviceState: () => api.DeviceStateSnapshot() as Promise<DeviceState | null>,
  syncDevice: () => api.SyncDevice() as Promise<DeviceState>,
  sendToDevice: (bookId: number) => api.SendToDevice(bookId) as Promise<DeviceState>,
  removeFromDevice: (paths: string[]) => api.RemoveFromDevice(paths) as Promise<DeviceState>,
  provisionDeviceOPDS: () => api.ProvisionDeviceOPDS() as Promise<void>,
  regeneratePin: () => api.RegeneratePin() as Promise<SettingsShape>,
  saveProvisioningFile: () => api.SaveProvisioningFile() as Promise<string>,
  provisioningPreview: () => api.GetProvisioningPreview() as Promise<string>,
  checkForUpdates: () => api.CheckForUpdates() as Promise<UpdateInfo>,
};

export interface SettingsShape {
  library_path: string;
  sources_enabled: Record<string, boolean>;
  tier2_acknowledged: boolean;
  clean_on_import: boolean;
  image_max_width: number;
  delete_source_after_import: boolean;
  source_credentials?: Record<string, Record<string, string>>;
  watch_enabled: boolean;
  watch_path: string;
  server_enabled: boolean;
  server_port: number;
  auth_enabled: boolean;
  pin: string;
  launch_at_login: boolean;
  filing_mode: string;
  default_source?: string;
  auto_send_device: boolean;
  wizard_done: boolean;
}

export function onEvent(name: string, cb: (data: any) => void): () => void {
  const off = Events.On(name, (ev: any) => {
    cb(ev?.data ?? ev?.Data ?? null);
  });
  return () => {
    try {
      (off as unknown as { off(): void }).off();
    } catch {
      /* older runtimes return void */
    }
  };
}


// ---------------------------------------------------------------------------
// Dev-only browser preview. Outside the wails webview there is no Go bridge,
// so `vite dev` in a plain browser probes once and falls back to canned data.
// Production builds never include any of this (import.meta.env.DEV is false).
// ---------------------------------------------------------------------------

function devPreview(): typeof wailsBackend | null {
  if (!import.meta.env.DEV) return null;

  let probed: Promise<boolean> | null = null;
  const bridgeUp = () => {
    if (probed === null) {
      probed = api.GetSettings().then(
        () => true,
        () => false,
      );
      probed.catch(() => {});
    }
    return probed;
  };

  const settings: SettingsShape = {
    library_path: "/Users/you/Library/manicule",
    sources_enabled: { gutendex: true, standardebooks: false, openlibrary: true, "z-library": true },
    tier2_acknowledged: true,
    clean_on_import: true,
    image_max_width: 1200,
    delete_source_after_import: false,
    source_credentials: {
      "z-library": { email: "reader@example.com", password: "••••••••", base_url: "https://singlelogin.re" },
    },
    watch_enabled: true,
    watch_path: "/Users/you/Downloads/to-read",
    server_enabled: true,
    server_port: 8787,
    auth_enabled: true,
    pin: "4821",
    launch_at_login: true,
    filing_mode: "author-title",
    default_source: "gutendex",
    auto_send_device: false,
    wizard_done: true,
  };

  const result = (id: number, title: string, author: string, year: string): SearchResult => ({
    source_id: "gutendex",
    source_name: "Project Gutenberg",
    id: String(id),
    title,
    authors: [author],
    year,
    description: `A well-loved work first published in ${year}, digitized for the public record. This preview description stands in for the catalog summary that accompanies real results.`,
    cover_url: `http://localhost:5178/cover/${id}.jpg`,
    formats: [
      { name: "EPUB", url: "#" },
      { name: "MOBI", url: "#" },
    ],
  });

  const shelf = [
    result(1, "The Picture of Dorian Gray", "Oscar Wilde", "1890"),
    result(2, "Frankenstein; Or, The Modern Prometheus", "Mary Wollstonecraft Shelley", "1818"),
    result(3, "Alice's Adventures in Wonderland", "Lewis Carroll", "1865"),
    result(4, "Pride and Prejudice", "Jane Austen", "1813"),
    result(5, "Moby Dick; Or, The Whale", "Herman Melville", "1851"),
    result(6, "A Tale of Two Cities", "Charles Dickens", "1859"),
  ];

  const mock: typeof wailsBackend = {
    ...wailsBackend,
    getSettings: () =>
      Promise.resolve(
        // ?wizard forces the first-run flow for preview/testing
        new URLSearchParams(location.search).has("wizard")
          ? { ...settings, wizard_done: false }
          : settings,
      ),
    saveSettings: (req) => {
      Object.assign(settings, req);
      return Promise.resolve(settings);
    },
    completeWizard: (p, l) => {
      settings.library_path = p;
      settings.launch_at_login = l;
      settings.wizard_done = true;
      return Promise.resolve(settings);
    },
    searchAll: () =>
      Promise.resolve([
        { source_id: "gutendex", source_name: "Project Gutenberg", state: "ok", results: shelf.slice(0, 5) },
        { source_id: "standardebooks", source_name: "Standard Ebooks", state: "needs-auth", message: "needs account", results: [] },
      ]),
    deviceScan: () =>
      Promise.resolve({
        phase: "connected",
        status: { version: "1.0.0", ip: "192.168.1.50", mode: "STA", rssi: -52, freeHeap: 118000, uptime: 640, device: "X3" },
        on_device: [
          { book_id: 1, title: shelf[0].title, author: "Oscar Wilde", format: "EPUB", device_path: "/Oscar Wilde/The Picture of Dorian Gray.epub", device_size: 412000, remote_path: "/Oscar Wilde/The Picture of Dorian Gray.epub" },
        ],
        missing: [
          { book_id: 2, title: shelf[1].title, author: "Mary Wollstonecraft Shelley", format: "EPUB", remote_path: "/Mary Wollstonecraft Shelley/Frankenstein; Or, The Modern Prometheus.epub" },
          { book_id: 4, title: shelf[3].title, author: "Jane Austen", format: "EPUB", remote_path: "/Jane Austen/Pride and Prejudice.epub" },
        ],
        orphan: [{ Path: "/To Sort/old-scan.epub", Size: 254000 }],
      } as DeviceState),
    deviceState: function () { return this.deviceScan(); },
    syncDevice: function () { return this.deviceScan(); },
    sendToDevice: function () { return this.deviceScan(); },
    removeFromDevice: function () { return this.deviceScan(); },
    provisionDeviceOPDS: () => Promise.resolve(),
    getQueue: () =>
      Promise.resolve([
        { id: "t1", source_id: "gutendex", title: shelf[0].title, authors: ["Oscar Wilde"], format_name: "EPUB", state: "done", book_id: 1, bytes_done: 412000, bytes_total: 412000, added_at: new Date().toISOString() },
        { id: "t2", source_id: "gutendex", title: "The Time Machine", authors: ["H. G. Wells"], format_name: "EPUB", state: "running", bytes_done: 190000, bytes_total: 320000, added_at: new Date().toISOString() },
        { id: "t3", source_id: "gutendex", title: "Dubliners", authors: ["James Joyce"], format_name: "EPUB", state: "failed", error: "mirror timed out", bytes_done: 0, bytes_total: 0, added_at: new Date().toISOString() },
      ]),
    listLibrary: () =>
      Promise.resolve(
        shelf.map((b, i) => ({
          book: {
            id: i + 1,
            title: b.title,
            authors: b.authors,
            year: Number(b.year),
            subjects: [["Gothic fiction", "Science fiction", "Fantasy", "Romance", "Sea stories", "Historical fiction"][i]],
            decade: `${(b.year ?? "").slice(0, 3)}0s`,
            cover_path: `/cover/${i + 1}.jpg`,
            added_at: new Date().toISOString(),
          },
          files: [
            { id: 1, format: "EPUB", path: "/x.epub", size_bytes: 412000, is_original: true },
            { id: 2, format: "EPUB", path: "/x.clean.epub", size_bytes: 388000, is_original: false },
          ],
        })),
      ),
    listByGenre: () => Promise.resolve([]),
    genres: () => Promise.resolve(["Gothic fiction", "Science fiction", "Fantasy", "Romance"]),
    serverStatus: () =>
      Promise.resolve({
        running: true,
        port: 5178, // preview: point library covers at the vite dev cover server
        url: "http://localhost:8787",
        lan_url: "http://192.168.1.24:8787",
        pin: "4821",
        auth_enabled: true,
        username: "reader",
      }),
    provisioningPreview: () =>
      Promise.resolve(JSON.stringify({ name: "manicule", url: "http://192.168.1.24:8787", username: "reader", password: "4821" }, null, 2)),
    checkForUpdates: () => Promise.resolve({ current: "0.4.0", update: false, url: "" }),
    pickFolder: () => Promise.resolve(""),
    openExternal: () => Promise.resolve(),
    workDescription: () => Promise.resolve("Preview description served by the dev mock layer — the real app backfills from Open Library's works API."),
    bookBlurb: () => Promise.resolve("Preview description served by the dev mock layer — the real app backfills from Open Library's works API and stores it."),
    probeFileSize: () => Promise.resolve(Math.round(300000 + Math.random() * 900000)),
  };

  const gated = { ...wailsBackend } as typeof wailsBackend;
  for (const key of Object.keys(mock) as (keyof typeof mock)[]) {
    if (mock[key] === wailsBackend[key]) continue;
    (gated as any)[key] = async (...args: any[]) => {
      const up = await bridgeUp();
      return up ? (wailsBackend as any)[key](...args) : (mock as any)[key](...args);
    };
  }
  return gated;
}

export const backend: typeof wailsBackend = devPreview() ?? wailsBackend;
