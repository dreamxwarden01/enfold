import { describe, expect, it } from "vitest";
import { CeremonyStep } from "./api";
import { NO_FORM, NO_TRACK, key, nextForm, track } from "./pinform";
import type { CeremonyFacts, FormEvent, PinForm } from "./pinform";

function ev(step: CeremonyStep, promptId = "", error = "", merged = true): FormEvent {
  return { step, promptId, error, merged };
}

// run walks the machine from nothing through a list of events.
function run(...events: FormEvent[]): PinForm {
  return events.reduce((f, e) => nextForm(f, e), NO_FORM);
}

describe("the merged card", () => {
  it("puts both fields under one Continue, which answers the PIN", () => {
    const f = run(ev(CeremonyStep.StepProbing), ev(CeremonyStep.StepPIN, "p1"));
    expect(f.shows).toBe("both");
    expect(f.answers).toBe("pin");
    expect(f.promptId).toBe("p1");
    expect(f.send).toBe("");
    expect(f.pinError).toBe("");
  });

  it("marks the PIN alone on a wrong PIN and moves nothing else", () => {
    const f = run(ev(CeremonyStep.StepPIN, "p1"), ev(CeremonyStep.StepPIN, "p2", "token.pin"));
    expect(f.shows).toBe("both");
    expect(f.answers).toBe("pin");
    expect(f.promptId).toBe("p2");
    expect(f.pinError).toBe("token.pin");
    // The password field keeps what was typed: not emptied, not marked,
    // not locked — an ordinary editable field the user may still change.
    expect(f.clearPassword).toBe(false);
    expect(f.passwordError).toBe("");
    expect(f.clearPin).toBe(true);
    expect(f.send).toBe("");
  });

  it("answers the password prompt at once from the field as it stands", () => {
    const f = run(ev(CeremonyStep.StepPIN, "p1"), ev(CeremonyStep.StepPassword, "w1"));
    expect(f.shows).toBe("password");
    expect(f.answers).toBe("password");
    expect(f.promptId).toBe("w1");
    expect(f.send).toBe("password");
    expect(f.clearPassword).toBe(false);
    expect(f.given).toBe(true);
    expect(f.held).toBe(false);
  });

  it("sends the password typed beside a PIN that had to be typed twice", () => {
    const f = run(ev(CeremonyStep.StepPIN, "p1"), ev(CeremonyStep.StepPIN, "p2", "token.pin"), ev(CeremonyStep.StepPassword, "w1"));
    expect(f.send).toBe("password");
    expect(f.clearPassword).toBe(false);
  });

  it("shows the password alone, emptied and marked, when the core refuses it after the touch", () => {
    const f = run(
      ev(CeremonyStep.StepPIN, "p1"),
      ev(CeremonyStep.StepPassword, "w1"),
      ev(CeremonyStep.StepTouch),
      ev(CeremonyStep.StepDeriving),
      ev(CeremonyStep.StepPassword, "w2", "vault.auth"),
    );
    expect(f.shows).toBe("password");
    expect(f.answers).toBe("password");
    expect(f.promptId).toBe("w2");
    expect(f.passwordError).toBe("vault.auth");
    expect(f.clearPassword).toBe(true);
    expect(f.send).toBe(""); // never sent unasked: the user types it again
    expect(f.pinError).toBe("");
  });

  it("asks no PIN again while the core holds the cached touch", () => {
    // Two wrong passwords in a row cost neither PIN nor touch (APP.md §2.2).
    const f = run(
      ev(CeremonyStep.StepPIN, "p1"),
      ev(CeremonyStep.StepPassword, "w1"),
      ev(CeremonyStep.StepTouch),
      ev(CeremonyStep.StepPassword, "w2", "vault.auth"),
      ev(CeremonyStep.StepPassword, "w3", "vault.auth"),
    );
    expect(f.shows).toBe("password");
    expect(f.clearPassword).toBe(true);
    expect(f.send).toBe("");
  });
});

describe("the PIN alone", () => {
  it("stands alone on a vault whose password switch is off", () => {
    const f = run(ev(CeremonyStep.StepPIN, "p1", "", false));
    expect(f.shows).toBe("pin");
    expect(f.answers).toBe("pin");
    expect(f.held).toBe(false);
  });

  it("stands alone when the PIN is asked again inside the agreement", () => {
    // The fallback APP.md §2.2 names: the card lost its verification
    // between the VERIFY and the ECDH, so the PIN is asked once more —
    // with no password prompt to follow, since it has already gone out.
    const f = run(ev(CeremonyStep.StepPIN, "p1"), ev(CeremonyStep.StepPassword, "w1"), ev(CeremonyStep.StepPIN, "p2"));
    expect(f.shows).toBe("pin");
    expect(f.answers).toBe("pin");
    expect(f.held).toBe(false);
    expect(f.given).toBe(true);
  });

  it("marks a wrong PIN on a vault with no password", () => {
    const f = run(ev(CeremonyStep.StepPIN, "p1", "", false), ev(CeremonyStep.StepPIN, "p2", "token.pin", false));
    expect(f.shows).toBe("pin");
    expect(f.pinError).toBe("token.pin");
    expect(f.clearPin).toBe(true);
  });
});

describe("an adopted touch", () => {
  // A ceremony that adopts a pending touch opens at StepTouch with
  // nothing before it: the password it needs is already in the attempt it
  // took over, so no password prompt ever reached this page. The card
  // must not offer the password field again for it.
  it("counts the vault's password as given, so a PIN asked after it stands alone", () => {
    const f = run(ev(CeremonyStep.StepTouch), ev(CeremonyStep.StepPIN, "p1"));
    expect(f.shows).toBe("pin");
    expect(f.answers).toBe("pin");
    expect(f.held).toBe(false);
    expect(f.given).toBe(true);
  });

  it("shows the password alone when the one it carried is refused after the touch", () => {
    const f = run(ev(CeremonyStep.StepTouch), ev(CeremonyStep.StepPassword, "w1", "vault.auth"));
    expect(f.shows).toBe("password");
    expect(f.answers).toBe("password");
    expect(f.passwordError).toBe("vault.auth");
    expect(f.clearPassword).toBe(true);
    expect(f.send).toBe(""); // nothing was typed here to send
  });
});

describe("the password alone", () => {
  it("is an ordinary prompt on a way in that is not the key's", () => {
    const f = run(ev(CeremonyStep.StepPassword, "w1", "", false));
    expect(f.shows).toBe("password");
    expect(f.send).toBe("");
    expect(f.clearPassword).toBe(true);
  });

  it("marks a refusal that no merged card was waiting on", () => {
    const f = run(ev(CeremonyStep.StepPassword, "w1", "", false), ev(CeremonyStep.StepPassword, "w2", "vault.auth", false));
    expect(f.passwordError).toBe("vault.auth");
  });
});

describe("what the machine forgets", () => {
  it("shows nothing at a step that asks for nothing, and remembers across it", () => {
    const held = nextForm(NO_FORM, ev(CeremonyStep.StepPIN, "p1"));
    const quiet = nextForm(held, ev(CeremonyStep.StepTouch));
    expect(quiet.shows).toBe("none");
    expect(quiet.answers).toBe("");
    expect(quiet.held).toBe(true);
    expect(nextForm(quiet, ev(CeremonyStep.StepPassword, "w1")).send).toBe("password");
  });

  it("forgets everything at an opening, at Done, at Failed and at Blocked", () => {
    const held = nextForm(NO_FORM, ev(CeremonyStep.StepPIN, "p1"));
    for (const step of [CeremonyStep.StepWaitingForKey, CeremonyStep.StepDone, CeremonyStep.StepFailed, CeremonyStep.StepBlocked]) {
      expect(nextForm(held, ev(step)), step).toEqual(NO_FORM);
    }
  });

  it("ignores a prompt step with no prompt id", () => {
    const held = nextForm(NO_FORM, ev(CeremonyStep.StepPIN, "p1"));
    expect(nextForm(held, ev(CeremonyStep.StepPIN)).shows).toBe("none");
    expect(nextForm(held, ev(CeremonyStep.StepPassword)).shows).toBe("none");
    expect(nextForm(held, ev(CeremonyStep.StepPassword)).held).toBe(true);
  });
});

describe("the card's key", () => {
  it("is one key for the merged card and the password alone", () => {
    // The password typed beside the PIN must survive the prompt that
    // follows it: a re-key would take the field with it.
    expect(key({ ...NO_FORM, shows: "both" })).toBe(key({ ...NO_FORM, shows: "password" }));
    expect(key({ ...NO_FORM, shows: "pin" })).toBe("pin");
    expect(key({ ...NO_FORM, shows: "none" })).toBe("none");
  });
});

describe("track", () => {
  const c = (seq: number, step: CeremonyStep, promptId = "", error = ""): CeremonyFacts => ({ seq, step, promptId, error });

  it("steps once per event and never on a re-render", () => {
    let t = track(NO_TRACK, c(1, CeremonyStep.StepPIN, "p1"), true);
    expect(t.form.shows).toBe("both");
    const again = track(t, c(1, CeremonyStep.StepPIN, "p1"), true);
    expect(again).toBe(t); // the same object: nothing moved
    t = track(t, c(2, CeremonyStep.StepPassword, "w1"), true);
    expect(t.form.send).toBe("password");
    expect(track(t, c(2, CeremonyStep.StepPassword, "w1"), true).form.send).toBe("password");
  });

  it("starts over when the ceremony is dismissed", () => {
    const t = track(NO_TRACK, c(4, CeremonyStep.StepPIN, "p1"), true);
    expect(track(t, null, true)).toBe(NO_TRACK);
    expect(track(NO_TRACK, null, true)).toBe(NO_TRACK);
  });
});
