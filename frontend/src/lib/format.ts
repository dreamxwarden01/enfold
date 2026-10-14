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

// plural is the one place a count is written with the thing it counts: "1
// file", "12,406 files", "1 archive". A count of one is singular
// everywhere the page counts (APP.md §3, ruled 2026-09-09 — "1 files" was
// on the status line), and the number is grouped as count() groups it.
// An irregular plural is given rather than guessed at.
export function plural(n: number, one: string, many = `${one}s`): string {
  return `${count(n)} ${n === 1 ? one : many}`;
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

// nearerDeadline picks the deadline the foot counts down to (APP.md §6,
// ruled 2026-09-12): the idle one and the absolute one, both Unix
// seconds, and the absolute cap locks whatever the user is doing — so the
// nearer of the two is the truth. A zero is no deadline at all, never a
// deadline in 1970; none of them is zero.
export function nearerDeadline(a: number, b: number): number {
  if (a <= 0) return Math.max(0, b);
  if (b <= 0) return a;
  return Math.min(a, b);
}

// clockTime renders a Unix deadline as the local "16:42" the foot shows
// for the session's end — the same 24-hour clock dateTime writes.
export function clockTime(unix: number): string {
  const d = new Date(unix * 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// idleMinutes is the idle timeout the session actually runs on, from the
// registry's stored minutes: 0 is the default, and a stored value outside
// the permitted range is replaced by the default as the core's reader
// replaces it (internal/app/settings.go timeouts: 10 minutes, 1 to 30).
// Undefined is not known yet — settings read while the vault was locked
// carry no vault's values — and says no number at all.
export function idleMinutes(stored: number | undefined): number | undefined {
  if (stored === undefined) return undefined;
  return stored >= 1 && stored <= 30 ? stored : 10;
}

// LockLine is what the foot's lock chip says while the vault is unlocked.
export interface LockLine {
  text: string;
  // urgent: the last minute, counted, in the attention colour.
  urgent: boolean;
}

// lockLine is the quiet countdown (APP.md §6, ruled 2026-10-14, refining
// 2026-09-12): the nearer of the two deadlines still decides, but it
// changes form rather than ticking all day. With the idle deadline nearer
// it is the rule — *Locks after 10 min idle* — since any input resets that
// timer and a ticking number was only noise; with the absolute cap nearer
// (or equal: no input moves it) it is the clock time it ends at —
// *Session ends 16:42* — which never needs to tick. Only in the last 60
// seconds is it a count, m:ss, marked urgent. The page's clock still
// moves once a second (store.now); the text it makes here changes only
// when its form or the count does.
export function lockLine(locksAt: number, absoluteAt: number, idle: number | undefined, nowMs: number): LockLine {
  const at = nearerDeadline(locksAt, absoluteAt);
  const cap = absoluteAt > 0 && (locksAt <= 0 || absoluteAt <= locksAt);
  if (at - nowMs / 1000 <= 60) {
    return { text: `${cap ? "Session ends in" : "Locks in"} ${countdown(at, nowMs)}`, urgent: true };
  }
  if (cap) return { text: `Session ends ${clockTime(absoluteAt)}`, urgent: false };
  return { text: idle ? `Locks after ${idle} min idle` : "Locks when idle", urgent: false };
}

// minutesLabel renders a timeout in minutes as the settings show it.
export function minutesLabel(min: number): string {
  if (min === 0) return "default";
  if (min % 60 === 0) return min === 60 ? "1 hour" : `${min / 60} hours`;
  return `${min} minutes`;
}

// keyId is a recovery key's ID (FORMAT.md §18.4): the recovery slot's
// recipient_id, its first eight hex digits upper-cased and grouped
// "3F7A-9C21". The digits' checksum catches a mistyped group; the ID
// catches the wrong sheet. An id that is short or not hex has none, and
// the page shows the label alone rather than a half-derived string.
export function keyId(recipientId: string): string {
  if (recipientId.length < 8 || !/^[0-9a-fA-F]+$/.test(recipientId)) return "";
  const h = recipientId.slice(0, 8).toUpperCase();
  return `${h.slice(0, 4)}-${h.slice(4)}`;
}

// leaf is the last segment of a stored name or a path.
export function leaf(p: string): string {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i < 0 ? p : p.slice(i + 1);
}

// ext is the extension rule of APP.md §6: the part after the last dot,
// folded; none when the dot is the first character (`.env` has none) or
// there is no dot; `gz` for `a.tar.gz`. The Type column, the preview's
// kind and the row's icon all read it.
export function ext(name: string): string {
  const i = name.lastIndexOf(".");
  return i <= 0 ? "" : name.slice(i + 1).toLowerCase();
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

// fileIcon is the sprite symbol a row wears: the five kinds the preview
// knows by extension, a folder, and a plain file for everything else. The
// file list and the conflict dialog's compare list draw from the one rule
// (APP.md §6).
export function fileIcon(name: string, isDir = false): string {
  if (isDir) return "i-folder";
  switch (previewKind(name)) {
    case "image": return "i-image";
    case "video": return "i-video";
    case "audio": return "i-audio";
    case "text": return "i-doc";
  }
  return "i-file";
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

// hashText is the details modal's ciphertext hash (APP.md §13). A hash of
// all zeros is no commit yet — the record was written before the archive
// ever was — and reads "N/A" rather than sixty-four zeros the reader has
// to count. The value itself is never shortened here: the modal wraps it
// onto a second line rather than cutting it.
export function hashText(hex: string | undefined): string {
  const h = (hex ?? "").trim();
  if (h === "" || /^0+$/.test(h)) return "N/A";
  return h;
}
