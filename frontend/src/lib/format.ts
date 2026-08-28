/** Human-readable byte size: 0/negative → "", 512000 → "500 KB". */
export function formatBytes(n: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${v >= 100 ? Math.round(v) : Math.round(v * 10) / 10} ${units[u]}`;
}

/** "42%" when the total is known, else the byte count so far. */
export function progressLabel(done: number, total: number): string {
  if (total > 0) return `${Math.min(100, Math.round((done / total) * 100))}%`;
  const b = formatBytes(done);
  return b ? `${b} in` : "…";
}
