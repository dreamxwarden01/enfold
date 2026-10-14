import { beforeEach, describe, expect, it, vi } from "vitest";
import type { VaultStatus } from "./api";

// The Archives page's own ways to open a file (APP.md §14, ruled
// 2026-10-14): Open file… and an .efd dropped on the page go through the
// chain Explorer's double-click takes. The services are mocked at the
// binding, as in state.test.ts, and the runtime records the handlers
// boot() subscribes.

const m = vi.hoisted(() => ({
  handlers: {} as Record<string, (e: { data: unknown }) => void>,
  Status: vi.fn(),
  List: vi.fn(),
  Leave: vi.fn(),
  Open: vi.fn(),
  OpenPath: vi.fn(),
  Page: vi.fn(),
  Stat: vi.fn(),
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
    Archive: { Stat: m.Stat, Page: m.Page, Children: vi.fn() },
    Archives: { List: m.List, Leave: m.Leave, Open: m.Open, OpenPath: m.OpenPath },
    Shell: { DragOut: vi.fn(), CloseDecided: vi.fn(), PendingOpen: m.PendingOpen },
    Vault: { Status: m.Status, Activity: vi.fn(), LastExportAt: never },
    Keys: { Slots: never, EntangledState: never },
    Settings: { Get: never },
  };
});

type Store = typeof import("./state.svelte").store;
const ROOT = "0".repeat(32);
const FILE = "D:\\Archives\\holiday.efd";

let store: Store;
beforeEach(async () => {
  vi.resetModules();
  for (const k of Object.keys(m.handlers)) delete m.handlers[k];
  m.Status.mockReset().mockResolvedValue({ seq: 1, state: "unlocked", openArchives: 0, ops: [] } as unknown as VaultStatus);
  m.List.mockReset().mockResolvedValue([]);
  m.Leave.mockReset().mockResolvedValue(undefined);
  m.Open.mockReset().mockResolvedValue({ seq: 1, id: "a", name: "A", state: "open" });
  m.OpenPath.mockReset().mockResolvedValue({ archiveId: "a", relocated: false });
  m.Page.mockReset().mockResolvedValue({ seq: 1, rows: [], total: 0, crumbs: [{ id: ROOT, name: "A" }] });
  m.Stat.mockReset();
  m.PendingOpen.mockReset().mockResolvedValue({ seq: 0, path: "", rest: null });
  store = (await import("./state.svelte")).store;
  store.boot();
  await settle();
});

async function settle() {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}

const drop = (paths: string[], archiveId = "") => m.handlers["shell.drop"]({ data: { paths, isDir: paths.map(() => false), archiveId, dirId: archiveId ? ROOT : "" } });

describe("Open file… on the Archives page", () => {
  it("opens the file by the chain a double-click in Explorer takes", async () => {
    await store.openFiles([FILE]);
    await settle();
    expect(m.OpenPath).toHaveBeenCalledWith(FILE);
    expect(m.Open).toHaveBeenCalledWith("a");
    expect(store.route).toBe("archive");
  });

  it("never swallows the shell's next request: the page numbers its own opens", async () => {
    await store.openFromShell({ seq: 1, path: "D:\\Archives\\one.efd", rest: null });
    await store.openFiles([FILE]);
    await store.openFromShell({ seq: 2, path: "D:\\Archives\\two.efd", rest: null });
    await settle();
    expect(m.OpenPath.mock.calls.map((c) => c[0])).toEqual(["D:\\Archives\\one.efd", FILE, "D:\\Archives\\two.efd"]);
  });
});

describe("a drop on the Archives page", () => {
  it("opens the first .efd and names the rest, as Explorer's several paths do", async () => {
    drop(["C:\\x\\notes.txt", FILE, "D:\\Archives\\spare.efd"]);
    await settle();
    expect(m.OpenPath).toHaveBeenCalledTimes(1);
    expect(m.OpenPath).toHaveBeenCalledWith(FILE);
    expect(store.toasts.map((t) => t.text)).toEqual([expect.stringContaining("spare.efd")]);
  });

  it("does nothing at all for a drop of other files", async () => {
    drop(["C:\\x\\notes.txt", "C:\\x\\photo.jpg"]);
    await settle();
    expect(m.OpenPath).not.toHaveBeenCalled();
    expect(store.toasts).toEqual([]);
  });

  it("leaves a drop elsewhere as it was: a toast off the file list, an add onto it", async () => {
    store.go("settings");
    drop([FILE]);
    expect(m.OpenPath).not.toHaveBeenCalled();
    expect(store.toasts.map((t) => t.kind)).toEqual(["error"]);
    drop(["C:\\x\\notes.txt"], "a");
    expect(store.drop?.archiveId).toBe("a");
  });
});
