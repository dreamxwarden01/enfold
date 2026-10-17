import { describe, expect, it } from "vitest";
import { asksPassword, begin, fixedLine, keepsPassword, nextLine, problems, refuses, shown } from "./firstwayin";
import type { FirstForm } from "./firstwayin";
import { firstMethodCopy } from "./strings";
import { CONFIRM_SECRET, PASSWORD_RULE, REQUIRED } from "./validate";

// The first unlock method's cards (APP.md §6, ruled 2026-10-17): which card
// is open and what it shows, what Continue refuses, what the line under
// the cards says, and what the ceremony is started with.

const form = (over: Partial<FirstForm> = {}): FirstForm => ({ method: "", entangle: false, password: "", again: "", ...over });

describe("the cards", () => {
  it("show nothing but their lines until one is chosen", () => {
    expect(shown("", false)).toEqual({ name: false, password: false });
    expect(shown("", true)).toEqual({ name: false, password: false });
  });

  it("open the YubiKey on its name and tick, and the password under the tick only when ticked", () => {
    expect(shown("token", false)).toEqual({ name: true, password: false });
    expect(shown("token", true)).toEqual({ name: true, password: true });
  });

  it("open the Password card on the two fields, whatever the YubiKey's tick says", () => {
    expect(shown("password", false)).toEqual({ name: false, password: true });
    expect(shown("password", true)).toEqual({ name: false, password: true });
    expect(asksPassword("password", true)).toBe(true);
    expect(asksPassword("token", false)).toBe(false);
    expect(asksPassword("", true)).toBe(false);
  });

  it("keep the typed password only while the same card keeps asking for it", () => {
    expect(keepsPassword({ method: "password", entangle: false }, { method: "password", entangle: true })).toBe(true);
    expect(keepsPassword({ method: "token", entangle: true }, { method: "token", entangle: true })).toBe(true);
    // The tick taken off, the card changed, either way: the fields clear.
    expect(keepsPassword({ method: "token", entangle: true }, { method: "token", entangle: false })).toBe(false);
    expect(keepsPassword({ method: "token", entangle: true }, { method: "password", entangle: true })).toBe(false);
    expect(keepsPassword({ method: "password", entangle: false }, { method: "token", entangle: true })).toBe(false);
  });
});

describe("what Continue refuses", () => {
  it("refuses with no card chosen, and says so on the group", () => {
    expect(refuses(form())).toBe(true);
    expect(problems(form()).method).toBe(firstMethodCopy.chooseOne);
    expect(begin(form(), "")).toBeNull();
  });

  it("takes a YubiKey with nothing typed, its name optional", () => {
    expect(refuses(form({ method: "token" }))).toBe(false);
    expect(begin(form({ method: "token" }), "")).toEqual({ kind: "token", label: "", entangle: false, password: "" });
    expect(begin(form({ method: "token" }), "Desk")).toEqual({ kind: "token", label: "Desk", entangle: false, password: "" });
  });

  it("marks both fields of an empty password, and only what is wrong", () => {
    expect(problems(form({ method: "password" }))).toEqual({ method: "", password: REQUIRED, again: REQUIRED });
    expect(problems(form({ method: "password", password: "short", again: "short" }))).toEqual({ method: "", password: PASSWORD_RULE, again: "" });
    expect(problems(form({ method: "password", password: "long enough", again: "long enougH" }))).toEqual({ method: "", password: "", again: CONFIRM_SECRET });
    expect(refuses(form({ method: "password", password: "long enough", again: "long enough" }))).toBe(false);
  });

  it("counts the minimum in characters, not bytes", () => {
    expect(problems(form({ method: "password", password: "密码密码密码密码", again: "密码密码密码密码" })).password).toBe("");
    expect(problems(form({ method: "password", password: "密码密码密码密", again: "密码密码密码密" })).password).toBe(PASSWORD_RULE);
  });

  it("asks the YubiKey's password only when ticked", () => {
    expect(refuses(form({ method: "token", entangle: true }))).toBe(true);
    expect(refuses(form({ method: "token", entangle: true, password: "long enough", again: "long enough" }))).toBe(false);
    // Unticked, what the hidden fields hold is nothing to judge.
    expect(problems(form({ method: "token", password: "x", again: "y" }))).toEqual({ method: "", password: "", again: "" });
  });
});

describe("what the ceremony is started with", () => {
  it("names a password unlock method Password, whatever was typed for a YubiKey", () => {
    expect(begin(form({ method: "password", password: "long enough", again: "long enough" }), "Desk")).toEqual({ kind: "password", label: "Password", entangle: false, password: "long enough" });
  });

  it("carries the tick for a YubiKey alone, with the password held beside it", () => {
    expect(begin(form({ method: "token", entangle: true, password: "long enough", again: "long enough" }), "")).toEqual({ kind: "token", label: "", entangle: true, password: "long enough" });
    expect(begin(form({ method: "password", entangle: true, password: "long enough", again: "long enough" }), "")?.entangle).toBe(false);
  });

  it("holds no password for a YubiKey without the tick, whatever the fields kept", () => {
    expect(begin(form({ method: "token", password: "long enough", again: "long enough" }), "")?.password).toBe("");
  });
});

describe("the line under the cards", () => {
  it("is empty until a card is chosen", () => {
    expect(nextLine("")).toBe("");
    expect(nextLine("", "adopt")).toBe("");
  });

  it("says what comes next, by the choice", () => {
    expect(nextLine("token")).toBe("Next: insert the YubiKey, enter its PIN and touch it. Then your recovery key is shown once.");
    expect(nextLine("password")).toBe("Next: your recovery key is shown once.");
  });

  it("puts the backup's own recovery key first for an adopted backup, and shows none", () => {
    for (const m of ["token", "password"] as const) {
      expect(nextLine(m, "adopt")).toMatch(/^Next: type the backup's recovery key/);
      expect(nextLine(m, "adopt")).not.toMatch(/shown once/);
    }
  });
});

// The two lines under the cards are captions in the dialog's 412 px
// measure (ruled 2026-10-17). Measured in Edge on 2026-10-17, the caption
// averages under 5.4 px a character, so a line of 410 px holds about 76
// characters, and the recovery key's line, beside its 14 px icon and 8 px
// gap, about 72. Budgets in characters keep the copy from outgrowing them.
const CAPTION_LINE = 76;
const FIXED_LINE = 72;

describe("the recovery key's line", () => {
  it("is fixed by the context, never a choice", () => {
    expect(fixedLine()).toBe(firstMethodCopy.recoveryMade);
    expect(fixedLine("create")).toMatch(/^Recovery key — always made\. /);
    expect(fixedLine("adopt")).toBe(firstMethodCopy.recoveryKept);
    expect(fixedLine("adopt")).toMatch(/^Recovery key — the backup's own\. /);
  });

  it("stays one caption line beside its icon", () => {
    for (const c of ["create", "adopt"] as const) expect(fixedLine(c).length, fixedLine(c)).toBeLessThanOrEqual(FIXED_LINE);
  });
});

describe("the next line's shape", () => {
  const all = (["create", "adopt"] as const).flatMap((c) => (["token", "password"] as const).map((m) => nextLine(m, c)));

  it("is one statement that begins with Next and ends with a full stop", () => {
    for (const t of all) {
      expect(t).toMatch(/^Next: [a-z]/);
      expect(t.endsWith(".")).toBe(true);
    }
  });

  it("takes two caption lines at the most, and one where only the recovery key follows", () => {
    for (const t of all) expect(t.length, t).toBeLessThanOrEqual(2 * CAPTION_LINE);
    expect(nextLine("password").length).toBeLessThanOrEqual(CAPTION_LINE);
    expect(nextLine("password", "adopt").length).toBeLessThanOrEqual(CAPTION_LINE);
  });
});
