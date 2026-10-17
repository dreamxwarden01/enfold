import { describe, expect, it } from "vitest";
import { CeremonyStep, Code } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";
import * as strings from "./strings";
import * as validate from "./validate";
import { ceremonyCopy, chipCopy, closeCopy, codeCopy, codeText, commandCopy, compressedText, dialogCopy, extensionNames, lockCopy, purgedText, reclaimedText, retriesText, sinceText, stepCopy, technicalCopy, typeLabel, unknownCodeParts, vaultCopy, warningCopy } from "./strings";

// ---- One word per thing (APP.md §7, ruled 2026-10-17) -------------------
//
// The format's words never reach a main surface: they stand in
// technicalCopy (Technical details, "Why?", "How this works") and nowhere
// else. Each banned term carries the word the screen uses instead.
const banned: [RegExp, string][] = [
  [/\bkeystores?\b/i, "the file is the vault"],
  [/\bKeys page\b/i, "the page is Vault"],
  [/\bslots?\b/i, "an unlock method"],
  [/\bways? (in|into|to unlock)\b/i, "an unlock method"],
  [/\bentangle(d|ment)?\b/i, "Password with every YubiKey"],
  [/\bsuperblocks?\b/i, "Technical details only"],
  [/\bregistry\b/i, "Technical details only"],
  [/\bKIDs?\b/, "Technical details only"],
  [/\bVMKs?\b/, "Technical details only"],
  [/\bsession keys?\b/i, "Opening the vault…"],
  [/\bgenerations?\b/i, "Technical details only"],
  [/\bseq\b/, "Technical details only"],
  [/\bzstd\b/i, "Compressed"],
  [/\benvelopes?\b/i, "Technical details only"],
  [/\brotat(e|es|ed|ing|ion)\b/i, "Re-key"],
  [/\benrol(l|ls|led|ling|ment)?\b/i, "add an unlock method"],
  [/\bImport records\b/i, "Import archives from a backup…"],
  [/\bImport a (copy|backup)\b/i, "Replace this vault with a copy…"],
  [/\bForget key\b/i, "Forget archive…"],
  [/\bCheck files\b/i, "Find missing files"],
  [/\bCheck (a|that a) backup\b/i, "Inspect a backup… / Test that a backup opens…"],
];

// A word that must stay where it is, with the line it stays in and why.
const exceptions: { term: RegExp; line: string; why: string }[] = [
  {
    term: /\bslots?\b/i,
    line: codeCopy[Code.CodeTokenOccupied],
    why: "a PIV slot on the YubiKey, the key's own word as Yubico's tools show it, not the vault's",
  },
  {
    term: /\bslots?\b/i,
    line: codeCopy[Code.CodeTokenFull],
    why: "a PIV slot on the YubiKey, the key's own word as Yubico's tools show it, not the vault's",
  },
];

// Arguments a copy function is tried with; one that throws on a shape is
// tried with the next.
const samples: unknown[][] = [
  [],
  ["Photos 2024"],
  [3],
  [1],
  [3, "Photos 2024"],
  ["Photos 2024", true],
  [["a.efd", "b.efd"]],
  [{ replace: 1, both: 1, skip: 1 }],
  [{ retries: 3, retriesKnown: true, verified: false }],
  [Code.CodeVaultTampered, Code.CodeTamperedGeneration],
  ["raw", 0],
  ["deflate", 71],
  [1_760_000_000, 1_760_100_000_000],
];

function lines(path: string, v: unknown, out: [string, string][], depth = 0): void {
  if (depth > 4 || v === null || v === undefined) return;
  if (typeof v === "string") {
    out.push([path, v]);
  } else if (typeof v === "function") {
    for (const args of samples) {
      try {
        lines(`${path}(${args.length ? "…" : ""})`, (v as (...a: unknown[]) => unknown)(...args), out, depth + 1);
      } catch {
        // a shape this function does not take
      }
    }
  } else if (Array.isArray(v)) {
    v.forEach((x, i) => lines(`${path}[${i}]`, x, out, depth + 1));
  } else if (typeof v === "object") {
    for (const [k, x] of Object.entries(v as Record<string, unknown>)) lines(`${path}.${k}`, x, out, depth + 1);
  }
}

function everyLine(): [string, string][] {
  const out: [string, string][] = [];
  for (const [name, v] of Object.entries(strings)) {
    if (name === "technicalCopy") continue; // the second register
    lines(name, v, out);
  }
  for (const [name, v] of Object.entries(validate)) lines(`validate.${name}`, v, out);
  for (const code of Object.values(Code)) if (code) out.push([`codeText(${code})`, codeText(code)]);
  for (const reason of [undefined, Code.CodeTamperedHash, Code.CodeTamperedGeneration]) {
    for (const w of [Code.CodeVaultTampered, Code.CodeVaultStale, Code.CodeTokenReset]) out.push([`warningCopy(${w}, ${reason})`, warningCopy(w, reason)]);
  }
  for (const step of Object.values(CeremonyStep)) if (step) out.push([`stepText(${step})`, strings.stepText(step).title + " " + strings.stepText(step).body]);
  return out;
}

describe("one word per thing", () => {
  it("keeps the format's words off every main surface", () => {
    const hits: string[] = [];
    for (const [path, text] of everyLine()) {
      for (const [term, instead] of banned) {
        if (!term.test(text)) continue;
        if (exceptions.some((e) => e.term.source === term.source && e.line === text)) continue;
        hits.push(`${path}: ${term} in "${text}" (say: ${instead})`);
      }
    }
    expect(hits).toEqual([]);
  });
  it("walks enough of the table to mean something", () => {
    expect(everyLine().length).toBeGreaterThan(400);
  });
  it("lets the technical-details table, and only it, keep them", () => {
    const tech: [string, string][] = [];
    lines("technicalCopy", technicalCopy, tech);
    expect(tech.some(([, t]) => banned.some(([term]) => term.test(t)))).toBe(true);
  });
  it("gives every exception a reason and a line that still needs it", () => {
    for (const e of exceptions) {
      expect(e.why).toBeTruthy();
      expect(e.term.test(e.line), e.line).toBe(true);
    }
  });
});

// The verb table and the dialog template (strings.ts head).
describe("the verb table", () => {
  it("ends a command with an ellipsis exactly when a dialog or a picker follows", () => {
    const opensNothing = ["open", "close", "hide", "unhide", "restore", "verify", "showInExplorer", "findMissing", "reopen", "goThere", "lockNow", "unlock"];
    for (const [k, v] of Object.entries(commandCopy)) {
      expect(v.endsWith("…"), k).toBe(!opensNothing.includes(k));
    }
  });
  it("has no ellipsis on a dialog's primary, save where a picker follows", () => {
    const primaries = Object.entries(dialogCopy).flatMap(([k, v]) =>
      typeof v === "object" && v && "primary" in v ? [[k, (v as { primary: string }).primary] as const] : [],
    );
    expect(primaries.length).toBeGreaterThan(8);
    for (const [k, p] of primaries) expect(p.endsWith("…"), k).toBe(k === "newArchive");
  });
  it("asks a consequence dialog's title as a question, and keeps its body to two sentences", () => {
    const d = dialogCopy;
    const asks = [d.deleteArchive.title("Photos 2024"), d.forgetArchive.title("Photos 2024"), d.compact.title("Photos 2024"), d.rekeyArchive.title("Photos 2024"), d.rekeyVault.title, d.removeUnlockMethod.title("Desk key"), d.passwordOn.title, d.passwordChange.title, d.passwordOff.title];
    for (const t of asks) expect(t).toMatch(/\?$/);
    const bodies = [d.deleteArchive.body("D:\\Archives\\photos-2024.efd"), d.forgetArchive.body("2026-11-12"), d.compact.body, d.rekeyArchive.body, d.rekeyVault.body, d.removeUnlockMethod.body("Desk key"), d.passwordOn.body, d.passwordChange.body, d.passwordOff.body];
    for (const b of bodies) expect(b.split(/(?<=[.?!])\s+(?=[A-Z])/).length, b).toBeLessThanOrEqual(2);
  });
  it("puts every chip in sentence case", () => {
    for (const [k, v] of Object.entries(chipCopy)) {
      const t = typeof v === "function" ? v("2026-10-13") : v;
      expect(t[0], k).toBe(t[0].toUpperCase());
    }
  });
});

// The phase 1 visual checks (ruled 2026-10-17): one name and one order
// for card 2, the YubiKey never a bare "key", the purge's consequence
// first, and one sentence for the backup that may still open an archive.
describe("the lock screen's and the dialogs' words", () => {
  it("names card 2 in its fields' order, the PIN first", () => {
    expect(ceremonyCopy.pinAndPassword).toBe("PIN and password");
    for (const line of [lockCopy.tokenPasswordHint, vaultCopy.passwordOn]) {
      expect(line.indexOf("PIN"), line).toBeGreaterThanOrEqual(0);
      expect(line.indexOf("PIN"), line).toBeLessThan(line.indexOf("password"));
    }
  });
  it("never calls the YubiKey a bare key", () => {
    const bare = /\b(the|this) key\b/i;
    for (const code of Object.values(Code)) {
      if (code && code.startsWith("token.")) expect(codeText(code), code).not.toMatch(bare);
    }
    expect(codeText(Code.CodePasswordDeadline)).not.toMatch(/\bthe key\b/i);
    expect(lockCopy.matched("Personal vault")).toBe("This YubiKey unlocks Personal vault.");
  });
  it("says what the purge means, the consequence first and no key", () => {
    expect(purgedText(["Old laptop backup"])).toBe('"Old laptop backup" was forgotten more than 30 days ago, so it can no longer be opened.');
    expect(purgedText(["A", "B"])).toMatch(/^2 forgotten archives can no longer be opened: A, B\./);
    for (const t of [purgedText(["A"]), purgedText(["A", "B"])]) expect(t).not.toMatch(/\bkeys?\b/i);
  });
  it("keeps the backup's safety net to one sentence", () => {
    const line = dialogCopy.lastBackup("2026-09-25");
    expect(line).toBe("The last backup of this vault, 2026-09-25, can open this archive if you still have it.");
    expect(line.split(/(?<=[.;?!])\s+/).length).toBe(1);
  });
  it("ends a lock-screen command that opens a dialog with an ellipsis", () => {
    expect(lockCopy.createInstead.endsWith("…")).toBe(true);
  });
  it("asks the close question in the order its buttons stand", () => {
    expect(closeCopy.body.indexOf("Quit")).toBe(0);
    expect(closeCopy.body.indexOf("tray")).toBeGreaterThan(0);
  });
  it("says a backup check's outcome one way, on the lock screen and in the panel", () => {
    expect(ceremonyCopy.verified(1)).toBe("The backup opens. 1 archive inside.");
    expect(ceremonyCopy.verified(3)).toBe("The backup opens. 3 archives inside.");
  });
  it("agrees in number when archives keep a vault from being created", () => {
    expect(lockCopy.stillOpen(1)).toBe("1 archive is still open. Close it first: its saves would land in the wrong vault.");
    expect(lockCopy.stillOpen(2)).toMatch(/^2 archives are still open\. Close them first: their saves/);
  });
  it("names the YubiKey, never a bare key, in the strip's and the panel's lines", () => {
    const said = [lockCopy.pendingTouch, ceremonyCopy.touchCount(2), ceremonyCopy.swapBoth("Desk", ""), stepCopy[CeremonyStep.StepBlocked].title];
    for (const s of said) expect(s).not.toMatch(/\b(the|this) key\b/i);
    expect(ceremonyCopy.swapBoth("Desk", "")).toBe("Remove Desk, then insert the YubiKey to add.");
    expect(stepCopy[CeremonyStep.StepBlocked].title).toBe("The PIN is blocked. Use your recovery key.");
  });
});

describe("the second register's plain words", () => {
  it("says how much smaller, never the method", () => {
    expect(compressedText("zstd+dict", 71)).toBe("71% smaller");
    expect(compressedText("zstd", 0)).toBe("Yes");
    expect(compressedText("raw", 0)).toBe("No, stored as is");
  });
});

// Relative dates only where recency is the point (strings.ts head).
describe("sinceText", () => {
  const at = (y: number, m: number, d: number, h = 3, min = 24) => new Date(y, m - 1, d, h, min).getTime() / 1000;
  const now = new Date(2026, 9, 13, 12, 0).getTime();
  it("keeps the clock time today and yesterday", () => {
    expect(sinceText(at(2026, 10, 13), now)).toBe("today, 03:24");
    expect(sinceText(at(2026, 10, 12), now)).toBe("yesterday, 03:24");
  });
  it("counts days, then falls back to the ISO form", () => {
    expect(sinceText(at(2026, 9, 21), now)).toBe("22 days ago");
    expect(sinceText(at(2026, 6, 1), now)).toBe("2026-06-01 03:24");
  });
  it("never says a time ahead of the clock is past", () => {
    expect(sinceText(at(2026, 10, 14), now)).toBe("2026-10-14 03:24");
    expect(sinceText(0, now)).toBe("—");
  });
});

describe("copy table", () => {
  it("covers every ceremony step", () => {
    for (const step of Object.values(CeremonyStep)) {
      if (!step) continue; // the generator's zero value
      expect(stepCopy[step as keyof typeof stepCopy]?.title, step).toBeTruthy();
    }
  });
  it("covers every error code", () => {
    for (const code of Object.values(Code)) {
      if (!code) continue;
      expect(codeCopy[code as keyof typeof codeCopy], code).toBeTruthy();
      expect(unknownCodeParts(codeText(code)), code).toBeNull();
    }
  });
  it("names an unknown code rather than inventing, and says where the log is", () => {
    expect(codeText("something.new")).toBe("Something went wrong (something.new). The log in the data folder has the details.");
  });
  it("splits the fallback around its code, wherever it stands in the line", () => {
    expect(unknownCodeParts(codeText("something.new"))).toEqual(["Something went wrong (", "something.new", "). The log in the data folder has the details."]);
    expect(unknownCodeParts(`Adding 3 files: ${codeText("x.y")}`)).toEqual(["Adding 3 files: Something went wrong (", "x.y", "). The log in the data folder has the details."]);
    expect(unknownCodeParts(codeText(Code.CodeArchiveDirty))).toBeNull();
  });
});

describe("retries", () => {
  it("never says 0 attempts", () => {
    expect(retriesText({ retries: 0, retriesKnown: true, verified: false })).not.toMatch(/0/);
    expect(retriesText({ retries: 0, retriesKnown: false, verified: false })).not.toMatch(/0/);
    expect(retriesText({ retries: 0, retriesKnown: true, verified: true })).not.toMatch(/0/);
  });
  it("shows the count before the PIN is asked", () => {
    expect(retriesText({ retries: 3, retriesKnown: true, verified: false })).toBe("3 attempts left");
    expect(retriesText({ retries: 1, retriesKnown: true, verified: false })).toMatch(/last/);
  });
  it("says unknown when the card is verified", () => {
    expect(retriesText({ retries: 3, retriesKnown: true, verified: true })).toMatch(/unknown/);
  });
});

describe("what a finished reclaim says", () => {
  it("names the bytes the file system got back", () => {
    expect(reclaimedText(1.2 * 1024 * 1024 * 1024)).toBe("Reclaimed 1.2 GB");
  });
});

// The Type column (APP.md §6): a folder, the table's name for an
// extension, "<EXT> file" for one without a name, *File* for none.
describe("the type column", () => {
  it("names a folder, whatever its name says", () => {
    expect(typeLabel("photos.zip", true)).toBe("Folder");
  });
  it("names the common extensions and says the extension for the rest", () => {
    expect(typeLabel("IMG_7201.jpg", false)).toBe("JPEG image");
    expect(typeLabel("DJI_0042.MP4", false)).toBe("MP4 video");
    expect(typeLabel("notes.txt", false)).toBe("Text document");
    expect(typeLabel("a.tar.gz", false)).toBe("Gzip archive");
    expect(typeLabel("model.blend", false)).toBe("BLEND file");
  });
  it("is File for a name with no extension", () => {
    expect(typeLabel("README", false)).toBe("File");
    expect(typeLabel(".env", false)).toBe("File");
  });
  it("keeps every key folded, since the extension is read folded", () => {
    for (const k of Object.keys(extensionNames)) expect(k).toBe(k.toLowerCase());
  });
});
