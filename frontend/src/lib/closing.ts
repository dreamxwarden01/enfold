// The close question and the setting it answers (APP.md §2.4, ruled
// 2026-09-13: "we must not default to going to the tray"). The first close
// — the button, Alt+F4, the taskbar's close — is cancelled by the shell,
// which emits `shell.close`; the page asks, and the answer goes back as
// Shell.CloseDecided(action, remember). The setting itself is
// Settings.closeAction, shown in Settings so the remembering can be undone
// there.

// CloseAction is what the settings file holds: ask — the close asks — tray,
// or quit. Nothing else is a value; a file that holds something else is
// read as the default by the core, so the page only ever sees these three.
export type CloseAction = "ask" | "tray" | "quit";

// Decision is what the question can be answered with. Never "ask": that is
// the question, not an answer to it.
export type Decision = Exclude<CloseAction, "ask">;

// The Settings row's own mapping: the three choices in the order they are
// offered, and the words the save bar names a staged one with. The old
// destroy/hide labels went with the setting they belonged to — the window
// is destroyed either way since 2026-09-13.
export const closeChoices: [CloseAction, string][] = [
  ["ask", "Ask each time"],
  ["tray", "Keep running in the tray"],
  ["quit", "Quit"],
];

// closeActionLabel is one choice's words, for the select and for the bar.
// A value that is none of the three reads as the default, exactly as the
// core reads a settings file it does not recognise.
export function closeActionLabel(v: unknown): string {
  return closeChoices.find((c) => c[0] === v)?.[1] ?? closeChoices[0][1];
}

// asksOnClose reports whether a close would raise the question at all.
export function asksOnClose(v: unknown): boolean {
  return v !== "tray" && v !== "quit";
}
