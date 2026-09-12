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
  DragOut: vi.fn(),
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
    Archives: { List: m.List, Leave: m.Leave },
    Shell: { DragOut: m.DragOut },
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
  m.DragOut.mockReset();
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
