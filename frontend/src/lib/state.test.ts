import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArchiveStat, DragOutResult, FileRow, OpView, Page, VaultStatus } from "./api";

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
}));

vi.mock("@wailsio/runtime", () => ({
  Events: {
    On: (name: string, fn: (e: { data: unknown }) => void) => {
      m.handlers[name] = fn;
    },
  },
  System: { invoke: vi.fn() },
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
    Archives: { List: m.List, Leave: m.Leave, Open: m.Open, OpenPath: m.OpenPath },
    Shell: { DragOut: m.DragOut, CloseDecided: m.CloseDecided, PendingOpen: m.PendingOpen },
    Vault: { Status: m.Status, Activity: vi.fn(), LastExportAt: never },
    Keys: { Slots: never, EntangledState: never },
    Settings: { Get: never },
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
    expect(store.toasts.map((t) => t.kind)).toEqual(["error"]);
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
    expect(store.toasts.length).toBe(1);
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
    expect(store.toasts.length).toBe(1);
    // Named, not only counted (APP.md §14), and by their leaves.
    expect(store.toasts[0].text).toContain("2 more archives were not opened: b.efd, c.efd.");
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
    expect(store.toasts.length).toBe(0); // and it said nothing
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
    expect(store.toasts.length).toBe(1);
    expect(store.toasts[0].text).toBe(`This archive is already open from ${open}. Close it there first.`);
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
    expect(store.toasts.length).toBe(1);
  });

  it("puts up the key's dialog when no record here holds the archive", async () => {
    await booted();
    m.OpenPath.mockRejectedValueOnce(refusal("archive.key_not_in_vault"));
    await store.openFromShell(request(1));
    expect(store.keyNotInVault).toBe(FILE);
    expect(store.route).toBe("archives"); // nowhere new: nothing was opened
    expect(store.toasts.length).toBe(0);
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
    expect(store.toasts.length).toBe(1);
    expect(store.toasts[0].kind).toBe("error");
    m.OpenPath.mockRejectedValueOnce(refusal("archive.forgotten"));
    await store.openFromShell(request(2));
    expect(store.toasts.length).toBe(2);
  });

  it("asks for nothing when the launch carried no file", async () => {
    await booted();
    expect(m.PendingOpen).toHaveBeenCalledTimes(1);
    expect(m.OpenPath).not.toHaveBeenCalled();
    expect(store.route).toBe("archives");
  });
});
