// Path comparison the way the core does it (samePath: filepath.Clean and
// a case-insensitive compare): separators unified, runs of them and "."
// segments collapsed, ".." resolved, trailing separators dropped.
export function samePath(a: string | undefined, b: string | undefined): boolean {
  if (!a || !b) return false;
  return clean(a) === clean(b);
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
