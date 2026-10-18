import { describe, expect, it } from "vitest";
import type { FileOutcome, OpView } from "./api";
import { Code } from "./api";
import { DONE_WAITING, backupLine, checkedLine, clearsDone, doneLine, fraction, latestOp, nextDone, progressCaption, railMark, sayDone, standingNotices, staysUnlocked, timeLeft, track } from "./foot";
import type { Track } from "./foot";
import { codeText, dialogCopy, doneCopy, footCopy, pendingCopy, reclaimedText, resultsCopy } from "./strings";

// The foot as the status bar (direction 4, phase 3): what it says while an
// operation runs, how it says the ending, and what the rail and the lock
// chip add. LayerFoot.svelte draws these and decides nothing itself.

const MB = 1024 * 1024;
const GB = 1024 * MB;

function op(o: Partial<OpView>): OpView {
  return { id: "op1", kind: "add", archiveId: "a1", done: 0, total: 0, items: 0, dragItems: 0, phase: "", startedAt: 100, finished: true, returned: 0, ...o };
}

function out(o: Partial<FileOutcome>): FileOutcome {
  return { path: "", name: "", isDir: false, outcome: "added", size: 0, modifiedAt: 0, ...o };
}

const names = new Map([
  ["a1", "Photos 2024"],
  ["b2", "Family videos"],
]);

// feed builds a track from (ms, bytes) pairs, as the foot sees progress.
function feed(pairs: [number, number][]): Track | undefined {
  let t: Track | undefined;
  for (const [ms, done] of pairs) t = track(t, { t: ms, done });
  return t;
}

describe("the time left", () => {
  // 1 MB a second, 28 MB in all.
  const steady = feed([0, 1, 2, 3, 4, 5, 6].map((s) => [s * 1000, s * MB] as [number, number]));

  it("says nothing for the first 3 s", () => {
    const early = feed([[0, 0], [1000, MB], [2000, 2 * MB]]);
    expect(timeLeft(early, 28 * MB, 2500)).toBe("");
    expect(timeLeft(early, 28 * MB, 3000)).not.toBe("");
  });

  it("measures the rate over the last 5 s and rounds the minutes up", () => {
    // 22 MB left at 1 MB/s is 22 s: under a minute.
    expect(timeLeft(steady, 28 * MB, 6000)).toBe("less than a minute left");
    // 154 MB left at 1 MB/s is 154 s: about 3 min, never 2.
    expect(timeLeft(steady, 160 * MB, 6000)).toBe("about 3 min left");
  });

  it("says nothing once 10 s or less remain", () => {
    expect(timeLeft(steady, 16 * MB, 6000)).toBe("");
    expect(timeLeft(steady, 17 * MB, 6000)).toBe("less than a minute left");
  });

  it("forgets samples older than 5 s, so the rate is the recent one", () => {
    // Fast for 4 s (10 MB/s), then slow (1 MB/s) for the last 5 s.
    const slowed = feed([[0, 0], [4000, 40 * MB], [5000, 41 * MB], [6000, 42 * MB], [7000, 43 * MB], [8000, 44 * MB], [9000, 45 * MB]]);
    expect(slowed?.samples[0].t).toBe(4000);
    // 115 MB left at 1 MB/s: 115 s, about 2 min.
    expect(timeLeft(slowed, 160 * MB, 9000)).toBe("about 2 min left");
  });

  it("says nothing when the bytes stall for the whole window, or the total is unknown", () => {
    expect(timeLeft(steady, 160 * MB, 6000 + 5001)).toBe("");
    expect(timeLeft(steady, 0, 6000)).toBe("");
    expect(timeLeft(undefined, 160 * MB, 6000)).toBe("");
  });

  it("starts the window again when the bytes go back", () => {
    const t = feed([[0, 0], [1000, 5 * MB], [2000, 0]]);
    expect(t?.samples).toEqual([{ t: 2000, done: 0 }]);
    expect(t?.since).toBe(0);
  });

  it("keeps a repeated figure out of the window", () => {
    const t = feed([[0, MB], [1000, MB]]);
    expect(t?.samples).toHaveLength(1);
  });
});

describe("the caption and the line", () => {
  it("says the bytes, then the time left", () => {
    expect(progressCaption(9.4 * MB, 28 * MB, "about 2 min left")).toBe("9.4 MB of 28 MB · about 2 min left");
    expect(progressCaption(9.4 * MB, 28 * MB)).toBe("9.4 MB of 28 MB");
    expect(progressCaption(0, 0)).toBe("");
  });

  it("fills the line by bytes, and has no fill while the total is unknown", () => {
    expect(fraction(7, 28)).toBe(0.25);
    expect(fraction(30, 28)).toBe(1);
    expect(fraction(3, 0)).toBeNull();
  });
});

describe("which operation the foot names", () => {
  it("is the one begun last", () => {
    const ops = [op({ id: "a", startedAt: 10, finished: false }), op({ id: "b", startedAt: 12, finished: false }), op({ id: "c", startedAt: 11, finished: false })];
    expect(latestOp(ops)?.id).toBe("b");
  });

  it("ignores what has finished, and is null with nothing running", () => {
    expect(latestOp([op({ id: "a", startedAt: 20, finished: true }), op({ id: "b", startedAt: 10, finished: false })])?.id).toBe("b");
    expect(latestOp([op({ finished: true })])).toBeNull();
  });
});

describe("the rail's mark", () => {
  it("is nothing while nothing runs", () => {
    expect(railMark([])).toBe("none");
    expect(railMark([op({ finished: true, done: 1, total: 2 })])).toBe("none");
  });

  it("is a ring at the percentage while exactly one runs with a known total", () => {
    expect(railMark([op({ finished: false, done: 58, total: 100 })])).toEqual({ ring: 58 });
    expect(railMark([op({ finished: false, done: 2 * GB, total: 3 * GB })])).toEqual({ ring: 67 });
  });

  it("is the dot when the total is not known, or more than one runs", () => {
    expect(railMark([op({ finished: false, total: 0 })])).toBe("dot");
    expect(railMark([op({ finished: false, done: 1, total: 4 }), op({ id: "op2", finished: false, done: 1, total: 4 })])).toBe("dot");
  });
});

describe("Stay unlocked", () => {
  const now = 1_000_000 * 1000;
  const t0 = now / 1000;

  it("is offered in the idle deadline's last minute", () => {
    expect(staysUnlocked(t0 + 45, t0 + 3600, now)).toBe(true);
    expect(staysUnlocked(t0 + 60, t0 + 3600, now)).toBe(true);
  });

  it("is not offered before the last minute", () => {
    expect(staysUnlocked(t0 + 61, t0 + 3600, now)).toBe(false);
  });

  it("is never offered when the cap is the nearer deadline: nothing extends it", () => {
    expect(staysUnlocked(t0 + 600, t0 + 45, now)).toBe(false);
    expect(staysUnlocked(t0 + 45, t0 + 45, now)).toBe(false);
  });

  it("is not offered with no idle deadline, or one already past", () => {
    expect(staysUnlocked(0, t0 + 45, now)).toBe(false);
    expect(staysUnlocked(t0 - 1, t0 + 3600, now)).toBe(false);
  });
});

describe("the standing notices", () => {
  it("ranks them by tier and leaves the Tampered one to its banner", () => {
    expect(standingNotices([Code.CodeVaultStale, Code.CodeVaultTampered, Code.CodeTokenReset])).toEqual([Code.CodeTokenReset, Code.CodeVaultStale]);
    expect(standingNotices(null)).toEqual([]);
  });
});

describe("the done line", () => {
  const dest = "D:\\Archives\\Photos 2024";

  it("says where an extract went, with the way there", () => {
    const o = op({ kind: "extract", items: 15, destination: dest, results: Array.from({ length: 15 }, () => out({ outcome: "extracted" })) });
    expect(doneLine(o, names)).toEqual({ tone: "ok", text: "Extracted 15 files to D:\\Archives\\Photos 2024", action: { kind: "reveal", path: dest } });
  });

  it("counts an extract that left some out, and opens the details", () => {
    const results = [...Array.from({ length: 12 }, () => out({ outcome: "extracted" })), ...Array.from({ length: 3 }, () => out({ outcome: "failed", code: Code.CodeIO }))];
    const o = op({ kind: "extract", items: 15, destination: dest, results });
    const line = doneLine(o, names);
    expect(line?.text).toBe("Extracted 12 of 15 files to D:\\Archives\\Photos 2024");
    expect(line?.tone).toBe("neutral");
    expect(line?.action).toEqual({ kind: "details", op: o });
  });

  it("says what an extract that comes back with a question did write", () => {
    const results = [...Array.from({ length: 10 }, () => out({ outcome: "extracted" })), ...Array.from({ length: 3 }, () => out({ outcome: "conflict" }))];
    const o = op({ kind: "extract", items: 13, destination: dest, policy: "ask", results });
    expect(doneLine(o, names)).toEqual({ tone: "neutral", text: "Extracted 10 of 13 files to D:\\Archives\\Photos 2024", action: { kind: "reveal", path: dest } });
  });

  it("says nothing for an extract that wrote nothing and asks", () => {
    // The question over the page is the news; "Extracted 0 of 1 file"
    // with a way to an empty result was not true to it.
    const asking = op({ kind: "extract", items: 1, destination: dest, policy: "ask", results: [out({ outcome: "conflict" })] });
    expect(doneLine(asking, names)).toBeNull();
    const refused = op({ kind: "extract", items: 1, destination: dest, results: [out({ outcome: "name_refused" })] });
    expect(doneLine(refused, names)).toBeNull();
    const pathRefused = op({ kind: "extract", items: 2, destination: dest, results: [out({ outcome: "path_refused" }), out({ outcome: "conflict" })] });
    expect(doneLine(pathRefused, names)).toBeNull();
    // Nothing written with no question standing is still said, with its details.
    const failed = op({ kind: "extract", items: 1, destination: dest, results: [out({ outcome: "failed", code: Code.CodeIO })] });
    expect(doneLine(failed, names)?.action).toEqual({ kind: "details", op: failed });
  });

  it("says a stopped extract in neutral ink, with what it kept", () => {
    const o = op({ kind: "extract", items: 15, destination: dest, error: Code.CodeOpCancelled, results: Array.from({ length: 6 }, () => out({ outcome: "extracted" })) });
    expect(doneLine(o, names)).toEqual({ tone: "neutral", text: "Stopped. 6 of 15 files were written to D:\\Archives\\Photos 2024", action: { kind: "reveal", path: dest } });
    const one = op({ kind: "extract", items: 15, destination: dest, error: Code.CodeOpCancelled, results: [out({ outcome: "extracted" })] });
    expect(doneLine(one, names)?.text).toBe("Stopped. 1 of 15 files was written to D:\\Archives\\Photos 2024");
    const none = op({ kind: "extract", items: 15, destination: dest, error: Code.CodeOpCancelled, results: [] });
    expect(doneLine(none, names)).toEqual({ tone: "neutral", text: "Stopped. Nothing was written" });
  });

  it("says an add, whole or in part", () => {
    expect(doneLine(op({ kind: "add", items: 2, results: [out({}), out({})] }), names)).toEqual({ tone: "ok", text: "Added 2 files to Photos 2024" });
    expect(doneLine(op({ kind: "add", items: 1 }), names)?.text).toBe("Added 1 file to Photos 2024");
    const part = op({ kind: "add", items: 4, results: [out({}), out({}), out({ outcome: "failed", code: Code.CodeIO }), out({ outcome: "skipped" })] });
    expect(doneLine(part, names)).toEqual({ tone: "neutral", text: "Added 2 of 4 files to Photos 2024", action: { kind: "details", op: part } });
  });

  it("counts the files an add wrote, not the folders it made on the way", () => {
    const o = op({ kind: "add", items: 2, results: [out({ isDir: true, outcome: "created" }), out({}), out({})] });
    expect(doneLine(o, names)?.text).toBe("Added 2 files to Photos 2024");
    const empty = op({ kind: "add", items: 0, results: [out({ isDir: true, outcome: "created" })] });
    expect(doneLine(empty, names)?.text).toBe("Added 1 folder to Photos 2024");
  });

  it("says a replace, and an archive it cannot name without a name", () => {
    expect(doneLine(op({ kind: "replace", items: 1, results: [out({ outcome: "replaced" })] }), names)?.text).toBe("Replaced 1 file in Photos 2024");
    expect(doneLine(op({ kind: "add", archiveId: "zz", items: 2 }), names)?.text).toBe("Added 2 files");
  });

  it("says what a compaction gave back, when it gave anything", () => {
    expect(doneLine(op({ kind: "compact", returned: 3_100_000_000 }), names)).toEqual({ tone: "ok", text: "Compacted Photos 2024: 2.9 GB given back" });
    expect(doneLine(op({ kind: "compact", returned: 0 }), names)?.text).toBe("Compacted Photos 2024");
  });

  it("says a reclaim's figure, and nothing when it gave nothing back", () => {
    expect(doneLine(op({ kind: "reclaim", returned: 1.2 * GB }), names)?.text).toBe(reclaimedText(1.2 * GB));
    expect(doneLine(op({ kind: "reclaim", returned: 0 }), names)).toBeNull();
  });

  it("says a refreshed fingerprint and a re-key in their buttons' words", () => {
    expect(doneLine(op({ kind: "verify" }), names)?.text).toBe("Fingerprint refreshed for Photos 2024");
    expect(doneLine(op({ kind: "rotate" }), names)?.text).toBe("Re-keyed Photos 2024");
  });

  it("says a drag out that left as files, and nothing for a self-drop or a cancel", () => {
    expect(doneLine(op({ kind: "dragout", dragItems: 4, items: 40, dragResult: "moved" }), names)?.text).toBe("4 items left Enfold as ordinary files");
    expect(doneLine(op({ kind: "dragout", dragItems: 1, dragResult: "copied" }), names)?.text).toBe("1 item left Enfold as ordinary files");
    expect(doneLine(op({ kind: "dragout", dragItems: 4, dragResult: "self_drop" }), names)).toBeNull();
    expect(doneLine(op({ kind: "dragout", dragItems: 4, dragResult: "cancelled", error: Code.CodeOpCancelled }), names)).toBeNull();
  });

  it("leaves a failure to its error toast", () => {
    expect(doneLine(op({ kind: "add", items: 2, error: Code.CodeIO }), names)).toBeNull();
    expect(doneLine(op({ kind: "extract", items: 2, error: Code.CodeIO, destination: dest }), names)).toBeNull();
  });

  it("says the user's own stop of every other kind as what did not happen", () => {
    const stop = (kind: string) => doneLine(op({ kind, error: Code.CodeOpCancelled }), names);
    expect(stop("add")).toEqual({ tone: "neutral", text: "Stopped. Nothing was added to Photos 2024" });
    expect(stop("replace")?.text).toBe("Stopped. Nothing was replaced in Photos 2024");
    expect(stop("verify")?.text).toBe("Stopped. The fingerprint of Photos 2024 was not refreshed");
    expect(stop("reclaim")?.text).toBe("Stopped. Reclaiming space in Photos 2024 did not finish");
    expect(stop("compact")?.text).toBe("Stopped. Photos 2024 is as it was");
  });
});

describe("the other endings the foot says", () => {
  it("says what Find missing files looked at and found", () => {
    expect(checkedLine({ probed: 3, missing: 0 })).toEqual({ tone: "ok", text: "Checked 3 archives · all files present" });
    expect(checkedLine({ probed: 3, missing: 1 })).toEqual({ tone: "neutral", text: "Checked 3 archives · 1 file missing" });
    expect(checkedLine({ probed: 1, missing: 2 }).text).toBe("Checked 1 archive · 2 files missing");
  });

  it("claims nothing when the pass measured no archive", () => {
    // Every archive on a USB stick, a network drive or a UNC path, an
    // empty vault, or a pass behind a probe still held over its budget
    // (the core pins that as {0,0}): no check, no "all files present".
    const none = checkedLine({ probed: 0, missing: 0 });
    expect(none).toEqual({ tone: "neutral", text: "No archive was checked" });
    expect(none.text).not.toMatch(/present/);
    expect(checkedLine({ probed: 0, missing: 0 }).tone).not.toBe("ok");
  });

  it("says where a backup went, with the way to it", () => {
    expect(backupLine("E:\\backup.eks")).toEqual({ tone: "ok", text: "Backup saved to E:\\backup.eks", action: { kind: "reveal", path: "E:\\backup.eks" } });
  });
});

// What the dialogs and the refusal say about stopping (direction 4).
describe("the words around a stop", () => {
  it("tells a compaction and a re-key that they run to the end, before they start", () => {
    expect(dialogCopy.compact.body).toMatch(/It runs to the end once started\.$/);
    expect(dialogCopy.rekeyArchive.body).toMatch(/It runs to the end once started\.$/);
  });

  it("gives the too-slow refusal the real way out, since the cap cannot be extended", () => {
    expect(codeText(Code.CodeTooSlow)).toBe("This would not finish before the session ends. Lock and unlock to start a fresh session, then try again.");
  });

  it("says the foot's and the done line's words in the house voice", () => {
    const said = [
      footCopy.cancelling, footCopy.runsToEnd, footCopy.stayUnlocked, footCopy.notices(1), footCopy.notices(2), footCopy.moreLabel(1),
      doneCopy.extractStopped(6, 15, "D:"), doneCopy.draggedOut(4), doneCopy.stoppedRefresh("Photos 2024"), doneCopy.checked(3, 1), doneCopy.checkedNone,
      pendingCopy.opening, resultsCopy.notDoneAndSkipped(2, "file", "added", 1), resultsCopy.skippedReason,
    ];
    for (const s of said) expect(s, s).not.toMatch(/n't|'s |'re |'ll /);
    expect(footCopy.notices(1)).toBe("1 notice");
    expect(footCopy.notices(2)).toBe("2 notices");
    expect(footCopy.more(1)).toBe("+1");
  });
});

describe("the done lines' turn", () => {
  it("is cleared by an operation the user started, never by the core's own reclaim", () => {
    // finishOp emits a commit's op.done and may start a reclaim at once:
    // that run must not erase "Replaced 1 file in Photos 2024" as it is said.
    expect(clearsDone(op({ kind: "reclaim", finished: false }))).toBe(false);
    for (const kind of ["add", "replace", "extract", "verify", "compact", "rotate", "dragout"]) {
      expect(clearsDone(op({ kind, finished: false })), kind).toBe(true);
    }
  });

  it("keeps the line and the queue through a reclaim, which shows once their turn ends", () => {
    let q = sayDone<string>({ shown: null, waiting: [] }, "Replaced 1 file in Photos 2024");
    q = sayDone(q, "Compacted Family videos");
    const reclaim = op({ id: "op9", kind: "reclaim", finished: false });
    const after = clearsDone(reclaim) ? { shown: null, waiting: [] } : q;
    expect(after).toEqual({ shown: "Replaced 1 file in Photos 2024", waiting: ["Compacted Family videos"] });
  });

  it("says a line at once when the foot is free", () => {
    expect(sayDone<string>({ shown: null, waiting: [] }, "a")).toEqual({ shown: "a", waiting: [] });
  });

  it("keeps a second ending waiting, then says it, so both are said", () => {
    // An add and a compaction that end together (the shoot's f06).
    let q = sayDone<string>({ shown: null, waiting: [] }, "Added 3 files to Photos 2024");
    q = sayDone(q, "Compacted Family videos");
    expect(q.shown).toBe("Added 3 files to Photos 2024");
    q = nextDone(q);
    expect(q).toEqual({ shown: "Compacted Family videos", waiting: [] });
    expect(nextDone(q)).toEqual({ shown: null, waiting: [] });
  });

  it("lets at most a few wait, the oldest going first", () => {
    let q = sayDone<number>({ shown: null, waiting: [] }, 0);
    for (let i = 1; i <= DONE_WAITING + 2; i++) q = sayDone(q, i);
    expect(q.shown).toBe(0);
    expect(q.waiting).toEqual([3, 4, 5]);
    expect(q.waiting.length).toBe(DONE_WAITING);
  });
});
