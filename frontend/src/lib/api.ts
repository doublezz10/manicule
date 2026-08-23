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
  title: string;
  authors: string[];
  cover_url?: string;
  format_name: string;
  state: "queued" | "running" | "done" | "failed" | "duplicate";
  error?: string;
  book_id?: number;
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
}

export const backend = {
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
  getSettings: () => api.GetSettings() as Promise<SettingsShape>,
  saveSettings: (req: SaveSettingsRequest) =>
    api.SaveSettings(req as never) as Promise<SettingsShape>,
  completeWizard: (libraryPath: string, launchAtLogin: boolean) =>
    api.CompleteWizard(libraryPath, launchAtLogin) as Promise<SettingsShape>,
  serverStatus: () => api.ServerStatus() as Promise<ServerStatus>,
  restartServer: () => api.RestartServer(),
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
