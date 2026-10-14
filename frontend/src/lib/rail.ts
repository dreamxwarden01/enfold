// The rail's interim hint (APP.md §6, ruled 2026-10-14): any route change
// away from an archive's page leaves the archive (§2.3), and the rail's
// items are such changes, the current Archives item included. Until the
// rail shows the open archive as a place of its own, each item says so on
// hover: "Leaves Photos 2024".

export interface RailPlace {
  route: string;
  current: string | null;
  // The page's own heading — its crumbs' first entry, the name the Archive
  // page shows — and, before the page's listing has arrived, the list's.
  pageName?: string;
  listName?: string;
}

// leavesHint is the title every rail item carries, or undefined where a
// click leaves nothing.
export function leavesHint(p: RailPlace): string | undefined {
  if (p.route !== "archive" || !p.current) return undefined;
  const name = p.pageName || p.listName || "";
  return name ? `Leaves ${name}` : "Leaves this archive";
}
