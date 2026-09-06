// Presentation helpers: sizes, dates, countdowns. Nothing here decides.

export function bytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function count(n: number): string {
  return new Intl.NumberFormat("en-US").format(n);
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

// dateTime renders Unix seconds as "2026-09-05 21:14" in local time.
export function dateTime(unix: number): string {
  if (!unix) return "—";
  const d = new Date(unix * 1000);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function date(unix: number): string {
  return dateTime(unix).slice(0, 10);
}

// countdown renders the seconds until a Unix deadline as "9:41" or "1:02:05".
export function countdown(untilUnix: number, nowMs: number = Date.now()): string {
  let s = Math.max(0, Math.floor(untilUnix - nowMs / 1000));
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

// minutesLabel renders a timeout in minutes as the settings show it.
export function minutesLabel(min: number): string {
  if (min === 0) return "default";
  if (min % 60 === 0) return min === 60 ? "1 hour" : `${min / 60} hours`;
  return `${min} minutes`;
}

// leaf is the last segment of a stored name or a path.
export function leaf(p: string): string {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i < 0 ? p : p.slice(i + 1);
}

export function ext(name: string): string {
  const i = name.lastIndexOf(".");
  return i < 0 ? "" : name.slice(i + 1).toLowerCase();
}

export type PreviewKind = "image" | "video" | "audio" | "text" | "none";

const imageExt = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "avif", "ico"]);
const videoExt = new Set(["mp4", "webm", "m4v", "mov", "ogv"]);
const audioExt = new Set(["mp3", "wav", "ogg", "m4a", "flac", "aac", "opus"]);
const textExt = new Set([
  "txt", "md", "csv", "json", "xml", "yaml", "yml", "toml", "ini", "log", "go", "ts", "js",
  "py", "rs", "c", "h", "cpp", "java", "cs", "html", "css", "sh", "ps1", "bat", "sql",
]);

// previewKind says how a file can be shown in the pane; everything else is
// "Extract…" (PDFs included, APP.md §4).
export function previewKind(name: string): PreviewKind {
  const e = ext(name);
  if (imageExt.has(e)) return "image";
  if (videoExt.has(e)) return "video";
  if (audioExt.has(e)) return "audio";
  if (textExt.has(e)) return "text";
  return "none";
}

export function storageLabel(storage: string, savedPercent: number): string {
  switch (storage) {
    case "raw":
      return "raw";
    case "zstd":
      return savedPercent > 0 ? `zstd, ${savedPercent}% smaller` : "zstd";
    case "zstd+dict":
      return savedPercent > 0 ? `zstd + dictionary, ${savedPercent}% smaller` : "zstd + dictionary";
  }
  return storage || "—";
}
