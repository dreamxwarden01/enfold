//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The data object is the drag: everything Explorer learns about the files, and
// every byte it ever gets, comes through here. It exposes IDataObject and
// IDataObjectAsyncCapability on one object (two cells, one reference count),
// hands out a fresh IStream per FileContents request, and enumerates its
// formats through a separate IEnumFORMATETC object as the interface requires.

// The Shell clipboard formats are not predefined -- "Each format you want to
// use must be registered by calling RegisterClipboardFormat" -- so they are
// registered once here. The feedback formats are registered only so that the
// log can name what Explorer asks for.
var (
	cfFileGroupDescriptorW uint16
	cfFileContents         uint16
	cfPerformedDropEffect  uint16
	cfLogicalPerformedDrop uint16
	cfPasteSucceeded       uint16
	cfPreferredDropEffect  uint16
	cfTargetCLSID          uint16
	cfInShellDragLoop      uint16

	registerFormatsOnce sync.Once
)

func registerFormats() {
	registerFormatsOnce.Do(func() {
		cfFileGroupDescriptorW = registerClipboardFormat("FileGroupDescriptorW")
		cfFileContents = registerClipboardFormat("FileContents")
		cfPerformedDropEffect = registerClipboardFormat("Performed DropEffect")
		cfLogicalPerformedDrop = registerClipboardFormat("Logical Performed DropEffect")
		cfPasteSucceeded = registerClipboardFormat("Paste Succeeded")
		cfPreferredDropEffect = registerClipboardFormat("Preferred DropEffect")
		cfTargetCLSID = registerClipboardFormat("TargetCLSID")
		cfInShellDragLoop = registerClipboardFormat("InShellDragLoop")
		logf("registered formats: FileGroupDescriptorW=%d FileContents=%d", cfFileGroupDescriptorW, cfFileContents)
	})
}

// formatName names a CLIPFORMAT for the log: the ones we registered by their
// constant name, anything else by whatever the shell has it registered as.
func formatName(cf uint16) string {
	switch cf {
	case cfFileGroupDescriptorW:
		return "CFSTR_FILEDESCRIPTORW"
	case cfFileContents:
		return "CFSTR_FILECONTENTS"
	case cfPerformedDropEffect:
		return "CFSTR_PERFORMEDDROPEFFECT"
	case cfLogicalPerformedDrop:
		return "CFSTR_LOGICALPERFORMEDDROPEFFECT"
	case cfPasteSucceeded:
		return "CFSTR_PASTESUCCEEDED"
	case cfPreferredDropEffect:
		return "CFSTR_PREFERREDDROPEFFECT"
	case cfTargetCLSID:
		return "CFSTR_TARGETCLSID"
	case cfInShellDragLoop:
		return "CFSTR_INDRAGLOOP"
	case cfHDrop:
		return "CF_HDROP"
	}
	if n := clipboardFormatName(cf); n != "" {
		return fmt.Sprintf("%q(%d)", n, cf)
	}
	return fmt.Sprintf("cf%d", cf)
}

func feString(fe *formatEtc) string {
	if fe == nil {
		return "<nil FORMATETC>"
	}
	return fmt.Sprintf("{%s, ptd=0x%X, aspect=%d, lindex=%d, %s}",
		formatName(fe.cfFormat), fe.ptd, fe.dwAspect, fe.lindex, tymedName(fe.tymed))
}

// ---------------------------------------------------------------------------

type dataObject struct {
	// files, write and streamAtEnd are set before the object is handed over and
	// never change afterwards. That matters more than it looks: with -agile they
	// are read from several of the target's threads at once, and immutability is
	// the whole of what makes reading them without a lock safe. The streams the
	// object hands out hold a pointer into files and read it the same way.
	files []synthFile
	write time.Time
	// streamAtEnd decides where a FileContents stream's seek pointer is when
	// GetData hands it over. See the comment on -streamat in main; it is a flag
	// because the documentation and the obvious reading disagree, and which one
	// Explorer means is a thing the prototype is here to measure.
	streamAtEnd bool

	// stage is the -hdrop mode, and is nil in the virtual-file mode. When it is
	// set the object offers CF_HDROP by delayed rendering over that staging
	// folder and CFSTR_PREFERREDDROPEFFECT beside it, and offers neither
	// FileGroupDescriptorW nor FileContents: the target picks the format, so
	// advertising both routes at once would mean measuring whichever one the
	// target happened to prefer. Set before the object is handed over and never
	// changed, like files above, which is what makes it safe to read from the
	// target's threads under -agile.
	stage *dragStage

	mu        sync.Mutex
	self      *comObject // the object these interfaces belong to; set by setCOMObject
	asyncMode bool
	inOp      bool
	selfRef   bool // an AddRef taken by SetAsyncMode, owed back

	// getCalls is an atomic because comDestroy reads it, and comDestroy runs on
	// whichever thread happened to make the last Release -- not necessarily one
	// that ever held the mutex.
	getCalls atomic.Int64

	formats []formatEtc
}

// setCOMObject runs inside newCOMObject, before the interface cells are
// published, so self is never nil for a call that arrives through one of them.
func (d *dataObject) setCOMObject(o *comObject) {
	d.mu.Lock()
	d.self = o
	d.mu.Unlock()
}

func (d *dataObject) comDestroy() {
	logf("IDataObject destroyed after %d GetData calls", d.getCalls.Load())
}

// newDataObject builds the object and its format list. The descriptor is
// advertised with lindex -1 -- there is one descriptor for the whole group --
// and FileContents once per file with its own zero-based lindex, which is how
// the Shell clipboard documentation says a particular file is named: "set the
// lIndex value of the FORMATETC structure to the zero-based index of the file's
// FILEDESCRIPTOR structure".
func newDataObject(files []synthFile, write time.Time, streamAtEnd bool) *comObject {
	registerFormats()
	d := &dataObject{files: files, write: write, streamAtEnd: streamAtEnd}
	d.formats = append(d.formats, formatEtc{
		cfFormat: cfFileGroupDescriptorW,
		dwAspect: dvAspectContent,
		lindex:   -1,
		tymed:    tymedHGlobal,
	})
	for i := range files {
		d.formats = append(d.formats, formatEtc{
			cfFormat: cfFileContents,
			dwAspect: dvAspectContent,
			lindex:   int32(i),
			tymed:    tymedIStream,
		})
	}
	return newDataObjectCOM(d)
}

// newHDropDataObject is -hdrop's data object: CF_HDROP by delayed rendering over
// the staging folder, CFSTR_PREFERREDDROPEFFECT beside it, and nothing else.
//
// Both are advertised with lindex -1 -- neither names one file of several; the
// HDROP is the whole list -- and both as TYMED_HGLOBAL, which is what the
// scenarios page says for CF_HDROP ("set ... the tymed member to TYMED_HGLOBAL")
// and what "The structure's hGlobal member points to a DWORD value" says for the
// drop effect.
//
// IDataObjectAsyncCapability stays on the object. Whether Explorer negotiates
// the asynchronous protocol for a CF_HDROP source at all is one of the open
// questions -- the research pass found no trace that it does -- and the only way
// to find out is to offer it and watch.
func newHDropDataObject(stage *dragStage) *comObject {
	registerFormats()
	d := &dataObject{stage: stage}
	d.formats = []formatEtc{
		{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal},
		{cfFormat: cfPreferredDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal},
	}
	return newDataObjectCOM(d)
}

// newDataObjectCOM wraps either mode's dataObject in its COM object. It is one
// function so that the two modes cannot drift apart in which interfaces they
// expose -- the mode is the format list and nothing else.
func newDataObjectCOM(d *dataObject) *comObject {
	// newCOMObject calls d.setCOMObject before it publishes the cells, so the
	// object knows itself from the first instant COM can reach it.
	return newTransferCOMObject("IDataObject", d,
		comIface{
			name: "IDataObject",
			vtbl: vtblIDataObject,
			iids: []windows.GUID{iidIDataObject},
		},
		comIface{
			name: "IDataObjectAsyncCapability",
			vtbl: vtblIDataObjectAsyncCapability,
			iids: []windows.GUID{iidIDataObjectAsyncCapability},
		},
	)
}

// offers reports whether this object ever advertised a format at all, whatever
// the aspect, index or medium asked for. It is what separates "you asked for the
// wrong medium of something I have" from "I never had that", and in -hdrop mode
// it is what makes a request for FileContents an honest DV_E_FORMATETC.
func (d *dataObject) offers(cf uint16) bool {
	for i := range d.formats {
		if d.formats[i].cfFormat == cf {
			return true
		}
	}
	return false
}

// when is the timing word for a log line: where this call sits relative to the
// button's release. Every question about delayed rendering is asked on that
// axis, so every format request carries it.
func (d *dataObject) when() string {
	if d.stage == nil {
		return ""
	}
	return " " + d.stage.timing()
}

func dataObjectOf(this uintptr) (*dataObject, bool) {
	o, _, ok := comLookup(this)
	if !ok {
		return nil, false
	}
	d, ok := o.impl.(*dataObject)
	return d, ok
}

// match finds the advertised format a request names. cfFormat, dwAspect and
// lindex must be equal; tymed only has to overlap, because a caller may OR
// several media together and mean "any of these will do".
//
// ptd is ignored on purpose, and that is the same answer GetCanonicalFormatEtc
// gives: nothing this object renders depends on a device -- a descriptor and a
// byte stream are the same whatever is going to display them -- so a request
// naming a target device matches the device-independent rendering rather than
// being refused. FORMATETC documents NULL as the device-independent case ("A
// NULL value is used whenever the specified data format is independent of the
// target device"), and GetCanonicalFormatEtc below tells every caller so, by
// storing NULL in the ptd of the format it hands back. Answering a non-null ptd
// here is what makes that promise true rather than advice.
func (d *dataObject) match(fe *formatEtc) (int, bool) {
	for i := range d.formats {
		f := &d.formats[i]
		if fe.cfFormat == f.cfFormat &&
			fe.dwAspect == f.dwAspect &&
			fe.lindex == f.lindex &&
			fe.tymed&f.tymed != 0 {
			return i, true
		}
	}
	return 0, false
}

// modeNote names which mode this object is in, for the one log line where the
// answer would otherwise look arbitrary: a target asking for a format the other
// mode would have offered.
func (d *dataObject) modeNote() string {
	if d.stage != nil {
		return "; this object is in -hdrop mode and offers CF_HDROP and CFSTR_PREFERREDDROPEFFECT only"
	}
	return "; this object is in virtual-file mode and offers FileGroupDescriptorW and FileContents only"
}

// renderHGlobal fills a TYMED_HGLOBAL medium with blob, which is how all three
// of the block-shaped formats here are delivered: the descriptor, the HDROP and
// the preferred drop effect. GHND is the conventional allocation for a handle a
// data-transfer consumer will free, and pUnkForRelease stays NULL because, as
// STGMEDIUM has it, "If pUnkForRelease is NULL, the receiver of the medium is
// responsible for releasing it".
func renderHGlobal(blob []byte, pmedium *stgMedium) uintptr {
	h := globalAlloc(gHND, uintptr(len(blob)))
	if h == 0 {
		logf("  -> STG_E_MEDIUMFULL (GlobalAlloc %d bytes failed)", len(blob))
		return stgEMediumFull
	}
	p := globalLock(h)
	if p == 0 {
		globalFree(h)
		logf("  -> STG_E_MEDIUMFULL (GlobalLock failed)")
		return stgEMediumFull
	}
	copyIntoNative(p, blob)
	globalUnlock(h)
	pmedium.tymed = tymedHGlobal
	pmedium.data = h
	pmedium.pUnkForRelease = 0
	return sOK
}

func dataGetData(this uintptr, pformatetcIn *formatEtc, pmedium *stgMedium) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if pmedium == nil || pformatetcIn == nil {
		return ePointer
	}
	*pmedium = stgMedium{}
	fe := *pformatetcIn

	n := d.getCalls.Add(1)
	logf("IDataObject::GetData #%d %s%s", n, feString(&fe), d.when())
	if d.stage != nil {
		d.stage.noteFormat("GetData", formatName(fe.cfFormat))
	}

	if _, ok := d.match(&fe); !ok {
		// Say precisely which part of the FORMATETC was refused. Getting this
		// wrong costs a whole drag test: a caller told DV_E_FORMATETC when the
		// real problem was the medium goes off and tries a different format
		// instead of a different medium, and the log never says which it was.
		//
		// A format this object never advertised in this mode is refused before
		// any of that: in -hdrop mode a request for FileContents is not a wrong
		// medium, it is a format that is not on offer.
		if !d.offers(fe.cfFormat) {
			logf("  -> DV_E_FORMATETC (a format this object never offered%s)", d.modeNote())
			return dvEFormatEtc
		}
		switch fe.cfFormat {
		case cfHDrop, cfPreferredDropEffect:
			if fe.dwAspect != dvAspectContent {
				logf("  -> DV_E_DVASPECT (only DVASPECT_CONTENT is offered)")
				return dvEDvAspect
			}
			if fe.tymed&tymedHGlobal == 0 {
				logf("  -> DV_E_TYMED (%s is only offered as TYMED_HGLOBAL)", formatName(fe.cfFormat))
				return dvETymed
			}
			logf("  -> DV_E_LINDEX (%s is the whole list; it is advertised with lindex -1, asked for lindex %d)",
				formatName(fe.cfFormat), fe.lindex)
			return dvELIndex

		case cfFileGroupDescriptorW:
			if fe.dwAspect != dvAspectContent {
				logf("  -> DV_E_DVASPECT (only DVASPECT_CONTENT is offered)")
				return dvEDvAspect
			}
			if fe.tymed&tymedHGlobal == 0 {
				logf("  -> DV_E_TYMED (the descriptor is only offered as TYMED_HGLOBAL)")
				return dvETymed
			}
			logf("  -> DV_E_LINDEX (the descriptor is the whole group; it is advertised with lindex -1)")
			return dvELIndex
		case cfFileContents:
			if fe.dwAspect != dvAspectContent {
				logf("  -> DV_E_DVASPECT (only DVASPECT_CONTENT is offered)")
				return dvEDvAspect
			}
			if fe.tymed&tymedIStream == 0 {
				logf("  -> DV_E_TYMED (file contents are only offered as TYMED_ISTREAM)")
				return dvETymed
			}
			// A caller asking for the contents of a one-file object with
			// lindex -1 has almost certainly not read the descriptor. Serve it
			// rather than fail the drag, but say so loudly: if this line ever
			// appears in a real log it is a finding about the consumer, and the
			// shell version it came from.
			if fe.lindex == -1 && len(d.files) == 1 {
				logf("  !! lindex -1 for file contents, which names no file; serving file 0 anyway")
				fe.lindex = 0
				break
			}
			logf("  -> DV_E_LINDEX (have %d file(s), asked for lindex %d)", len(d.files), fe.lindex)
			return dvELIndex
		default:
			logf("  -> DV_E_FORMATETC (a format this object never offered)")
			return dvEFormatEtc
		}
	}

	switch fe.cfFormat {
	case cfFileGroupDescriptorW:
		blob := encodeFileGroupDescriptorW(d.files, d.write)
		if hr := renderHGlobal(blob, pmedium); hr != sOK {
			return hr
		}
		logf("  -> TYMED_HGLOBAL, %d bytes, %d descriptors", len(blob), len(d.files))
		return sOK

	case cfHDrop:
		if d.stage == nil {
			return eUnexpected
		}
		// The state machine decides what this request means -- hover, drop, or a
		// repeat -- and runs the extraction when it is the drop's own. It can
		// fail, and then so does GetData: handing out the names of files that are
		// not there is the defect 7-Zip's own comments describe.
		paths, word, hr := d.stage.requestPaths()
		if hr != sOK {
			logf("  -> %s (%s)", hrName(hr), word)
			return hr
		}
		blob := encodeDropFiles(paths)
		if hr := renderHGlobal(blob, pmedium); hr != sOK {
			return hr
		}
		logf("  -> TYMED_HGLOBAL, %d bytes, CF_HDROP naming %d path(s) -- %s: %s",
			len(blob), len(paths), word, strings.Join(paths, " | "))
		return sOK

	case cfPreferredDropEffect:
		effect := uint32(dropEffectCopy)
		if d.stage != nil {
			effect = d.stage.cfg.preferredEffect()
		}
		blob := encodeDropEffect(effect)
		if hr := renderHGlobal(blob, pmedium); hr != sOK {
			return hr
		}
		logf("  -> TYMED_HGLOBAL, %d bytes, CFSTR_PREFERREDDROPEFFECT = %s", len(blob), effectName(effect))
		return sOK

	case cfFileContents:
		f := &d.files[fe.lindex]
		// GetData's Notes to Callers say the data on a stream medium "extends
		// from position zero of the stream pointer through to the position
		// immediately before the current stream pointer (that is, the stream
		// pointer position upon exit)" -- which makes the end of the data the
		// seek position the provider leaves behind, and is why Raymond Chen's
		// virtual-file sample seeks his stream to the end before returning it.
		// A consumer that instead just starts reading would then get nothing.
		// Both readings are defensible; -streamat picks between them and the
		// log says which was used, so a wrong guess reads as a measurement
		// rather than a mystery.
		pos := int64(0)
		where := "start (position 0)"
		if d.streamAtEnd {
			pos = f.size
			where = "end (position = file size), per GetData's Notes to Callers"
		}
		o := newStreamObject(f, pos)
		pmedium.tymed = tymedIStream
		pmedium.data = o.unknown()
		pmedium.pUnkForRelease = 0
		logf("  -> TYMED_ISTREAM for %q (%d bytes), stream at 0x%X, seek pointer at the %s",
			f.name, f.size, pmedium.data, where)
		return sOK
	}
	logf("  -> DV_E_FORMATETC (matched but unhandled)")
	return dvEFormatEtc
}

// dataGetDataHere would have to render into a medium the caller allocated. This
// object has no such rendering: a file is an IStream it creates, and there is
// no HGLOBAL rendering of a 5 GiB file worth offering. Note that GetDataHere's
// documented return values do not include E_NOTIMPL -- the codes it gives for
// "I cannot do that" are DV_E_FORMATETC and DV_E_TYMED -- so it declines with
// the one that fits.
func dataGetDataHere(this uintptr, pformatetc *formatEtc, pmedium *stgMedium) uintptr {
	// The answer does not depend on the object, but the lookup happens anyway:
	// it is what counts this call and its thread, and what names a call that
	// arrived on an interface pointer the caller had already released. The same
	// goes for the three advisory methods below.
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	logf("IDataObject::GetDataHere %s -> DV_E_FORMATETC (no caller-allocated rendering)", feString(pformatetc))
	return dvEFormatEtc
}

func dataQueryGetData(this uintptr, pformatetc *formatEtc) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if pformatetc == nil {
		return ePointer
	}
	fe := *pformatetc
	if d.stage != nil {
		d.stage.noteFormat("QueryGetData", formatName(fe.cfFormat))
	}
	if _, ok := d.match(&fe); ok {
		logf("IDataObject::QueryGetData %s -> S_OK%s", feString(&fe), d.when())
		return sOK
	}
	// A caller asking about FileContents with lindex -1 is asking whether the
	// format exists at all rather than about one file. Answer the question that
	// was meant.
	if fe.cfFormat == cfFileContents && fe.lindex == -1 &&
		fe.dwAspect == dvAspectContent && fe.tymed&tymedIStream != 0 && len(d.files) > 0 {
		logf("IDataObject::QueryGetData %s -> S_OK (format probe, lindex -1)", feString(&fe))
		return sOK
	}
	// DV_E_FORMATETC rather than S_FALSE: S_FALSE is a success code, so a
	// caller testing SUCCEEDED(hr) would read it as "yes, I have that".
	logf("IDataObject::QueryGetData %s -> DV_E_FORMATETC%s", feString(&fe), d.when())
	return dvEFormatEtc
}

// dataGetCanonicalFormatEtc: this object never renders per device, so the
// documented simplest implementation applies -- "copy the input FORMATETC to
// the output FORMATETC, store a NULL in the ptd member of the output FORMATETC,
// and return DATA_S_SAMEFORMATETC".
func dataGetCanonicalFormatEtc(this uintptr, pformatetcIn *formatEtc, pformatetcOut *formatEtc) uintptr {
	if pformatetcOut == nil {
		return ePointer
	}
	if pformatetcIn != nil {
		*pformatetcOut = *pformatetcIn
	} else {
		*pformatetcOut = formatEtc{}
	}
	pformatetcOut.ptd = 0
	logf("IDataObject::GetCanonicalFormatEtc %s -> DATA_S_SAMEFORMATETC", feString(pformatetcIn))
	return dataSSameFormatEtc
}

// feedbackFormats are the formats a drop target uses to report back to the
// source. Nothing here acts on any of them -- the drag offers DROPEFFECT_COPY
// only, so there is no optimized move to finish and no delete-on-paste to
// complete -- but which ones arrive, and with what values, is exactly what the
// drag test wants to see.
func (d *dataObject) isFeedbackFormat(cf uint16) bool {
	switch cf {
	case cfPerformedDropEffect, cfLogicalPerformedDrop, cfPasteSucceeded,
		cfPreferredDropEffect, cfTargetCLSID, cfInShellDragLoop:
		return true
	}
	return false
}

func dataSetData(this uintptr, pformatetc *formatEtc, pmedium *stgMedium, fRelease uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if pformatetc == nil || pmedium == nil {
		return ePointer
	}
	if !d.isFeedbackFormat(pformatetc.cfFormat) {
		// "A data object implements this method if it supports receiving data
		// from another object. If it does not support this, it should be
		// implemented to return E_NOTIMPL." Declining also leaves the medium
		// with its caller: ownership only moves on success.
		logf("IDataObject::SetData %s fRelease=%d -> E_NOTIMPL (not a feedback format)",
			feString(pformatetc), uint32(fRelease))
		return eNotImpl
	}
	// Only now, and only this far. Every feedback format above is documented as
	// a DWORD in an HGLOBAL, so reading the first four bytes of one is reading
	// what the sender sent; reading four bytes of an arbitrary medium the caller
	// allocated is an overread of somebody else's memory, and the format is the
	// only thing that says which of the two this is. GlobalSize is the second
	// half of that: it returns zero for a handle that is not one, so a block it
	// says is shorter than a DWORD is left alone.
	value := ""
	if pmedium.tymed == tymedHGlobal && pmedium.data != 0 && globalSize(pmedium.data) >= 4 {
		if p := globalLock(pmedium.data); p != 0 {
			var b [4]byte
			copyFromNative(b[:], p)
			globalUnlock(pmedium.data)
			v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
			value = fmt.Sprintf(", first DWORD = %d (%s)", v, effectName(v))
		}
	}
	logf("IDataObject::SetData %s fRelease=%d%s -> S_OK", feString(pformatetc), uint32(fRelease), value)
	if fRelease != 0 {
		// "If TRUE, the data object called, which implements SetData, owns the
		// storage medium after the call returns. This means it must free the
		// medium after it has been used by calling the ReleaseStgMedium
		// function."
		procReleaseStgMedium.Call(uintptr(unsafe.Pointer(pmedium)))
	}
	return sOK
}

func dataEnumFormatEtc(this uintptr, dwDirection uintptr, ppenumFormatEtc *uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if ppenumFormatEtc == nil {
		return ePointer
	}
	*ppenumFormatEtc = 0
	switch uint32(dwDirection) {
	case dataDirGet:
	case dataDirSet:
		// "E_NOTIMPL: The direction specified by dwDirection is not supported."
		// Nothing can be set on this object, so there is nothing to enumerate.
		logf("IDataObject::EnumFormatEtc(DATADIR_SET) -> E_NOTIMPL")
		return eNotImpl
	default:
		// "E_INVALIDARG: The supplied dwDirection is invalid."
		logf("IDataObject::EnumFormatEtc(direction %d) -> E_INVALIDARG", uint32(dwDirection))
		return eInvalidArg
	}
	o := newEnumFormatEtcObject(d.formats, 0)
	*ppenumFormatEtc = o.unknown()
	logf("IDataObject::EnumFormatEtc(DATADIR_GET) -> enumerator at 0x%X over %d formats",
		*ppenumFormatEtc, len(d.formats))
	return sOK
}

// Advisory connections are for data that changes underneath the consumer. The
// files this object offers do not change while the drag is up, so it supports
// none of it, which OLE_E_ADVISENOTSUPPORTED is the code for.
func dataDAdvise(this uintptr, pformatetc *formatEtc, advf uintptr, pAdvSink uintptr, pdwConnection *uint32) uintptr {
	if pdwConnection != nil {
		*pdwConnection = 0
	}
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	logf("IDataObject::DAdvise -> OLE_E_ADVISENOTSUPPORTED")
	return oleEAdviseNotSupp
}

func dataDUnadvise(this uintptr, dwConnection uintptr) uintptr {
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	logf("IDataObject::DUnadvise -> OLE_E_ADVISENOTSUPPORTED")
	return oleEAdviseNotSupp
}

func dataEnumDAdvise(this uintptr, ppenumAdvise *uintptr) uintptr {
	if ppenumAdvise != nil {
		*ppenumAdvise = 0
	}
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	logf("IDataObject::EnumDAdvise -> OLE_E_ADVISENOTSUPPORTED")
	return oleEAdviseNotSupp
}

const (
	dataDirGet = 1
	dataDirSet = 2
)

var vtblIDataObject = pinVtbl(iDataObjectVtbl{
	QueryInterface:        cbQueryInterface,
	AddRef:                cbAddRef,
	Release:               cbRelease,
	GetData:               syscall.NewCallback(dataGetData),
	GetDataHere:           syscall.NewCallback(dataGetDataHere),
	QueryGetData:          syscall.NewCallback(dataQueryGetData),
	GetCanonicalFormatEtc: syscall.NewCallback(dataGetCanonicalFormatEtc),
	SetData:               syscall.NewCallback(dataSetData),
	EnumFormatEtc:         syscall.NewCallback(dataEnumFormatEtc),
	DAdvise:               syscall.NewCallback(dataDAdvise),
	DUnadvise:             syscall.NewCallback(dataDUnadvise),
	EnumDAdvise:           syscall.NewCallback(dataEnumDAdvise),
})

// ---------------------------------------------------------------------------
// IDataObjectAsyncCapability.
//
// The documented handshake: the source calls SetAsyncMode(VARIANT_TRUE) before
// the drag; the target, in its Drop, calls GetAsyncMode, and if it agrees calls
// StartOperation, returns from Drop, extracts on a thread of its own, and calls
// EndOperation when it is done. The source calls InOperation after DoDragDrop
// returns to find out which of the two happened.
//
// The reference count is the whole point on this side: "If fDoOpAsync is set to
// VARIANT_TRUE, SetAsyncMode must call AddRef, and store the interface pointer
// for use by EndOperation" -- that reference is what keeps the object, its
// files and its streams alive after the drag loop has gone, and EndOperation is
// what gives it back.

func dataSetAsyncMode(this uintptr, fDoOpAsync uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	on := uint32(fDoOpAsync) != 0
	n := int32(-1)
	d.mu.Lock()
	d.asyncMode = on
	// selfRef is the ledger EndOperation and finishSync release against, so it
	// must not be possible for it to say a reference was taken when none was.
	// Deciding and taking under the one lock is what makes them agree; the
	// self != nil arm is belt and braces, since setCOMObject runs before the
	// cells are published and nothing can call in before that.
	take := on && !d.selfRef && d.self != nil
	if take {
		d.selfRef = true
		n = d.self.addRef()
	}
	d.mu.Unlock()
	logf("IDataObjectAsyncCapability::SetAsyncMode(%d) -> async=%v, self-reference taken=%v (refs=%d)",
		int32(uint32(fDoOpAsync)), on, take, n)
	return sOK
}

func dataGetAsyncMode(this uintptr, pfIsOpAsync *uint32) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if pfIsOpAsync == nil {
		return ePointer
	}
	d.mu.Lock()
	on := d.asyncMode
	d.mu.Unlock()
	if on {
		*pfIsOpAsync = variantTrue
	} else {
		*pfIsOpAsync = variantFalse
	}
	logf("IDataObjectAsyncCapability::GetAsyncMode -> %v (0x%08X)", on, *pfIsOpAsync)
	return sOK
}

func dataStartOperation(this uintptr, pbcReserved uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	d.mu.Lock()
	d.inOp = true
	stage := d.stage
	d.mu.Unlock()
	logf("IDataObjectAsyncCapability::StartOperation -- the target will extract on a thread of its own")
	if stage != nil {
		// One of the mode's open questions, answered in the log the moment it
		// happens: nothing in the research proved that Explorer runs the
		// asynchronous protocol for a CF_HDROP source rather than only for a
		// virtual-file one.
		stage.noteAsyncStarted()
	}
	return sOK
}

func dataInOperation(this uintptr, pfInAsyncOp *uint32) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if pfInAsyncOp == nil {
		return ePointer
	}
	d.mu.Lock()
	in := d.inOp
	d.mu.Unlock()
	if in {
		*pfInAsyncOp = variantTrue
	} else {
		*pfInAsyncOp = variantFalse
	}
	logf("IDataObjectAsyncCapability::InOperation -> %v", in)
	return sOK
}

func dataEndOperation(this uintptr, hResult uintptr, pbcReserved uintptr, dwEffects uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	logf("IDataObjectAsyncCapability::EndOperation(hResult=%s, dwEffects=%s)",
		hrName(hResult), effectName(uint32(dwEffects)))
	d.mu.Lock()
	d.inOp = false
	self := d.self
	give := d.selfRef
	d.selfRef = false
	stage := d.stage
	d.mu.Unlock()
	if stage != nil {
		// Recorded here, acted on by the stage's own goroutine: deleting the
		// staged files inside the call the target is waiting to return from
		// would make this program the reason its copy looked slow.
		stage.noteEndOperation()
	}
	if give && self != nil {
		// Give back the reference SetAsyncMode took. This may well be the last
		// one, so nothing may touch d afterwards.
		self.release()
	}
	// A close that arrived while this operation was running was deferred waiting
	// for exactly this call; tell the window it may go. With -agile this runs on
	// a thread of the target's, which is why the window is told by posting to it
	// rather than by touching it.
	dropOperationEnded()
	return sOK
}

// inOperation reports whether the target has called StartOperation and not yet
// EndOperation. While that is true the transfer is live: the object, the files
// it describes and every stream it handed out have to stay alive and answering,
// however much the user would like the window to go away.
func (d *dataObject) inOperation() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inOp
}

// finishSync gives back the SetAsyncMode reference when no asynchronous
// operation ever started, which is the source's own cleanup step: "If
// InOperation fails or returns VARIANT_FALSE, a normal synchronous data
// transfer has taken place ... The source should do any cleanup that is
// required." Without it the object would sit on that reference forever.
func (d *dataObject) finishSync() {
	d.mu.Lock()
	self := d.self
	give := d.selfRef && !d.inOp
	if give {
		d.selfRef = false
	}
	d.mu.Unlock()
	if give && self != nil {
		logf("no asynchronous operation started; giving back the SetAsyncMode reference")
		self.release()
	}
}

// The struct is kept as well as the pinned copy, because the source side of the
// handshake -- SetAsyncMode before the drag, InOperation after it -- is made as
// a real indirect call through these same function pointers. Going the long way
// round is deliberate: it exercises the vtable in the direction COM uses it, so
// a slot in the wrong place shows up in the prototype's own log rather than
// only in Explorer's.
var asyncVtbl = iDataObjectAsyncCapabilityVtbl{
	QueryInterface: cbQueryInterface,
	AddRef:         cbAddRef,
	Release:        cbRelease,
	SetAsyncMode:   syscall.NewCallback(dataSetAsyncMode),
	GetAsyncMode:   syscall.NewCallback(dataGetAsyncMode),
	StartOperation: syscall.NewCallback(dataStartOperation),
	InOperation:    syscall.NewCallback(dataInOperation),
	EndOperation:   syscall.NewCallback(dataEndOperation),
}

var vtblIDataObjectAsyncCapability = pinVtbl(asyncVtbl)

// ---------------------------------------------------------------------------
// IEnumFORMATETC.

type enumFormatEtc struct {
	formats []formatEtc
	mu      sync.Mutex
	idx     int
}

func (e *enumFormatEtc) comDestroy() {}

func newEnumFormatEtcObject(formats []formatEtc, idx int) *comObject {
	// Copy: the enumerator outlives nothing in particular, but it must not be
	// able to see a later edit of the data object's list.
	cp := make([]formatEtc, len(formats))
	copy(cp, formats)
	e := &enumFormatEtc{formats: cp, idx: idx}
	return newTransferCOMObject("IEnumFORMATETC", e, comIface{
		name: "IEnumFORMATETC",
		vtbl: vtblIEnumFORMATETC(),
		iids: []windows.GUID{iidIEnumFORMATETC},
	})
}

func enumOf(this uintptr) (*enumFormatEtc, bool) {
	o, _, ok := comLookup(this)
	if !ok {
		return nil, false
	}
	e, ok := o.impl.(*enumFormatEtc)
	return e, ok
}

func enumNext(this uintptr, celt uintptr, rgelt *formatEtc, pceltFetched *uint32) uintptr {
	e, ok := enumOf(this)
	if !ok {
		return eUnexpected
	}
	n := uint32(celt)
	if rgelt == nil && n != 0 {
		return ePointer
	}
	// "This parameter can be NULL if celt is 1."
	if n > 1 && pceltFetched == nil {
		return eInvalidArg
	}
	e.mu.Lock()
	avail := len(e.formats) - e.idx
	got := int(n)
	if got > avail {
		got = avail
	}
	if got > 0 {
		// got, not n: the slice is only ever as long as the elements actually
		// being written, so nothing here can name memory past what was fetched.
		copy(unsafe.Slice(rgelt, got), e.formats[e.idx:e.idx+got])
		e.idx += got
	}
	at := e.idx
	e.mu.Unlock()
	if pceltFetched != nil {
		*pceltFetched = uint32(got)
	}
	hr := uintptr(sOK)
	if uint32(got) != n {
		hr = sFALSE
	}
	logf("IEnumFORMATETC::Next(celt=%d) -> %d formats (now at %d/%d), %s",
		n, got, at, len(e.formats), hrName(hr))
	return hr
}

func enumSkip(this uintptr, celt uintptr) uintptr {
	e, ok := enumOf(this)
	if !ok {
		return eUnexpected
	}
	n := int(uint32(celt))
	e.mu.Lock()
	avail := len(e.formats) - e.idx
	skipped := n
	if skipped > avail {
		skipped = avail
	}
	e.idx += skipped
	at := e.idx
	e.mu.Unlock()
	hr := uintptr(sOK)
	if skipped != n {
		hr = sFALSE
	}
	logf("IEnumFORMATETC::Skip(%d) -> skipped %d (now at %d), %s", n, skipped, at, hrName(hr))
	return hr
}

func enumReset(this uintptr) uintptr {
	e, ok := enumOf(this)
	if !ok {
		return eUnexpected
	}
	e.mu.Lock()
	e.idx = 0
	e.mu.Unlock()
	logf("IEnumFORMATETC::Reset")
	return sOK
}

func enumClone(this uintptr, ppenum *uintptr) uintptr {
	e, ok := enumOf(this)
	if !ok {
		return eUnexpected
	}
	if ppenum == nil {
		return ePointer
	}
	e.mu.Lock()
	idx := e.idx
	formats := e.formats
	e.mu.Unlock()
	o := newEnumFormatEtcObject(formats, idx)
	*ppenum = o.unknown()
	logf("IEnumFORMATETC::Clone -> 0x%X at index %d", *ppenum, idx)
	return sOK
}

// Built inside the Once for the same reason as the stream's: Clone makes
// another enumerator, which needs the vtable.
var (
	enumVtblOnce sync.Once
	enumVtblAddr uintptr
)

func vtblIEnumFORMATETC() uintptr {
	enumVtblOnce.Do(func() {
		enumVtblAddr = pinVtbl(iEnumFORMATETCVtbl{
			QueryInterface: cbQueryInterface,
			AddRef:         cbAddRef,
			Release:        cbRelease,
			Next:           syscall.NewCallback(enumNext),
			Skip:           syscall.NewCallback(enumSkip),
			Reset:          syscall.NewCallback(enumReset),
			Clone:          syscall.NewCallback(enumClone),
		})
	})
	return enumVtblAddr
}

// ---------------------------------------------------------------------------
// IDropSource.

type dropSource struct {
	// queries and feedbacks are atomics for the same reason the data object's
	// getCalls is: comDestroy reads them from whichever thread made the last
	// Release, which need never have held the mutex. The mutex is still what
	// guards lastEffect and haveLast, which are a pair and have to be read and
	// written together.
	queries   atomic.Int64
	feedbacks atomic.Int64

	// stage is -hdrop's staging folder, or nil. QueryContinueDrag is where the
	// button coming up is observed, and that release is what arms the
	// extraction: it is the only moment in the whole protocol at which a source
	// learns that the drop is about to happen, before the target asks for
	// anything. Fixed at creation and read from the drag thread only -- OLE
	// calls IDropSource nowhere else, which is why it is not agile.
	stage *dragStage

	mu         sync.Mutex
	lastEffect uint32
	haveLast   bool
}

func (s *dropSource) comDestroy() {
	logf("IDropSource destroyed after %d QueryContinueDrag and %d GiveFeedback calls",
		s.queries.Load(), s.feedbacks.Load())
}

func newDropSourceObject(stage *dragStage) *comObject {
	return newCOMObject("IDropSource", &dropSource{stage: stage}, comIface{
		name: "IDropSource",
		vtbl: vtblIDropSource,
		iids: []windows.GUID{iidIDropSource},
	})
}

func dropSourceOf(this uintptr) (*dropSource, bool) {
	o, _, ok := comLookup(this)
	if !ok {
		return nil, false
	}
	s, ok := o.impl.(*dropSource)
	return s, ok
}

// dropSourceQueryContinueDrag is the documented rule, and nothing more: Esc
// cancels, letting go of the button that started the drag drops, anything else
// carries on.
func dropSourceQueryContinueDrag(this uintptr, fEscapePressed uintptr, grfKeyState uintptr) uintptr {
	s, ok := dropSourceOf(this)
	if !ok {
		return dragDropSCancel
	}
	esc := uint32(fEscapePressed) != 0
	keys := uint32(grfKeyState)
	n := s.queries.Add(1)

	switch {
	case esc:
		logf("IDropSource::QueryContinueDrag #%d: Esc pressed -> DRAGDROP_S_CANCEL", n)
		return dragDropSCancel
	case keys&mkLButton == 0:
		logf("IDropSource::QueryContinueDrag #%d: left button released -> DRAGDROP_S_DROP", n)
		if s.stage != nil {
			// "The drop operation should occur completing the drag operation.
			// This result occurs if grfKeyState indicates that the key that
			// started the drag-and-drop operation has been released." So this is
			// the moment, and the extraction is armed before DRAGDROP_S_DROP is
			// returned -- the target's GetData can follow immediately.
			s.stage.arm()
		}
		return dragDropSDrop
	default:
		return sOK
	}
}

// dropSourceGiveFeedback asks OLE for the standard cursors. It is called on
// every mouse move, so it logs only when the effect changes -- the transitions
// are the interesting part, and a line per move would bury everything else.
func dropSourceGiveFeedback(this uintptr, dwEffect uintptr) uintptr {
	s, ok := dropSourceOf(this)
	if !ok {
		return dragDropSUseDefaultCursors
	}
	e := uint32(dwEffect)
	n := s.feedbacks.Add(1)
	s.mu.Lock()
	changed := !s.haveLast || s.lastEffect != e
	s.lastEffect = e
	s.haveLast = true
	s.mu.Unlock()
	if changed {
		logf("IDropSource::GiveFeedback #%d: effect now %s -> DRAGDROP_S_USEDEFAULTCURSORS", n, effectName(e))
	}
	return dragDropSUseDefaultCursors
}

var vtblIDropSource = pinVtbl(iDropSourceVtbl{
	QueryInterface:    cbQueryInterface,
	AddRef:            cbAddRef,
	Release:           cbRelease,
	QueryContinueDrag: syscall.NewCallback(dropSourceQueryContinueDrag),
	GiveFeedback:      syscall.NewCallback(dropSourceGiveFeedback),
})
