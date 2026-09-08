// The merge checklist's rows (APP.md §13): what each incoming record's
// action is called, what its differences read as, and which rows are
// ticked when the dialog opens. The core classified the row — running the
// merge itself on a copy, so the row and the write cannot disagree — and
// this module only words it and decides the ticks the page starts with.
import type { Difference, IncomingRecord } from "./api";

// The four actions the core reports.
export type RowAction = "skip" | "version" | "add" | "forgotten";

export function actionOf(r: Pick<IncomingRecord, "action">): RowAction {
  switch (r.action) {
    case "skip":
    case "version":
    case "add":
    case "forgotten":
      return r.action;
  }
  return "skip"; // an action this build does not know changes nothing here
}

// actionLabel is the row's short word.
export function actionLabel(r: Pick<IncomingRecord, "action">): string {
  switch (actionOf(r)) {
    case "add":
      return "new";
    case "version":
      return "updates";
    case "forgotten":
      return "forgotten here";
  }
  return "already here";
}

// actionNote says what ticking the row would do.
export function actionNote(r: Pick<IncomingRecord, "action" | "versions">): string {
  switch (actionOf(r)) {
    case "add":
      return r.versions === 1 ? "an archive this vault has no record of, with one key version" : `an archive this vault has no record of, with ${r.versions} key versions`;
    case "version":
      return "already in this vault; brings a key version or a field this vault leaves empty";
    case "forgotten":
      return "held here as forgotten — ticking it restores the record";
  }
  return "this vault already holds this record and its keys";
}

// tickable: a row the merge would not change is never ticked, so the
// checkbox is not offered at all.
export function tickable(r: Pick<IncomingRecord, "action">): boolean {
  return actionOf(r) !== "skip";
}

// defaultTicks is the dialog's opening state, keyed by archive id: the
// core's own default (ticked by default, save a skip and a record held
// here as forgotten, which is an explicit restore).
export function defaultTicks(rows: readonly IncomingRecord[]): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const r of rows) out[r.archiveId] = tickable(r) && r.ticked;
  return out;
}

// tickedIds is what MergeRecords is given: the ids ticked now, in the
// order the rows are shown, and never one whose row cannot be ticked.
export function tickedIds(rows: readonly IncomingRecord[], ticks: Record<string, boolean>): string[] {
  return rows.filter((r) => tickable(r) && ticks[r.archiveId]).map((r) => r.archiveId);
}

// differText words one difference the merge keeps the local value for.
// The core sends the other vault's value already rendered for `policy`.
export function differText(d: Difference): string {
  switch (d.field) {
    case "name":
      return `named “${d.theirs}” there`;
    case "description":
      return d.theirs ? `described “${d.theirs}” there` : "no description there";
    case "policy":
      return `policy there: ${d.theirs}`;
  }
  return `${d.field} there: ${d.theirs}`;
}

// differLine is the row's second line, or "" when the two agree.
export function differLine(ds: Difference[] | null | undefined): string {
  if (!ds || ds.length === 0) return "";
  return `${ds.map(differText).join("; ")} — this vault's value is kept`;
}

// summary counts what the button is about to write.
export interface MergeSummary {
  added: number;
  updated: number;
  restored: number;
  total: number;
}

export function summarise(rows: readonly IncomingRecord[], ticks: Record<string, boolean>): MergeSummary {
  const s: MergeSummary = { added: 0, updated: 0, restored: 0, total: 0 };
  for (const r of rows) {
    if (!tickable(r) || !ticks[r.archiveId]) continue;
    s.total++;
    switch (actionOf(r)) {
      case "add":
        s.added++;
        break;
      case "version":
        s.updated++;
        break;
      case "forgotten":
        s.restored++;
        break;
    }
  }
  return s;
}
