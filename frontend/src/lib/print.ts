// The recovery key's name and the print's outcome (APP.md §3 Keys, §6).
//
// The saved file and the print are named "Enfold Keystore Recovery Key
// <ID>" — BitLocker's own shape, with the key's ID (FORMAT.md §18.4) and
// never the vault's name, which says nothing about which sheet this is.
// The same string heads the text file the core writes, heads the sheet,
// and is document.title while the print runs, which is what Print to PDF
// offers as the file name.

export function recoveryKeyTitle(id: string | undefined): string {
  const key = (id ?? "").trim();
  return key ? `Enfold Keystore Recovery Key ${key}` : "Enfold Keystore Recovery Key";
}

export function recoveryKeyFileName(id: string | undefined): string {
  return `${recoveryKeyTitle(id)}.txt`;
}

// What the page does once the print dialog has been dismissed. The shell
// watches the print spooler around window.print() (APP.md §3 Shell):
// PrintEnd answers whether a new job appeared, and errors when the
// spooler cannot be read at all.
//
//   done      a job was submitted — the print counts, with no second
//             question, as a save the core acknowledged does
//   cancelled the spooler saw nothing: said in place, with *Print…* still
//             offered
//   ask       the spooler could not be read: the second confirmation,
//             "it printed, and all 48 digits are legible?"
export type PrintOutcome = "done" | "cancelled" | "ask";

export function printOutcome(submitted: boolean): PrintOutcome {
  return submitted ? "done" : "cancelled";
}

// askSpooler runs PrintEnd and maps its three answers. A refusal of any
// kind is the unreadable spooler: the page asks rather than claiming
// either way.
export async function askSpooler(end: () => Promise<boolean>): Promise<PrintOutcome> {
  try {
    return printOutcome(await end());
  } catch {
    return "ask";
  }
}
