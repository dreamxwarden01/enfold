// The recovery key's name (APP.md §3 Keys, §6).
//
// The saved file and the print are named "Enfold Keystore Recovery Key
// <ID>" — BitLocker's own shape, with the key's ID (FORMAT.md §18.4) and
// never the vault's name, which says nothing about which sheet this is.
// The same string heads the text file the core writes, heads the sheet,
// and is document.title while the print runs, which is what Print to PDF
// offers as the file name.
//
// Nothing here reads a print's outcome. The spooler watch that once did —
// Shell.PrintBegin / PrintEnd — was retired on 2026-09-09: the WebView's
// own print dialog offers *Save as PDF*, which never reaches the spooler,
// so the watch missed the likeliest path. Pressing *Print…* counts as
// done, and so does *I have written it down*, with no second confirmation
// for either.

export function recoveryKeyTitle(id: string | undefined): string {
  const key = (id ?? "").trim();
  return key ? `Enfold Keystore Recovery Key ${key}` : "Enfold Keystore Recovery Key";
}

export function recoveryKeyFileName(id: string | undefined): string {
  return `${recoveryKeyTitle(id)}.txt`;
}
