import { describe, expect, it } from "vitest";
import { FILE_MISSING, filesLine } from "./vaultfiles";

const ok = { note: "" };
const gone = { note: FILE_MISSING };

describe("the Vault page's Archive files line", () => {
  // While Locked the core lists the open archives alone, with no notes: a
  // count read then is a count of nothing, even right after a pass found
  // one missing.
  it("claims nothing while the vault is not Unlocked", () => {
    expect(filesLine({ unlocked: false, records: [ok], check: { probed: 3, missing: 1 } })).toEqual({ text: "not known while the vault is locked", owed: false });
    expect(filesLine({ unlocked: false, records: null, check: null }).text).toBe("not known while the vault is locked");
  });

  it("says not checked yet before the registry has been read", () => {
    expect(filesLine({ unlocked: true, records: null, check: null })).toEqual({ text: "not checked yet", owed: false });
  });

  // A note is a measurement: missing is said on it, with or without a pass
  // of the page's own, and hidden records count (the page reads them).
  it("counts the missing notes, forgotten records apart", () => {
    expect(filesLine({ unlocked: true, records: [ok, gone, gone, { note: FILE_MISSING, forgottenAt: 5 }], check: null })).toEqual({ text: "2 missing", owed: true });
  });

  // Right after an unlock, before the presence pass has ended, no record
  // carries a note yet: that is not "all present".
  it("says not checked yet when no note is set and no pass has answered", () => {
    expect(filesLine({ unlocked: true, records: [ok, ok], check: null }).text).toBe("not checked yet");
  });

  it("says all present only on a pass that measured every record", () => {
    expect(filesLine({ unlocked: true, records: [ok, ok], check: { probed: 2, missing: 0 } })).toEqual({ text: "all present", owed: false });
    expect(filesLine({ unlocked: true, records: [ok, ok, ok], check: { probed: 2, missing: 0 } }).text).toBe("none missing of the 2 checked");
    expect(filesLine({ unlocked: true, records: [ok], check: { probed: 0, missing: 0 } }).text).toBe("none could be checked from this PC");
    expect(filesLine({ unlocked: true, records: [ok, { note: "", forgottenAt: 9 }], check: { probed: 1, missing: 0 } }).text).toBe("all present");
  });
});
