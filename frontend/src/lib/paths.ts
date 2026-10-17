// Path comparison the way the core does it (samePath: filepath.Clean and
// a case-insensitive compare): separators unified, runs of them and "."
// segments collapsed, ".." resolved, trailing separators dropped.
export function samePath(a: string | undefined, b: string | undefined): boolean {
  if (!a || !b) return false;
  return clean(a) === clean(b);
}

// foreignPath: a stored last_path whose syntax is not this platform's
// (FORMAT.md §18.2, APP.md §13). last_path is written in the writing
// machine's own syntax and read elsewhere as text — a drive letter on
// macOS is obviously not a path there — so the page offers "Show in
// Explorer" only for a path this platform could open, and says "last seen
// on another system at …" for the rest. Windows syntax is a drive letter
// ("D:\Archives\a.efd", or the same with forward slashes) or a UNC path
// ("\\host\share\a.efd"); anything else, a bare relative name included,
// is foreign. An empty path is not foreign: there is no path to say
// anything about.
export function foreignPath(p: string): boolean {
  if (!p) return false;
  if (/^[A-Za-z]:[\\/]/.test(p)) return false;
  if (/^(\\\\|\/\/)[^\\/]/.test(p)) return false; // UNC, in either slash
  return true;
}

export function clean(p: string): string {
  const parts = p.replace(/\//g, "\\").split("\\");
  const out: string[] = [];
  for (const [i, seg] of parts.entries()) {
    if (seg === "" && i > 0) continue; // a run of separators, or a trailing one
    if (seg === ".") continue;
    if (seg === ".." && out.length > 1) {
      out.pop();
      continue;
    }
    out.push(seg);
  }
  return out.join("\\").toLowerCase();
}

// renameSelection is what *Rename* selects as its field opens (APP.md §6,
// after Explorer): a file's stem, the part before the last dot, so typing
// replaces the name and keeps the type; the whole name when there is no
// dot or the dot is the first character (".env" has no extension, by the
// Type column's own rule, format.ts ext), and always the whole name for a
// folder, whose dot is part of its name.
export function renameSelection(name: string, isDir: boolean): [number, number] {
  const dot = isDir ? -1 : name.lastIndexOf(".");
  return [0, dot > 0 ? dot : name.length];
}

// middlePath is a path shortened in the middle to at most max characters,
// for a line that has room for one row (the create dialog's "Kept in";
// APP.md §6, ruled 2026-10-17). CSS can only cut a line's end, which takes
// the file name, the one part the user is choosing; so the cut is made
// here, on the text. The root (a drive, a UNC share, or a leading
// separator) and the file name are kept whole; the folders between are
// put back as they fit, alternately the one nearest the file and the one
// nearest the root, and those left out become one "…". The path's own
// separator is kept. It is a display only: the caller puts the full path
// in the title, and a path that fits is returned as it is.
export function middlePath(p: string, max: number): string {
  if (p.length <= max) return p;
  const root = /^(?:[\\/]{2}[^\\/]+[\\/][^\\/]+|[A-Za-z]:|[\\/]?)/.exec(p)?.[0] ?? "";
  const sep = /[\\/]/.exec(p)?.[0] ?? "\\";
  const segs = p.slice(root.length).split(/[\\/]/).filter(Boolean);
  const leaf = segs.pop();
  if (leaf === undefined || segs.length === 0) return p; // nothing between to leave out
  const prefix = root === "" || /[\\/]$/.test(root) ? root : root + sep;
  const head: string[] = [];
  const tail: string[] = [];
  const show = () => prefix + [...head, "…", ...tail, leaf].join(sep);
  let i = 0;
  let j = segs.length - 1;
  let nearFile = true;
  let open = { file: true, root: true };
  while (i <= j && (open.file || open.root)) {
    const side = (nearFile && open.file) || !open.root ? "file" : "root";
    nearFile = !nearFile;
    if (side === "file") tail.unshift(segs[j]);
    else head.push(segs[i]);
    if (show().length <= max) {
      if (side === "file") j--;
      else i++;
      continue;
    }
    if (side === "file") tail.shift();
    else head.pop();
    open = { ...open, [side]: false };
  }
  return show();
}
