import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArchiveStat, ArchiveSummary, DragOutResult, FileRow, OpView, Page, VaultStatus } from "./api";
import { railChildren } from "./rail";

// The store's ordering rules (APP.md §2.4, §6, ruled 2026-09-10): which
// reply is applied and which is dropped, what a navigation clears, and
// when the boot gate opens. The services are mocked at the binding, so
// each test hands the store the replies in the order it chooses — a
// delayed one after a newer one, an event before the first reply — and
// the runtime is a stub that records the handlers boot() subscribes.

const m = vi.hoisted(() => ({
  handlers: {} as Record<string, (e: { data: unknown }) => void>,
  Stat: vi.fn(),
  Page: vi.fn(),
  Children: vi.fn(),
  Status: vi.fn(),
  List: vi.fn(),
  Leave: vi.fn(),
  Open: vi.fn(),
  OpenPath: vi.fn(),
  DragOut: vi.fn(),
  CloseDecided: vi.fn(),
  PendingOpen: vi.fn(),
  invoke: vi.fn(),
  Lock: vi.fn(),
  Close: vi.fn(),
  CloseAll: vi.fn(),
  BackupStatus: vi.fn(),
  SettingsGet: vi.fn(),
  SettingsSet: vi.fn(),
  CheckFiles: vi.fn(),
}));

vi.mock("@wailsio/runtime", () => ({
  Events: {
    On: (name: string, fn: (e: { data: unknown }) => void) => {
      m.handlers[name] = fn;
    },
  },
  System: { invoke: m.invoke },
  Call: { ByID: vi.fn() },
  CancellablePromise: Promise,
  Create: new Proxy({}, { get: () => () => (v: unknown) => v }),
}));

vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  const never = () => new Promise(() => {});
  return {
    ...actual,
    Archive: { Stat: m.Stat, Page: m.Page, Children: m.Children },
    Archives: { List: m.List, Leave: m.Leave, Open: m.Open, OpenPath: m.OpenPath, Close: m.Close, CloseAll: m.CloseAll, CheckFiles: m.CheckFiles },
    Shell: { DragOut: m.DragOut, CloseDecided: m.CloseDecided, PendingOpen: m.PendingOpen },
    Vault: { Status: m.Status, Activity: vi.fn(), LastExportAt: never, Lock: m.Lock, BackupStatus: m.BackupStatus },
    Keys: { Slots: never, EntangledState: never },
    Settings: { Get: m.SettingsGet, Set: m.SettingsSet },
  };
});

type Store = typeof import("./state.svelte").store;
// The grace a self-drop's flight waits for its landing (the store's own
// SELF_DROP_GRACE; the module is re-imported per test, so the number is
// pinned here).
const SELF_DROP_GRACE = 1000;

const ROOT = "0".repeat(32);

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function row(id: string, isDir = false): FileRow {
  return { id, parentId: ROOT, isDir, name: id, path: id, size: 1, storage: "raw", savedPercent: 0, modifiedAt: 1 };
}

function reply(seq: number, ids: string[], total: number, crumbs = [{ id: ROOT, name: "A" }]): Page {
  return { seq, rows: ids.map((id) => row(id, id.startsWith("d"))), total, crumbs };
}

function stat(seq: number): ArchiveStat {
  return { seq, id: "a", name: "A", size: 0, files: 0, records: 0, freeSpace: 0, keyVersion: 1, lastSavedAt: 0, state: "open", receiptOwed: false } as ArchiveStat;
}

function status(seq: number, state: string): VaultStatus {
  return { seq, state, openArchives: 0, ops: [] } as unknown as VaultStatus;
}

// A fresh store for every test: the module is a singleton.
let store: Store;
beforeEach(async () => {
  vi.resetModules();
  for (const k of Object.keys(m.handlers)) delete m.handlers[k];
  m.Stat.mockReset();
  m.Page.mockReset();
  m.Children.mockReset();
  m.Status.mockReset();
  m.List.mockReset().mockResolvedValue([]);
  m.Leave.mockReset().mockResolvedValue(undefined);
  m.Open.mockReset().mockResolvedValue(stat(1));
  m.OpenPath.mockReset();
  m.DragOut.mockReset();
  m.CloseDecided.mockReset().mockResolvedValue(undefined);
  m.PendingOpen.mockReset().mockResolvedValue({ seq: 0, path: "", rest: null });
  m.invoke.mockReset();
  m.Lock.mockReset().mockResolvedValue(undefined);
  m.Close.mockReset().mockResolvedValue(undefined);
  m.CloseAll.mockReset().mockResolvedValue([]);
  m.BackupStatus.mockReset().mockResolvedValue({ at: 0, archives: 0, missing: 0, rekeyed: 0 });
  m.SettingsGet.mockReset().mockImplementation(() => new Promise(() => {}));
  m.SettingsSet.mockReset().mockResolvedValue(undefined);
  store = (await import("./state.svelte")).store;
});
afterEach(() => {
  vi.useRealTimers();
});

// open lists the root of archive "a" at Seq 1 with the rows given.
async function open(ids: string[], total = ids.length) {
  store.current = "a";
  m.Page.mockResolvedValueOnce(reply(1, ids, total));
  await store.loadPage();
}

describe("the header's tick and Ctrl+A (selectFolder)", () => {
  it("applies the Children reply only to the selection it was asked over — a change meanwhile stands", async () => {
    await open(["x", "y"], 3);
    store.selectClick("x");
    const d = deferred<{ id: string; isDir: boolean }[]>();
    m.Children.mockReturnValueOnce(d.promise);
    const p = store.selectFolder();
    store.clearSelection(); // the blank click while the reply is on its way
    d.resolve([{ id: "x", isDir: false }, { id: "y", isDir: false }, { id: "z", isDir: true }]);
    await p;
    expect(store.sel.ids.size).toBe(0);
  });

  it("ticks the whole folder — every id Children answers — when nothing changed meanwhile", async () => {
    await open(["x", "y"], 3);
    store.selectClick("x");
    m.Children.mockResolvedValueOnce([{ id: "x", isDir: false }, { id: "y", isDir: false }, { id: "z", isDir: true }]);
    await store.selectFolder();
    expect([...store.sel.ids].sort()).toEqual(["x", "y", "z"]);
    expect(store.sel.anchor).toBe("x");
  });
});

describe("a sort change (setSort)", () => {
  it("makes the listing stale at once: no page is added to it until the new order's first page lands", async () => {
    await open(["a", "b"], 4);
    const first = deferred<Page>();
    m.Page.mockReturnValueOnce(first.promise);
    const sorting = store.setSort({ key: "name", desc: true, nameDesc: true });
    // The scroll asks for more while the first page of the new order is
    // still on its way: nothing is asked, and the pending request is not
    // superseded by a page of the old order.
    await store.loadMore();
    expect(m.Page).toHaveBeenCalledTimes(2);
    expect(store.page?.rows.map((r) => r.id)).toEqual(["a", "b"]); // the old rows, kept until then
    first.resolve(reply(1, ["b", "a"], 4));
    await sorting;
    expect(store.page?.rows.map((r) => r.id)).toEqual(["b", "a"]);
    // Now the listing is the new order's, and paging goes on from it.
    m.Page.mockResolvedValueOnce(reply(1, ["d", "c"], 4));
    await store.loadMore();
    expect(m.Page).toHaveBeenLastCalledWith("a", ROOT, "-name", 2, 1000);
    expect(store.page?.rows.map((r) => r.id)).toEqual(["b", "a", "d", "c"]);
  });
});

describe("paging (loadMore)", () => {
  it("is one request at a time, and pages on once the reply is applied", async () => {
    await open(["a", "b"], 4);
    const d = deferred<Page>();
    m.Page.mockReturnValueOnce(d.promise);
    const p = store.loadMore();
    await store.loadMore(); // refused while one is in flight
    expect(m.Page).toHaveBeenCalledTimes(2);
    d.resolve(reply(1, ["c", "d"], 4));
    await p;
    expect(store.page?.rows.map((r) => r.id)).toEqual(["a", "b", "c", "d"]);
  });

  it("clears its in-flight mark when a navigation supersedes it, so the new folder pages", async () => {
    await open(["a", "b"], 4);
    const stale = deferred<Page>();
    m.Page.mockReturnValueOnce(stale.promise);
    const p = store.loadMore();
    m.Page.mockResolvedValueOnce(reply(1, ["e", "f"], 3, [{ id: ROOT, name: "A" }, { id: "d1", name: "d1" }]));
    await store.enterDir("d1", "d1");
    stale.resolve(reply(1, ["c", "d"], 4)); // the old folder's page, answered late
    await p;
    expect(store.page?.rows.map((r) => r.id)).toEqual(["e", "f"]); // dropped
    m.Page.mockResolvedValueOnce(reply(1, ["g"], 3, [{ id: ROOT, name: "A" }, { id: "d1", name: "d1" }]));
    await store.loadMore();
    expect(m.Page).toHaveBeenCalledTimes(4);
    expect(store.page?.rows.map((r) => r.id)).toEqual(["e", "f", "g"]);
  });

  it("clears it on an error, so the next scroll asks again", async () => {
    await open(["a", "b"], 4);
    m.Page.mockRejectedValueOnce(new Error("internal"));
    await store.loadMore();
    m.Page.mockResolvedValueOnce(reply(1, ["c", "d"], 4));
    await store.loadMore();
    expect(store.page?.rows.map((r) => r.id)).toEqual(["a", "b", "c", "d"]);
  });

  it("clears it when the archive is left, so the next archive pages", async () => {
    await open(["a", "b"], 4);
    const stale = deferred<Page>();
    m.Page.mockReturnValueOnce(stale.promise);
    const p = store.loadMore();
    store.leaveArchive();
    stale.resolve(reply(1, ["c", "d"], 4));
    await p;
    expect(store.page).toBeNull();
    await open(["x", "y"], 3);
    m.Page.mockResolvedValueOnce(reply(1, ["z"], 3));
    await store.loadMore();
    expect(store.page?.rows.map((r) => r.id)).toEqual(["x", "y", "z"]);
  });
});

describe("re-reading the archive (refreshArchive)", () => {
  it("drops a reply to a superseded request, so an older Stat never stands over a newer one", async () => {
    await open(["a"]);
    const s1 = deferred<ArchiveStat>();
    const s2 = deferred<ArchiveStat>();
    m.Stat.mockReturnValueOnce(s1.promise).mockReturnValueOnce(s2.promise);
    m.Page.mockResolvedValue(reply(12, ["a", "n"], 2));
    const p1 = store.refreshArchive();
    const p2 = store.refreshArchive();
    s2.resolve(stat(12));
    await p2;
    expect(store.stat?.seq).toBe(12);
    s1.resolve(stat(11)); // answered late, for the request before
    await p1;
    expect(store.stat?.seq).toBe(12);
    expect(store.page?.seq).toBe(12);
  });

  it("drops a Stat older than the revision archive.changed announced, which the handler records", async () => {
    vi.useFakeTimers();
    m.Status.mockReturnValue(new Promise(() => {}));
    store.boot();
    await open(["a"]);
    const s = deferred<ArchiveStat>();
    m.Stat.mockReturnValueOnce(s.promise);
    m.handlers["archive.changed"]({ data: { id: "a", seq: 12 } });
    expect(m.Stat).toHaveBeenCalledTimes(1);
    s.resolve(stat(11)); // a Stat taken before the change it was asked after
    await s.promise;
    await Promise.resolve();
    expect(store.stat).toBeNull(); // nothing accepted: the tree it describes is older than the one announced
    expect(m.Page).toHaveBeenCalledTimes(1); // and no listing was re-read off it
  });
});

describe("going up (focusAfter)", () => {
  const inD = [{ id: ROOT, name: "A" }, { id: "d1", name: "d1" }];

  it("keeps the folder just left until its row appears in a later page, and the next navigation clears it", async () => {
    store.current = "a";
    m.Page.mockResolvedValueOnce(reply(1, [], 0, inD));
    await store.enterDir("d1", "d1");
    m.Page.mockResolvedValueOnce(reply(1, ["x"], 2)); // the parent's first page, without d1
    await store.goUp();
    expect(store.dirId).toBe(ROOT);
    expect(store.focusAfter).toBe("d1"); // waited for, not dropped
    m.Page.mockResolvedValueOnce(reply(1, ["d1"], 2));
    await store.loadMore();
    expect(store.page?.rows.map((r) => r.id)).toEqual(["x", "d1"]);
    expect(store.focusAfter).toBe("d1"); // the page applies it once the row is on screen
    m.Page.mockResolvedValueOnce(reply(1, [], 0, inD));
    await store.enterDir("d1", "d1");
    expect(store.focusAfter).toBeNull();
  });
});

describe("the boot gate (booted)", () => {
  it("opens when the first Status call has answered, not on an event that lands first — and draws the newest status", async () => {
    vi.useFakeTimers();
    const first = deferred<VaultStatus>();
    m.Status.mockReturnValueOnce(first.promise);
    store.boot();
    m.handlers["vault.state"]({ data: status(5, "locked") });
    expect(store.status?.seq).toBe(5); // applied by its Seq
    expect(store.booted).toBe(false); // but the gate stays shut
    first.resolve(status(4, "unlocked")); // the reply, older than the event
    await first.promise;
    await Promise.resolve();
    expect(store.booted).toBe(true);
    expect(store.status?.seq).toBe(5); // the newest accepted, not the reply
  });

  it("opens on the reply itself when nothing landed before it", async () => {
    vi.useFakeTimers();
    m.Status.mockResolvedValueOnce(status(1, "unlocked"));
    store.boot();
    await Promise.resolve();
    await Promise.resolve();
    expect(store.booted).toBe(true);
    expect(store.status?.state).toBe("unlocked");
  });

  it("shows the Retry line, not the lock scene, when the first call fails with nothing accepted", async () => {
    vi.useFakeTimers();
    m.Status.mockRejectedValueOnce(new Error("internal"));
    store.boot();
    await Promise.resolve();
    await Promise.resolve();
    expect(store.booted).toBe(false);
    expect(store.bootFailed).not.toBe("");
    expect(store.status).toBeNull();
  });

  it("opens on a failed call when an event was accepted meanwhile — there is a scene to draw", async () => {
    vi.useFakeTimers();
    const first = deferred<VaultStatus>();
    m.Status.mockReturnValueOnce(first.promise);
    store.boot();
    m.handlers["vault.state"]({ data: status(5, "locked") });
    first.reject(new Error("internal"));
    await first.promise.catch(() => {});
    await Promise.resolve();
    expect(store.booted).toBe(true);
    expect(store.bootFailed).toBe("");
    expect(store.status?.seq).toBe(5);
  });
});

// The operations a status carries (APP.md §2.4, ruled 2026-09-12): the
// snapshot is taken under the core's lock and emitted after it, so a
// newer op.progress may already have landed — a status carried by an
// event fills in what the page does not know and never overwrites what it
// tracks, and only a status the page asked for replaces them.
describe("the operations a status carries", () => {
  const op = (id: string, done: number, finished = false) =>
    ({ id, kind: "add", done, total: 100, items: 0, dragItems: 0, phase: "", startedAt: 1, finished, returned: 0 }) as unknown as OpView;
  const withOps = (seq: number, ops: OpView[]) => ({ seq, state: "unlocked", openArchives: 0, ops }) as unknown as VaultStatus;

  // The store booted on an empty status, ready to be told about ops.
  async function booted() {
    vi.useFakeTimers();
    m.Status.mockResolvedValueOnce(status(1, "unlocked"));
    store.boot();
    await Promise.resolve();
    await Promise.resolve();
  }

  // A Status the page asks for, applied.
  async function asked(s: VaultStatus) {
    m.Status.mockResolvedValueOnce(s);
    void store.refreshAll();
    await Promise.resolve();
    await Promise.resolve();
  }

  it("fills in an operation the page has never seen", async () => {
    await booted();
    m.handlers["vault.state"]({ data: withOps(2, [op("o1", 10)]) });
    expect(store.ops["o1"]?.done).toBe(10);
  });

  it("leaves the progress the page already tracks alone", async () => {
    await booted();
    m.handlers["op.progress"]({ data: op("o1", 50) });
    m.handlers["vault.state"]({ data: withOps(2, [op("o1", 10), op("o2", 5)]) });
    expect(store.ops["o1"].done).toBe(50); // the event's older figure is not written back
    expect(store.ops["o2"].done).toBe(5); // and the one it did not know is filled in
  });

  it("replaces them from a status the page asked for", async () => {
    await booted();
    m.handlers["op.progress"]({ data: op("o1", 50) });
    await asked(withOps(3, [op("o1", 70)]));
    expect(store.ops["o1"].done).toBe(70);
  });

  it("never un-finishes an operation that has ended, asked for or not", async () => {
    await booted();
    m.handlers["op.progress"]({ data: op("o1", 100, true) });
    m.handlers["vault.state"]({ data: withOps(2, [op("o1", 60)]) });
    expect(store.ops["o1"].finished).toBe(true);
    await asked(withOps(3, [op("o1", 60)]));
    expect(store.ops["o1"].finished).toBe(true);
    expect(store.ops["o1"].done).toBe(100);
  });
});

// The one gesture's flight (APP.md §3, §6, ruled 2026-09-11, lib/dragout.ts):
// the store calls Shell.DragOut once and keeps the ids in flight until the
// answer and, for a self-drop, the WebView's landing add up to a verdict —
// a Move handed to the page, or nothing.
describe("the drag out in flight (beginDragOut)", () => {
  // The drag's folder Shell.DragOut answers, the manifest at its top, and
  // the items folder beneath it where every path handed out lies.
  const STAGING = "C:\\Users\\me\\AppData\\Local\\Enfold\\drag\\1a2b3c4d";
  const ITEMS = `${STAGING}\\items`;
  const went = (selfDrop: boolean): DragOutResult => ({ selfDrop, extracted: !selfDrop, effect: selfDrop ? 0 : 2, folder: STAGING, opId: "op9" });

  it("calls Shell.DragOut once with the archive and the ids, and ignores a second gesture meanwhile", async () => {
    await open(["x", "y", "d1"]);
    const d = deferred<DragOutResult>();
    m.DragOut.mockReturnValueOnce(d.promise);
    const first = store.beginDragOut(["x", "y"]);
    expect(store.dragOut?.ids).toEqual(["x", "y"]);
    expect(store.dragOut?.names).toEqual(["x", "y"]); // the loaded rows name them
    expect(store.dragOut?.from).toBe(ROOT);
    await store.beginDragOut(["d1"]); // ignored: one is in flight
    expect(m.DragOut).toHaveBeenCalledTimes(1);
    expect(m.DragOut).toHaveBeenCalledWith("a", ["x", "y"]);
    d.resolve(went(false));
    await first;
    expect(store.dragOut).toBeNull(); // the strip follows the operation from here
    expect(store.selfDrop).toBeNull();
  });

  it("starts nothing for an empty selection", async () => {
    await open(["x"]);
    await store.beginDragOut([]);
    expect(m.DragOut).not.toHaveBeenCalled();
    expect(store.dragOut).toBeNull();
  });

  it("hands the page the Move when a self-drop is answered and then lands on a folder row", async () => {
    await open(["x", "d1"]);
    const d = deferred<DragOutResult>();
    m.DragOut.mockReturnValueOnce(d.promise);
    const p = store.beginDragOut(["x"]);
    d.resolve(went(true));
    await p;
    expect(store.dragOut).not.toBeNull(); // waiting for the landing
    expect(store.dragOut?.opId).toBe("op9");
    store.dragLanded({ id: "d1", at: "row", isDir: true });
    expect(store.selfDrop).toEqual({ ids: ["x"], to: "d1", at: "row" });
    expect(store.dragOut).toBeNull();
  });

  it("hands the page the same Move when the landing comes before the answer", async () => {
    await open(["x", "d1"]);
    const d = deferred<DragOutResult>();
    m.DragOut.mockReturnValueOnce(d.promise);
    const p = store.beginDragOut(["x"]);
    store.dragLanded({ id: "d1", at: "row", isDir: true });
    expect(store.selfDrop).toBeNull(); // not yet: the answer may say it went out
    d.resolve(went(true));
    await p;
    expect(store.selfDrop).toEqual({ ids: ["x"], to: "d1", at: "row" });
    expect(store.dragOut).toBeNull();
  });

  it("moves nothing when the self-drop landed elsewhere, or on a file", async () => {
    await open(["x", "y"]);
    m.DragOut.mockResolvedValueOnce(went(true));
    await store.beginDragOut(["x"]);
    store.dragLanded("elsewhere");
    expect(store.selfDrop).toBeNull();
    expect(store.dragOut).toBeNull();
    m.DragOut.mockResolvedValueOnce(went(true));
    await store.beginDragOut(["x"]);
    store.dragLanded({ id: "y", at: "row", isDir: false });
    expect(store.selfDrop).toBeNull();
    expect(store.dragOut).toBeNull();
  });

  it("names only the ids the loaded rows can: a whole folder taken past them keeps its count", async () => {
    await open(["x"], 3);
    m.DragOut.mockResolvedValueOnce(went(false));
    const p = store.beginDragOut(["x", "y", "z"]);
    expect(store.dragOut?.ids).toEqual(["x", "y", "z"]);
    expect(store.dragOut?.names).toEqual(["x"]);
    await p;
  });

  it("ends the flight as nothing when someone else's files land on the page meanwhile", async () => {
    await open(["x", "d1"]);
    m.DragOut.mockResolvedValueOnce(went(true));
    await store.beginDragOut(["x"]);
    expect(store.dragOut).not.toBeNull(); // the grace: waiting for the landing
    store.dragLanded("foreign");
    expect(store.dragOut).toBeNull();
    expect(store.selfDrop).toBeNull();
  });

  it("lets a self-drop that never lands on the page go after a moment", async () => {
    vi.useFakeTimers();
    await open(["x"]);
    m.DragOut.mockResolvedValueOnce(went(true));
    await store.beginDragOut(["x"]);
    expect(store.dragOut).not.toBeNull();
    vi.advanceTimersByTime(SELF_DROP_GRACE + 1);
    expect(store.dragOut).toBeNull();
    expect(store.selfDrop).toBeNull();
  });

  it("says why when the call is refused, and the flight is over", async () => {
    await open(["x"]);
    m.DragOut.mockRejectedValueOnce(new Error("drag.busy"));
    await store.beginDragOut(["x"]);
    expect(store.dragOut).toBeNull();
    expect(store.notices.map((t) => t.kind)).toEqual(["error"]);
  });

  it("lands nothing without a flight", async () => {
    await open(["x", "d1"]);
    store.dragLanded({ id: "d1", at: "row", isDir: true });
    expect(store.selfDrop).toBeNull();
  });

  it("goes with the page when the archive is left", async () => {
    await open(["x"]);
    const d = deferred<DragOutResult>();
    m.DragOut.mockReturnValueOnce(d.promise);
    const p = store.beginDragOut(["x"]);
    store.leaveArchive();
    expect(store.dragOut).toBeNull();
    d.resolve(went(true));
    await p;
    store.dragLanded({ id: "d1", at: "row", isDir: true });
    expect(store.selfDrop).toBeNull();
  });

  // The shell's drop (APP.md §3): a self-drop's own paths come back as a
  // drop too — under the drag's folder, files that never exist — and are
  // never an add; real files from Explorer go on being one, a flight
  // waiting for its landing notwithstanding (the review's finding 3).
  describe("and the shell's drop", () => {
    const drop = (paths: string[]) => m.handlers["shell.drop"]({ data: { paths, isDir: paths.map(() => false), archiveId: "a", dirId: ROOT } });

    it("is never an add while the drag is in flight and the paths name its items", async () => {
      m.Status.mockResolvedValueOnce(status(1, "unlocked"));
      store.boot();
      await open(["x"]);
      const d = deferred<DragOutResult>();
      m.DragOut.mockReturnValueOnce(d.promise);
      const p = store.beginDragOut(["x"]);
      drop([`${ITEMS}\\x`]); // before the answer: told by its name
      expect(store.drop).toBeNull();
      d.resolve(went(true));
      await p;
      drop([`${ITEMS}\\x`]); // in the grace: under the drag's folder
      expect(store.drop).toBeNull();
    });

    it("is never an add after the flight when the paths lie under the drag's folder", async () => {
      m.Status.mockResolvedValueOnce(status(1, "unlocked"));
      store.boot();
      await open(["x"]);
      m.DragOut.mockResolvedValueOnce(went(true));
      await store.beginDragOut(["x"]);
      store.dragLanded("elsewhere");
      expect(store.dragOut).toBeNull();
      drop([`${ITEMS}\\x`]);
      expect(store.drop).toBeNull();
    });

    it("is the add of §3 for a real file dropped while a self-drop still waits for its landing", async () => {
      m.Status.mockResolvedValueOnce(status(1, "unlocked"));
      store.boot();
      await open(["x"]);
      m.DragOut.mockResolvedValueOnce(went(true));
      await store.beginDragOut(["x"]);
      expect(store.dragOut).not.toBeNull(); // the grace
      drop(["D:\\Pictures\\a.jpg"]);
      expect(store.drop?.paths).toEqual(["D:\\Pictures\\a.jpg"]);
    });

    it("is the add of §3 for real files, before and after a drag", async () => {
      m.Status.mockResolvedValueOnce(status(1, "unlocked"));
      store.boot();
      await open(["x"]);
      const dropped = () => store.drop?.paths;
      drop(["D:\\Pictures\\a.jpg"]);
      expect(dropped()).toEqual(["D:\\Pictures\\a.jpg"]);
      store.drop = null;
      m.DragOut.mockResolvedValueOnce(went(false));
      await store.beginDragOut(["x"]);
      drop(["D:\\Pictures\\b.jpg"]);
      expect(dropped()).toEqual(["D:\\Pictures\\b.jpg"]);
    });
  });
});

// The close question (APP.md §2.4, ruled 2026-09-13): the shell cancels the
// first close and emits shell.close; the page asks, and the answer goes
// back as Shell.CloseDecided(action, remember). Esc calls nothing at all.
describe("the close the shell cancelled", () => {
  const close = () => m.handlers["shell.close"]({ data: undefined });

  beforeEach(() => {
    m.Status.mockResolvedValueOnce(status(1, "unlocked"));
    store.boot();
  });

  it("puts the question up, and a second close while it is up is nothing", () => {
    expect(store.closeAsked).toBe(false);
    close();
    expect(store.closeAsked).toBe(true);
    close();
    expect(store.closeAsked).toBe(true);
    expect(m.CloseDecided).not.toHaveBeenCalled();
  });

  it("tells the shell nothing when Esc takes it away — the window stays", () => {
    close();
    store.dismissClose();
    expect(store.closeAsked).toBe(false);
    expect(m.CloseDecided).not.toHaveBeenCalled();
  });

  it("sends the answer and whether to remember it", async () => {
    close();
    await store.decideClose("quit", true);
    expect(m.CloseDecided).toHaveBeenCalledWith("quit", true);
    expect(store.closeAsked).toBe(false);
    expect(store.closeBusy).toBe(false);
  });

  it("does not remember an answer the user did not tick", async () => {
    close();
    await store.decideClose("tray", false);
    expect(m.CloseDecided).toHaveBeenCalledWith("tray", false);
  });

  it("answers once: a second press while the first is on its way is nothing", async () => {
    close();
    const d = deferred<void>();
    m.CloseDecided.mockReturnValueOnce(d.promise);
    const p = store.decideClose("tray", false);
    expect(store.closeBusy).toBe(true);
    await store.decideClose("quit", true); // the second press
    expect(m.CloseDecided).toHaveBeenCalledTimes(1);
    d.resolve();
    await p;
    expect(store.closeAsked).toBe(false);
  });

  it("keeps the question up and says why when the shell refused", async () => {
    close();
    m.CloseDecided.mockRejectedValueOnce({ cause: { code: "params" } });
    await store.decideClose("tray", false);
    expect(store.closeAsked).toBe(true);
    expect(store.closeBusy).toBe(false);
    expect(store.notices.length).toBe(1);
  });
});

// Opening an archive from Explorer (APP.md §14, decision 4): the shell
// hands the page a request — the launch's own, taken from the shell at
// boot, or a second launch's, which arrives as `shell.open` — and the
// page turns Archives.OpenPath's four answers into the archive's page,
// the lock scene, the key's dialog, or a toast.
describe("a file opened from Explorer (APP.md §14)", () => {
  const FILE = "D:\\Archives\\holiday.efd";
  const request = (seq: number, path = FILE, rest: string[] | null = null) => ({ seq, path, rest });
  const refusal = (code: string) => Object.assign(new Error(code), { cause: { code } });

  // A store booted on a vault in the state given, with the shell holding
  // the pending request given. The refreshes after the list never settle
  // in these mocks, which is as far as this needs to run.
  async function booted(state = "unlocked", pending = { seq: 0, path: "", rest: null as string[] | null }) {
    m.Status.mockResolvedValueOnce(status(1, state));
    m.PendingOpen.mockResolvedValueOnce(pending);
    m.Page.mockResolvedValue(reply(1, [], 0)); // an opened archive lists its empty root
    store.boot();
    for (let i = 0; i < 20; i++) await Promise.resolve();
  }

  it("asks the shell at boot and opens what it was given", async () => {
    m.OpenPath.mockResolvedValueOnce({ archiveId: "a", relocated: true });
    await booted("unlocked", { seq: 1, path: FILE, rest: null });
    expect(m.PendingOpen).toHaveBeenCalledTimes(1);
    expect(m.OpenPath).toHaveBeenCalledWith(FILE);
    expect(m.Open).toHaveBeenCalledWith("a"); // the route a row's double-click takes
    expect(store.route).toBe("archive");
    expect(store.current).toBe("a");
  });

  it("opens a second launch's file off the event, and empties the slot it was staged in", async () => {
    await booted();
    m.OpenPath.mockResolvedValueOnce({ archiveId: "b", relocated: false });
    // The shell stages every request and emits this one too: a page that
    // heard it takes the slot as well, or the next window would open the
    // same file again.
    m.PendingOpen.mockResolvedValueOnce({ seq: 1, path: FILE, rest: null });
    await m.handlers["shell.open"]({ data: request(1) });
    for (let i = 0; i < 20; i++) await Promise.resolve();
    expect(store.route).toBe("archive");
    expect(store.current).toBe("b");
    expect(m.PendingOpen).toHaveBeenCalledTimes(2); // the boot's, and this one
    expect(m.OpenPath).toHaveBeenCalledTimes(1); // the numbered request, opened once
  });

  it("opens one request once, however many ways it reached the page", async () => {
    m.OpenPath.mockResolvedValue({ archiveId: "a", relocated: false });
    await booted("unlocked", { seq: 3, path: FILE, rest: null });
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    // The same launch, arriving again as an event: a second launch that
    // had to recreate a window fills both the slot and the event.
    await store.openFromShell(request(3));
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    // An older one is past, and the next launch is its own request.
    await store.openFromShell(request(2, "D:\\Archives\\old.efd"));
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    await store.openFromShell(request(4, "D:\\Archives\\next.efd"));
    expect(m.OpenPath).toHaveBeenCalledTimes(2);
    expect(m.OpenPath).toHaveBeenLastCalledWith("D:\\Archives\\next.efd");
  });

  it("opens the first of several and counts the rest", async () => {
    m.OpenPath.mockResolvedValueOnce({ archiveId: "a", relocated: false });
    await booted();
    await store.openFromShell(request(1, FILE, ["D:\\Archives\\b.efd", "D:\\Archives\\c.efd"]));
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    expect(m.OpenPath).toHaveBeenCalledWith(FILE);
    expect(store.notices.length).toBe(1);
    // Named, not only counted (APP.md §14), and by their leaves.
    expect(store.notices[0].text).toContain("2 more archives were not opened: b.efd, c.efd.");
  });

  it("goes to the lock scene with the path kept, and finishes the open on the unlock — once", async () => {
    await booted("locked");
    m.OpenPath.mockRejectedValueOnce(refusal("vault.needs_unlock"));
    await store.openFromShell(request(1));
    expect(store.route).toBe("lock");
    expect(store.openWhenUnlocked).toBe(FILE);
    expect(store.keyNotInVault).toBeNull();
    // The unlock lands: the open is made again, and the path is let go.
    m.OpenPath.mockResolvedValueOnce({ archiveId: "a", relocated: false });
    m.handlers["vault.state"]({ data: status(2, "unlocked") });
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(m.OpenPath).toHaveBeenCalledTimes(2);
    expect(store.openWhenUnlocked).toBe("");
    expect(store.current).toBe("a");
  });

  // The unlock can land while the call that is about to be refused is
  // still in flight: the refusal was decided before it, and there is no
  // transition left to wait for (the review's finding 3).
  it("retries at once when Unlocked landed while the call was still in flight", async () => {
    await booted("locked");
    const late = deferred<{ archiveId: string; relocated: boolean }>();
    m.OpenPath.mockReturnValueOnce(late.promise);
    const p = store.openFromShell(request(1));
    m.handlers["vault.state"]({ data: status(2, "unlocked") }); // the unlock, first
    m.OpenPath.mockResolvedValueOnce({ archiveId: "a", relocated: false });
    late.reject(refusal("vault.needs_unlock")); // the stale refusal, after it
    await p;
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(m.OpenPath).toHaveBeenCalledTimes(2); // asked again at once
    expect(store.openWhenUnlocked).toBe(""); // not left waiting for an unlock that has been
    expect(store.current).toBe("a");
  });

  // Opening one archive over another never changes route, so the handle
  // the page held would stay mounted with nothing holding it (the
  // review's finding 4).
  it("leaves the archive already on the page before opening another", async () => {
    await booted();
    m.OpenPath.mockResolvedValueOnce({ archiveId: "one", relocated: false });
    await store.openFromShell(request(1));
    expect(store.current).toBe("one");
    m.OpenPath.mockResolvedValueOnce({ archiveId: "two", relocated: false });
    await store.openFromShell(request(2, "D:\\Archives\\two.efd"));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(m.Leave).toHaveBeenCalledWith("one");
    expect(store.current).toBe("two");
    expect(store.route).toBe("archive");
  });

  it("does not leave the archive when the same one is opened again", async () => {
    await booted();
    m.OpenPath.mockResolvedValue({ archiveId: "one", relocated: false });
    await store.openFromShell(request(1));
    await store.openFromShell(request(2, "D:\\Archives\\same.efd"));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(m.Leave).not.toHaveBeenCalled();
    expect(store.current).toBe("one");
  });

  // Two requests in flight and the older one answering last: it must not
  // land its route, or its dialog, over what the newer one put there (the
  // review's finding 5).
  it("serialises the opens and drops a result a newer request has overtaken", async () => {
    await booted();
    const slow = deferred<{ archiveId: string; relocated: boolean }>();
    m.OpenPath.mockReturnValueOnce(slow.promise);
    m.OpenPath.mockResolvedValueOnce({ archiveId: "newer", relocated: false });
    const first = store.openFromShell(request(1, "D:\\Archives\\slow.efd"));
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    const second = store.openFromShell(request(2, "D:\\Archives\\quick.efd"));
    for (let i = 0; i < 5; i++) await Promise.resolve();
    // The newer one is queued behind the older one and has not been asked.
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    slow.resolve({ archiveId: "older", relocated: false });
    await first;
    await second;
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(m.Open).toHaveBeenCalledTimes(1); // the overtaken result opened nothing
    expect(m.Open).toHaveBeenCalledWith("newer");
    expect(store.current).toBe("newer");
  });

  // A superseded success has already mounted the archive in the core: a
  // mount no page will show is an orphan, and the next open of that
  // archive would meet archive.open_elsewhere against a handle nobody
  // wanted (the second review's finding 2).
  it("gives back the mount of a success a newer request overtook", async () => {
    await booted();
    const slow = deferred<{ archiveId: string; relocated: boolean }>();
    m.OpenPath.mockReturnValueOnce(slow.promise);
    m.OpenPath.mockResolvedValueOnce({ archiveId: "newer", relocated: false });
    const first = store.openFromShell(request(1, "D:\\Archives\\slow.efd"));
    for (let i = 0; i < 5; i++) await Promise.resolve();
    const second = store.openFromShell(request(2, "D:\\Archives\\quick.efd"));
    slow.resolve({ archiveId: "stale", relocated: false }); // the core mounted it
    await first;
    await second;
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(m.Leave).toHaveBeenCalledWith("stale");
    expect(store.current).toBe("newer");
  });

  it("never asks the core for a request already overtaken in the queue", async () => {
    await booted();
    const slow = deferred<{ archiveId: string; relocated: boolean }>();
    m.OpenPath.mockReturnValueOnce(slow.promise);
    m.OpenPath.mockResolvedValueOnce({ archiveId: "second", relocated: false });
    const first = store.openFromShell(request(1, "D:\\Archives\\a.efd"));
    for (let i = 0; i < 5; i++) await Promise.resolve();
    const second = store.openFromShell(request(2, "D:\\Archives\\b.efd"));
    const third = store.openFromShell(request(3, "D:\\Archives\\c.efd"));
    slow.resolve({ archiveId: "first", relocated: false });
    await first;
    await second;
    await third;
    for (let i = 0; i < 10; i++) await Promise.resolve();
    // Three requests, and the middle one was overtaken before its turn
    // came: it never reached the core, so it mounted nothing to give back.
    expect(m.OpenPath).toHaveBeenCalledTimes(2);
    expect(m.OpenPath).toHaveBeenLastCalledWith("D:\\Archives\\c.efd");
    expect(m.Leave).toHaveBeenCalledWith("first"); // the one that did mount
    expect(m.Leave).not.toHaveBeenCalledWith("second");
  });

  // The Open is a second call of its own, and the check before it is not
  // the check after it (the second review's finding 3).
  it("applies nothing of a request overtaken while the core's Open was in flight", async () => {
    await booted();
    m.OpenPath.mockResolvedValueOnce({ archiveId: "stale", relocated: false });
    const slowOpen = deferred<ArchiveStat>();
    m.Open.mockReturnValueOnce(slowOpen.promise);
    const first = store.openFromShell(request(1, "D:\\Archives\\a.efd"));
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(m.Open).toHaveBeenCalledWith("stale");
    // B is accepted while A's Open is still travelling.
    m.OpenPath.mockResolvedValueOnce({ archiveId: "fresh", relocated: false });
    const second = store.openFromShell(request(2, "D:\\Archives\\b.efd"));
    slowOpen.resolve(stat(9));
    await first;
    await second;
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(store.current).toBe("fresh"); // not the stale one's id
    expect(store.route).toBe("archive");
    expect(store.notices.length).toBe(0); // and it said nothing
    expect(m.Leave).toHaveBeenCalledWith("stale"); // its mount went back
  });

  // A → B → A with the leave of the first A still travelling: it would
  // reach the core after the second A had been opened and close the
  // archive the page is showing (the second review's finding 4).
  it("waits for the leave before the next open, so A → B → A leaves A open", async () => {
    await booted();
    const order: string[] = [];
    const leaveA = deferred<void>();
    m.Leave.mockImplementation((id: string) => {
      order.push(`leave:${id}`);
      return id === "A" ? leaveA.promise : Promise.resolve();
    });
    m.Open.mockImplementation((id: string) => {
      order.push(`open:${id}`);
      return Promise.resolve(stat(1));
    });
    m.OpenPath.mockResolvedValueOnce({ archiveId: "A", relocated: false });
    await store.openFromShell(request(1, "D:\\Archives\\a.efd"));
    m.OpenPath.mockResolvedValueOnce({ archiveId: "B", relocated: false });
    const second = store.openFromShell(request(2, "D:\\Archives\\b.efd"));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    // B cannot be opened while the leave of A is unanswered.
    expect(order).toEqual(["open:A", "leave:A"]);
    leaveA.resolve();
    await second;
    m.OpenPath.mockResolvedValueOnce({ archiveId: "A", relocated: false });
    await store.openFromShell(request(3, "D:\\Archives\\a.efd"));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(order).toEqual(["open:A", "leave:A", "open:B", "leave:B", "open:A"]);
    expect(store.current).toBe("A");
  });

  // The core mounts the archive before the page waits for the leave of
  // the one it is showing: a request overtaken inside that wait must
  // still give its mount back (the third review's finding 2).
  it("gives back the mount of a request overtaken while the displayed archive was being left", async () => {
    await booted();
    const order: string[] = [];
    const leaveA = deferred<void>();
    m.Open.mockImplementation((id: string) => {
      order.push(`open:${id}`);
      return Promise.resolve(stat(1));
    });
    m.Leave.mockImplementation((id: string) => {
      order.push(`leave:${id}`);
      return id === "A" ? leaveA.promise : Promise.resolve();
    });
    m.OpenPath.mockResolvedValueOnce({ archiveId: "A", relocated: false });
    await store.openFromShell(request(1, "D:\\Archives\\a.efd"));
    expect(store.current).toBe("A");
    // B: the core mounts it, and then the leave of A is waited for.
    m.OpenPath.mockResolvedValueOnce({ archiveId: "B", relocated: false });
    const second = store.openFromShell(request(2, "D:\\Archives\\b.efd"));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(order).toEqual(["open:A", "leave:A"]);
    // C arrives inside that wait.
    m.OpenPath.mockResolvedValueOnce({ archiveId: "C", relocated: false });
    const third = store.openFromShell(request(3, "D:\\Archives\\c.efd"));
    leaveA.resolve();
    await second;
    await third;
    for (let i = 0; i < 15; i++) await Promise.resolve();
    // B's mount went back before C was opened, and C is what is shown.
    expect(order).toEqual(["open:A", "leave:A", "leave:B", "open:C"]);
    expect(store.current).toBe("C");
  });

  // The stale cleanup's own leave is waited for in the chain, or a newer
  // request opening the same archive could finish first and the leave
  // arrive afterwards, closing what the page is showing (the third
  // review's finding 3).
  it("waits for the stale mount to go back before the next open of the same archive", async () => {
    await booted();
    const order: string[] = [];
    const openA = deferred<ArchiveStat>();
    const leaveA = deferred<void>();
    let firstOpen = true;
    m.Open.mockImplementation((id: string) => {
      order.push(`open:${id}`);
      if (id === "A" && firstOpen) {
        firstOpen = false;
        return openA.promise;
      }
      return Promise.resolve(stat(1));
    });
    m.Leave.mockImplementation((id: string) => {
      order.push(`leave:${id}`);
      return id === "A" ? leaveA.promise : Promise.resolve();
    });
    m.OpenPath.mockResolvedValueOnce({ archiveId: "A", relocated: false });
    const first = store.openFromShell(request(1, "D:\\Archives\\a.efd"));
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(order).toEqual(["open:A"]); // with the core, unanswered
    // A newer request — for the very same archive — is accepted while
    // that Open is still in flight.
    m.OpenPath.mockResolvedValueOnce({ archiveId: "A", relocated: false });
    const second = store.openFromShell(request(2, "D:\\Archives\\a-again.efd"));
    openA.resolve(stat(1));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    // The stale mount is going back, and nothing has been opened again.
    expect(order).toEqual(["open:A", "leave:A"]);
    leaveA.resolve();
    await first;
    await second;
    for (let i = 0; i < 15; i++) await Promise.resolve();
    expect(order).toEqual(["open:A", "leave:A", "open:A"]);
    expect(store.current).toBe("A");
  });

  it("drops an overtaken refusal too: no dialog from a request the page has moved past", async () => {
    await booted();
    const slow = deferred<{ archiveId: string; relocated: boolean }>();
    m.OpenPath.mockReturnValueOnce(slow.promise);
    m.OpenPath.mockResolvedValueOnce({ archiveId: "newer", relocated: false });
    const first = store.openFromShell(request(1, "D:\\Archives\\slow.efd"));
    // The older request is with the core before the newer one is
    // accepted: its refusal is on its way back, not still in the queue.
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(m.OpenPath).toHaveBeenCalledWith("D:\\Archives\\slow.efd");
    const second = store.openFromShell(request(2, "D:\\Archives\\quick.efd"));
    slow.reject(refusal("archive.key_not_in_vault"));
    await first;
    await second;
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(store.keyNotInVault).toBeNull();
    expect(store.current).toBe("newer");
  });

  // A dialog is about the file it was raised for: a new request is the
  // user asking for something else (the review's finding 8).
  it("clears the dialog and the waiting path the previous request left", async () => {
    await booted();
    m.OpenPath.mockRejectedValueOnce(refusal("archive.key_not_in_vault"));
    await store.openFromShell(request(1));
    expect(store.keyNotInVault).toBe(FILE);
    m.OpenPath.mockResolvedValueOnce({ archiveId: "a", relocated: false });
    await store.openFromShell(request(2, "D:\\Archives\\other.efd"));
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(store.keyNotInVault).toBeNull();
    expect(store.openWhenUnlocked).toBe("");
    expect(store.current).toBe("a");
  });

  // archive.open_elsewhere carries the file the handle is on, and the
  // toast names it (APP.md §14, the review's finding 1).
  it("names the open file when the archive is already open from another copy", async () => {
    await booted();
    const open = "D:\\Archives\\photos.efd";
    m.OpenPath.mockRejectedValueOnce(Object.assign(new Error("archive.open_elsewhere"), { cause: { code: "archive.open_elsewhere", path: open } }));
    await store.openFromShell(request(1, "D:\\Downloads\\photos (1).efd"));
    expect(store.keyNotInVault).toBeNull();
    expect(store.notices.length).toBe(1);
    expect(store.notices[0].text).toBe(`This archive is already open from ${open}. Close it there first.`);
  });

  it("says so and stops when the retry after an unlock is refused again", async () => {
    await booted("locked");
    m.OpenPath.mockRejectedValueOnce(refusal("vault.needs_unlock"));
    await store.openFromShell(request(1));
    expect(store.openWhenUnlocked).toBe(FILE);
    m.OpenPath.mockRejectedValueOnce(refusal("vault.needs_unlock"));
    m.handlers["vault.state"]({ data: status(2, "unlocked") });
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(store.openWhenUnlocked).toBe(""); // not armed for a second unlock
    expect(store.notices.length).toBe(1);
  });

  it("puts up the key's dialog when no record here holds the archive", async () => {
    await booted();
    m.OpenPath.mockRejectedValueOnce(refusal("archive.key_not_in_vault"));
    await store.openFromShell(request(1));
    expect(store.keyNotInVault).toBe(FILE);
    expect(store.route).toBe("archives"); // nowhere new: nothing was opened
    expect(store.notices.length).toBe(0);
    // Its one action hands Import records… to the Archives page.
    store.askImportRecords();
    expect(store.keyNotInVault).toBeNull();
    expect(store.importRecordsAsked).toBe(true);
    expect(store.route).toBe("archives");
  });

  it("toasts a file that is not an archive, and every other refusal", async () => {
    await booted();
    m.OpenPath.mockRejectedValueOnce(refusal("archive.not_an_archive"));
    await store.openFromShell(request(1));
    expect(store.keyNotInVault).toBeNull();
    expect(store.notices.length).toBe(1);
    expect(store.notices[0].kind).toBe("error");
    m.OpenPath.mockRejectedValueOnce(refusal("archive.forgotten"));
    await store.openFromShell(request(2));
    expect(store.notices.length).toBe(2);
  });

  it("asks for nothing when the launch carried no file", async () => {
    await booted();
    expect(m.PendingOpen).toHaveBeenCalledTimes(1);
    expect(m.OpenPath).not.toHaveBeenCalled();
    expect(store.route).toBe("archives");
  });
});

// New archive… ends on the new archive's empty root with the Add menu open
// (APP.md §13, ruled 2026-10-14): the store holds the ask, and the page of
// that archive takes it once as it mounts.
describe("a created archive opened with the Add menu asked for (openNew)", () => {
  const refusal = (code: string) => Object.assign(new Error(code), { cause: { code } });

  it("opens it on its root and asks for the menu once — a later visit opens nothing", async () => {
    m.Page.mockResolvedValueOnce(reply(1, [], 0));
    expect(await store.openNew("a")).toBe(true);
    expect(m.Open).toHaveBeenCalledWith("a");
    expect(store.route).toBe("archive");
    expect(store.current).toBe("a");
    expect(store.dirId).toBe(ROOT);
    expect(store.takeAddAsked("a")).toBe(true);
    expect(store.takeAddAsked("a")).toBe(false);
  });

  it("asks for nothing when the open is refused", async () => {
    m.Open.mockRejectedValueOnce(refusal("vault.locked"));
    expect(await store.openNew("a")).toBe(false);
    expect(store.route).toBe("archives");
    expect(store.addAsked).toBeNull();
    expect(store.takeAddAsked("a")).toBe(false);
  });

  it("is spent by any page that takes it, so another archive never inherits it", async () => {
    m.Page.mockResolvedValueOnce(reply(1, [], 0));
    await store.openNew("a");
    expect(store.takeAddAsked("b")).toBe(false);
    expect(store.takeAddAsked("a")).toBe(false);
    expect(store.takeAddAsked("")).toBe(false);
  });
});

// The Tampered banner's *Replace with a copy…* on a page (direction 9,
// ruled 2026-10-18): the vault is locked first, the route is the lock
// screen, and the lock screen takes the ask once.
describe("replacing a tampered vault from a page (askReplaceVault)", () => {
  const refusal = (code: string) => Object.assign(new Error(code), { cause: { code } });

  it("locks an unlocked vault, goes to the lock screen, and is taken once", async () => {
    store.status = status(1, "unlocked");
    store.go("keys");
    await store.askReplaceVault();
    expect(m.Lock).toHaveBeenCalledTimes(1);
    expect(store.route).toBe("lock");
    expect(store.takeReplaceAsked()).toBe(true);
    expect(store.takeReplaceAsked()).toBe(false);
  });

  // A lock of a Locked vault still runs its every-state half in the core
  // (lockLocked: the token.reset_failed notice, a pending touch, a merge's
  // records), so Settings opened from the lock screen's gear asks for no
  // second lock: the route alone ends the visit.
  it("asks a Locked vault for no lock: it ends the gear's Settings visit and goes to the lock screen", async () => {
    store.status = status(1, "locked");
    store.openSettingsLocked();
    await store.askReplaceVault();
    expect(m.Lock).not.toHaveBeenCalled();
    expect(store.route).toBe("lock");
    expect(store.settingsLocked).toBe(false);
    expect(store.takeReplaceAsked()).toBe(true);
  });

  it("is withdrawn when the route leaves the lock screen before it is taken", async () => {
    store.status = status(1, "locked");
    await store.askReplaceVault();
    store.go("archives");
    expect(store.replaceAsked).toBe(false);
    expect(store.takeReplaceAsked()).toBe(false);
  });

  it("asks for nothing and stays where it was when the lock is refused", async () => {
    store.status = status(1, "unlocked");
    store.go("settings");
    m.Lock.mockRejectedValueOnce(refusal("internal"));
    await store.askReplaceVault();
    expect(store.route).toBe("settings");
    expect(store.replaceAsked).toBe(false);
    expect(store.notices.length).toBe(1);
  });
});

// The locked banners' and the lock screen's close (direction 9): one
// archive by its id, or every open one; the page that showed a closed
// archive is dropped as closed, so nothing is left to Leave.
describe("closing what a lock left open (closeOpened)", () => {
  it("closes the one archive and drops its page when it is the one shown", async () => {
    await open(["x"]);
    store.go("lock");
    await store.closeOpened("a");
    expect(m.Close).toHaveBeenCalledWith("a");
    expect(m.Leave).not.toHaveBeenCalled();
    expect(store.current).toBeNull();
    expect(m.List).toHaveBeenCalled();
  });

  it("keeps the page of another archive", async () => {
    await open(["x"]);
    await store.closeOpened("b");
    expect(store.current).toBe("a");
  });

  it("closes them all, and says so when one could not be closed", async () => {
    await open(["x"]);
    m.CloseAll.mockResolvedValueOnce(["a"]);
    await store.closeOpened();
    expect(m.CloseAll).toHaveBeenCalledTimes(1);
    // the archive still open keeps its page
    expect(store.current).toBe("a");
    expect(store.notices.at(-1)?.kind).toBe("error");
    await store.closeOpened();
    expect(store.current).toBeNull();
  });
});

// The backups card's status is read again when the archives change: a
// create, a re-key or a forget moves its counts (direction 9).
describe("the backup status", () => {
  it("is read again on archives.changed", async () => {
    store.boot();
    m.BackupStatus.mockResolvedValueOnce({ at: 5, archives: 2, missing: 1, rekeyed: 0 });
    m.handlers["archives.changed"]({ data: { purged: [] } });
    await vi.waitFor(() => expect(store.backup).toEqual({ at: 5, archives: 2, missing: 1, rekeyed: 0 }));
  });

  it("is dropped when the state changes, so no count of nothing reads as current", async () => {
    store.boot();
    m.handlers["vault.state"]({ data: status(1, "unlocked") });
    store.backup = { at: 5, archives: 2, missing: 0, rekeyed: 0 };
    // The lock's read is held back: until it lands the card says the date alone.
    m.BackupStatus.mockReturnValue(new Promise(() => {}));
    m.handlers["vault.state"]({ data: status(2, "locked") });
    expect(store.backup).toBeNull();
  });

  it("is no status when the call is refused", async () => {
    m.BackupStatus.mockRejectedValueOnce(new Error("internal"));
    await store.refreshBackup();
    expect(store.backup).toBeNull();
  });
});

// The password chosen in the create, restore and finish-setup dialogs
// (APP.md §6, ruled 2026-10-17): held by the store from the dialog's
// Continue, sent at once on the ceremony's first Choose password prompt of
// that kind, and dropped at every other end.
describe("the dialog's chosen password, held for the Choose prompt (holdChosen)", () => {
  const event = (seq: number, kind: string, step: string, promptId = "", choose = false, error = "") =>
    m.handlers["vault.ceremony"]({ data: { seq, kind, step, promptId, choose, error } });
  const sent = () => m.invoke.mock.calls.map((c) => c[0]);

  beforeEach(() => {
    m.Status.mockReturnValue(new Promise(() => {}));
    store.boot();
  });

  it("answers the create's first Choose prompt at once, and only that one", () => {
    store.holdChosen("create", "long enough");
    event(2, "create", "");
    expect(sent()).toEqual([]);
    event(3, "create", "password", "p1", true);
    expect(sent()).toEqual(["secret password p1 long enough"]);
    expect(store.chosenSent).toBe("p1");
    // Spent: a second Choose prompt (the first refused, asked anew) is
    // the panel's password step's to ask.
    event(4, "create", "password", "p2", true);
    expect(sent()).toHaveLength(1);
  });

  it("waits through an import's recovery key for the Choose prompt after it", () => {
    store.holdChosen("import", "long enough");
    event(2, "import", "recovery", "r1");
    expect(sent()).toEqual([]);
    event(3, "import", "deriving");
    event(4, "import", "password", "p1", true);
    expect(sent()).toEqual(["secret password p1 long enough"]);
  });

  it("answers from a status that carries the prompt as well as from the event", () => {
    store.holdChosen("setup", "long enough");
    m.handlers["vault.state"]({
      data: { ...status(5, "unlocking"), ceremony: { seq: 6, kind: "setup", step: "password", promptId: "p1", choose: true } },
    });
    expect(sent()).toEqual(["secret password p1 long enough"]);
    event(6, "setup", "password", "p1", true); // the same event, already applied
    expect(sent()).toHaveLength(1);
  });

  it("never answers another ceremony's prompt, nor a password asked to prove one", () => {
    store.holdChosen("create", "long enough");
    event(2, "entangle", "password", "p1", true);
    event(3, "unlock", "password", "p2", false);
    event(4, "create", "password", "p3", false);
    expect(sent()).toEqual([]);
    event(5, "create", "password", "p4", true);
    expect(sent()).toEqual(["secret password p4 long enough"]);
  });

  it("drops it unsent when the ceremony ends before the prompt — failed, cancelled, blocked", () => {
    for (const [i, end] of [["failed", "token.no_service"], ["failed", "ceremony.cancelled"], ["blocked", ""]].entries()) {
      store.holdChosen("import", "long enough");
      event(10 * i + 2, "import", end[0], "", false, end[1]);
      event(10 * i + 3, "import", "password", "p1", true);
    }
    expect(sent()).toEqual([]);
  });

  it("drops it when the call that would start the ceremony is refused, or the panel is dismissed", () => {
    store.holdChosen("create", "long enough");
    store.dropChosen();
    event(2, "create", "password", "p1", true);
    store.holdChosen("create", "long enough");
    store.dismissCeremony();
    event(3, "create", "password", "p2", true);
    expect(sent()).toEqual([]);
  });

  it("holds nothing for a YubiKey without the tick", () => {
    store.holdChosen("create", "");
    event(2, "create", "password", "p1", true);
    expect(sent()).toEqual([]);
  });

  it("gives the prompt back to card 2 when the core refuses the answer, and forgets it at the end", () => {
    store.holdChosen("create", "long enough");
    event(2, "create", "password", "p1", true);
    expect(store.chosenSent).toBe("p1");
    m.handlers["secret.refused"]({ data: { code: "vault.password_short" } });
    expect(store.chosenSent).toBe("");
    store.holdChosen("create", "long enough");
    event(3, "create", "password", "p2", true);
    expect(store.chosenSent).toBe("p2");
    event(4, "create", "done");
    expect(store.chosenSent).toBe("");
  });
});

// Delete archive… on the Archive page is asked over that page, and the
// archive is let go of only on consent (APP.md §13, ruled 2026-10-14). Its
// dialog stands over the page until it closes, so nothing more is read of
// the archive meanwhile: a Stat would answer archive.not_open and drop the
// page, and the dialog with it, mid-act.
describe("an archive let go of for Delete archive… (releasedForDelete)", () => {
  const cancelled = { id: "op1", archiveId: "a", kind: "add", items: 1, finished: true, error: "op.cancelled", results: [] } as unknown as OpView;

  it("reads nothing more of it while the dialog is up — not an op's end, not archive.changed", async () => {
    vi.useFakeTimers();
    m.Status.mockReturnValue(new Promise(() => {}));
    store.boot();
    await open(["x"]);
    store.route = "archive";
    store.releasedForDelete = "a";
    m.handlers["archive.changed"]({ data: { id: "a", seq: 5 } });
    m.handlers["op.done"]({ data: cancelled }); // the op the close cancelled
    await store.refreshArchive();
    expect(m.Stat).not.toHaveBeenCalled();
    expect(store.current).toBe("a"); // the page, and the dialog over it, stay
    expect(store.route).toBe("archive");
  });

  it("is over when the page is left: the dialog's leave, a fresh list, and the archive read again on its next visit", async () => {
    await open(["x"]);
    store.route = "archive";
    store.releasedForDelete = "a";
    m.List.mockClear();
    store.leaveArchive(); // the dialog's close (a route change leaves nothing)
    expect(store.current).toBeNull();
    expect(store.releasedForDelete).toBeNull();
    expect(m.Leave).toHaveBeenCalledWith("a");
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(m.List).toHaveBeenCalled();
    await open(["x"]);
    m.Stat.mockResolvedValueOnce(stat(2));
    m.Page.mockResolvedValueOnce(reply(2, ["x"], 1));
    await store.refreshArchive();
    expect(m.Stat).toHaveBeenCalledWith("a");
  });
});

// Whether the running ceremony has left its opening WaitingForKey (APP.md
// §6, ruled 2026-10-17): followed on every event the store accepts, so a
// create's SwapKey and the WaitingForKey after it, dispatched in one tick,
// still leave the lock screen drawing the wait for the YubiKey.
describe("the ceremony's opening step, passed (ceremonyPassed)", () => {
  const event = (seq: number, kind: string, step: string, method = "") =>
    m.handlers["vault.ceremony"]({ data: { seq, kind, step, method, promptId: "", choose: false, error: "" } });

  beforeEach(() => {
    m.Status.mockReturnValue(new Promise(() => {}));
    store.boot();
  });

  it("is passed once any step but the opening WaitingForKey is seen, back to back", () => {
    event(2, "create", "waiting_for_key");
    expect(store.ceremonyPassed.passed).toBe(false);
    event(3, "create", "swap_key");
    event(4, "create", "waiting_for_key");
    expect(store.ceremonyPassed).toMatchObject({ kind: "create", passed: true });
  });

  it("starts afresh after the end, a cancel and a dismissal", () => {
    event(2, "setup", "recovery", "recovery");
    event(3, "setup", "failed");
    expect(store.ceremonyPassed.passed).toBe(false);
    event(4, "unlock", "pin", "token");
    expect(store.ceremonyPassed.passed).toBe(true);
    store.dismissCeremony();
    expect(store.ceremonyPassed.passed).toBe(false);
  });
});

// Leaving is explicit (APP.md §2.3, ruled 2026-10-18): a route change keeps
// the archive open and mounted, and coming back shows the same folder,
// sort and selection, read again from the core. It is left by its rail
// row's ×, the head's Close, opening another, Delete archive… and the
// window closing.
describe("an archive kept open across pages (explicit leave)", () => {
  const D = "d".repeat(32);
  const sorted = { key: "size", desc: true, nameDesc: false } as const;

  // In folder D of archive "a", sorted by size, "x" selected, on its page.
  async function inFolder() {
    store.current = "a";
    store.route = "archive";
    store.dirId = D;
    m.Page.mockResolvedValueOnce(reply(1, ["x", "y"], 2, [{ id: ROOT, name: "A" }, { id: D, name: "Trips" }]));
    await store.loadPage();
    store.sort = { ...sorted };
    store.selectClick("x");
  }
  const settle = async () => {
    for (let i = 0; i < 10; i++) await Promise.resolve();
  };

  it("keeps the archive, its folder, sort and selection through Vault and Settings", async () => {
    await inFolder();
    store.go("settings");
    store.go("keys");
    store.go("archives");
    await settle();
    expect(m.Leave).not.toHaveBeenCalled();
    expect(store.current).toBe("a");
    expect(store.dirId).toBe(D);
    expect(store.sort).toEqual(sorted);
    expect([...store.sel.ids]).toEqual(["x"]);
  });

  it("asks the core for the same folder under the same sort when the page comes back", async () => {
    await inFolder();
    store.go("settings");
    m.Stat.mockResolvedValueOnce(stat(2));
    m.Page.mockResolvedValueOnce(reply(2, ["x", "y", "z"], 3, [{ id: ROOT, name: "A" }, { id: D, name: "Trips" }]));
    store.go("archive");
    await settle();
    expect(store.route).toBe("archive");
    expect(m.Stat).toHaveBeenCalledWith("a");
    expect(m.Page).toHaveBeenLastCalledWith("a", D, store.sortBy, 0, expect.any(Number));
    expect(store.page?.rows.map((r) => r.id)).toEqual(["x", "y", "z"]);
    expect([...store.sel.ids]).toEqual(["x"]);
  });

  it("shows the archive already open on its row's Open, without opening it again or starting at the root", async () => {
    await inFolder();
    store.go("archives");
    m.Stat.mockResolvedValueOnce(stat(1));
    m.Page.mockResolvedValueOnce(reply(1, ["x", "y"], 2, [{ id: ROOT, name: "A" }, { id: D, name: "Trips" }]));
    expect(await store.openArchive("a")).toBe(true);
    expect(m.Open).not.toHaveBeenCalled();
    expect(store.route).toBe("archive");
    expect(store.dirId).toBe(D);
    expect(store.sort).toEqual(sorted);
  });

  it("leaves the archive open when another is opened over it — once the new one is mounted", async () => {
    await inFolder();
    store.go("archives");
    const order: string[] = [];
    m.Open.mockImplementationOnce(async (id: string) => {
      order.push(`open:${id}`);
      return stat(1);
    });
    m.Leave.mockImplementationOnce(async (id: string) => {
      order.push(`leave:${id}`);
    });
    m.Page.mockResolvedValueOnce(reply(1, [], 0));
    expect(await store.openArchive("b")).toBe(true);
    expect(order).toEqual(["open:b", "leave:a"]);
    expect(store.current).toBe("b");
    expect(store.dirId).toBe(ROOT);
    expect(store.route).toBe("archive");
  });

  it("closes by its Close: left, and back to the list when the core holds it no more", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [] });
    await store.closeArchive("a");
    expect(m.Leave).toHaveBeenCalledWith("a");
    expect(store.current).toBeNull();
    expect(store.draining).toBeNull();
    expect(store.route).toBe("archives");
  });

  it("closes a second open archive by its rail row without touching the page's", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }] });
    await store.closeArchive("b");
    expect(m.Leave).toHaveBeenCalledWith("b");
    expect(store.current).toBe("a");
    expect(store.draining).toBeNull();
    expect(store.route).toBe("archive");
  });

  it("stands draining while the core still names it, reads nothing meanwhile, and goes when the status lets it go", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }] });
    await store.closeArchive("a");
    expect(store.draining).toBe("a");
    expect(store.current).toBe("a");
    expect(store.route).toBe("archive");
    m.Stat.mockClear();
    await store.refreshArchive();
    expect(m.Stat).not.toHaveBeenCalled();
    // The core's next status, as the vault.state event carries it.
    (store as unknown as { applyStatus(s: VaultStatus): void }).applyStatus({ ...status(6, "unlocked"), opened: [] } as unknown as VaultStatus);
    expect(store.draining).toBeNull();
    expect(store.current).toBeNull();
    expect(store.route).toBe("archives");
  });

  it("goes as soon as the list says the core closed it, before a status does", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }] });
    await store.closeArchive("a");
    expect(store.draining).toBe("a");
    m.List.mockResolvedValueOnce([{ id: "a", name: "A", path: "D:/a.efd", open: false }]);
    await store.refreshArchives();
    expect(store.draining).toBeNull();
    expect(store.current).toBeNull();
  });

  it("mounts a draining archive again on Open, where it stood", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }] });
    await store.closeArchive("a");
    store.go("archives");
    m.Stat.mockResolvedValueOnce(stat(2));
    m.Page.mockResolvedValueOnce(reply(2, ["x", "y"], 2, [{ id: ROOT, name: "A" }, { id: D, name: "Trips" }]));
    expect(await store.openArchive("a")).toBe(true);
    await settle();
    expect(m.Open).toHaveBeenCalledWith("a");
    expect(store.draining).toBeNull();
    expect(store.dirId).toBe(D);
    expect([...store.sel.ids]).toEqual(["x"]);
  });

  // The review of 2026-10-18: a kill switch that did not go through the
  // store (the inspector's, as it was) left store.current naming a closed
  // handle — a rail row for it, and an Open that showed a page bouncing
  // back to the list.
  it("lets the page's archive go once the list says the core closed it outside the store", async () => {
    await inFolder();
    store.go("archives");
    m.List.mockResolvedValueOnce([{ id: "a", name: "A", path: "D:/a.efd", open: false }]);
    await store.refreshArchives(); // archives.changed after Archives.Close
    expect(store.current).toBeNull();
    expect(railChildren(store.current, "A", [])).toEqual([]);
    m.Page.mockResolvedValueOnce(reply(1, [], 0));
    expect(await store.openArchive("a")).toBe(true);
    expect(m.Open).toHaveBeenCalledWith("a");
    expect(store.current).toBe("a");
    expect(store.dirId).toBe(ROOT);
  });

  it("lets it go once a status that named it stops naming it", async () => {
    await inFolder();
    store.go("archives");
    const apply = (s: unknown) => (store as unknown as { applyStatus(s: VaultStatus): void }).applyStatus(s as VaultStatus);
    apply({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }] });
    expect(store.current).toBe("a");
    apply({ ...status(6, "unlocked"), opened: [] });
    expect(store.current).toBeNull();
  });

  it("does not take a status that never named it, or a list read before an open landed, as a close", async () => {
    await inFolder();
    const apply = (s: unknown) => (store as unknown as { applyStatus(s: VaultStatus): void }).applyStatus(s as VaultStatus);
    apply({ ...status(5, "unlocked"), opened: [] });
    expect(store.current).toBe("a");
    // A list asked for while an Open of "a" travels, answered after it.
    let answer: (v: unknown) => void = () => {};
    m.List.mockReturnValueOnce(new Promise((r) => (answer = r)));
    const listed = store.refreshArchives();
    m.Stat.mockResolvedValueOnce(stat(1));
    m.Page.mockResolvedValueOnce(reply(1, ["x", "y"], 2, [{ id: ROOT, name: "A" }, { id: D, name: "Trips" }]));
    store.archives = [{ id: "a", name: "A", path: "D:/a.efd", open: false } as ArchiveSummary];
    const opened = store.openArchive("a");
    answer([{ id: "a", name: "A", path: "D:/a.efd", open: false }]);
    await listed;
    await opened;
    expect(store.current).toBe("a");
  });

  it("keeps an archive let go of for Delete archive… under its dialog", async () => {
    await inFolder();
    store.releasedForDelete = "a";
    m.List.mockResolvedValueOnce([{ id: "a", name: "A", path: "D:/a.efd", open: false }]);
    await store.refreshArchives();
    expect(store.current).toBe("a");
    expect(store.route).toBe("archive");
  });

  it("holds another archive a Close left still held as lingering, until the status lets it go", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }, { id: "b", name: "B" }] });
    await store.closeArchive("b");
    expect(store.leftHeld("b")).toBe(true);
    expect(store.current).toBe("a");
    (store as unknown as { applyStatus(s: VaultStatus): void }).applyStatus({ ...status(6, "unlocked"), opened: [{ id: "a", name: "A" }] } as unknown as VaultStatus);
    expect(store.leftHeld("b")).toBe(false);
    expect(store.current).toBe("a");
  });

  it("forgets the lingering mark when the kill switch closes it", async () => {
    await inFolder();
    m.Status.mockResolvedValueOnce({ ...status(5, "unlocked"), opened: [{ id: "a", name: "A" }, { id: "b", name: "B" }] });
    await store.closeArchive("b");
    await store.closeOpened("b");
    expect(m.Close).toHaveBeenCalledWith("b");
    expect(store.leftHeld("b")).toBe(false);
    expect(store.current).toBe("a");
  });
});

// Phase 4a (direction 3): getting back to where you were. The archive
// opened last is remembered per vault in memory only, the list focuses
// its row after an unlock, a file opened from Explorer while locked can be
// forgotten from the lock screen, an Import-archives merge reached from
// such a file opens it once it succeeds, and View › Preview pane writes
// Settings.previewPane through the whole-struct Set.
describe("getting back to the archive (direction 3, phase 4a)", () => {
  const VAULT = "D:/Vaults/personal.eks";
  const FILE = "D:\Archives\holiday.efd";
  const at = (seq: number, state: string, path = VAULT) => ({ ...status(seq, state), path }) as unknown as VaultStatus;
  const refusal = (code: string) => Object.assign(new Error(code), { cause: { code } });
  const settle = async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  };

  async function bootAt(state: string) {
    m.Status.mockResolvedValueOnce(at(1, state));
    m.Page.mockResolvedValue(reply(1, [], 0));
    store.boot();
    await settle();
  }

  async function openThenClose() {
    await store.openArchive("a");
    m.Status.mockResolvedValueOnce(at(2, "unlocked"));
    await store.closeArchive("a");
    expect(store.current).toBeNull();
  }

  it("remembers the archive opened last and asks the list to focus it after an unlock", async () => {
    await bootAt("unlocked");
    await openThenClose();
    expect(store.remembered).toBe("a");
    m.handlers["vault.state"]({ data: at(3, "locked") });
    expect(store.focusArchiveAsked).toBeNull();
    m.handlers["vault.state"]({ data: at(4, "unlocked") });
    expect(store.route).toBe("archives");
    expect(store.focusArchiveAsked).toBe("a");
    // Taken once.
    expect(store.takeFocusAsked()).toBe("a");
    expect(store.takeFocusAsked()).toBeNull();
  });

  it("asks nothing when the archive is still open: the unlock goes back to its page", async () => {
    await bootAt("unlocked");
    await store.openArchive("a");
    m.handlers["vault.state"]({ data: at(3, "locked") });
    m.handlers["vault.state"]({ data: at(4, "unlocked") });
    expect(store.current).toBe("a");
    expect(store.focusArchiveAsked).toBeNull();
  });

  it("asks nothing before anything was opened in this process", async () => {
    await bootAt("unlocked");
    m.handlers["vault.state"]({ data: at(3, "locked") });
    m.handlers["vault.state"]({ data: at(4, "unlocked") });
    expect(store.remembered).toBe("");
    expect(store.focusArchiveAsked).toBeNull();
  });

  it("drops what it remembered when the vault is another one", async () => {
    await bootAt("unlocked");
    await openThenClose();
    m.handlers["vault.state"]({ data: at(3, "locked", "E:/Other/vault.eks") });
    m.handlers["vault.state"]({ data: at(4, "unlocked", "E:/Other/vault.eks") });
    expect(store.remembered).toBe("");
    expect(store.focusArchiveAsked).toBeNull();
    // Back on the first vault, nothing comes back: it was forgotten.
    m.handlers["vault.state"]({ data: at(5, "unlocked") });
    expect(store.remembered).toBe("");
  });

  it("forgets a file opened from Explorer while locked, so the unlock opens nothing", async () => {
    await bootAt("locked");
    m.OpenPath.mockRejectedValueOnce(refusal("vault.needs_unlock"));
    await store.openFiles([FILE]);
    await settle();
    expect(store.openWhenUnlocked).toBe(FILE);
    store.forgetOpenWhenUnlocked();
    expect(store.openWhenUnlocked).toBe("");
    m.handlers["vault.state"]({ data: at(2, "unlocked") });
    await settle();
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
  });

  it("opens the file that led to an Import-archives merge once the merge succeeds", async () => {
    await bootAt("unlocked");
    m.OpenPath.mockRejectedValueOnce(refusal("archive.key_not_in_vault"));
    await store.openFiles([FILE]);
    await settle();
    expect(store.keyNotInVault).toBe(FILE);
    store.askImportRecords();
    m.OpenPath.mockResolvedValueOnce({ archiveId: "a", relocated: false });
    store.mergeDone();
    await settle();
    expect(m.OpenPath).toHaveBeenCalledTimes(2);
    expect(m.OpenPath).toHaveBeenLastCalledWith(FILE);
    expect(store.current).toBe("a");
    // Once: a later merge by hand opens nothing.
    store.mergeDone();
    await settle();
    expect(m.OpenPath).toHaveBeenCalledTimes(2);
  });

  it("opens nothing after a merge when the import was abandoned first", async () => {
    await bootAt("unlocked");
    m.OpenPath.mockRejectedValueOnce(refusal("archive.key_not_in_vault"));
    await store.openFiles([FILE]);
    await settle();
    store.askImportRecords();
    store.forgetImport(); // the picker closed with nothing, or the merge dialog did
    store.mergeDone();
    await settle();
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
  });

  it("writes the preview pane through the whole settings, changing nothing else", async () => {
    const saved = { vaultPath: VAULT, displayName: "Personal vault", closeAction: "ask", theme: "dark", previewPane: true, idleMinutes: 5 };
    m.SettingsGet.mockReset().mockResolvedValueOnce(saved).mockResolvedValueOnce({ ...saved, previewPane: false });
    store.settings = { ...saved } as never;
    expect(store.previewPane).toBe(true);
    const p = store.setPreviewPane(false);
    expect(store.previewPane).toBe(false); // shown at once
    await p;
    expect(m.SettingsSet).toHaveBeenCalledWith({ ...saved, previewPane: false });
    expect(store.previewPane).toBe(false);
  });

  it("shows the pane until the settings say otherwise", () => {
    store.settings = null;
    expect(store.previewPane).toBe(true);
  });
});

// Vault things in one place (direction 10, phase 4a): a choice applies the
// moment it is made, through one chain of whole-struct writes; the Vault
// page's test of a backup locks and hands the lock screen its ask; the
// merge dialog stands above every route and goes with a lock.
describe("choices that apply at once (applySettings, choose)", () => {
  const saved = { vaultPath: "D:/v.eks", displayName: "Personal vault", closeAction: "ask", theme: "system", previewPane: true, idleMinutes: 0, absoluteMinutes: 0, dictionaryBelow: 262144 };
  const refusal = (code: string) => Object.assign(new Error(code), { cause: { code } });
  const settle = async () => {
    for (let i = 0; i < 30; i++) await Promise.resolve();
  };

  it("shows the choice at once and writes the whole settings with only that field changed", async () => {
    m.SettingsGet.mockReset().mockResolvedValueOnce(saved).mockResolvedValueOnce({ ...saved, idleMinutes: 5 });
    store.settings = { ...saved } as typeof store.settings;
    const p = store.choose("idleMinutes", 5);
    expect(store.settings?.idleMinutes).toBe(5);
    expect(store.choosing.idleMinutes).toBeDefined();
    await p;
    expect(m.SettingsSet).toHaveBeenCalledWith({ ...saved, idleMinutes: 5 });
    expect(store.choosing.idleMinutes).toBeUndefined();
    expect(store.choiceRefused.idleMinutes).toBeUndefined();
    expect(store.notices.length).toBe(0); // a choice that applied is its own confirmation
  });

  it("puts the old value back and says the refusal under the control, with no toast", async () => {
    m.SettingsGet.mockReset().mockResolvedValue(saved);
    m.SettingsSet.mockRejectedValueOnce(refusal("params"));
    store.settings = { ...saved } as typeof store.settings;
    await store.choose("absoluteMinutes", 480);
    expect(store.settings?.absoluteMinutes).toBe(0);
    expect(store.choiceRefused.absoluteMinutes).toBeTruthy();
    expect(store.choosing.absoluteMinutes).toBeUndefined();
    expect(store.notices.length).toBe(0);
  });

  it("lands two quick choices in order, neither putting the other's field back", async () => {
    let core = { ...saved };
    m.SettingsGet.mockReset().mockImplementation(async () => ({ ...core }));
    m.SettingsSet.mockReset().mockImplementation(async (v: typeof saved) => {
      core = { ...v };
    });
    store.settings = { ...saved } as typeof store.settings;
    const a = store.choose("theme", "dark");
    const b = store.setPreviewPane(false);
    expect(store.settings?.theme).toBe("dark");
    expect(store.settings?.previewPane).toBe(false);
    await Promise.all([a, b]);
    expect(core.theme).toBe("dark");
    expect(core.previewPane).toBe(false);
    expect(store.settings?.theme).toBe("dark");
    expect(store.settings?.previewPane).toBe(false);
  });

  it("keeps a later choice shown while an earlier one is refused", async () => {
    let core = { ...saved };
    m.SettingsGet.mockReset().mockImplementation(async () => ({ ...core }));
    m.SettingsSet.mockReset()
      .mockImplementationOnce(async () => {
        throw refusal("io");
      })
      .mockImplementation(async (v: typeof saved) => {
        core = { ...v };
      });
    store.settings = { ...saved } as typeof store.settings;
    const a = store.choose("closeAction", "quit");
    const b = store.choose("theme", "light");
    await a;
    // The first is back to what the core holds; the second still shows.
    expect(store.settings?.closeAction).toBe("ask");
    expect(store.settings?.theme).toBe("light");
    expect(store.choiceRefused.closeAction).toBeTruthy();
    await b;
    expect(core.theme).toBe("light");
    expect(store.choiceRefused.theme).toBeUndefined();
  });

  it("says a refusal only for the latest choice of a control", async () => {
    let core = { ...saved };
    m.SettingsGet.mockReset().mockImplementation(async () => ({ ...core }));
    m.SettingsSet.mockReset()
      .mockImplementationOnce(async () => {
        throw refusal("io");
      })
      .mockImplementation(async (v: typeof saved) => {
        core = { ...v };
      });
    store.settings = { ...saved } as typeof store.settings;
    const a = store.choose("idleMinutes", 2);
    const b = store.choose("idleMinutes", 15);
    await Promise.all([a, b]);
    await settle();
    expect(store.choiceRefused.idleMinutes).toBeUndefined();
    expect(core.idleMinutes).toBe(15);
    expect(store.settings?.idleMinutes).toBe(15);
  });

  it("drops a refusal's line when the page changes", async () => {
    m.SettingsGet.mockReset().mockResolvedValue(saved);
    m.SettingsSet.mockRejectedValueOnce(refusal("params"));
    store.settings = { ...saved } as typeof store.settings;
    store.go("settings");
    await store.choose("theme", "dark");
    expect(store.choiceRefused.theme).toBeTruthy();
    store.go("keys");
    expect(store.choiceRefused.theme).toBeUndefined();
  });
});

describe("testing that a backup opens, from the Vault page (askTestBackup)", () => {
  const refusal = (code: string) => Object.assign(new Error(code), { cause: { code } });

  it("locks an unlocked vault, goes to the lock screen, and hands the path over once", async () => {
    store.status = status(1, "unlocked");
    store.go("keys");
    await store.askTestBackup("E:/backup.eks");
    expect(m.Lock).toHaveBeenCalledTimes(1);
    expect(store.route).toBe("lock");
    expect(store.takeTestAsked()).toBe("E:/backup.eks");
    expect(store.takeTestAsked()).toBe("");
  });

  it("asks a Locked vault for no lock", async () => {
    store.status = status(1, "locked");
    store.go("keys");
    await store.askTestBackup("E:/backup.eks");
    expect(m.Lock).not.toHaveBeenCalled();
    expect(store.route).toBe("lock");
    expect(store.testAsked).toBe("E:/backup.eks");
  });

  it("asks for nothing without a file", async () => {
    store.status = status(1, "unlocked");
    store.go("keys");
    await store.askTestBackup("");
    expect(m.Lock).not.toHaveBeenCalled();
    expect(store.route).toBe("keys");
  });

  it("is withdrawn when the route leaves the lock screen before it is taken", async () => {
    store.status = status(1, "locked");
    await store.askTestBackup("E:/backup.eks");
    store.go("keys");
    expect(store.takeTestAsked()).toBe("");
  });

  it("asks for nothing and stays where it was when the lock is refused", async () => {
    store.status = status(1, "unlocked");
    store.go("keys");
    m.Lock.mockRejectedValueOnce(refusal("internal"));
    await store.askTestBackup("E:/backup.eks");
    expect(store.route).toBe("keys");
    expect(store.testAsked).toBe("");
    expect(store.notices.length).toBe(1);
  });
});

describe("the summary's export, taken by the Vault page (askExport)", () => {
  it("goes to the Vault page and is taken once", () => {
    store.status = status(1, "unlocked");
    store.go("archives");
    store.askExport();
    expect(store.route).toBe("keys");
    expect(store.takeExportAsked()).toBe(true);
    expect(store.takeExportAsked()).toBe(false);
  });

  it("is withdrawn when the route leaves the Vault page before it is taken", () => {
    store.status = status(1, "unlocked");
    store.askExport();
    store.go("archives");
    expect(store.takeExportAsked()).toBe(false);
  });
});

describe("the merge dialog over every route, and the list's notes", () => {
  it("closes the merge dialog when the vault locks", async () => {
    m.Status.mockResolvedValueOnce(status(1, "unlocked"));
    store.boot();
    for (let i = 0; i < 20; i++) await Promise.resolve();
    store.merging = "E:/backup.eks";
    m.handlers["vault.state"]({ data: status(2, "locked") });
    expect(store.merging).toBe("");
  });

  // The Vault page's "Archive files:" line claims presence only on the
  // word of a pass of this unlock (lib/vaultfiles.ts): the answer is kept
  // while Unlocked and dropped when the vault leaves Unlocked, since the
  // presence it measured was the session's.
  it("keeps Find missing files' answer for this unlock only", async () => {
    m.Status.mockResolvedValueOnce(status(1, "unlocked"));
    store.boot();
    for (let i = 0; i < 20; i++) await Promise.resolve();
    expect(store.filesCheck).toBe(null);
    m.CheckFiles.mockResolvedValueOnce({ probed: 3, missing: 1 });
    expect(await store.checkFiles()).toEqual({ probed: 3, missing: 1 });
    expect(store.filesCheck).toEqual({ probed: 3, missing: 1 });
    m.handlers["vault.state"]({ data: status(2, "locked") });
    expect(store.filesCheck).toBe(null);
    // An answer that lands after a lock is not kept.
    m.CheckFiles.mockResolvedValueOnce({ probed: 3, missing: 0 });
    await store.checkFiles();
    expect(store.filesCheck).toBe(null);
  });

  // The name Rename… gave is the one shown (store.vaultName), even in the
  // moment before the core's status follows it.
  it("names the vault by its setting first, then its status", async () => {
    m.Status.mockResolvedValueOnce({ ...status(1, "unlocked"), displayName: "Personal vault", path: "C:/v.eks" });
    m.SettingsGet.mockResolvedValue({ displayName: "Personal vault" });
    store.boot();
    for (let i = 0; i < 40; i++) await Promise.resolve();
    expect(store.vaultName).toBe("Personal vault");
    m.SettingsGet.mockResolvedValue({ displayName: "Home vault" });
    m.SettingsSet.mockResolvedValueOnce(undefined);
    await store.applySettings({ displayName: "Home vault" });
    expect(store.status?.displayName).toBe("Personal vault");
    expect(store.vaultName).toBe("Home vault");
  });

  // Another vault file opened (a switch, an import) carries the name the
  // core wrote into the settings: they are read again, so the old
  // vault's name is not shown over the new one.
  it("reads the settings again when the vault file changes", async () => {
    m.Status.mockResolvedValueOnce({ ...status(1, "locked"), displayName: "Personal vault", path: "C:/v.eks" });
    m.SettingsGet.mockResolvedValue({ displayName: "Personal vault" });
    store.boot();
    for (let i = 0; i < 40; i++) await Promise.resolve();
    expect(store.vaultName).toBe("Personal vault");
    m.SettingsGet.mockResolvedValue({ displayName: "Travel vault" });
    m.handlers["vault.state"]({ data: { ...status(2, "locked"), displayName: "Travel vault", path: "E:/t.eks" } });
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(store.vaultName).toBe("Travel vault");
  });
});

// The InfoBars (direction 4, phase 4b; lib/infobar.ts): what toasts were.
// The store keeps them and their clock; the hosts only draw them.
describe("the InfoBars", () => {
  it("lets a success go after its lifetime and keeps an error", () => {
    vi.useFakeTimers();
    store.notice("Details copied", "success");
    store.notice("The disk is full.", "error");
    vi.advanceTimersByTime(3000 + 50 * 14 + 5);
    expect(store.notices.map((n) => n.kind)).toEqual(["error"]);
    vi.advanceTimersByTime(60_000);
    expect(store.notices).toHaveLength(1);
    store.dismissNotice(store.notices[0].id);
    expect(store.notices).toEqual([]);
  });

  it("stops the clock while the bars are hovered or the window is away", () => {
    vi.useFakeTimers();
    store.notice("Saved");
    vi.advanceTimersByTime(1000);
    store.pauseNotices({ hovered: true });
    vi.advanceTimersByTime(20_000);
    expect(store.notices).toHaveLength(1);
    store.pauseNotices({ hovered: false, away: true });
    vi.advanceTimersByTime(20_000);
    expect(store.notices).toHaveLength(1);
    store.pauseNotices({ away: false });
    vi.advanceTimersByTime(3250 - 1000 - 10);
    expect(store.notices).toHaveLength(1);
    vi.advanceTimersByTime(20);
    expect(store.notices).toEqual([]);
  });

  it("holds a page's bar under a dialog, and gives a dialog's bar to the page once it closes", async () => {
    vi.useFakeTimers();
    store.notice("On the page");
    store.noticeHost(1);
    vi.advanceTimersByTime(20_000);
    expect(store.notices).toHaveLength(1);
    store.notices = [...store.notices, { id: 99, text: "In the dialog", kind: "error", inDialog: true, left: Infinity }];
    store.noticeHost(-1);
    await Promise.resolve();
    expect(store.notices.every((n) => !n.inDialog)).toBe(true);
    vi.advanceTimersByTime(3000 + 50 * 11 + 5);
    expect(store.notices.map((n) => n.text)).toEqual(["In the dialog"]);
  });

  // A dialog covered by another unmounts its host as the covering one
  // mounts its own, in one flush: the count touches zero between them,
  // and the lower dialog's bars stay the dialogs'.
  it("keeps a dialog's bars in the dialogs when one covers another", async () => {
    store.noticeHost(1);
    store.notices = [{ id: 99, text: "In the dialog", kind: "error", inDialog: true, left: Infinity }];
    store.noticeHost(-1);
    store.noticeHost(1);
    await Promise.resolve();
    expect(store.notices[0].inDialog).toBe(true);
    store.noticeHost(-1);
    await Promise.resolve();
    expect(store.notices[0].inDialog).toBe(false);
  });

  it("holds the page's bars while the viewer stands", () => {
    vi.useFakeTimers();
    store.notice("Saved");
    store.pauseNotices({ viewerUp: true });
    vi.advanceTimersByTime(20_000);
    expect(store.notices).toHaveLength(1);
    store.pauseNotices({ viewerUp: false });
    vi.advanceTimersByTime(3250 + 10);
    expect(store.notices).toEqual([]);
  });
});

// The list's wash (direction 4, list motion): the ids an add on the
// archive shown made, from its outcomes, for a while.
describe("the rows an operation added", () => {
  it("keeps the added and created ids of an add on the archive shown", async () => {
    vi.useFakeTimers();
    m.Status.mockResolvedValueOnce(status(1, "unlocked"));
    store.boot();
    await open(["x"]);
    m.Stat.mockResolvedValue(stat(2));
    m.Page.mockResolvedValue(reply(2, ["x", "n1", "d2"], 3));
    const results = [
      { path: "C:/a", name: "n1", isDir: false, outcome: "added", id: "n1", size: 1, modifiedAt: 1 },
      { path: "C:/d", name: "d2", isDir: true, outcome: "created", id: "d2", size: 0, modifiedAt: 1 },
      { path: "C:/s", name: "s", isDir: false, outcome: "skipped", size: 0, modifiedAt: 0 },
    ];
    m.handlers["op.done"]({ data: { id: "o9", archiveId: "a", kind: "add", done: 1, total: 1, items: 2, dragItems: 0, phase: "", startedAt: 1, finished: true, returned: 0, results } });
    expect([...store.fresh].sort()).toEqual(["d2", "n1"]);
    vi.advanceTimersByTime(4001);
    expect(store.fresh.size).toBe(0);
  });

  it("forgets them on a change of folder", async () => {
    await open(["x", "d1"]);
    store.markFresh(["x"]);
    m.Page.mockResolvedValueOnce(reply(2, [], 0, [{ id: ROOT, name: "A" }, { id: "d1", name: "d1" }]));
    await store.enterDir("d1", "d1");
    expect(store.fresh.size).toBe(0);
  });
});
