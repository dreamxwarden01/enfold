import { describe, expect, it } from "vitest";
import { backupKind, backupLine } from "./backup";

// The backups card's status (direction 9, ruled 2026-10-18; APP.md §13).
const at = 1_760_000_000;
const b = (over: Partial<{ at: number; archives: number; missing: number; rekeyed: number; vaultRekeyed: boolean; keysReplaced: boolean }> = {}) => ({
  at,
  archives: 3,
  missing: 0,
  rekeyed: 0,
  vaultRekeyed: false,
  keysReplaced: false,
  ...over,
});

describe("which status the card opens with", () => {
  it("is none with no archives and no backup", () => {
    expect(backupKind(b({ at: 0, archives: 0 }), true)).toBe("none");
    expect(backupKind(null, true)).toBe("none");
  });
  it("is never with archives and no backup", () => {
    expect(backupKind(b({ at: 0, missing: 3 }), true)).toBe("never");
  });
  it("is behind when archives were created or re-keyed since", () => {
    expect(backupKind(b({ missing: 2 }), true)).toBe("behind");
    expect(backupKind(b({ rekeyed: 1 }), true)).toBe("behind");
    expect(backupKind(b({ missing: 2, rekeyed: 1 }), true)).toBe("behind");
  });
  it("is current when the backup holds every archive key", () => {
    expect(backupKind(b(), true)).toBe("current");
    // a backup taken of an empty vault holds everything there is
    expect(backupKind(b({ archives: 0 }), true)).toBe("current");
  });
  it("reads the date alone while the counts cannot be believed", () => {
    // Locked or Tampered: the core answers the counts as 0.
    expect(backupKind(b({ archives: 0 }), false)).toBe("current");
    expect(backupKind(b({ at: 0, archives: 0 }), false)).toBe("never");
    expect(backupKind(null, false)).toBe("never");
  });
});

describe("what the status says", () => {
  const when = "22 days ago";
  it("says nothing at all for none", () => {
    const l = backupLine(b({ at: 0, archives: 0 }), true, when);
    expect(l).toMatchObject({ kind: "none", tone: null, head: "", rest: "", caveat: "", suggestExport: false });
  });
  it("says the vault alone holds them, in attention, with the export suggested", () => {
    const l = backupLine(b({ at: 0, archives: 3, missing: 3 }), true, when);
    expect(l.tone).toBe("attention");
    expect(`${l.head} ${l.rest}`).toBe("No backup is recorded here. All 3 archives are only in this vault file.");
    expect(backupLine(b({ at: 0, archives: 1, missing: 1 }), true, when).rest).toBe("Your 1 archive is only in this vault file.");
    // no backup is named, so no caveat about one
    expect(l.caveat).toBe("");
    expect(l.suggestExport).toBe(true);
  });
  it("counts what the backup lacks, created and re-keyed, each in its number", () => {
    const say = (m: number, k: number) => {
      const l = backupLine(b({ missing: m, rekeyed: k }), true, when);
      return `${l.head} ${l.rest}`;
    };
    expect(say(2, 0)).toBe("Backed up 22 days ago. 2 archives created since are not in it.");
    expect(say(1, 0)).toBe("Backed up 22 days ago. 1 archive created since is not in it.");
    expect(say(0, 1)).toBe("Backed up 22 days ago. 1 archive re-keyed since is not in it.");
    expect(say(0, 2)).toBe("Backed up 22 days ago. 2 archives re-keyed since are not in it.");
    expect(say(2, 1)).toBe("Backed up 22 days ago. 2 archives created since and 1 re-keyed since are not in it.");
    expect(say(1, 1)).toBe("Backed up 22 days ago. 1 archive created since and 1 re-keyed since are not in it.");
    const l = backupLine(b({ missing: 2 }), true, when);
    expect(l.tone).toBe("attention");
    expect(l.suggestExport).toBe(true);
  });
  it("says a current backup holds every key, plainly, with the export standard", () => {
    const l = backupLine(b(), true, when);
    expect(l.tone).toBe("plain");
    expect(`${l.head} ${l.rest}`).toBe("Backed up 22 days ago. It holds every archive key.");
    expect(l.caveat).toBe("Recorded here, not proof the file is still there.");
    expect(l.suggestExport).toBe(false);
    expect(l.checked).toBe(true);
  });
  it("claims nothing about contents while Locked or Tampered", () => {
    const dated = backupLine(b({ archives: 0 }), false, when);
    expect(dated).toMatchObject({ tone: "plain", head: "Backed up 22 days ago.", rest: "", checked: false });
    expect(dated.caveat).toBe("Recorded here, not proof the file is still there.");
    expect(dated.suggestExport).toBe(false);
    // Not counted: the quiet line, not a bar, and the export not suggested,
    // whatever the stamp says.
    const never = backupLine(null, false, when);
    expect(never).toMatchObject({ kind: "never", tone: "plain", head: "No backup is recorded here.", rest: "", caveat: "", suggestExport: false, checked: false });
    expect(backupLine(b({ at: 0, archives: 0 }), false, when).suggestExport).toBe(false);
  });
  it("keeps the house voice", () => {
    for (const [m, k] of [[0, 0], [2, 0], [0, 1], [2, 1]]) {
      const l = backupLine(b({ missing: m, rekeyed: k }), true, when);
      expect(`${l.head} ${l.rest} ${l.caveat}`).not.toMatch(/n't|'re|'ll|'ve|it's/i);
    }
  });
});

// A backup behind for a reason no count says (Vault.BackupStatus, ruled
// 2026-10-27): made before the vault was re-keyed, or opening only with
// recovery keys a replacement removed. Either stands for the counts and
// makes Export backup… the accent.
describe("a backup the vault's re-key or a replacement left behind", () => {
  const when = "22 days ago";
  it("is behind when the vault was re-keyed after it, with no count to say", () => {
    expect(backupKind(b({ vaultRekeyed: true }), true)).toBe("behind");
    const l = backupLine(b({ vaultRekeyed: true }), true, when);
    expect(l).toMatchObject({ kind: "behind", tone: "attention", suggestExport: true, checked: false });
    expect(`${l.head} ${l.rest}`).toBe("Backed up 22 days ago. It was made before the vault was re-keyed.");
  });
  it("is behind when it opens only with replaced recovery keys, which wins over the re-key", () => {
    expect(backupKind(b({ keysReplaced: true }), true)).toBe("behind");
    expect(backupLine(b({ keysReplaced: true }), true, when).rest).toBe("It opens only with recovery keys this vault no longer has.");
    // A replacement also re-keyed the vault; the more pressing fact is said.
    const l = backupLine(b({ keysReplaced: true, vaultRekeyed: true }), true, when);
    expect(l.rest).toBe("It opens only with recovery keys this vault no longer has.");
    expect(l.suggestExport).toBe(true);
  });
  it("says the same in place of the counts", () => {
    expect(backupLine(b({ missing: 2, rekeyed: 1, vaultRekeyed: true }), true, when).rest).toBe("It was made before the vault was re-keyed.");
    expect(backupLine(b({ missing: 2, keysReplaced: true }), true, when).rest).toBe("It opens only with recovery keys this vault no longer has.");
  });
  it("claims neither while the facts cannot be believed", () => {
    // Locked or Tampered: the core answers both false, and the card says the date alone.
    expect(backupKind(b({ archives: 0, vaultRekeyed: true }), false)).toBe("current");
    expect(backupLine(b({ archives: 0, keysReplaced: true }), false, when)).toMatchObject({ tone: "plain", rest: "", suggestExport: false });
  });
  it("keeps the house voice", () => {
    for (const over of [{ vaultRekeyed: true }, { keysReplaced: true }]) {
      const l = backupLine(b(over), true, when);
      expect(`${l.head} ${l.rest}`).not.toMatch(/n't|'re|'ll|'ve|it's/i);
    }
  });
});
