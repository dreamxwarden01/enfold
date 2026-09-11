# Drag-out research — the raw findings

Three outside research passes (OpenAI Codex, model gpt-6-astra, dispatched read-only from
this repository with web access) on how Enfold should let a user drag a file out of an archive
onto the desktop or an Explorer folder, kept here verbatim so that later work can consult
them without running the research again. The decisions drawn from them are in
`docs/DECISIONS.md` (2026-09-10, the drag-out entries) and the design in `docs/APP.md` §3.

How to read a finding: *confidence* is the researcher's — high = several independent sources
agree, medium = one solid source or several weak ones, low = a single anecdote or an
inference. *verified* means the researcher read the documentation or the code that shows it;
*reported* means practitioners say so; *inferred* means neither. Every finding lists the URLs
the researcher actually opened; two per pass were spot-checked when the pass came back. The
"gaps" are the researcher's own statement of what it could not find or confirm. Nothing here
was executed by the researcher; the measured facts are in the last section.


## Pass 1 — Can a WebView2/Wails app drag files out at all, and how? (2026-09-10)

*Codex Astra, read-only, web research. 40 sources. Its subagents failed to launch; it researched alone.*

### The question as put

> Research question: how can a Go (CGO_ENABLED=0) desktop app whose UI is a WebView2 window managed by Wails v3 (v3.0.0-beta.16) let the user DRAG a file out of the app's HTML file list and DROP it onto the Windows desktop or an Explorer folder — with the file's bytes produced by the app on demand (they are decrypted from an archive) — and what conflict handling does Explorer apply to such a drop?

### The researcher's position

Recommended approach: build a Windows-only, pure-Go OLE drag source that offers FileGroupDescriptorW metadata and indexed FileContents IStreams, with DROPEFFECT_COPY only. Connect the HTML gesture to the host, suppress the browser’s competing drag, and invoke DoDragDrop on an explicitly OLE-initialized UI STA. Implement IDataObject, IEnumFORMATETC, IDropSource, a properly seekable/readable IStream, and IDataObjectAsyncCapability; retain the archive, decryption state, and COM objects throughout asynchronous extraction. Explorer should own destination selection and collision handling. Do not depend on learning the destination or receiving per-file success receipts. The principal risks are COM lifetime/marshaling, gesture handoff, slow reads blocking the STA, and whether the archive’s compression/authentication scheme permits bounded-memory streaming and seeking. Prototype first one complete HTML-to-Explorer drag using a throttled, synthetic file larger than 4 GiB, without decryption or temporary files. Test a colliding filename, Replace/Skip, cancellation, final hash, memory consumption, callback threads, and object lifetime. That single experiment resolves the important integration uncertainties before building the archive-backed stream.

### Findings

**1. Both mechanisms work, but only the virtual-file mechanism avoids source-side plaintext staging** — high confidence, verified.

CF_HDROP supplies a DROPFILES structure containing existing filesystem paths; Explorer subsequently copies or moves those files. File contents are not embedded in CF_HDROP. For virtual files, register the Unicode format name FileGroupDescriptorW and supply an HGLOBAL containing FILEGROUPDESCRIPTORW plus FILEDESCRIPTORW entries. Advertise FileContents with TYMED_ISTREAM: Explorer requests each file through IDataObject::GetData, with FORMATETC.lindex identifying its zero-based descriptor index. Return a stream that generates decrypted bytes when read. Microsoft documents this mechanism and provides an actual virtual-file IStream drag example. No real source file is required. However, 'delayed' means requested by the consumer, not a contractual promise that no request occurs before mouse release. Keep metadata inexpensive and defer expensive work until content reads. Source-side absence of temporary files also does not guarantee that Windows or another target never caches data on disk.

Sources:

- [Microsoft: Shell clipboard formats](https://learn.microsoft.com/en-us/windows/win32/shell/clipboard)
- [Raymond Chen: Dragging a virtual file, IStream edition](https://devblogs.microsoft.com/oldnewthing/20080319-00/?p=23073)

**2. The premise about 7-Zip is incorrect; the three archive applications cannot be treated as equivalent** — high confidence, verified.

7-Zip’s official FAQ explicitly describes extracting dragged archive entries into a temporary folder and letting Explorer copy them. Its current PanelDrag.cpp corroborates this: the public transfer path advertises CF_HDROP, has temporary extraction machinery, and calls DoDragDrop. It also implements private 7-Zip transfer formats for cooperation between compatible windows; those are not a general Explorer destination API. Extraction timing can be delayed without changing the transfer into FILECONTENTS streaming. WinRAR using temporary extraction for drag-out is reported in the opened Super User discussion, but I did not verify current WinRAR internals. Windows ZIP-folder transfers exposing FileGroupDescriptorW/FileContents are reported by developers handling those transfers, including a report of a working fix. That supports the format claim, not a universal assertion about every Windows version or its internal disk use. The FAQ’s implication that Explorer always requires disk files should not be generalized beyond 7-Zip’s chosen implementation.

Sources:

- [7-Zip FAQ](https://www.7-zip.org/faq.html)
- [7-Zip PanelDrag.cpp](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [Firsthand discussion of 7-Zip/WinRAR temporary extraction](https://superuser.com/questions/302116/why-do-7-zip-winrar-unzip-files-to-temp-before-moving-them-to-their-destinat)
- [Developer report handling files copied from Windows ZIP folders](https://stackoverflow.com/questions/27173844/accessviolationexception-after-copying-a-file-from-inside-a-zip-archive-to-the-c)

**3. Explorer collision handling is available for virtual files too** — medium confidence, reported.

Using virtual files does not inherently bypass Explorer’s overwrite handling. Microsoft’s FILEDESCRIPTORW documentation explicitly describes using descriptor metadata in an overwrite dialog. A firsthand report independently describes Explorer asking to overwrite when receiving Outlook attachment streams, whereas Directory Opus automatically renamed them. Therefore, CF_HDROP is not a prerequisite for Explorer-managed conflict handling: both physical-file copies and virtual-file materialization can encounter destination-name collisions. Supply accurate names, sizes, modification times, and corresponding validity flags; FD_PROGRESSUI requests progress UI. The exact modern 'Replace or Skip Files' wording and available comparison/keep-both choices depend on Explorer’s operation and Windows version. I did not execute that exact dialog test. The target owns the conflict decision; IDataObject does not let the source dictate Explorer’s dialog or receive its individual button selections.

Sources:

- [Microsoft: FILEDESCRIPTORW, including overwrite-dialog metadata](https://learn.microsoft.com/en-us/windows/win32/api/shlobj_core/ns-shlobj_core-filedescriptorw)
- [Firsthand Explorer-versus-Directory-Opus collision report](https://resource.dopus.com/t/auto-filename-numbering-when-copying-from-a-certain-source/21409)

**4. Standard OLE drag-out exposes neither the destination directory nor a reliable per-file receipt** — high confidence, verified.

The target chooses what to do with supplied data; standard source interfaces do not deliver its destination filesystem path. Shell feedback formats such as Performed DropEffect, Logical Performed DropEffect, and Paste Succeeded communicate operation-level information. TargetCLSID identifies certain target classes, not a directory. IDataObjectAsyncCapability::EndOperation adds an extraction HRESULT and effects, again without a per-file result array. Consequently, do not interpret a successful DoDragDrop return, COPY/MOVE effect, or fully consumed stream as proof that every selected file was saved. A target can skip items or fail after reading. Tracking requested indices is useful diagnostic information, not a persistence receipt. For archive exports, allow COPY and leave archive entries intact. Reliable destination paths and individual outcomes require a cooperative target/private protocol or an explicit destination-selection export workflow.

Sources:

- [Microsoft: Shell feedback formats](https://learn.microsoft.com/en-us/windows/win32/shell/clipboard)
- [Developer explanation of why drag sources do not receive destination paths](https://stackoverflow.com/questions/28807046/how-to-get-destination-path-when-drag-out-a-file-from-my-application)
- [Microsoft: IDataObjectAsyncCapability::EndOperation](https://learn.microsoft.com/en-us/windows/win32/api/shldisp/nf-shldisp-idataobjectasynccapability-endoperation)

**5. Gigabyte streaming is supported architecturally; an IStream label alone does not ensure bounded memory** — high confidence, verified.

Microsoft explicitly recommends interface-based transfer for very large virtual files because global-memory transfer requires the entire payload in memory. IStream has 64-bit size/position semantics, and FILEDESCRIPTORW supplies high/low size fields. Return an actual incremental stream rather than decrypting everything inside GetData or filling CreateStreamOnHGlobal before returning it. A developer investigating large virtual-file drags found exactly that whole-file buffering trap and reported fixing it with a reader-backed IStream. Explorer can consume chunks; the protocol does not require whole-file buffering. Nevertheless, the reviewed material establishes neither a universal Explorer timeout nor immunity to slow or stalled producers. Compression, authenticated decryption, seeking, and repeated content requests can independently force buffering or replay. Support accurate Stat and sensible Seek behavior, and measure the real implementation rather than assuming every reader-backed adapter is sufficient.

Sources:

- [Microsoft: Transferring virtual objects and large data](https://learn.microsoft.com/en-us/windows/win32/shell/datascenarios)
- [Microsoft: IStream](https://learn.microsoft.com/en-us/windows/win32/api/objidl/nn-objidl-istream)
- [Firsthand large-file buffering diagnosis and stream-backed fix](https://stackoverflow.com/questions/22700195/drag-and-drop-large-virtual-files-with-istream-using-virtualfiledataobject)

**6. Asynchronous extraction requires an explicit protocol and careful COM lifetime/threading** — high confidence, verified.

Expose IDataObjectAsyncCapability, formerly IAsyncOperation, and enable asynchronous mode. A cooperating target calls StartOperation, returns from Drop, extracts in the background, then calls EndOperation. After DoDragDrop returns, inspect InOperation; extraction may still be running. Keep the archive, keys, stream state, and referenced COM objects alive accordingly. This interface permits asynchronous extraction; it does not guarantee that every consumer will use it. It also does not automatically move your Go callbacks off the UI thread: calls to an STA object are dispatched through its owning apartment. Long Read callbacks can still block that STA. Respect COM marshaling when crossing apartments, keep the message loop alive, and design thread-safe state if using an appropriate free-threaded implementation. Simply starting a goroutine around DoDragDrop or handing raw COM pointers between threads is not a correct substitute.

Sources:

- [Microsoft: IDataObjectAsyncCapability](https://learn.microsoft.com/en-us/windows/win32/api/shldisp/nn-shldisp-idataobjectasynccapability)
- [Microsoft: Asynchronous Shell drag/drop sequence](https://learn.microsoft.com/en-us/windows/win32/shell/datascenarios)
- [Microsoft: STA dispatch and marshaling requirements](https://learn.microsoft.com/en-us/windows/win32/com/single-threaded-apartments)

**7. WebView2 can initiate native OLE drags from web content** — high confidence, verified.

The blanket claim that HTML dragstart inside WebView2 cannot become a native Windows drag is false. Microsoft explicitly documents WebView2’s default behavior as synchronously invoking DoDragDrop when it detects a drag. The newer ICoreWebView2CompositionController5::DragStarting event exposes the IDataObject and permitted effects and allows a host override through Handled. That interface was introduced in stable SDK 1.0.3719.77 and applies to composition hosting; it is not automatically an available Wails beta.16 window API. A WebView2Feedback issue also reports an existing browser-originated native drag continuing when the host unsuccessfully tries to replace its data from DragLeave. That report concerns intercepting an already-running drag, not inability to start one. For this application, controlling the initial drag source is more dependable than trying to replace WebView2’s data after the drag is underway.

Sources:

- [Microsoft: CompositionController5 DragStarting and default DoDragDrop behavior](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2compositioncontroller5?view=webview2-1.0.4129.50)
- [WebView2Feedback issue 2313: replacing outgoing drag data](https://github.com/MicrosoftEdge/WebView2Feedback/issues/2313)

**8. DataTransfer supports browser formats, not arbitrary page-defined COM file providers** — medium confidence, verified.

HTML setData supplies strings; adding a type named FileGroupDescriptorW does not install a native descriptor structure or IStream implementation. The inspected Chromium Windows code maps text, HTML, URLs, existing file paths, and selected browser-generated file content into native formats; arbitrary custom strings use Chromium’s custom-data packaging. text/uri-list conveys URLs, potentially resulting in a link/shortcut, not an archive-byte callback. Chromium’s DownloadURL convention uses mime:filename:url and implements Windows drag-download through delayed temporary-file materialization and CF_HDROP, not your own FILECONTENTS stream. Chromium can itself synthesize FILEDESCRIPTOR/FILECONTENTS for certain browser data, so claiming that no browser drag ever contains these formats would also be wrong. JavaScript File items are an HTML capability, but I did not verify that programmatically added Files export successfully to Explorer in the relevant WebView2 runtime; they provide no lazy Go IStream callback. Microsoft’s WinUI-specific release notes explicitly exclude DownloadURL support, demonstrating why Chromium support cannot be assumed across all WebView2 hosts.

Sources:

- [HTML Standard: drag data store and DataTransfer](https://html.spec.whatwg.org/multipage/dnd.html)
- [Chromium: preparing native drag data and temporary downloads](https://raw.githubusercontent.com/chromium/chromium/main/content/browser/web_contents/web_contents_view_aura.cc)
- [Chromium: Windows native format mappings](https://raw.githubusercontent.com/chromium/chromium/main/ui/base/dragdrop/os_exchange_data_provider_win.cc)
- [Microsoft explainer: DownloadURL and proposed DownloadURL-list](https://github.com/MicrosoftEdge/MSEdgeExplainers/blob/main/DownloadURL-list/explainer.md)
- [Microsoft: Windows App SDK 2.0 WebView2 drag limitations](https://learn.microsoft.com/en-us/windows/apps/windows-app-sdk/release-notes/windows-app-sdk-2-0)

**9. Wails v3.0.0-beta.16 supplies drop-in handling, not a ready-made drag-out source** — high confidence, verified.

Inspection and symbol searches of the pinned beta.16 source found no outbound DoDragDrop/IDropSource implementation. EnableFileDrop and the documented file-drop events handle files arriving in the window. The Windows implementation obtains incoming paths through WebView2 messaging; its comments deliberately preserve browser drag behavior because disabling external dragging also breaks HTML drag/drop. The existing w32 IDataObject code wraps calls to an object supplied by native code; it is not a complete outbound virtual-file COM server. Wails issue 4648 separately requests cross-platform drag-out and remains an enhancement request in the inspected issue. Thus EnableFileDrop is not the switch needed here. You need a Windows host extension, your own native OLE implementation, or a framework contribution exposing one.

Sources:

- [Wails beta.16: Windows WebView window implementation](https://raw.githubusercontent.com/wailsapp/wails/v3.0.0-beta.16/v3/pkg/application/webview_window_windows.go)
- [Wails beta.16: IDataObject wrapper](https://raw.githubusercontent.com/wailsapp/wails/v3.0.0-beta.16/v3/pkg/w32/idataobject.go)
- [Wails: file-drop documentation](https://v3.wails.io/features/drag-and-drop/files/)
- [Wails issue 4648: drag-out feature request](https://github.com/wailsapp/wails/issues/4648)

**10. Wails initializes an STA, but its source does not establish the required OleInitialize call** — high confidence, verified.

The pinned WebView2 initialization locks the OS thread and calls CoInitializeEx with COINIT_APARTMENTTHREADED. That is not equivalent to establishing OLE drag/drop initialization. A w32 OleInitialise wrapper exists, but the inspected beta.16 module contains no call sites; its implementation also omits an explicit NULL argument and ignores the HRESULT. Microsoft requires OleInitialize before DoDragDrop. Call OleInitialize(0) explicitly on the actual drag/UI OS thread, check its result, and balance successful calls—including S_FALSE—with OleUninitialize at an appropriate lifetime boundary. This source finding does not prove that no internal dependency ever initializes OLE; it means Wails provides no explicit guarantee to rely on. Use Wails’s Windows main-thread dispatch mechanism. Its callback path can execute inline when already on the main thread, so do not assume an 'async' dispatch always unwinds the current WebView callback before entering OLE’s nested loop.

Sources:

- [Wails beta.16: WebView2 COM initialization](https://raw.githubusercontent.com/wailsapp/wails/v3.0.0-beta.16/v3/internal/webview2/pkg/edge/corewebview2.go)
- [Wails beta.16: OLE wrapper](https://raw.githubusercontent.com/wailsapp/wails/v3.0.0-beta.16/v3/pkg/w32/ole32.go)
- [Wails beta.16: Windows main-thread dispatch](https://raw.githubusercontent.com/wailsapp/wails/v3.0.0-beta.16/v3/pkg/application/mainthread_windows.go)
- [Microsoft: OleInitialize requirements](https://learn.microsoft.com/en-us/windows/win32/api/ole2/nf-ole2-oleinitialize)
- [Microsoft: DoDragDrop requirements](https://learn.microsoft.com/en-us/windows/win32/api/ole2/nf-ole2-dodragdrop)

**11. Pure-Go COM implementations exist, including the requested interfaces, but require adaptation** — high confidence, verified.

CGO_ENABLED=0 does not prevent implementing these interfaces. go-ole’s Winsock example implements native-callable COM vtables using syscall.NewCallback. More directly, zzl/go-com contains callback-vtable implementations of IDataObject, IDropSource, and IStream. These establish concrete pure-Go building blocks, not a verified drop-in virtual-file exporter. The default IDropSource does not implement the required button-release/Escape decisions. Its stream adapter also needs scrutiny: the inspected Read path turns reader errors, including EOF, into E_UNEXPECTED, while Microsoft requires correct byte counts and S_FALSE for a short EOF read; several other stream methods default to E_NOTIMPL. Implement correct ABI layouts, QueryInterface identity, AddRef/Release, FORMATETC enumeration, STGMEDIUM ownership, and GC-safe object lifetimes. Return independent or correctly reset streams for repeated requests. I found no end-to-end example demonstrating these Go objects streaming multi-gigabyte virtual files into Explorer.

Sources:

- [go-ole: native-callable COM event-sink example](https://raw.githubusercontent.com/go-ole/go-ole/master/_example/winsock/winsock.go)
- [zzl/go-com: IDataObject implementation](https://raw.githubusercontent.com/zzl/go-com/main/com/comimpl/IDataObject.go)
- [zzl/go-com: IDropSource implementation](https://raw.githubusercontent.com/zzl/go-com/main/com/comimpl/IDropSource.go)
- [zzl/go-com: IStream implementation](https://raw.githubusercontent.com/zzl/go-com/main/com/comimpl/IStream.go)
- [Microsoft: ISequentialStream::Read return semantics](https://learn.microsoft.com/en-us/windows/win32/api/objidl/nf-objidl-isequentialstream-read)

**12. Electron and Tauri provide a useful gesture-to-host pattern, but their examined implementations export real paths** — high confidence, verified.

Electron’s documented pattern cancels the page’s dragstart, sends IPC to the main process, and calls webContents.startDrag. Its sample writes files before dragging; the native implementation uses OSExchangeData::SetFilenames. It does not demonstrate lazy virtual-file generation. The examined drag-rs Windows implementation, used in the Tauri ecosystem, obtains a Shell IDataObject from existing filesystem paths and invokes native DoDragDrop; its callback reports drop/cancel and cursor position, not the destination directory. For Wails, adopt the gesture handoff but replace the path-backed object with your own virtual-file IDataObject. Either detect a mouse-movement threshold after mousedown or use dragstart with preventDefault and a bound host call. Only one native drag loop should start, and it must start while the gesture remains active. The specific Wails/WebView2 handoff remains an integration inference requiring the prototype; manual mousedown interception is not the only possible design.

Sources:

- [Electron: native file drag/drop tutorial](https://www.electronjs.org/docs/latest/tutorial/native-file-drag-drop)
- [Electron: native drag implementation](https://raw.githubusercontent.com/electron/electron/main/shell/browser/ui/drag_util_views.cc)
- [drag-rs: Windows implementation](https://raw.githubusercontent.com/crabnebula-dev/drag-rs/main/crates/drag/src/platform_impl/windows/mod.rs)
- [Tauri issue 2593: dragging files out](https://github.com/tauri-apps/tauri/issues/2593)

### Gaps, in the researcher's words

Evidence was checked against documentation and source code; I did not run an Explorer/WebView2 drag test or compile the Go examples. Consequently, I cannot confirm the exact current Windows 'Replace or Skip Files' presentation, Explorer’s behavior under prolonged stalled reads, or its precise Seek/Clone/read-order requirements. I found no published universal timeout guarantee and no verified, complete CGO_ENABLED=0 Wails beta.16 virtual-file drag sample. DownloadURL and JavaScript-created File export were not verified on this app’s actual WebView2 runtime; Chromium behavior and WinUI WebView2 behavior must not be conflated. Current WinRAR internals and whether Windows ZIP folders ever use internal temporary storage remain unconfirmed. 'Verified' below means documentation/source inspection, not execution; firsthand community observations are identified as reports. The source count covers the 40 distinct opened sources cited below. All three requested subagent launches failed with an infrastructure 'no thread with id' error, so I completed and merged the three research tracks directly.


## Pass 2 — Virtual files versus staged temp files; the legacy progress dialog (2026-09-10)

*Codex Astra, 52 sources, three subagents. Asked after the first real drops.*

### The question as put

> Research question: for a Windows desktop application whose files exist only inside an encrypted archive, which mechanism should "drag a file out of the app onto the desktop or an Explorer folder" use — (A) virtual files (CFSTR_FILEDESCRIPTORW + CFSTR_FILECONTENTS as IStream, the bytes pulled by Explorer at drop time), or (B) the 7-Zip/WinRAR way (extract to a temporary folder, hand Explorer CF_HDROP paths, let Explorer copy, delete the temporary afterwards or on the next launch if it was still locked) — and what are the real-world trade-offs and failure modes of each as practitioners report them?

### The researcher's position

Choose A for Explorer/desktop drag-out, with an explicit “Extract/Save As” fallback that writes directly to the chosen destination. Multi-gigabyte files and the prohibition on lingering plaintext outweigh B’s broader compatibility and better Explorer UI. Do not automatically advertise CF_HDROP alongside the virtual formats: the target can choose paths and trigger staging. The old dialog is a longstanding behavior of the descriptor/contents transfer route; unknown size alone does not explain it, and no descriptor flag is documented to upgrade it. Treat the uncancellable locked-destination stall as a release-blocking reliability issue requiring diagnosis. I would reconsider B only if plaintext staging became acceptable; if modern Explorer UI were mandatory, I would investigate a full Shell namespace/ITransferSource implementation first. All three requested agents completed; their evidence is merged below.

### Findings

**1. Your old progress dialog matches longstanding practitioner reports** — high confidence, reported.

A 2012 namespace-extension developer described the same FILEDESCRIPTOR → FILECONTENTS → IStream sequence producing the old UI despite FD_PROGRESSUI. A separate 2023 practitioner contrasted its basic progress dialog with CF_HDROP’s speed graph and pause/resume UI. That corroborates your observation without establishing Explorer’s internal class or algorithm. The 2023 account did report an estimated remaining time, so “virtual transfers never show ETA” would overstate the evidence; your bar-only result is a narrower observation.

Sources:

- [Developer report, 2012](https://stackoverflow.com/questions/9093103/file-operation-progress-dialog)
- [7-Zip discussion, 2023](https://sourceforge.net/p/sevenzip/discussion/45797/thread/f7b34bb6/)

**2. FD_FILESIZE supplies size; FD_PROGRESSUI requests progress, not modern styling** — high confidence, verified.

Microsoft defines FD_FILESIZE as validating the two size fields and FD_PROGRESSUI as requesting a progress indicator. A developer independently reported that FD_PROGRESSUI fixed a missing dialog. Neither flag documents a visual-style selector. Supply accurate 64-bit descriptor size and IStream::Stat information, but do not promise a graph or modern dialog. Raymond Chen’s account explains that Shell stream copying historically used expected size for progress and ETA, demonstrating that this route is not inherently an unknown-size transfer.

Sources:

- [FILEDESCRIPTORW documentation](https://learn.microsoft.com/en-us/windows/win32/api/shlobj_core/ns-shlobj_core-filedescriptorw)
- [FD_PROGRESSUI developer result](https://stackoverflow.com/questions/1534435/cfstr-filedescriptor-cfstr-filecontents-copying-files-dialog-missing)
- [Raymond Chen, pages 40–42](https://ptgmedia.pearsoncmg.com/images/9780321440303/samplechapter/Chen_bonus_ch01.pdf)

**3. Modern UI is possible with virtual Shell items, through a larger architecture** — medium confidence, reported.

A 2022 developer reported modern UI with ITransferSource, but old UI when a custom IDataObject diverted copying through descriptor/contents. Their eventual solution supplied valid CFSTR_SHELLIDLIST/CIDA data so Explorer could operate on the items’ PIDLs. Microsoft independently documents Shell-item transfer, ITransferSource resources and IFileOperation support for virtual namespace items. This is evidence against “all IStreams force old UI.” It is not a flag-only fix for an ordinary application’s drag object: it requires resolvable Shell namespace items and associated implementation. Creating IOperationsProgressDialog yourself likewise does not change how Explorer consumes your IDataObject.

Sources:

- [Developer investigation and solution](https://stackoverflow.com/questions/73938149/how-to-force-explorer-use-modern-file-operation-dialog-with-shell-namespace-extension)
- [Handling Shell Data Transfer Scenarios](https://learn.microsoft.com/en-us/windows/win32/shell/datascenarios)
- [ITransferSource](https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-itransfersource)
- [IFileOperation](https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-ifileoperation)
- [SetProgressDialog](https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperation-setprogressdialog)

**4. The protocol remains supported; active investment in its old dialog is unproven** — medium confidence, inferred.

Microsoft still documents descriptor/contents transfer, including email/database-backed items, without marking it deprecated. Current Chromium source still implements it. Calling the UI legacy is reasonable descriptively; calling the protocol deprecated or abandoned is unsupported. Conversely, documentation availability and continued interoperability do not prove ongoing improvements to Explorer’s old copy dialog. Microsoft explicitly identifies SHFileOperation as replaced by IFileOperation, but that is not a deprecation statement about FILECONTENTS.

Sources:

- [Shell Clipboard Formats](https://learn.microsoft.com/en-us/windows/win32/shell/clipboard)
- [Chromium Windows drag data provider](https://chromium.googlesource.com/chromium/src/%2B/refs/heads/main/ui/base/dragdrop/os_exchange_data_provider_win.cc)
- [SHFileOperation documentation](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-shfileoperationw)

**5. Cancellation is cooperative; error recovery is reportedly less capable** — medium confidence, reported.

Chen explains that Shell adopted a Read/Write loop to check Cancel between chunks; a blocking Read could hang both participants. This is historical engineering evidence, not a guarantee of current implementation. The 2023 practitioner reports that returning HRESULT_FROM_WIN32(ERROR_CANCELLED) from Read cancels, while other errors produce an error message; they also report stopping at the first error without Retry and no elevation Continue option for protected destinations. Those current-behavior details remain one practitioner’s account. Keep reads bounded and responsive, and test corruption, cancellation and permission failures explicitly.

Sources:

- [Raymond Chen, stream-copy cancellation history](https://ptgmedia.pearsoncmg.com/images/9780321440303/samplechapter/Chen_bonus_ch01.pdf)
- [Practitioner cancellation/error report](https://sourceforge.net/p/sevenzip/discussion/45797/thread/f7b34bb6/)
- [ISequentialStream::Read contract](https://learn.microsoft.com/en-us/windows/win32/api/objidl/nf-objidl-isequentialstream-read)

**6. The locked-destination hang cannot yet be attributed to a documented stream limitation** — low confidence, inferred.

Your trace establishes that copying stalls before the first Read; it does not identify where Explorer is waiting. Windows documents an incompatible file-sharing open as failing with ERROR_SHARING_VIOLATION, rather than requiring an indefinite wait. Possible locations include destination handling, a retry/UI loop, another source callback or COM synchronization; none is confirmed. A cancellation check inside your Read cannot help while Read is never called. Capture Explorer thread stacks and all IDataObject/IStream calls, including GetData and Stat, and compare A and B against the same lock. Do not assume FD_FILESIZE or smaller read buffers fixes this.

Sources:

- [CreateFileW sharing semantics](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew)
- [Read contract](https://learn.microsoft.com/en-us/windows/win32/api/objidl/nf-objidl-isequentialstream-read)

**7. Source lifetime and genuine streaming matter more than the sample class name** — high confidence, verified.

Microsoft’s async protocol explicitly allows extraction to continue after DoDragDrop returns: check InOperation and defer cleanup until EndOperation. Anson’s follow-ups describe the same lifetime problem and warn that targets need not choose async mode. Keep the archive, decryption state and COM objects alive for extraction. If the source disappears, remaining bytes cannot be supplied; exact Explorer timeout and partial-output behavior are unverified. Also, an inspected reuse of VirtualFileDataObject generates the complete contents into an HGLOBAL-backed stream inside GetData. That is not the bounded-memory pull stream demonstrated by your 5 GiB prototype.

Sources:

- [IDataObjectAsyncCapability](https://learn.microsoft.com/en-us/windows/win32/api/shldisp/nn-shldisp-idataobjectasynccapability)
- [EndOperation](https://learn.microsoft.com/en-us/windows/win32/api/shldisp/nf-shldisp-idataobjectasynccapability-endoperation)
- [Anson’s asynchronous follow-up](https://dlaa.me/blog/post/9917797)
- [Anson’s completion follow-up](https://dlaa.me/blog/post/9923072)
- [Inspected VirtualFileDataObject implementation](https://github.com/Shendare/EQZip/blob/master/VirtualFileDataObject.cs)

**8. 7-Zip waits for drop intent through an explicit state machine** — high confidence, verified.

Current PanelDrag.cpp creates an empty temporary directory before DoDragDrop. Early CF_HDROP requests receive that directory; after button release, QueryContinueDrag switches to final item paths and arms NeedCall_Copy. A subsequent GetData performs extraction. Therefore “extract whenever Explorer asks for CF_HDROP” is insufficient: requests can occur before dropping. Chen documents that targets may request data during DragEnter. 7-Zip’s own comments identify compatibility trouble with Sticky Notes rejecting nonexistent paths and Edge caching the early directory. Virtual formats are also not an absolute after-drop promise: a Terminal issue reports requesting FILECONTENTS while hovering.

Sources:

- [7-Zip PanelDrag.cpp](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [Chen on delayed CF_HDROP rendering](https://devblogs.microsoft.com/oldnewthing/20120413-00/?p=7863)
- [Terminal premature-content request report](https://github.com/microsoft/terminal/issues/13498)

**9. B buys filesystem-copy behavior at a staging cost that depends on the volume and effect** — high confidence, verified.

7-Zip documents extracting to temp and then having Explorer transfer the files. PeaZip documents temp-volume exhaustion and additional cross-disk writing. For a copy of N uncompressed bytes, B needs staging space and adds staging writes/reads before the destination copy completes; destination free space alone is insufficient. But “always writes twice” is too broad: Igor Pavlov explains that a same-volume move from staging can avoid the second payload copy. A move effect here need not delete the archive entry. Additional antivirus work is plausible, but exactly two scans was not verified.

Sources:

- [7-Zip FAQ](https://www.7-zip.org/faq.html)
- [7-Zip maintainer on copy/move, 2026](https://sourceforge.net/p/sevenzip/bugs/2625/)
- [PeaZip drag-and-drop documentation](https://peazip.github.io/screenshots-peazip-3.html)
- [Firsthand large-archive staging report](https://www.reddit.com/r/DataHoarder/comments/ih0e88/extracting_large_7z_files/)

**10. Temp cleanup creates a trade-off between broken consumers and lingering plaintext** — high confidence, reported.

Pavlov acknowledges that deleting staging after DoDragDrop breaks consumers that open paths later; Godot has a Windows 11 reproduction report. Inspected 7-Zip cleanup uses recursive removal, without persistent retry machinery in those functions. WinRAR documents next-run cleanup of files at least one hour old for external viewing; that scope does not prove every drag-out uses the same policy. PeaZip explicitly documents crash/locked-file residue and cleanup controls. Per-session retention or next-launch scavenging improves compatibility but cannot satisfy an absolute no-lingering-plaintext rule. Ordinary deletion also does not establish secure erasure.

Sources:

- [7-Zip lifetime acknowledgement](https://sourceforge.net/p/sevenzip/bugs/2625/)
- [Godot Windows 11 report](https://github.com/godotengine/godot/issues/97128)
- [7-Zip temporary-directory destructor](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/Windows/FileDir.h)
- [7-Zip removal implementation](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/Windows/FileDir.cpp)
- [WinRAR temporary-artifacts policy](https://www.win-rar.com/leave-artifacts-temp-folder.html?&L=0)
- [PeaZip manual](https://peazip.github.io/peazip_help.pdf)

**11. CF_HDROP has broader compatibility, but browsers are not universally path-only** — high confidence, verified.

A Delphi author reports virtual drag working in Explorer but failing in Notepad++ and simple WM_DROPFILES receivers; adding CF_HDROP bridged that gap. However, current Chromium implements virtual-file receipt and materializes the contents into temporary files. Offering both formats does not preserve your preferred route: Mozilla independently reproduced and fixed a Windows 11 Firefox→Total Commander format-selection problem involving CF_HDROP. Thus A avoids source-created staging, not all target-created plaintext. For your policy, use an explicit compatibility/export action instead of silently advertising both. Explorer receiving a drop into a synced folder is also a different target from a browser’s SharePoint upload area.

Sources:

- [Delphi author’s implementation experience](https://www.freepascal.org/~michael/articles/dragdrop3/dragdrop3.pdf)
- [Chromium virtual-file extraction](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/ui/base/clipboard/clipboard_util_win.cc)
- [Mozilla/Total Commander investigation and QA](https://bugzilla.mozilla.org/show_bug.cgi?id=1958096)

**12. Chromium download drag-out demonstrates delayed CF_HDROP, including long-lived temp files** — high confidence, verified.

Chromium’s Windows download-drag preparation creates a temporary directory, constructs a DragDownloadFile and advertises delayed CF_HDROP through SetDownloadFileInfo. The data provider coordinates download rendering with async extraction. Its code schedules deletion after reboot because it cannot know when the target finishes using the temporary file. Separately, SetFileContents offers descriptor/stream data, so Chromium has both architectures. Electron’s public startDrag API accepts absolute file paths, not an IStream callback; A therefore requires additional native integration beyond that API. These are current source/API observations, not a tested mapping to every released browser or WebView2 build.

Sources:

- [Chromium download-drag preparation](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/content/browser/web_contents/web_contents_view_aura.cc)
- [Chromium Windows data provider](https://chromium.googlesource.com/chromium/src/%2B/refs/heads/main/ui/base/dragdrop/os_exchange_data_provider_win.cc)
- [Electron startDrag API](https://www.electronjs.org/docs/latest/api/web-contents/)

**13. Outlook is evidence of continued use, not a universal interoperability guarantee** — medium confidence, reported.

Chromium explicitly identifies native Outlook attachments as virtual incoming files and converts them to temporary files. New Outlook must be treated separately: a November 2025 report distinguished still-working attachment drag-out from failing message drag-out, with later comments reporting inconsistent recovery and continued browser-target failures. These reports cannot establish a single current Outlook transport or dialog style. The existence of commercial Outlook-to-temp-file bridges further illustrates the compatibility problem, but such bridges accept plaintext retention: one documents periodic cleanup plus cleanup at Outlook start/close.

Sources:

- [Chromium’s Outlook virtual-file handling](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/content/browser/web_contents/web_contents_view_aura.cc)
- [New Outlook practitioner reports](https://learn.microsoft.com/en-us/answers/questions/5631334/no-longer-able-to-drag-and-drop-email-from-new-out)
- [DragDrop for Outlook implementation and cleanup](https://www.dragdrop.com/faq/how-does-dragdrop-for-outlook-work-and-how-secure-is-it/)

**14. OneDrive placeholders, MTP and ZIP folders should not be treated as identical virtual sources** — high confidence, verified.

Microsoft’s cloud-placeholder architecture exposes filesystem objects that hydrate on ordinary access, with progress potentially shown through the copy engine; it is distinct from handing Explorer an arbitrary FILECONTENTS stream. WPD/MTP documentation exposes device objects through IStream, but does not establish the current Explorer drag format or dialog. ZIP history is also insufficient: Chen described limited development of the original ZIP component in 2018, while Microsoft announced expanded archive functionality in Windows 11 in 2023. Neither establishes today’s ZIP drag-out implementation. These examples support architectural distinctions, not a claim that all four use your prototype’s code path.

Sources:

- [Cloud Files platform architecture](https://learn.microsoft.com/en-us/windows/win32/cfapi/build-a-cloud-file-sync-engine)
- [WPD device-to-PC streams](https://learn.microsoft.com/en-us/windows/win32/wpd_sdk/transferring-content-from-the-device-to-a-pc)
- [WPD/MTP driver overview](https://learn.microsoft.com/en-us/windows-hardware/drivers/portable/wpd-drivers-overview)
- [Chen on original ZIP-folder implementation](https://devblogs.microsoft.com/oldnewthing/20180515-00/?p=98755)
- [Windows 11 archive improvements, 2023](https://blogs.windows.com/windowsdeveloper/2023/09/26/new-experiences-designed-to-make-every-developer-more-productive-on-windows-11/)

### Gaps, in the researcher's words

52 distinct relevant URLs were successfully opened across the team; searches and failed fetches are excluded. We reviewed documentation, source code and practitioners’ reports, but ran no Windows reproduction. We could not confirm Explorer’s exact Windows 11 dialog-selection implementation, a descriptor-only modern-UI switch, or your precise locked-destination/pre-first-Read hang. No maintenance roadmap or deprecation announcement for this particular dialog was found. Current build-specific ZIP/MTP dialog behavior, source-crash timeout and partial-file cleanup remain unverified. No complete compatibility matrix was established for chat apps, file dialogs, RDP/VMs, network shares or OneDrive/SharePoint folders. Direct file-manager forum coverage was incomplete; usable substitutes included Mozilla investigations and the Directory Opus manual. Bandizip, NanaZip and WinZip behavior was not established. WebView2-specific behavior was not independently established from Chromium behavior. Exactly two antivirus scans was not substantiated. “Verified” below means verified documentation/code, not independently executed behavior.


## Pass 3 — How 7-Zip and WinRAR manage the staging folder (2026-09-11)

*Codex Astra, 62 sources, 7-Zip's source read at 26.03 (mutable main, 2026-09-03).*

### The question as put

> Research question: when a Windows application drags files out of an archive by STAGING them in a temporary folder and handing Explorer CF_HDROP paths (the 7-Zip / WinRAR way), how exactly do 7-Zip and WinRAR manage that staging folder's lifetime — creation, naming and location, when the extraction actually runs relative to the drop, how they learn (or guess) that the consumer is done, when they delete, what they do when deletion fails or the process exits mid-drop, and what edge cases their users and developers have hit — so that we can copy what works and avoid what did not, instead of rediscovering it by testing.

### The researcher's position

Recommended lifecycle — these are design recommendations, not claims that either archive manager implements the whole policy.

1. Create a short, application-owned root beneath the user’s validated Windows temporary directory, with a configurable alternative for large extractions. Use one atomically created GUID-named directory per drag. Persist a small manifest containing creation time, state and owner identity; use your own timestamps rather than restored archive timestamps. [F1, F7, F9]
2. Keep QueryGetData side-effect-free. Create/materialize staging on the first valid CF_HDROP GetData request, even if it occurs during hover. Return one stable list of final, existing paths. This deliberately accepts possible extraction on a subsequently cancelled drag to avoid 7-Zip’s changing-parent-path compatibility problem. [F2, F6, F12]
3. Extract once, with progress and cooperative cancellation. Return an error if extraction fails; do not publish incomplete paths as a successful transfer. Wait for extraction workers to close their handles before reclaiming unpublished staging. [F3]
4. Delete immediately when staging was never handed out and the drag failed/cancelled. For an explicitly negotiated asynchronous transfer, retain ownership through EndOperation. Treat that as completion of that transfer, not proof that every possible application has finished with copied paths. Neither DoDragDrop return, COM release, nor a currently unlocked file universally establishes that. [F4, F10, F12, F14]
5. For paths handed to an unknown consumer, retain staging across application exit. A configurable 24-hour grace period is a reasonable starting policy, deliberately longer than WinRAR’s documented one-hour threshold; it is not a guaranteed safe lifetime. Consumers needing longer-lived references must receive durable exported files. [F6, F8, F12]
6. Scavenge on startup and periodically: inspect only your own manifested directories, exclude active operations across application instances, and reclaim expired abandoned/completed directories. Do not traverse reparse points outside the staging tree. After eligible deletion fails, retry with bounded backoff—for example 1, 10 and 60 seconds—then leave the manifest for subsequent sweeps. Normal exit should preserve unresolved consumer leases; crashes should require no special exit handler. [F1, F4, F8, F14]
7. Treat MOVE as permission to move the disposable staged copy, never as permission to remove the archive member. Keep an explicit extraction-to-destination command for large/network transfers that should bypass staging. [F5, F9]
8. Log drag ID, requested formats and their timing, extraction start/end/result, bytes staged, DoDragDrop result/effect, async negotiation/completion, expiry, deletion attempts and exact Windows errors. Keep filenames out of routine telemetry. Do not depend on reboot deletion: scheduling can fail and has privilege, path and directory restrictions. [F2, F3, F10, F11, F13, F14]

### Findings

**1. 7-Zip creates a per-drag 7zE directory directly under Windows TEMP** — medium confidence, verified.

OnDrag allocates an empty staging directory before DoDragDrop. In CPP/Windows/FileDir.cpp, CTempDir::Create obtains MyGetTempPath, which calls GetTempPathW on modern Windows. There is no additional permanent 7-Zip-specific parent directory in this path. CreateTempFile2 generates a directory named 7zE followed by eight uppercase hexadecimal characters, without .tmp; its generator uses tick/thread/process information and allows up to 100 collision attempts. Microsoft documents GetTempPath’s selection order as TMP, TEMP, USERPROFILE, then the Windows directory; it does not validate existence or access. Thus '%LOCALAPPDATA%\Temp' is a common configuration, not a hard-coded location. Microsoft now recommends GetTempPath2; non-SYSTEM callers retain the same environment-based selection behavior.

Sources:

- [7-Zip PanelDrag.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [7-Zip FileDir.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/Windows/FileDir.cpp)
- [Microsoft GetTempPathW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-gettemppathw)
- [Microsoft GetTempPath2W](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-gettemppath2w)

**2. 7-Zip defers extraction until a post-release data request** — medium confidence, verified.

CDropSource::QueryContinueDrag cancels on Escape, opposite-button input, or release with DROPEFFECT_NONE. Accepted release switches UsePreGlobal off; an unknown destination arms NeedCall_Copy. CDataObject::GetData subsequently calls CopyFromPanelTo_Folder once, before format validation; it invokes Panel->CopyTo. Early GetData returns the existing parent directory. QueryGetData only checks support. Consequently, hover requests normally avoid extracting contents in this implementation, while an accepted drop’s subsequent data request performs extraction inside the outstanding DoDragDrop call. A cancelled drag can therefore leave only the empty directory for cleanup.

Sources:

- [7-Zip PanelDrag.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)

**3. Extraction has a worker and modal progress, but failure is not reliably reflected in GetData** — medium confidence, verified.

GetData does not propagate Copy_HRESULT: failed or cancelled extraction can still return filenames. CopyFromPanelTo_Folder records failures and catches exceptions. Separately, PanelCopy.cpp’s CPanel::CopyTo configures CPanelCopyThread and extraction callbacks, invokes Create, and reads the worker result/messages. ProgressDialog2.cpp’s CProgressThreadVirt::Create starts the worker; the modal progress path waits for it before returning. Its Cancel button pauses and asks for confirmation; CProgressSync::CheckStop subsequently returns E_ABORT. ExtractCallback.cpp carries that cancellation into extraction through progress/open callbacks. Errors can keep the progress dialog open until Close. Thus extraction cancellation and Escape cancelling the earlier drag loop are distinct events. The history identifies a 15.13 mouse-capture fix for archive-to-Explorer dragging; the current extraction entry still calls SetCapture before potentially displaying modal UI.

Sources:

- [7-Zip PanelDrag.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [7-Zip PanelCopy.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelCopy.cpp)
- [7-Zip ProgressDialog2.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/ProgressDialog2.cpp)
- [7-Zip ExtractCallback.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/ExtractCallback.cpp)
- [7-Zip release history](https://www.7-zip.org/history.txt)

**4. Drag staging has scope-based cleanup, without a demonstrated recovery sweep** — medium confidence, verified.

OnDrag’s local CTempDir dies after post-drop handling. CTempDir::~CTempDir calls Remove and ignores its Boolean result. Remove makes one recursive RemoveDirWithSubItems attempt; failure retains the deletion-needed flag, but destruction leaves no owner to retry. The recursive helper handles read-only attributes and avoids traversing reparse-point directories; there is no delayed retry schedule here. Crash residue follows from this ownership model and is independently reported in F9.

Do not substitute the external-open lifecycle: PanelItemOpen.cpp has process tracking and DisableDeleting for a separate workflow. FM.cpp’s WM_CLOSE releases drag registration and the application; App.cpp/Panel.cpp close panels and open folders. No startup sweep was found in those inspected paths. Version 24.04, dated 2024-04-05, added a manual temporary-file deletion window; MyLoadMenu.cpp dispatches IDM_TEMP_DIR to MyBrowseForTempFolder. That is not evidence of automatic next-launch cleanup.

Sources:

- [7-Zip FileDir.h — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/Windows/FileDir.h)
- [7-Zip FileDir.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/Windows/FileDir.cpp)
- [7-Zip PanelDrag.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [7-Zip PanelItemOpen.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelItemOpen.cpp)
- [7-Zip FM.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/FM.cpp)
- [7-Zip App.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/App.cpp)
- [7-Zip Panel.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/Panel.cpp)
- [7-Zip MyLoadMenu.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/MyLoadMenu.cpp)
- [7-Zip release history](https://www.7-zip.org/history.txt)

**5. MOVE optimizes staged-file transfer; private formats enable direct extraction between 7-Zip windows** — medium confidence, verified.

In February 2026 ticket #2625, a user traces COPY|MOVE being advertised and MOVE returned without removal of the archive member. Igor explains that deleting archive members can risk data loss and require expensive recompression. His quoted implementation comments prefer moving staging on the same volume for speed and creation-time preservation, while allowing COPY for other applications.

The private formats are 7-Zip::SetTargetFolder, 7-Zip::SetTransfer and 7-Zip::GetTransfer; flags include WaitFinish, TempFiles, NamesAreParent and DoNotProcessInTarget. A known destination permits direct source-side CopyTo after DoDragDrop. The FAQ independently documents avoiding staging by dragging between 7-Zip windows or using Extract. The source identifies the newer transfer formats as v23; release history records expanded drag/drop and right-button support in 23.00. These private messages are not Explorer’s destination-discovery or completion protocol.

Sources:

- [7-Zip bug #2625 — February 2026 discussion](https://sourceforge.net/p/sevenzip/bugs/2625/)
- [7-Zip PanelDrag.cpp — raw](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [7-Zip FAQ](https://www.7-zip.org/faq.html)
- [7-Zip release history](https://www.7-zip.org/history.txt)

**6. Both early path substitution and immediate cleanup have broken consumers** — high confidence, reported.

Verified developer comments in PanelDrag.cpp include: “Some programs (like Sticky Notes in Win10) do not like virtual non-existing items” and “some programs (like Edge in Win10) can use names”. The documented tradeoff is an existing parent placeholder versus targets caching that early pathname.

Igor’s February 2026 quotation states: “But we delete temp folder after DoDragDrop(), and another program can't open input files in that case.” Independent reports supply concrete examples: a 2015 Log4View configuration dialog outlived staging; a proposed retain-until-panel-destruction patch was discussed but not accepted in that thread. Bug #1033 reports disappearing staging when dragging to FileZilla, starting with 4.65 in 2009. Later comments report VMware failure, and the Log4View thread contains a 2026 TeraCopy report. These establish recurring compatibility problems, not a verified common root cause for every report. The Firefox example in #1033 concerns opening archived HTML and must not be mistaken for proof of the drag-out path.

Sources:

- [7-Zip PanelDrag.cpp — raw consumer comments](https://raw.githubusercontent.com/ip7z/7zip/main/CPP/7zip/UI/FileManager/PanelDrag.cpp)
- [7-Zip bug #2625](https://sourceforge.net/p/sevenzip/bugs/2625/)
- [7-Zip Log4View and subsequent drag reports](https://sourceforge.net/p/sevenzip/discussion/45797/thread/3b8f3e73/)
- [7-Zip bug #1033 — FileZilla and subsequent reports](https://sourceforge.net/p/sevenzip/bugs/1033/)

**7. WinRAR’s temporary location is configurable; exact current Rar$DRa naming remains unverified** — medium confidence, verified.

WinRAR’s Paths documentation specifies a temporary-files folder, normally Windows TEMP. A blank setting can select the current or Windows temporary folder depending on the operation. The 'Use only for removable disks' restriction concerns archive modification; it should not be read as disabling all extraction staging. The documented command-line override is -w<path>, with an existing directory required by the Spanish manual—not a verified -tempdir switch.

A historical first-hand large-file report describes extraction followed by transfer from a rar$dr... directory. That supports the observed naming family, but neither it nor the documentation establishes the exact current Rar$DRa* suffix algorithm. The Spanish documentation is published by a site identifying itself as the official WinRAR registration, translation and support center for Spain and Latin America.

Sources:

- [WinRAR official Spanish Paths manual](https://winrar.es/soporte/manual/HELPPathsSettings)
- [WinRAR official Spanish -w manual](https://winrar.es/soporte/manual/HELPSwW)
- [WinRAR support-site provenance](https://winrar.es/acerca)
- [Historical first-hand rar$dr transfer report](https://forums.commentcamarche.net/forum/affich-4751242-temps-de-decompression-tres-tres-long)

**8. WinRAR documents next-run, age-based cleanup when external use cannot be determined** — medium confidence, verified.

The vendor’s English FAQ says externally viewed files are extracted to temp and deleted on a following run once at least one hour old, because external applications may still need them. The official Spanish FAQ explicitly includes drag extraction and files remaining in use when WinRAR closes. It describes deletion on a later use after more than an hour unused. These wordings do not identify the exact timestamp or establish a background one-hour timer.

Security documentation extends the policy to viewing, executing archived files and drag extraction. 'Wipe temporary files: Never' means normal deletion, not permanent retention; other choices alter wiping behavior. Busy files or uncertain external use can defer deletion. Separately, RARLAB’s 6.23 history changed Rar$LS* list-file cleanup from subsequent runs after one hour to immediate deletion. Those list files are a different category from drag staging.

Sources:

- [WinRAR vendor FAQ — temporary artifacts](https://www.win-rar.com/leave-artifacts-temp-folder.html)
- [WinRAR official Spanish FAQ](https://winrar.es/soporte/manual/HELPFAQ)
- [WinRAR official Spanish Security settings](https://winrar.es/soporte/manual/HELPSecuritySettings)
- [English WinRAR Security help mirror](https://documentation.help/WinRAR/HELPSecuritySettings.htm)
- [RARLAB release history — 6.23](https://www.rarlab.com/rarnew.htm)

**9. Large transfers expose staging capacity, duplicate I/O and crash-residue costs** — medium confidence, reported.

A 2013 Super User report describes a 7-Zip crash during drag extraction to a network share leaving approximately 7 GB behind. A 2015 SourceForge discussion reports a RAM-backed TEMP filling despite a separately configured working folder; Extract succeeded. A 2024 follow-up reports using process TEMP/TMP variables to redirect drag/open staging. These are version-specific observations, not a verified modern configuration contract.

The 7-Zip FAQ confirms the architectural cost: extract locally, then Explorer transfers those files. Cross-volume or network destinations can therefore require substantial temporary capacity and an additional transfer. Historical WinRAR users likewise report extraction completing before a second long transfer. External-viewer residue is another category: in #2518, Igor advised closing Notepad++ before 7-Zip and using its manual cleanup command. That advice should not be applied as the drag-out lifecycle.

Sources:

- [Super User — 7-Zip crash and 7 GB residue](https://superuser.com/questions/623537/7z-file-manager-crashed-and-left-temp-files-behind)
- [7-Zip TEMP versus working-folder discussion](https://sourceforge.net/p/sevenzip/discussion/45797/thread/69dc9d56/)
- [7-Zip FAQ](https://www.7-zip.org/faq.html)
- [WinRAR large-file transfer report](https://forums.commentcamarche.net/forum/affich-4751242-temps-de-decompression-tres-tres-long)
- [7-Zip bug #2518 — external-editor cleanup](https://sourceforge.net/p/sevenzip/bugs/2518/)

**10. Windows provides negotiated async completion, not a universal CF_HDROP consumption signal** — medium confidence, verified.

Microsoft’s Shell transfer documentation says a source supporting IDataObjectAsyncCapability checks InOperation after DoDragDrop: an active asynchronous extraction requires retaining resources until EndOperation. The documented synchronous path completes extraction within the drop operation. EndOperation reports completion of the negotiated extraction, not every subsequent use of pathnames retained by arbitrary applications.

Chromium’s Windows provider comments explicitly describe Explorer starting a background thread before StartOperation. This supports an intended Explorer async path, but does not prove Explorer always negotiates it for CF_HDROP. No current Explorer trace establishing that universal behavior was found. A historical Outlook/Shell developer report describes absent or unused async support, but its payload was CFSTR_FILECONTENTS/IStream, not staged CF_HDROP; it cannot settle this question.

Sources:

- [Microsoft Shell data-transfer scenarios](https://learn.microsoft.com/en-us/windows/win32/shell/datascenarios)
- [Microsoft IDataObjectAsyncCapability::EndOperation](https://learn.microsoft.com/en-us/windows/win32/api/shldisp/nf-shldisp-idataobjectasynccapability-endoperation)
- [Chromium Windows data provider — raw](https://raw.githubusercontent.com/chromium/chromium/main/ui/base/dragdrop/os_exchange_data_provider_win.cc)
- [Historical Outlook/IStream developer report](https://stackoverflow.com/questions/66180356/idataobjectasynccapability-not-being-handled-by-outlook-in-ole-drag-and-drop)

**11. Chromium’s outgoing download drag attempts reboot deletion, not timer-based cleanup** — medium confidence, verified.

In web_contents_view_aura.cc, PrepareDragForDownload creates a chrome_drag temporary directory and schedules the download pathname, then its directory, through DeleteFileAfterReboot. Its rationale is that the target’s completion time is unknown. The call-site ignores registration return values. file_util_win.cc implements the helper with MoveFileEx(..., nullptr, MOVEFILE_DELAY_UNTIL_REBOOT) and rejects paths at least MAX_PATH long. DragDownloadFile destruction disposes of its helper, not the completed staged file. No cleanup timer was found in this outgoing path.

The Windows data provider coordinates delayed rendering with its drag-loop/async state; its EndOperation changes operation state rather than implementing universal staging deletion. Incoming virtual-file staging that is deleted on tab closure is a separate Chromium path and should not be cited as the outgoing policy.

Sources:

- [Chromium WebContentsViewAura — raw](https://raw.githubusercontent.com/chromium/chromium/main/content/browser/web_contents/web_contents_view_aura.cc)
- [Chromium Windows file utilities — raw](https://raw.githubusercontent.com/chromium/chromium/main/base/files/file_util_win.cc)
- [Chromium DragDownloadFile — raw](https://raw.githubusercontent.com/chromium/chromium/main/content/browser/download/drag_download_file.cc)
- [Chromium Windows data provider — raw](https://raw.githubusercontent.com/chromium/chromium/main/ui/base/dragdrop/os_exchange_data_provider_win.cc)

**12. Targets can demand files during hover and retain file references after drop** — high confidence, verified.

Raymond Chen explicitly explains that GetData(CF_HDROP) during DragEnter can force materialization even if the drag is later abandoned; QueryGetData alone need not. Thus generic compatibility and 'never extract before acceptance' cannot both be assumed.

On the other end, Chromium converts dropped filenames into file-backed File objects. The File API specification explicitly handles a file being deleted or moved after a reference was obtained but before a later read, producing NotFoundError. Native drop completion therefore does not guarantee that a browser has durably copied the bytes. This is code/specification evidence of the capability, not a measurement of every upload widget.

Electron’s startDrag tutorial supplies existing paths. The inspected WebContents::StartDrag → DragFileItems implementation constructs FileInfo entries, calls SetFilenames and starts the drag; it provides no staged-file deletion policy or consumer-finished callback. Applications using it must manage staging themselves.

Sources:

- [Raymond Chen — premature CF_HDROP requests](https://devblogs.microsoft.com/oldnewthing/20120413-00/?p=7863)
- [Chromium dropped data objects — raw](https://raw.githubusercontent.com/chromium/chromium/main/third_party/blink/renderer/core/clipboard/data_object.cc)
- [Chromium file-backed File implementation — raw](https://raw.githubusercontent.com/chromium/chromium/main/third_party/blink/renderer/core/fileapi/file.cc)
- [W3C File API](https://www.w3.org/TR/FileAPI/)
- [Electron native drag tutorial](https://www.electronjs.org/docs/latest/tutorial/native-file-drag-drop/)
- [Electron WebContents API — raw](https://raw.githubusercontent.com/electron/electron/main/shell/browser/api/electron_api_web_contents.cc)
- [Electron Views drag implementation — raw](https://raw.githubusercontent.com/electron/electron/main/shell/browser/ui/drag_util_views.cc)

**13. Delete-at-reboot has privilege and directory limitations** — medium confidence, verified.

Microsoft documents MOVEFILE_DELAY_UNTIL_REBOOT as available only in administrator-group or LocalSystem context. It records operations in PendingFileRenameOperations, cannot handle remote-share paths, and deletes a directory only if it is empty. Files and child directories must therefore be scheduled before their parents. Success reports scheduling, not guaranteed eventual deletion. Chromium’s use demonstrates an attempted fallback, not proof that an ordinary unelevated application can reliably reclaim its staging this way. Always check the registration result if using it.

Sources:

- [Microsoft MoveFileExW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw)
- [Chromium DeleteFileAfterReboot — raw](https://raw.githubusercontent.com/chromium/chromium/main/base/files/file_util_win.cc)

**14. Deletion failures deserve retries; successful deletion is not proof that future reads are safe** — medium confidence, verified.

Microsoft documents deletion failure when existing handles do not permit FILE_SHARE_DELETE, and access-denied failures for read-only files. Ordinary successful deletion can mark a file pending deletion until remaining handles close; subsequent opens fail. Consequently, checking whether a file is currently locked does not establish whether a consumer will open or reopen it later.

A 2025 Microsoft Q&A report attributes intermittent rename failures to MsSense handles, with a Microsoft employee recommending retry handling. This is a first-hand report supporting transient-lock handling, not verified evidence that Defender routinely blocks archive drag cleanup. No comparable current Windows Search staging-specific behavior was confirmed. Preserve deletion errors and retry eligible cleanup rather than silently losing ownership as a failed scope-exit attempt can do.

Sources:

- [Microsoft DeleteFileW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-deletefilew)
- [Microsoft Q&A — reported MsSense locking](https://learn.microsoft.com/en-us/answers/questions/4372957/how-to-prevent-random-access-denied-errors-on-fold)

### Gaps, in the researcher's words

The three agents completed independently; 62 distinct successfully read sources were deduplicated, excluding failed opens and search-only hits. No interoperability tests were run. 'Verified' below means read in implementation or documentation, not reproduced on Windows. The 7-Zip source examined was mutable main identifying itself as 26.03, dated 2026-09-03; no commit-pinned or successful historical-source comparison was obtained. No automatic startup scavenger appeared in the inspected startup/lifecycle code, but this was not an exhaustive repository-wide proof of absence. The implementation behind 7-Zip’s manual temporary-file browser could not be fetched, so its selection rules and active-folder protections remain unknown. WinRAR’s closed implementation leaves its exact current Rar$DRa naming algorithm, CF_HDROP/GetData timing, cancellation behavior, retry schedule and expiry timestamp unconfirmed. Documentation establishes deferred cleanup, not its complete implementation. No current Explorer CF_HDROP trace proved universal StartOperation/EndOperation behavior or universal synchronous copying. No solid staged-CF_HDROP timing evidence was found for current Outlook, Teams, Slack, Windows Notepad or image editors. The Outlook evidence located concerns virtual IStream contents, a different transfer mechanism. Defender-related locking evidence is anecdotal; Windows Search locking of these staging directories was not confirmed. No sufficiently useful Reddit report or authoritative WinRAR '-tempdir' documentation was found. A finite retention interval cannot guarantee safety for applications that retain paths indefinitely.


## Measured on this machine (Windows 11, 2026-09-10 late evening)

The prototype `tools/dragproto` — a stand-alone, cgo-free OLE drag source offering
virtual files (`FileGroupDescriptorW` + `FileContents` as `IStream`) — was run by the user;
the stderr log is the record. What Explorer did:

- A 1 MiB virtual file dragged onto the desktop arrived whole; its SHA-256 matched the
  generator's.
- A 5 GiB virtual file and a 1 KiB one dragged together arrived whole (5 368 709 120 and 1 024
  bytes). Explorer asked the data object for `IMarshal` and `IAgileObject` and, refused both,
  marshalled every call back to the apartment that made the object: all 20 481 `Read` calls
  (256 KiB each), the `Seek`s, the async-capability calls — one thread id in the whole log.
  It ran the asynchronous protocol (`SetAsyncMode`, `StartOperation`, `InOperation`,
  `EndOperation(S_OK, DROPEFFECT_COPY)`). It requested the contents stream once *before* the
  drop (during the hover) and never read that one, then again after the drop and read that in
  7.3 s (about 730 MB/s), then `Seek(0, SET)` and release. Peak working set 85.5 MiB, almost
  all of it the Go runtime and the window; the stream held about 1.25 MiB in flight. The copy
  dialog was the old "copying from a device" one: a bar, no speed, no time remaining, no
  graph, no byte counts.
- Dropped again onto the desktop with the same names, Explorer showed its *Replace or Skip
  Files* dialog; on *Replace* it began the operation (`StartOperation`) and asked for the
  stream 2 s later, then never read a byte and never called `EndOperation`, because a
  `Get-FileHash` in another window still held the destination open and the copy engine
  stalled before its first read; the progress dialog could not be cancelled. The user then
  closed the prototype, which destroyed a data object Explorer still held five references to.
- `Get-FileHash` over the 5 GiB file merely took its time.

What the prototype gained from those runs: an `-agile` mode (the free-threaded marshaler
aggregated, `IAgileObject` answered — verified against the real ole32 marshaler in a test),
a close guard that refuses to tear down a data object while a drop is in flight (a second
close forces it and leaves the streams answering `STG_E_REVERTED`), and — after the ruling for
the staged route — an `-hdrop` mode that stages synthetic files and hands Explorer `CF_HDROP`
by delayed rendering, to measure that route the same way.

### The staged route, measured (2026-09-11, 00:28)

`-hdrop -n 1 -size 5GiB -agile`, dropped onto the desktop where the previous evening's 5 GiB
file still lay, then — after the user deleted that file — dropped again:

- Explorer asked for `CF_HDROP` **twice during the hover** and accepted the future paths it was
  given (the files did not exist yet). After the button's release its first request ran the
  extraction inside `GetData`: 5 GiB written in 6.4 s (about 840 MB/s, the synthetic
  generator), on Explorer's own thread — with `-agile`, 390 of the 726 COM calls ran off the
  drag thread. Four more `CF_HDROP` requests followed and were answered with the same paths.
- Explorer negotiated the asynchronous protocol for a `CF_HDROP` source (`StartOperation`,
  `InOperation` true) and `DoDragDrop` returned `DRAGDROP_S_DROP` with `DROPEFFECT_NONE` —
  the effect of an asynchronous drop is not reported there. **`EndOperation` never came**, not
  in the 80 s the process ran, not even for the drop that completed; `EndOperation` is not a
  cleanup signal for this route, the scavenge is.
- The first drop — a *Replace* over the existing 5 GiB file — stalled exactly as the
  virtual-file drop had: Explorer's copy engine never opened the staged file at all (the
  exclusive-share poll saw it free throughout) and its progress dialog stuck; the staged
  plaintext was left behind. The second drop, onto a desktop with no file of that name, was a
  **same-volume move**: the staged file was gone at once and the desktop file carries the
  staged file's write time — no second pass over the bytes. The stall therefore belongs to
  replacing a large existing destination on this machine, where Kaspersky's on-access scan of
  a 5 GiB file it has not seen holds the open for minutes (`avp.exe` at the top of the CPU,
  the shell sluggish, the previous evening's `Get-FileHash` "hang" on the same file); it is
  not the route's. Attribution test proposed: replace the same file with a plain Explorer
  copy, no prototype involved.
- Peak working set 60.8 MiB. A close while both drops were still "in operation" was refused
  once and forced on the second; the forced cleanup deleted only the last drag's (empty)
  folder — a prototype defect — and the stalled drag's folder with its 5 GiB stayed.
- **Confirmed** (00:40, Kaspersky paused by the user): the same *Replace* over the existing
  5 GiB file completed at once — a same-volume move — and the process exited without a
  pause. Kaspersky had been reading the large synthetic files at tens of MB/s with `avp.exe`
  at 5% CPU: an on-access scan of a file it had never seen, held across Explorer's open. The
  stall belongs to the machine's scanner, on either route. What the user then ruled: the wait
  before Explorer starts (the extraction inside the post-release `GetData`, 6.4 s here) must
  be visible in Enfold — the operation strip, by bytes, cancellable — and the window must never
  freeze; that is why WinRAR and 7-Zip show a progress window at exactly that moment.

