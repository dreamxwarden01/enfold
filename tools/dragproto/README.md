# dragproto — the drag-out prototype

A stand-alone Windows program that drags files — synthetic ones, by default one of 5 GiB, or
real ones with `-file` — out of its own window and onto the desktop or an Explorer folder, with
a pure-Go, cgo-free OLE drag source behind it. It has **two modes**, because the design tried one route and then ruled for
the other:

- **virtual files** (the default): `IDataObject` offering `FileGroupDescriptorW` plus one
  `FileContents` `IStream` per file, `IDataObjectAsyncCapability`, and `IDropSource`. Nothing
  is ever written to disk. This is the record of what that route does, and experiments 1–10
  are its.
- **`-hdrop`: the staged route**, which is what APP.md §3 now specifies — 7-Zip's and
  WinRAR's mechanism. `CF_HDROP` by **delayed rendering** over a staging folder: the folder is
  made before the drag, a hover-time request is answered with the paths the files *will* have,
  and the first request after the button comes up extracts into it, inside `GetData`.
  Experiments 11–17 are its.

On top of both there is a third group of flags — `-postprobe`, `-thread`, `-disable` and
`-poll-after` — which are not about the protocol at all but about **the window during a drag**:
whether posted messages reach its loop, whether the drag can run somewhere else, whether the
window can be held rather than frozen, and whether the target is still reading after
`DoDragDrop` has returned. Experiments 18–21 are theirs, and they exist because the real
application's window shows nothing at all while a drag is in progress.

It exists because APP.md §3 and DECISIONS.md ("Drag out, researched") say it must come
first: *"a stand-alone prototype under `tools/dragproto` — a window that drags a synthetic
5 GiB virtual file onto the desktop — before a line of it enters the shell."* The staged mode
is here for the same reason, one ruling later: the route changed, the rule that it is measured
before it enters the shell did not.

It touches no vault, no key and no archive. The bytes are generated from a counter, and their
SHA-256 is printed at start so you can check what landed. In the default mode it writes
nothing at all; in `-hdrop` it writes the synthetic files, and only into
`%LOCALAPPDATA%\Enfold-dragproto\drag\<id>\` — never into `%LOCALAPPDATA%\Enfold\`, which is
the real application's folder.

**The log is the result.** Every COM call Explorer makes goes to stderr with the thread id
that made it. That log is what to paste back.

## Build and run

From the repository root:

```
go build ./tools/dragproto
./dragproto.exe 2> drag.log
```

or, keeping the log on screen next to the window:

```
go run ./tools/dragproto
```

In PowerShell, to keep stdout (the hashes) on screen and the COM log in a file:

```powershell
go build .\tools\dragproto
.\dragproto.exe 2> drag.log
```

### Flags

| Flag | Default | What it does |
| --- | --- | --- |
| `-n` | `2` | How many virtual files the drag offers. `-n 1` is the big file alone; beyond 2 you get 1 MiB fillers, to see a descriptor with several entries. |
| `-size` | `5GiB` | Size of the first file. Accepts `5GiB`, `256MiB`, `64m`, or a plain byte count. The file is named after it (`enfold-proto-5GiB.bin`). |
| `-file` | — | Offer **this existing file** instead of a synthetic one. May be given several times, once per file, and combines with `-hdrop`, `-agile`, `-delay` and `-folder`. The source is **read and nothing else** — never written to, renamed or deleted, and opened without `FILE_SHARE_DELETE` so nothing else can take it away mid-read. The descriptor and `CF_HDROP` use the source's own base name, real size and real modification time; under `-hdrop` the "extraction" is a copy of it into the staging folder, through the same 256 KiB buffer with the same `-delay` and progress lines. A path that is missing, a directory, or unreadable is refused **before any window opens**. `-n` and `-size` name the synthetic set and are not used when `-file` is given. |
| `-delay` | `0` | Milliseconds the producer takes per MiB, to stand in for a slow decrypt. `-delay 50` makes 5 GiB take about four and a half minutes. |
| `-folder` | off | Put the files under a relative folder `Proto\` in the descriptor name. |
| `-hash` | off | Also compute the SHA-256 of files larger than 64 MiB. Off by default because 5 GiB takes a while; when on it runs on a goroutine and prints when it finishes. |
| `-streamat` | `start` | Where a file's stream is positioned when `GetData` hands it over — `start` or `end`. `end` is what the documentation and Microsoft's sample do; see experiment 9, which is about whether it matters. |
| `-agile` | off | Make the data object, its format enumerator and every stream **agile**: each aggregates the free-threaded marshaler and answers `QueryInterface(IID_IAgileObject)`, so the target can call them straight from its own threads instead of having every call marshalled back onto this program's STA. `IDropSource` stays apartment-bound. See experiment 10. |

### `-hdrop` flags

| Flag | Default | What it does |
| --- | --- | --- |
| `-hdrop` | off | Offer **`CF_HDROP`** by delayed rendering over a staging folder, and `CFSTR_PREFERREDDROPEFFECT` beside it, instead of the virtual-file formats. The two sets are never offered together: the *target* picks the format, so a data object advertising both would measure whichever route Explorer happened to prefer. |
| `-stage-early` | off | Write the staged files at drag start instead of inside the drop's own `GetData`. Experiment 16 is the comparison. |
| `-keep` | off | Never delete a staging folder of this run, so it can be inspected afterwards. It beats every other rule, the forced close included; the log says so each time. |
| `-fail-extract` | off | Make the extraction fail partway through the first file on purpose. `GetData(CF_HDROP)` then returns `E_UNEXPECTED` instead of naming files that are not there. Experiment 17. |
| `-copy-only` | off | Allow `DROPEFFECT_COPY` only and prefer copy. The default allows **copy and move** and prefers **move**, which is 7-Zip's practice: the staged file is a disposable copy, so a same-volume drop is one rename with nothing left to clean up. |
| `-scavenge` | `1h` | At launch and every ten minutes, delete **manifested** staging folders older than this whose state is not `live`. This is the cleanup policy, not a backstop — see "How `-hdrop` cleans up". |

### The frozen-window flags

| Flag | Default | What it does |
| --- | --- | --- |
| `-postprobe` | off | Post a **registered window message** (`EnfoldProto.Probe`) to the drag window every 250 ms from just before `DoDragDrop` is called until two seconds after it returns, each carrying a sequence number and the millisecond it was posted, and post one more per 64 MiB from **inside the extraction**, on whatever thread that is running on. Every receipt is written down with its latency and the phase at each end, and a summary per phase is printed at the end of the drag. Experiment 18. |
| `-thread` | off | Run `DoDragDrop` on a **dedicated OLE thread**: its own `OleInitialize`, its own message-only window, its own data object and drop source, `AttachThreadInput` joining it to the window thread for the duration. The window's thread hands the drag over and goes straight back to its loop; it never waits for the drag thread, and the drag reports back by posting. A 250 ms `WM_TIMER` on the main window is the window thread saying, from inside its own loop, that it is still dispatching. Experiment 19. |
| `-disable` | off | `EnableWindow(hwnd, FALSE)` for **one phase only** — from the moment the post-release `GetData` has handed the paths (or a contents stream) back, until `DoDragDrop` returns — and `TRUE` again on every path out of the drag. Not for the hover and not for the extraction: the window needs a Cancel and a self-drop during those. Experiment 20. |
| `-poll-after` | `20s` | With `-hdrop`: after `DoDragDrop` returns, keep sampling every staged file with an exclusive open for this long, and print one verdict saying whether the target was **still reading** after the return. `0` turns it off. Experiment 21. |

`-n`, `-size`, `-file`, `-delay`, `-folder` and `-agile` all apply to `-hdrop` too. `-delay` there is a
sleep per MiB *inside the extraction*, which is inside `GetData`, which is the target waiting.
`-streamat` and `-hash` are virtual-file only (`-hash` still prints the digests, and they are
still what the dropped file must hash to). Under `-folder`, `CF_HDROP` names the **folder**,
not the files inside it: the format's list is paths, and a path that names a directory is the
directory — naming the files would drop them loose into the destination and the subfolder
would never appear there at all.

## What to try (the virtual-file mode)

Press the left mouse button anywhere in the window's client area and move past the system
drag threshold; the native drag starts there. Work through these in order and keep the log.
Experiments 1–10 are the default mode's; the staged route's are 11–17, and the frozen-window
ones are 18–21, both below.

1. **Onto the desktop.** Drop the default two files on the desktop. Does Explorer accept
   them? Does a copy dialog appear, and does it have a progress bar with a known total (that
   is `FD_FILESIZE` doing its job)? What does `DoDragDrop` return in the log, and with what
   effect?

2. **Onto a folder that already has a file of the same name.** First make sure **nothing
   else holds the destination file open** — no `Get-FileHash` running on it, no editor, no
   preview pane. A locked destination stalls Explorer's copy engine *before its first read*,
   and the run then measures a lock rather than a collision; that is how the finding in
   "Findings so far" below was made. Then drop once into an empty folder, and drop again into
   the same folder. Expect Explorer's own **Replace or Skip Files** dialog — this is the whole point of the design decision that the conflict question
   is Explorer's and is asked when the conflict occurs, not before. Note the exact wording
   and which buttons it offers (Replace / Skip / Compare / keep both), and whether the source
   is told anything about your choice (it should not be; check the log for a
   `CFSTR_PERFORMEDDROPEFFECT` or `CFSTR_PASTESUCCEEDED` arriving at `SetData`).

3. **Esc mid-drag.** Start a drag, move over the desktop, press Escape before releasing the
   button. The log should show `IDropSource::QueryContinueDrag` returning
   `DRAGDROP_S_CANCEL`, and `DoDragDrop` returning `DRAGDROP_S_CANCEL`. Nothing should be
   written anywhere.

4. **A slow producer.**

   ```
   ./dragproto.exe -size 512MiB -delay 50 2> drag-slow.log
   ```

   Drop it on the desktop and then, while it copies, try to move, resize and click the
   prototype's window.
   - Does Explorer show a progress dialog, and does it move?
   - Does the prototype's window stay responsive, or does it freeze?
   - In the log, are the `IStream::Read` lines on the **same thread id** as the window and
     `DoDragDrop`, or on a different one? This was the open question, and it is now answered:
     the handshake succeeds (`StartOperation`, `InOperation -> true`) and **the reads still
     arrive on the STA**, because `IDataObjectAsyncCapability` moves the work off the
     *target's* UI thread and says nothing about the source's apartment. See "Findings so far"
     and experiment 10. Worth repeating under `-delay` anyway, because a slow read on the STA
     is the case where it hurts — and then repeating with `-agile`.
   - Does Explorer ever give up on a stalled read? Try `-delay 400` if `-delay 50` is too
     patient.

   **What `-delay` measures, and what it does not.** The delay runs on the producer
   goroutine, never on the caller — a `Read` only drains a bounded queue, which is the shape
   the real thing will have. But `IStream::Read` holds that stream's mutex for the whole
   call, including while it waits for the producer, and that is deliberate: one stream is one
   sequence of bytes with one seek pointer, and two concurrent readers of it would interleave
   into nonsense. The consequence is that under `-delay` **a second call on the same stream
   waits for the first**, and it waits inside this prototype. So:
   - A `Read` that takes a long time is the delay doing exactly what it was asked to.
   - A `Stat` during a slow read does **not** wait: it is answered outside the mutex, from
     the file's fixed size and name, precisely so that a target asking "how big is this?" on
     the STA is never blocked by a background read. If that ever appears to block, it is a
     finding.
   - A second `Read` on the **same** stream during a slow one does wait, here, by design.
     If Explorer looks stalled and the log shows two reads on one stream, the queue is this
     program's, not the shell's — do not report it as Explorer being slow. Reads on
     *different* streams (different `IStream#n` in the log) never wait for each other.
   - With `-agile` those two callers can be on two different threads, which is the first time
     that mutex has had anything real to do. The behaviour is the same: they take turns.

   > **For the large-file runs, prefer `-file` with a real video.** A 5 GB synthetic `.bin` is
   > exactly the shape an on-access scanner holds for a full scan, and a run measured through a
   > scanner's stall is a measurement of the scanner rather than of Explorer — the `Replace`
   > stall in "Findings so far" is that suspicion written down. `./dragproto.exe -hdrop -file
   > "D:\media\holiday.mp4"` offers the real file under its own name, size and modification
   > time; the source is read and nothing else. Keep a synthetic run beside it when the point
   > is the *generator* (experiment 6's digests), and use `-file` when the point is Explorer.

5. **Memory.** Run the default 5 GiB drag, drop it somewhere with room, and watch
   `dragproto.exe` in Task Manager while Explorer copies. What matters is that it stays
   **flat**: the whole point is that nothing buffers the file. The measured peak on the first
   real 5 GiB drop was 85.5 MiB and it did not move with the copy — a Go runtime, a window and
   a COM apartment cost that much before a single byte is served. The log prints the peak
   working set after each drag and at exit; compare it with what Task Manager showed. If it
   climbs towards gigabytes, something in the chain is buffering and the design has a problem
   to solve.

6. **Check what landed.** The program prints the expected SHA-256 of each file at start.
   Compare:

   ```powershell
   Get-FileHash -Algorithm SHA256 "$env:USERPROFILE\Desktop\enfold-proto-small.txt"
   ```

   and for the big one, run the program with `-hash` so it prints that file's digest too:

   ```
   ./dragproto.exe -hash
   Get-FileHash -Algorithm SHA256 "D:\somewhere\enfold-proto-5GiB.bin"
   ```

   A mismatch means the stream handed over the wrong bytes — a seek that resumed in the
   wrong place, a short read treated as the end, a chunk delivered twice. The small file is
   printable text, so you can also just open it.

7. **A folder in the descriptor.** Run with `-folder` and drop on the desktop. The
   descriptor names each file `Proto\<name>` — a backslash, because that is the Windows path
   separator, though nothing documents that a separator is allowed at all. Does Explorer
   create a `Proto` folder and put the files inside it, or does it create files with a
   backslash-mangled name, or refuse? **This one is genuinely unknown**: Microsoft's
   documentation for `FILEDESCRIPTORW` says only that `cFileName` is "the null-terminated
   string that contains the name of the file", and neither it nor the Shell Clipboard Formats
   page says anything about path separators either way. Whatever happens here is a finding,
   not a bug — and the design needs it, because an archive folder dragged out is exactly this
   case.

8. **Several files.** `-n 4` offers four files. Watch the log for the order Explorer asks
   for them in, whether it reads them one at a time or overlaps them, and on how many
   threads.

9. **Where the stream's seek pointer starts.** This is the other genuine unknown.
   `IDataObject::GetData`'s Notes to Callers say:
   *"Data transferred across a stream extends from position zero of the stream pointer
   through to the position immediately before the current stream pointer (that is, the stream
   pointer position upon exit)."* Read literally, a source handing over a whole file must
   leave the seek pointer at the **end** — and Raymond Chen's virtual-file sample does
   exactly that, with the comment "set the stream position properly". The obvious reading is
   the opposite: hand the stream over ready to read, at position 0. Which one Explorer
   actually means is not documented anywhere I could find.

   The default is now `-streamat start`, so the first run of every other experiment is the
   one most likely to produce files. **`end` is the experiment.** Run it second, on a small
   file, and report what came out:

   ```
   ./dragproto.exe -n 1 -size 1MiB              # the default: handed over ready to read
   ./dragproto.exe -n 1 -size 1MiB -streamat end   # the documented convention, and the sample's
   ```

   The question to answer is whether `-streamat end` **still produces a whole file** — if it
   does, Explorer seeks the stream itself and the convention is harmless advice; if it
   produces a zero-byte file, the documentation's reading is not what the shell implements.
   Either answer settles it, and goes straight into the design. If a drop lands empty under
   either setting, that is the first thing to say.

10. **`-agile`: getting the reads off the UI thread.** This is the experiment the first 5 GiB
    drop created. `IDataObjectAsyncCapability` did everything it promises — Explorer agreed to
    extract asynchronously and did the whole copy after `Drop` returned — and *every one of
    the 20481 reads still arrived on this program's STA*, because an apartment-bound object is
    called in the apartment that created it, whatever thread the caller is on. In the real
    application that STA is the WebView's UI thread, which would then spend the whole copy
    pumping reads.

    `-agile` is the standard answer: aggregate the free-threaded marshaler, answer
    `IID_IAgileObject`, and the target's own thread calls straight in.

    ```
    ./dragproto.exe 2> drag-baseline.log          # the apartment-bound baseline, again
    ./dragproto.exe -agile 2> drag-agile.log      # the same drag, agile
    ```

    Drop the 5 GiB file on the desktop each time and compare:

    - **Which thread do the reads arrive on?** Every object now prints a summary line when it
      dies: `IStream#1(...): agile, 20488 call(s): 0 on the drag thread, 20488 on other
      threads [tid 9876 (other): 20488]`. Without `-agile` every call is on the drag thread;
      with it, the reads should be somewhere else. The end of the run prints the same totals
      for the whole process. This one line is the result of the experiment.
    - **Does the window stay responsive?** Drag the prototype's window around, and resize it,
      *while the copy runs* — with and without `-agile`, both times. Without it the window is
      answering reads between mouse messages; with it the window thread should have nothing to
      do at all. Add `-delay 50 -size 512MiB` to make the difference impossible to miss.
    - **Does Explorer's progress dialog appear, and does it still move?** Note whether the
      dialog looks any different (a total, a rate, a time estimate) when the source is agile.
    - **Memory.** Same check as experiment 5, and the same expectation: flat. If the working
      set climbs with `-agile` where it did not before, something about being called on
      several threads is buffering, and that is a finding.
    - **Does anything change in the QueryInterface traffic?** Explorer asks for `IID_IMarshal`
      and `IID_IAgileObject` on every run; without `-agile` both are refused. Watch for what
      it does *differently* once they are answered — whether it still asks for the other
      interfaces, and whether the stream is requested the same number of times.

    What to report: the two summary lines (baseline and agile), whether the window stayed
    responsive in each, the elapsed time and the peak working set of each, and anything in the
    log that only appears in one of them.

## `-hdrop`: the staged route

```
go build ./tools/dragproto
./dragproto.exe -hdrop -n 1 -size 1MiB 2> hdrop-1MiB.log      # start here
./dragproto.exe -hdrop -n 1 -size 5GiB -agile 2> hdrop-5GiB.log
./dragproto.exe -hdrop -file "D:\media\holiday.mp4" -agile 2> hdrop-video.log   # a real large file
```

For anything large, prefer the third line. A multi-gigabyte synthetic `.bin` is what an
on-access scanner stops to read in full, and a measurement taken through that is a measurement
of the scanner; a real video is an ordinary file to everything watching. `-file` reads the
source and does nothing else to it.

What happens, in order, and what each step is there to measure:

1. **Before the drag**, a folder `%LOCALAPPDATA%\Enfold-dragproto\drag\<8 hex>\` is created
   with a `manifest.json` in it (`tool`, `created`, `state`, `pid`, `files`). The paths the
   files will have are computed and logged. Nothing else is written.
2. **During the hover**, a `GetData(CF_HDROP)` is answered with those **final** paths, over
   files that do not exist yet, and logged as `early CF_HDROP request answered with future
   paths`. Whether Explorer asks at all during the hover is the first measurement; 7-Zip's
   source says targets do. Never a placeholder folder: Edge caches the early name and Sticky
   Notes refuses a path that is not there, so the first name handed out has to be the real one.
3. **The button comes up.** `QueryContinueDrag` sees it, arms the extraction, and every format
   request from then on is logged with its timing relative to that moment.
4. **The first `GetData(CF_HDROP)` after the release** writes the files, inside the call — for
   a `-file` entry that write is a copy of the source, through the same buffer, and the copy
   takes the source's modification time so the dropped file looks like the original. The
   log says how long the target waited and on which thread it ran — with `-agile` that should
   be a thread of Explorer's, which is the whole reason the real thing is agile. A failed or
   cancelled extraction **fails `GetData`** with `E_UNEXPECTED` rather than handing out names.
5. **Every later request** returns the same paths and rewrites nothing.
6. **Afterwards** each staged file is polled every 250 ms with an exclusive open
   (`CreateFileW`, `dwShareMode` 0). The watch says what it found the first time it looked
   (`the watch's first look at …: file.bin: present, free`), and then only transitions:
   `staged file in use by another process` is somebody reading it, `staged file free` is that
   ending, and `staged file GONE (moved away by the target)` is a same-volume move — which is
   the outcome the move-preferred default is *for*. **The first look is a transition too**: a
   rename can finish inside those first 250 ms, and a file the extraction wrote that is already
   missing when the watch first looks is logged as `GONE … before the first poll tick`. When
   every staged file has gone that way the drag is over in the cleanest way it can be, and the
   manifest goes to `done` and the empty folder with it.

Note what is deliberately **not** offered: `CFSTR_INDRAGLOOP`. The Shell Data Object page tells
targets that they "should not use the `IDataObject::GetData` method to render Shell data before
it has been dropped", and offers that format as the polite way for a target to check. Offering
it here would let a well-behaved target skip the hover request — which is exactly the thing
being measured.

### How `-hdrop` cleans up

Staged plaintext has to go, and the third research pass (DECISIONS.md) settled *when* against
the obvious answer. Deleting as soon as the drop looks finished is what took files from under
FileZilla, VMware and a configuration dialog that kept 7-Zip's paths for minutes; a file that
is not locked right now says nothing about whether a consumer will reopen it. So:

- **At once** when the drag ends having handed nothing out (Escape before any request, a drop
  nothing accepted), and when the extraction failed.
- **At `EndOperation`**, if the target negotiated the asynchronous protocol. Whether Explorer
  does that for a `CF_HDROP` source at all is one of the open questions — no trace in the
  research showed it does.
- **Otherwise the folder outlives the drop, and the process**, and the **scavenge** takes it:
  at launch and every ten minutes, every folder that carries our manifest, is not `live`, and
  is older than `-scavenge` (default an hour, WinRAR's threshold). A folder something still
  has open is retried with a bounded backoff — 1 s, 10 s, 60 s — and then left for the next
  sweep. A folder without our manifest is never touched, and a reparse point is never followed.
- **When every staged file has been moved away**, which is what a same-volume drop does, the
  drag ended in the cleanest way it can: the manifest goes to `done` and the empty folder goes
  with it, there and then. This counts the move that finished *before the watch's first tick*
  too — a rename is fast enough to beat 250 ms, and a round where it did is what put this
  sentence here.
- **A forced close visits every staging folder this run still has registered**, not the drag
  that happened to be last, and logs each attempt with its result: the exact error Windows gave
  for a delete that failed, which file was still open, and what `MOVEFILE_DELAY_UNTIL_REBOOT`
  said. A folder left behind keeps (or gets back) its manifest, because a folder without one is
  a folder the sweep is forbidden to touch. The exit summary then names every folder of the run
  still on disk and why it is there.
- **`MOVEFILE_DELAY_UNTIL_REBOOT` is recorded, not relied on.** Only a forced close calls it,
  and only so that the log says what an unelevated process gets: the flag "can be used only if
  the process is in the context of a user who belongs to the administrators group or the
  LocalSystem account", and even on success "the return value ... reflects success or failure
  in placing the appropriate entries into the registry", not in deleting anything.

There is deliberately no "delete once every file has been free for N seconds" rule. It was in
the first draft of this mode and the third research pass ruled it out: it is the rule that
breaks late consumers.

## What to try with `-hdrop`

Experiments 11–17. Same discipline as above: keep the log, and report what the dialogs said.

11. **Onto the desktop.** `./dragproto.exe -hdrop -n 1 -size 1MiB 2> hdrop-desktop.log`, then
    the 5 GiB one — and for that one prefer a **real file**:
    `./dragproto.exe -hdrop -file "D:\media\holiday.mp4" 2> hdrop-video.log`, because a 5 GB
    synthetic `.bin` looks suspicious to a scanner and gets held for a full scan, which is a
    stall in the measurement that has nothing to do with the protocol.
    The question the whole route was chosen for: **which copy dialog appears?**
    The modern one with a speed graph, a pause button and a *More details* pane — the thing the
    virtual-file route could never get — or the plain one? Does it show a rate and a time
    estimate? Does *Pause* work, and does *Cancel*?

12. **Onto a folder that already holds a file of that name.** Drop once into an empty folder,
    then again into the same one. Expect Explorer's **Replace or Skip Files** dialog; note
    whether it is the modern one (with *Compare info for both files* and the two-column
    chooser) rather than the older *Confirm File Replace*. Note also whether the source is told
    anything about the choice (`CFSTR_PERFORMEDDROPEFFECT` or `CFSTR_PASTESUCCEEDED` arriving
    at `SetData` in the log).

13. **Esc, and cancelling the copy.** Two different moments, and they answer different things:
    - Esc **during the hover**: the log should say `DRAGDROP_S_CANCEL`, then whether `CF_HDROP`
      had been requested at all before the cancel — that is 7-Zip's claim under test — and the
      staging folder should be deleted at once, with `nothing was handed out`.
    - **Cancel the copy dialog** while a 5 GiB copy runs. Does the staged file stay `in use`
      after the cancel? How long before `staged file free` appears? Does an `EndOperation`
      arrive at all, and with what `HRESULT`? This is the case where the difference between
      "the transfer ended" and "the consumer let go" is visible in the log.

14. **A move.** The default already allows copy and move and prefers move, so an ordinary drop
    onto the desktop may well *be* a move — `%LOCALAPPDATA%` and the desktop are usually the
    same volume, which makes it one rename. Watch for `staged file GONE (moved away by the
    target)` and for `DoDragDrop returned ... effect = DROPEFFECT_MOVE`. Then:
    - Shift-drag (forces a move) and Ctrl-drag (forces a copy), and compare the effect the log
      reports and how long the drop takes.
    - Drop onto a **different volume** (a USB stick, another drive letter) and see whether the
      effect is still move and whether Explorer deletes the staging file itself.
    - `./dragproto.exe -hdrop -copy-only` for the comparison: copy only, preferred copy.
    Report the effect in each case, and whether anything was left behind in the staging folder.

15. **A slow extraction, which is Explorer waiting inside `GetData`.**

    ```
    ./dragproto.exe -hdrop -n 1 -size 512MiB -delay 200 2> hdrop-slow.log
    ```

    That is about 100 seconds spent inside one `GetData` call. What does Explorer do?
    - Does anything appear at all while it waits — a "Preparing to copy" dialog, a progress
      window with no bar, nothing?
    - Does the Explorer window that was dropped on stay responsive, or does it freeze? Does the
      desktop?
    - Is there a **cancel** during that wait, and if you press it, what does `GetData` see —
      does the call come back, does the drag abandon?
    - Does Explorer ever give up and time out? (If 100 s is not enough, `-delay 400`.)
    - With and without `-agile`: which thread is the extraction running on, and does that
      change what freezes?

16. **`-stage-early` against the default, on a non-Explorer target.** The one case the whole
    delayed-rendering design is a bet on: a consumer that wants the file to exist at hover
    time.

    ```
    ./dragproto.exe -hdrop -n 1 -size 1MiB                # the files appear at the drop
    ./dragproto.exe -hdrop -n 1 -size 1MiB -stage-early   # the files exist before the drag
    ```

    Drop each onto: Notepad (or Notepad++), a browser upload box (an `<input type=file>` on any
    page), the Windows Mail or Outlook compose window, a chat client, Sticky Notes if it is
    installed. For each target and each mode, say whether the file arrived, whether the target
    read it immediately or minutes later (the `in use` lines say when), and whether the default
    mode failed where `-stage-early` worked. **That difference, if it exists, is the finding**:
    it is what decides whether the real thing has to write a small selection early.

17. **A failed extraction.** `./dragproto.exe -hdrop -n 1 -size 8MiB -fail-extract`, dropped on
    the desktop. `GetData` returns `E_UNEXPECTED` where Explorer expected a path list. What
    does Explorer show — an error dialog, a silent nothing, a retry? Does it ask again? Does
    the half-written file get cleaned up (the log should say `the extraction failed, so nothing
    usable was ever handed out`)? This is the behaviour APP.md §3 chose *against* 7-Zip, which
    hands out the names either way, so what Explorer does with the refusal matters.

Two more things to run once, whatever else you do:

- **`-keep` and look inside.** `./dragproto.exe -hdrop -keep`, drop, then open
  `%LOCALAPPDATA%\Enfold-dragproto\drag\` and read the `manifest.json` of the folder: the
  `state` should be `handed-out`. Leave it there, and on the **next** run with `-scavenge 1s`
  the launch sweep should remove it and say why. That is the scavenge under test without
  waiting an hour.
- **Memory.** Same as experiment 5: the extraction writes through one 256 KiB buffer, so
  `dragproto.exe` should stay flat while it writes 5 GiB. If it climbs, something buffers.

## The frozen window: experiments 18–21

These are not about the transfer. They are about what the **source's window** can do while a
drag is in progress, and they exist because of a difference nobody has been able to explain
from the code.

Enfold's shell is a Wails v3 WebView2 window whose main thread runs `DoDragDrop`. Its frontend
is told what is happening by callbacks that are **posted** to that thread's hidden window —
`PostMessage` of a registered message, picked up by the thread's message loop and dispatched to
its window procedure. During a real drag out of the application, the frontend shows **no
progress strip at all**. Three explanations fit:

1. `DoDragDrop`'s modal loop never dispatches those posted messages;
2. it does, but the synchronous `IDropTarget::Drop` — an outgoing COM call into Explorer, on
   which our thread blocks — does not, and the whole extraction happens inside it;
3. they arrive perfectly well and something else in the frontend is the problem.

WinRAR's window, meanwhile, looks *held* during a drag out of it rather than dead: it repaints,
it plainly is not taking clicks, and it comes back the instant the button goes up. That is a
fourth thing to account for. `-postprobe` decides between 1, 2 and 3 by measurement; `-thread`
and `-disable` are the two mechanisms that could produce WinRAR's behaviour.

### What the documentation says, and what it does not

Checked on Microsoft Learn rather than recalled, because most of what is "known" about this is
folklore.

**`DoDragDrop`** (`ole2/nf-ole2-dodragdrop`) says `"You must call OleInitialize before calling
this function."` and that it `"enters a loop in which it calls various methods"` in
`IDropSource` and `IDropTarget`. Step 1 of its numbered list is that it `"determines the window
under the current cursor location"`.

What that page does **not** say, verbatim-searched: which **thread** may call it — the word
"thread" does not appear on it at all; that the caller needs a message queue; that the caller
must own the window under the cursor; anything about mouse capture; and, crucially, **anything
about what its loop does with the message queue** — no "pump", "dispatch", `GetMessage`,
`PeekMessage` or `DispatchMessage`. `IDropSource::QueryContinueDrag` is the same: it says
`DoDragDrop` calls it `"whenever it detects a change in the keyboard or mouse button state"`,
and says nothing about which thread that happens on.

**`OleInitialize`** does document the apartment: it `"identifies the concurrency model as
single-thread apartment (STA)"`, lists Drag and Drop among the functionality that requires it,
and requires that `"each successful call to OleInitialize, including those that return S_FALSE,
must be balanced"`. It is written entirely in terms of *apartments*, never "per thread" — the
thread binding follows from the STA model, not from a sentence on that page.

**The one documented statement about modal loops** is on `PostThreadMessage`, not on
`DoDragDrop`: messages not associated with a window `"cannot be dispatched by the
DispatchMessage function"`, so `"if the recipient thread is in a modal loop (as used by
MessageBox or DialogBox), the messages will be lost."` Note what it names — `MessageBox` and
`DialogBox`, **not** `DoDragDrop` — and note that it is about *thread* messages. `PostMessage`
to a **window** is the documented opposite: it `"Places (posts) a message in the message queue
associated with the thread that created the specified window"` and returns without waiting.
That is why the probe posts to a window and never to a thread.

**Raymond Chen's blog, which is not reference documentation**, is the only place the two are
joined up. "Why do messages posted by PostThreadMessage disappear?" (2009‑09‑30) lists
`DoDragDrop` among `"functions that are explicitly modal"`, says that when such a loop
`"call[s] DispatchMessage, your thread message will simply be thrown away"`, and recommends
exactly the shape Enfold uses: `"create a hidden window and send or post messages to that
window."` Read together, the blog's claim is that `DoDragDrop`'s loop **does** call
`DispatchMessage`, so window-targeted posted messages should get through. Two other posts of
his state that `"The Do­Drag­Drop function calls Set­Capture"` and that during the loop
`"the mouse is captured to the drag/drop window"`. None of that is on Learn. **Experiment 18 is
the test of the blog's claim**, and of whether the synchronous `Drop` is the exception to it.

**`EnableWindow`** `"Enables or disables mouse and keyboard input to the specified window or
control"`, and `"If the window is being disabled, the system sends a WM_CANCELMODE message"`
followed by `WM_ENABLE`. Its return is documented the other way round from what reads naturally:
`"If the window was previously disabled, the return value is nonzero."` What the page does
**not** say is whether a disabled window still repaints or still receives **posted** messages.
That is inference from the scope of the word "input" — and it is what experiment 20 measures.

**`AttachThreadInput`** `"Attaches or detaches the input processing mechanism of one thread to
that of another thread"`, and the whole of what it buys is one sentence: `"a thread can share
its input states (such as keyboard states and the current focus window) with another thread."`
Three caveats are documented and all three matter here: it `"fails if either of the specified
threads does not have a message queue"` (which is why the message-only window is created
first); `"key state ... is reset after a call to AttachThreadInput"` (so if it resets the left
button under the drag, the very first `QueryContinueDrag` will see the button up and drop
instantly — watch for `QueryContinueDrag #1` returning `DRAGDROP_S_DROP` milliseconds after the
call); and `"You cannot attach a thread to a thread in another desktop."` The page says
**nothing** about `DoDragDrop`, nothing about mouse capture being shared, nothing about the
foreground window, nothing about deadlocks, and it does not require you to detach.

**Chromium is the only real-world precedent**, and it is a cautionary one. Its Windows drag ran
on a `base::Thread` named `Chrome_DragDropThread` with a `TYPE_UI` message loop and
`OleInitialize` on it, used for the drag-out-download case only, with the stated reason in
`web_contents_drag_win.h`: it did not want to run a nested message loop on the UI thread. It
called `AttachThreadInput(drag_thread, ui_thread, TRUE)` — **the drag thread attached to the UI
thread**, that direction — from the UI thread, for exactly one stated purpose: so that
`SetCursor` would work from the background thread. It needed a `WH_MSGFILTER` hook on the UI
thread as well, forwarding `WM_MOUSEMOVE`, `WM_LBUTTONUP`, `WM_KEYDOWN` and `WM_KEYUP` to the
drag thread with `PostThreadMessage`. The design produced at least one whole-browser freeze —
the fix commit says a `GetData(CF_HDROP)` on the UI thread deadlocked against the drag thread —
and the whole file was **deleted** in January 2014 when the non-Aura Windows path went. Current
Chromium calls `DoDragDrop` **directly on the UI thread** and merely tells its hang watcher not
to count the wait.

So `-thread` is an **experiment, not an implementation of a documented capability**, and it is
one that a browser tried and abandoned. It is here to be measured, and this prototype is
exactly where measuring it belongs.

Two deliberate differences from Chromium, both worth knowing before reading the log:

- **The direction of the attach is Chromium's**, `idAttach` = the drag thread, `idAttachTo` =
  the window thread, because that is the one with evidence behind it. The design review that
  asked for this experiment suggested the reverse order; the documentation describes the result
  as the two threads *sharing* state either way and does not settle it, so the log prints both
  ids in the order they are passed and the choice can be revisited from a real run.
- **There is no message forwarding.** Chromium needed it because its drag began from a
  mouse-down the UI thread already owned. Here the window thread calls `ReleaseCapture` before
  the hand-off, so by the time `DoDragDrop` runs on the OLE thread there is no capture to
  compete with and the system should deliver mouse messages to the drag thread's own queue.
  **If the drag cursor does not track the mouse, a `WH_MSGFILTER` hook forwarding those four
  messages is the next thing to try** — that is the finding to report, not a bug in this build.

### 18. Do posted messages reach the loop while `DoDragDrop` runs?

```
go build ./tools/dragproto
./dragproto.exe -hdrop -n 1 -size 64MiB -postprobe 2> probe-main.log
```

or with a real file, which is better for anything large:

```
./dragproto.exe -hdrop -file "D:\media\holiday.mp4" -postprobe -agile 2> probe-file.log
```

Drag onto the desktop and let it drop. Then read the `-postprobe summary for drag 1` block at
the end. One row per phase:

- **hover** — the modal loop with the button still down. Chen's account predicts these all
  arrive within a few milliseconds.
- **drop** — the button released and `DoDragDrop` not yet returned. This is where the
  synchronous `IDropTarget::Drop` is, and hypothesis 2 says this is the row that stalls.
- **extraction** — inside `GetData`, which is inside `Drop`. Our own code is on the stack here,
  so a stall in this row is *this program* not pumping, which is expected and is the shape the
  real application is in. `-size 64MiB` is deliberately small enough to finish quickly and
  large enough to post a probe from the extraction thread; `-delay 200` makes the row much
  louder if the first run is too fast to see.
- **returned** — after `DoDragDrop` came back. A burst of deliveries here, with latencies
  matching the length of the drop phase, is the **proof** that the messages were posted and
  queued rather than lost. The poster deliberately runs for two seconds past the return to
  catch it.

What to look for in each row: `delivered`, `N of them while the drag was still in that phase`,
the worst latency, and `the longest stretch with nothing delivered`. A row that ends
`-- NOTHING posted in this phase was delivered before the phase ended` is the answer that
explains the missing progress strip.

Run it twice, with and without `-agile`, because with `-agile` the extraction runs on
Explorer's thread and the probes posted from it come from there.

### 19. Can the drag run on a thread of its own?

```
./dragproto.exe -hdrop -n 1 -size 64MiB -thread -postprobe 2> probe-thread.log
```

The first question is whether it works **at all**. Report, in order:

- Does the drag cursor appear and follow the mouse?
- Does `IDropSource::GiveFeedback` get called, and does the effect change as you move over
  different targets?
- Does the drop land — is the file where you dropped it, and does the log show
  `IDropSource::QueryContinueDrag ... left button released ... -> DRAGDROP_S_DROP`?
- What does the log say for `AttachThreadInput(...)` in both directions, TRUE and FALSE?
- What does `DoDragDrop` **return**? If it fails, the `HRESULT` is the result of the
  experiment.
- Anything Explorer does differently — a different cursor, a slower first response, a target
  that refuses where it did not before.

Then the three gestures that stress the input handover, each once:

- **A fast release.** Press, move just past the threshold, let go immediately. The documented
  key-state reset in `AttachThreadInput` makes this the gesture most likely to go wrong; a
  `QueryContinueDrag #1` that returns `DRAGDROP_S_DROP` within milliseconds of the call is that
  failure, not a fast user.
- **Escape mid-drag.** The log should say `Esc pressed, keys ... -> DRAGDROP_S_CANCEL`, on the
  drag thread's tid. If Escape does nothing, the drag thread is not seeing the keyboard.
- **Ctrl and Shift mid-drag.** Every change is logged as `keys now MK_LBUTTON|MK_CONTROL ...`,
  and `GiveFeedback` should follow it with a changed effect. If the modifiers never reach the
  drag thread, that is the input attachment failing and it is the finding.

And the point of the whole flag: **the window thread must stay free**. Watch for

- `-thread: the window thread is alive and dispatching -- WM_TIMER tick N` four times a second
  throughout, with **no gap** during the drop or the extraction;
- in the probe summary, a row for `the main window` in every phase with everything delivered on
  time — that is the window thread's loop running while the drag thread sits in the modal loop;
- a row for `the message-only window` where, most likely, nothing was delivered until the
  one-off drain after the return. That queue belongs to the thread inside `DoDragDrop`, which
  pumps nothing of its own, and the line
  `draining the OLE thread's own queue once -- N message(s) were still in it` says how much was
  waiting there.

While it runs, try to **move and resize the prototype's window**. Under `-thread` it should
move; in experiment 18 it will not.

### 20. Can the window be held rather than frozen?

```
./dragproto.exe -hdrop -n 1 -size 64MiB -thread -disable -postprobe 2> probe-hold.log
```

`-disable` is deliberately narrow: the window stays **enabled** during the hover and during the
extraction, because those are the moments the real application needs a Cancel button and needs
to notice a drop back onto itself, and it is disabled only from the hand-over to the return.
The log says each transition, and the window procedure logs `WM_ENABLE` and `WM_CANCELMODE`
arriving on its own thread.

What to report:

- Does the disabled window **still repaint**? Drag another window across it, or make it redraw
  by uncovering it, during the held stretch. Learn does not document this either way.
- Does it **still process posted messages**? The probe summary under `-disable` is the
  measurement: the rows for the held phase should show deliveries if it does.
- Does it **feel** like WinRAR's — held rather than dead — and does it come back the instant
  the drop completes?
- Try **clicking it** while it is held. Nothing should happen, and nothing should be queued up
  and replayed afterwards.
- Try `-disable` **without** `-thread` as the comparison. There the window's own thread is
  inside the modal loop, so the disable is asked for from inside `GetData` and the window is
  frozen anyway; the interesting part is whether the log shows the `EnableWindow` happening at
  all and what `WM_CANCELMODE` does to the drag.

### 21. Is the target still reading after `DoDragDrop` returns?

This is the one that decides whether a staging folder may be deleted at the return, and the
answer is expected to depend on the **volume**. Staging is under `%LOCALAPPDATA%`, which is on
`C:`. So:

```
./dragproto.exe -hdrop -n 1 -size 512MiB -poll-after 20 2> poll-same-volume.log   # drop on the desktop
./dragproto.exe -hdrop -n 1 -size 512MiB -poll-after 20 2> poll-cross-volume.log  # drop on a folder on D:\
```

Use a folder on `D:\` that is yours and ordinary — **not** `D:\Archives` and not
`D:\ECON 280 (2026 Summer)`. A scratch folder made for the run is the right target.

For each drop, the line to read is `staging: -poll-after for ... --` at the end:

- The same-volume drop should be a **rename**: the staged file is gone within the first sample
  and the verdict says the folder could have been deleted at the return.
- The cross-volume drop should be a **copy on Explorer's own schedule**, with the staged file
  still open seconds after `DoDragDrop` came back and the verdict saying it could **not** have
  been deleted at the return.

If that difference appears, it settles the cleanup design: the return is not a signal, and the
scavenge is the mechanism. If it does *not* appear — if even a cross-volume copy is finished
before `DoDragDrop` returns — say so, because it would be the one result that makes deleting at
the return defensible. Use `512MiB` or larger; a small file crosses volumes faster than the
first sample. `-poll-after 60` if 20 s is not enough to see the end of the copy.

## Findings so far

From real drops on Windows 11. These are measurements, not expectations.

**The 5 GiB drop onto the desktop, apartment-bound (no `-agile`).** It works, end to end:

- Explorer took `FileGroupDescriptorW` and produced the file.
- It asked our data object for `IMarshal`, `IAgileObject`, `{ECC8691B-…}`, `{2132B005-…}`,
  `{0000001B-…}` (an old friend of `IMarshal`'s) and `{00000018-…}`. All were refused with
  `E_NOINTERFACE`, and refusing them changed nothing about the drop.
- The asynchronous handshake happened in full: `SetAsyncMode`, then `StartOperation`, then
  `InOperation` → true after `DoDragDrop` returned, then `EndOperation(S_OK,
  DROPEFFECT_COPY)`. The copy really did run after `Drop` returned.
- **The contents stream was requested twice**: once *before* the drop, which Explorer never
  read a byte from, and once *after*, which it read to the end. A source must be ready for a
  stream to be taken and dropped unread.
- The read itself: **20481 `Read` calls of 256 KiB, 5368709120 bytes, 7.3 s** — about
  730 MB/s — followed by `Seek(0, STREAM_SEEK_SET)` and a release. Nothing read the file
  twice; nothing asked for a byte out of order.
- **Every single call in the whole log arrived on one thread id**: the STA that created the
  objects, the reads of the asynchronous copy included. `IDataObjectAsyncCapability` moves the
  *work* off the target's UI thread; it does nothing whatever for the *source's*. That is what
  experiment 10 is for.
- Peak working set **85.5 MiB**, flat across the copy. Nothing buffered the file — 5 GiB went
  through 1.25 MiB of queue — though the floor is higher than experiment 5's "single-digit
  MiB" guess: a Go runtime, a window and a COM apartment cost what they cost.

**The second drop, onto the same file, with the destination held open.** This one is a finding
about tearing down, and it is why the close behaviour exists:

- Explorer put up its **Replace or Skip Files** dialog; Replace was chosen. It called
  `StartOperation` (at 170.0 s) and asked for the contents stream (at 172.1 s) — and then read
  **nothing at all**, because a `Get-FileHash` in another window still had the destination file
  open and its copy engine was stuck before its first read.
- The window was closed during that pause, at 190 s. Closing it ran `OleUninitialize`, which
  "Closes the COM library on the apartment, releases any class factories, other COM objects,
  or servers held by the apartment" — so that one call took the data object from five
  references to zero and destroyed the streams under a target that was still using them. After
  that Explorer's progress dialog could not even be cancelled.
- **The lesson is not about Explorer.** A source of virtual files is never told when the target
  will get round to reading them — a dialog, a locked destination, a slow disk, a user who
  walks away — and the only thing it *is* told is `EndOperation`. So: never destroy a data
  object the target still references, and keep the files, the streams and everything behind
  them alive until `EndOperation` arrives.
- What the prototype does about it now: a close while a transfer is in flight is **refused
  once**. The log says `close requested while a drop operation is in flight — waiting for
  EndOperation (close again to force)` and lists what is still out there; `EndOperation` then
  closes the window by itself. A second close forces the exit, naming every object the target
  still holds with its reference count and every stream's bytes served, revoking the streams
  so that a late call answers `STG_E_REVERTED` instead of being served out of a transfer that
  is over, and skipping `OleUninitialize` rather than pulling the references out from under
  the target on the way past.

**The staged route (`-hdrop`), first real drops: 1 MiB and 5 GiB onto the desktop, two drags in
one process.** Measurements, in the order the questions were asked:

- **Explorer asks for `CF_HDROP` during the hover, twice**, and takes the **future paths**
  without complaint — the files do not exist yet and it does not care. The rule that the first
  name handed out has to be the final one is therefore not theoretical.
- **It negotiates the asynchronous protocol for a `CF_HDROP` source**: `SetAsyncMode`,
  `StartOperation`, and `InOperation` → true after `DoDragDrop` returned. That answers one open
  question — and the next measurement takes the answer away again.
- **`EndOperation` did not arrive within 80 s, not even for a move that had already completed.**
  So for this route `EndOperation` is **not** a cleanup signal, whatever the handshake says: the
  file was gone from the staging folder seconds after the drop and the source was never told.
  The **scavenge** is the mechanism; watching the files is the only thing that reports progress.
- **`DoDragDrop` returned `DRAGDROP_S_DROP` with `effect = DROPEFFECT_NONE` for a move that
  completed.** For an asynchronous drop the effect out-parameter is not where the answer is, so
  the log now says `(asynchronous: the effect is not reported here)` when `InOperation` is true.
  Read on its own, that `NONE` looks exactly like a refusal.
- **With `-agile` the extraction really did run on Explorer's thread**: 390 of 726 calls arrived
  off the drag thread, `GetData` and the 5 GiB write inside it among them. That is the opposite
  of the apartment-bound finding above, where every call in the whole log was on the STA, and it
  is the reason the real thing is agile.
- **A same-volume drop is an instant move.** The desktop file's modification time equals the
  staged file's: nothing re-read the bytes, the file was renamed out of
  `%LOCALAPPDATA%\Enfold-dragproto\drag\…` into the desktop folder. That is what the
  move-preferred default is for, and it is why "the staged copy is disposable" is not just a
  nice property on paper.
- **A `Replace` over an existing 5 GiB file stalled before Explorer ever opened the source.**
  The machine's on-access scanner (Kaspersky, `avp.exe`) was reading large files at tens of MB/s
  throughout. Attributed to the scanner rather than to the protocol — **to be confirmed with the
  scanner paused**, which is the next run's job.

**Two defects the same round exposed, both now fixed and under test.** They are recorded because
each was invisible in the log rather than loud:

- **A forced close cleaned only the last drag's folder.** Drag 1 stalled in the target and its
  folder kept a 5 GiB staged file in state `handed-out`; drag 2 completed as a move. The forced
  close logged one deletion — drag 2's, already empty — and never so much as attempted drag 1's,
  because the close path held a pointer to the *current* stage. Five gigabytes of plaintext were
  left behind with nothing in the log to say so. Every unresolved stage is now kept in a
  registry, the close walks all of them, each attempt and its result is logged (the exact
  Windows error, which file was open, what delete-at-reboot returned), a folder left behind gets
  its manifest back so the sweep can still recognise it, and the exit summary names every folder
  of the run still on disk.
- **A move that beat the watch's first tick was never logged.** Drag 2's staged file was renamed
  away inside the 250 ms between `watching 1 staged file(s)` and the first poll. The poll had no
  case for "already missing the first time it was looked at", so it said nothing, the file never
  counted as gone, and the stage sat in `handed-out` over an empty folder waiting for a scavenge
  an hour away. The first observation is now a transition like any other: the watch reports what
  it found (`present, free` / `present, in use` / `missing`), a file the extraction wrote and
  that is missing at the first tick is logged as `GONE … before the first poll tick`, and a
  stage whose files have all gone that way ends then and there.

**Still open after this round:** which copy dialog a `CF_HDROP` source gets on a *slow*
extraction (the 5 GiB write finished in about 7 s, too fast to watch), what Explorer does while
the extraction holds `GetData` for a minute and a half (`-delay`), whether a target that needs
the files at hover time (Sticky Notes, an upload box) fails without `-stage-early`, and whether
the `Replace` stall survives pausing the scanner.

**Experiments 18–21: expected, and not yet measured.** Nothing below has been run. It is written
down before the fact so that a result that contradicts it is recognised as a result rather than
quietly explained away.

| | Expected | Why, and how confident |
| --- | --- | --- |
| 18, hover | Probes delivered within milliseconds | Chen's blog says `DoDragDrop`'s loop calls `DispatchMessage`. **Blog, not documentation** — if this is wrong, hypothesis 1 is right and the real application's whole callback mechanism needs replacing. |
| 18, drop | Delivered late or not at all, arriving in a burst after the return | This is hypothesis 2, and it is the one that best fits the real application's symptom. **Nothing documents it either way.** |
| 18, extraction | Nothing delivered while it runs | Our own code is on the stack inside `GetData`; no loop of ours is running. This one is near-certain, and it is also the one the real application can fix by itself. |
| 19 | Unknown whether it works at all | No Microsoft page sanctions calling `DoDragDrop` off-thread. Chromium made it work, needed `AttachThreadInput` plus a message hook, hit a whole-browser deadlock, and removed it. Treat a working drag here as the surprise. |
| 19, fast release | The likeliest failure | `AttachThreadInput` is documented to **reset** key state. If the left button reads as up, `QueryContinueDrag #1` drops immediately. |
| 19, window thread | Stays free throughout | `WM_TIMER` once a second and every main-window probe delivered on time. If this does not hold, `-thread` buys nothing and the experiment is over. |
| 20 | A disabled window still paints and still gets posted messages | Inference from `EnableWindow` being scoped to "mouse and keyboard input". **Not documented**; the probe rows are the measurement. |
| 21, same volume | A rename; nothing reading after the return | Already measured in the earlier rounds — a same-volume drop was an instant move. |
| 21, cross volume | Still being read for seconds after the return | Explorer copies on its own schedule and tells the source nothing. If true, the staging folder may **not** be deleted at `DoDragDrop`'s return, and the scavenge stays the mechanism. |

## What to paste back

The **stderr log** — all of it — plus:

- what the copy dialog looked like (progress bar with a total, or a spinner),
- the exact text of the Replace or Skip dialog,
- the peak working set Task Manager showed during the 5 GiB copy,
- whether `Get-FileHash` matched,
- whether the window stayed responsive under `-delay`,
- whether `-streamat end` still produced a whole file, or only `start` did,
- what `-folder` did,
- for `-agile`: the thread summary line of the big file's stream from both runs, and whether
  the window could be dragged around during the copy in either.

For `-hdrop`, the `staging summary for ...` block at the end of each drag, and:

- **which dialog** Explorer put up, in as much detail as you can give — a speed graph, a pause
  button, a *More details* pane, a rate, a time estimate — and whether it differed from the
  virtual-file route's,
- the exact text of the Replace or Skip dialog, and whether it was the modern two-column one,
- whether `staged file GONE (moved away by the target)` appeared, i.e. whether the drop was a
  rename rather than a copy, and what effect `DoDragDrop` reported,
- whether `StartOperation`/`EndOperation` appeared at all for the `CF_HDROP` source,
- how long the extraction took inside `GetData`, on which thread, and what Explorer showed
  while it waited,
- for each non-Explorer target: did it work with the default, did it work with `-stage-early`,
- what `-fail-extract` made Explorer show,
- what the scavenge said on the next launch, and whether anything was left in
  `%LOCALAPPDATA%\Enfold-dragproto\drag\` afterwards.

For experiments 18–21, the **stderr log** of each run is again the whole of it, and the blocks
to look at first are:

- the `---- -postprobe summary for drag N ----` block of every run, in full — it is four or five
  lines and it is the answer,
- for `-thread`: whether the drag cursor tracked the mouse, whether the drop landed, what the
  two `AttachThreadInput` lines said, what `DoDragDrop` returned, and what happened on a fast
  release, on Escape, and on Ctrl/Shift mid-drag,
- for `-thread`: whether the prototype's window could be **moved and resized while the drag was
  running**, and whether the `WM_TIMER` heartbeat ran throughout,
- for `-disable`: whether the held window still repainted, whether clicking it did anything, and
  whether it felt held or dead,
- for `-poll-after`: the one `staging: -poll-after for ... --` verdict line from the
  same-volume run and from the cross-volume run, side by side.

## What the log says

Every line is `<seconds since start> [tid <thread id>] <message>`. Worth knowing:

- `IStream::Read` is logged in full for the first ten calls of each stream, then once per
  64 MiB. A 5 GiB file is tens of thousands of reads; ten plus a heartbeat is enough to see
  the shape.
- `IDropSource::GiveFeedback` is logged only when the effect changes; it is called on every
  mouse move.
- `QueryInterface` prints the IID resolved to a name where the name is known, and as a raw
  GUID otherwise — an unfamiliar GUID in the log is Explorer asking for something we do not
  implement, which is usually fine and occasionally the explanation for a refusal.
- `COM interface cells still registered` at the end of a drag is the leak check: it counts
  the cells of objects that are still alive, and it should fall back to zero once Explorer
  has released everything. A non-zero count long after a completed drop means a reference was
  never given back.
- `CALL ON A RELEASED this` is the opposite failure, and is a finding about the consumer: a
  call arrived on an interface pointer whose object had already been released. The address of
  a released cell is kept out of circulation for the life of the process precisely so that
  such a call produces this line instead of quietly landing on some later object.
- Every object says what it is when it is created — `apartment-bound`, or `agile: aggregates
  the free-threaded marshaler` — and prints a **thread summary** when it dies: how many calls
  it answered, how many of them on the drag thread, how many elsewhere, and the per-thread
  breakdown. The same totals for the whole process are printed at exit, followed by any object
  the target never let go of. Every call that resolves an interface pointer is counted,
  including the reads the log itself throttles away, so these numbers are the traffic and not
  a sample of it.
- `STG_E_REVERTED` from a stream means the program was torn down while the target still held
  that stream: a forced close. It is not an error in the transfer, it is the end of one.
- `close requested while a drop operation is in flight` and the inventory under it are the
  window refusing to go away mid-transfer; see "Findings so far".

In `-hdrop` the lines to read are:

- `early CF_HDROP request answered with future paths` — a target asking during the hover, which
  is the behaviour the whole delayed-rendering design is built around.
- Every `GetData` and `QueryGetData` carries `during the hover, the button still down` or
  `+1.234s after the release`, so the whole exchange can be read against that one moment.
- `staging: the extraction wrote N file(s), X bytes in D -- the target waited that long inside
  GetData, on tid ...` — the measurement of what the drop costs the target, and on whose thread.
- `staged file in use by another process` / `staged file free` / `staged file GONE (moved away
  by the target)` — the consumer's behaviour, observed rather than reported.
- `staging cleanup: <action> -- <reason>` — every decision the policy takes, with the rule that
  produced it. It is logged when it changes, not four times a second.
- `scavenge: ...` — the launch sweep and the ten-minute one, with a line per folder saying why
  it was removed or left.
- `staging summary for <folder>` at the end of each drag: the request counts, the extraction,
  the manifest state, each file's fate and every format that was asked for.
- `staging: -poll-after for <folder> -- ...` — the one verdict on whether the target was still
  reading a staged file after `DoDragDrop` returned.

Under `-postprobe`, `-thread` and `-disable`:

- `probe #N from ... reached ... Xs after it was posted: posted in phase A, delivered in phase B`
  — one line per receipt, throttled to the first of each phase, anything slower than 50 ms, and
  otherwise one a second. The phase at each end is the whole point: `posted in phase drop,
  delivered in phase returned` is the missing progress strip, in one line.
- `---- -postprobe summary for drag N ----` and the rows under it: per phase and per window,
  how many were posted, how many arrived, how many arrived **while that phase was still going
  on**, the worst latency, and the longest stretch with nothing delivered. A row ending
  `-- NOTHING posted in this phase was delivered before the phase ended` is the finding.
- `-thread: the window thread is alive and dispatching -- WM_TIMER tick N` — every 250 ms,
  from the window thread's own loop, while the drag runs elsewhere. It is deliberately not
  throttled: a **gap** in it is the failure worth seeing.
- `-thread: AttachThreadInput(idAttach=..., idAttachTo=..., TRUE/FALSE)` — both calls, with
  their results. A `FALSE` that fails leaves two threads attached and is a finding of its own.
- `-thread: ... draining the OLE thread's own queue once -- N message(s) were still in it` —
  what the modal loop never dispatched. It is the difference between a message that was queued
  and ignored and one that was never posted.
- `-disable: EnableWindow(0x..., false/true)` with what the window was before and after, and
  `the main window received WM_ENABLE` / `WM_CANCELMODE` from the window's own thread.
  `WM_CANCELMODE` is logged and **swallowed** while a drag is running, because `DefWindowProc`
  would answer it by releasing a capture that belongs to `DoDragDrop`.
- `the button came up at screen (x, y): ...` — the release position against the main window's
  own frame and against the hit test, on every drag. `a SELF-DROP` means the user let go over
  the prototype's own window. The two signals are reported separately on purpose:
  `WindowFromPoint` does not return disabled windows, so under `-disable` the frame is the only
  one of the two that can answer.

## What this deliberately does not do

- No `%TEMP%`. `-hdrop` stages under `%LOCALAPPDATA%\Enfold-dragproto\drag\`, which is ours to
  scavenge, outside what OneDrive backs up, and on the volume most drops land on — which is
  what makes a move a rename. (The real application will use `%LOCALAPPDATA%\Enfold\drag\`;
  this prototype stays out of that folder on purpose.)
- No `CFSTR_INDRAGLOOP` offered to targets: it would let them skip the hover request, which is
  the thing being measured.
- No `DROPEFFECT_LINK`, and in the default mode no `DROPEFFECT_MOVE` either — a file read out
  of a stream is a copy. `-hdrop` allows move because there the thing moved is a disposable
  staged copy, never the record in the archive.
- No knowledge of where the drop landed. A standard OLE drag source is not told, and this
  prototype is here partly to confirm that — which is why `-hdrop` watches the staged files
  instead of asking. The one thing it does read is the **cursor's own position** when the button
  comes up, and only to say whether the release was over the prototype's own window; that is
  anyone's to read and says nothing about the target.
- No message forwarding under `-thread`. Chromium's dedicated drag thread needed a
  `WH_MSGFILTER` hook forwarding mouse and key messages; this does not have one, because the
  window thread releases capture before handing the drag over. If the drag turns out not to
  track the mouse, that hook is the next thing to add — see experiment 19.
- Nothing waits on the drag thread. Under `-thread` the window thread hands the drag over and
  goes back to its loop; results come back by `PostMessage` and never by a wait. Chromium's one
  documented whole-browser freeze in this area was a UI thread blocked on its drag thread.
- Nothing is recorded, no policy applies, and no archive is opened.
