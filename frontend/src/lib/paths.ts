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
