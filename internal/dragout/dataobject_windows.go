//go:build windows

package dragout

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The data object is the drag: everything the target learns about the
// files comes through here. It exposes IDataObject and
// IDataObjectAsyncCapability on one object (two cells, one reference
// count), offers CF_HDROP by delayed rendering over the staging folder and
// CFSTR_PREFERREDDROPEFFECT beside it, and enumerates its formats through a
// separate IEnumFORMATETC object as the interface requires.

// The Shell clipboard formats are not predefined — "Each format you want to
// use must be registered by calling RegisterClipboardFormat" — so they are
// registered once here. The feedback formats are registered so that SetData
// can accept them and the log can name what the target reported.
var (
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
		cfPerformedDropEffect = registerClipboardFormat("Performed DropEffect")
		cfLogicalPerformedDrop = registerClipboardFormat("Logical Performed DropEffect")
		cfPasteSucceeded = registerClipboardFormat("Paste Succeeded")
		cfPreferredDropEffect = registerClipboardFormat("Preferred DropEffect")
		cfTargetCLSID = registerClipboardFormat("TargetCLSID")
		cfInShellDragLoop = registerClipboardFormat("InShellDragLoop")
	})
}

// formatName names a CLIPFORMAT for the log: the ones registered here by
// their constant name, anything else by whatever the shell has it registered
// as, or its number.
func formatName(cf uint16) string {
	switch cf {
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

// ---------------------------------------------------------------------------

type dataObject struct {
	// stage is the staging folder and its state machine. Set before the
	// object is handed over and never changed, which is what makes reading
	// it from the target's threads safe.
	stage   *stage
	formats []formatEtc
	log     func(string, ...any)

	// calls and queries number this drag's GetData and QueryGetData calls,
	// every format included, for the per-drag trace APP.md §3 asks for.
	calls   atomic.Int32
	queries atomic.Int32

	mu        sync.Mutex
	self      *comObject // the object these interfaces belong to; set by setCOMObject
	asyncMode bool
	inOp      bool
	selfRef   bool // an AddRef taken by SetAsyncMode, owed back

	// revoked is the forced teardown: the process is going while the target
	// still holds this object. From then on GetData answers E_UNEXPECTED
	// rather than name files out of a drag that is over.
	revoked atomic.Bool
}

// setCOMObject runs inside newCOMObject, before the interface cells are
// published, so self is never nil for a call that arrives through one of
// them.
func (d *dataObject) setCOMObject(o *comObject) {
	d.mu.Lock()
	d.self = o
	d.mu.Unlock()
}

// comDestroy is the target letting go — our own reference went at the end
// of Run — which the stage needs to know: nothing can ask any more.
func (d *dataObject) comDestroy() {
	if d.stage != nil {
		d.stage.noteReleased()
	}
}

// comOnlySelfLeft is every outside reference being gone while the
// SetAsyncMode reference is still held: the target let the object go
// without ever calling EndOperation — which for a CF_HDROP source Explorer
// never does (measured 2026-09-11) — or, before any drop, our own reference
// went at the end of Run with nothing else holding on. Either way nobody
// can call EndOperation any more, so the reference it would have given back
// is given back here, and the object's destruction tells the stage the
// target has gone. A concurrent EndOperation finds the ledger already
// cleared and gives nothing back twice.
func (d *dataObject) comOnlySelfLeft() {
	d.mu.Lock()
	self := d.self
	give := d.selfRef
	d.selfRef = false
	inOp := d.inOp
	d.mu.Unlock()
	if !give || self == nil {
		return
	}
	if d.stage != nil {
		if inOp {
			d.log("drag %s: the target let the data object go without EndOperation; the asynchronous reference is given back", d.stage.id)
		} else {
			d.log("drag %s: the last outside reference to the data object went; the asynchronous reference is given back", d.stage.id)
		}
	}
	// The last reference, so nothing may touch d afterwards.
	self.releaseSelfRef()
}

// newHDropDataObject is the drag's data object: CF_HDROP by delayed
// rendering over the staging folder, CFSTR_PREFERREDDROPEFFECT beside it,
// and nothing else.
//
// Both are advertised with lindex -1 — neither names one file of several;
// the HDROP is the whole list — and both as TYMED_HGLOBAL, which is what
// the scenarios page says for CF_HDROP ("set ... the tymed member to
// TYMED_HGLOBAL") and what "The structure's hGlobal member points to a DWORD
// value" says for the drop effect.
//
// IDataObjectAsyncCapability stays on the object: Explorer negotiates the
// protocol for a CF_HDROP source (measured 2026-09-11), even though it never
// calls EndOperation, and the self-reference the negotiation takes is what
// keeps the object alive for a target that asks after DoDragDrop returned.
func newHDropDataObject(s *stage, log func(string, ...any)) *comObject {
	registerFormats()
	if log == nil {
		log = discard
	}
	d := &dataObject{stage: s, log: log}
	d.formats = []formatEtc{
		{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal},
		{cfFormat: cfPreferredDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal},
	}
	// newCOMObject calls d.setCOMObject before it publishes the cells, so
	// the object knows itself from the first instant COM can reach it.
	return newCOMObject("IDataObject", d, true, log,
		comIface{name: "IDataObject", vtbl: vtblIDataObject, iids: []windows.GUID{iidIDataObject}},
		comIface{name: "IDataObjectAsyncCapability", vtbl: vtblIDataObjectAsyncCapability, iids: []windows.GUID{iidIDataObjectAsyncCapability}},
	)
}

// offers reports whether this object ever advertised a format at all,
// whatever the aspect, index or medium asked for. It is what separates "you
// asked for the wrong medium of something I have" from "I never had that".
func (d *dataObject) offers(cf uint16) bool {
	for i := range d.formats {
		if d.formats[i].cfFormat == cf {
			return true
		}
	}
	return false
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
// ptd is ignored on purpose, and that is the same answer
// GetCanonicalFormatEtc gives: nothing this object renders depends on a
// device, so a request naming a target device matches the
// device-independent rendering rather than being refused.
func (d *dataObject) match(fe *formatEtc) bool {
	for i := range d.formats {
		f := &d.formats[i]
		if fe.cfFormat == f.cfFormat &&
			fe.dwAspect == f.dwAspect &&
			fe.lindex == f.lindex &&
			fe.tymed&f.tymed != 0 {
			return true
		}
	}
	return false
}

// renderHGlobal fills a TYMED_HGLOBAL medium with blob, which is how both
// formats here are delivered. GHND is the conventional allocation for a
// handle a data-transfer consumer will free, and pUnkForRelease stays NULL
// because, as STGMEDIUM has it, "If pUnkForRelease is NULL, the receiver of
// the medium is responsible for releasing it".
func renderHGlobal(blob []byte, pmedium *stgMedium) uintptr {
	h := globalAlloc(gHND, uintptr(len(blob)))
	if h == 0 {
		return stgEMediumFull
	}
	p := globalLock(h)
	if p == 0 {
		globalFree(h)
		return stgEMediumFull
	}
	copyIntoNative(p, blob)
	globalUnlock(h)
	pmedium.tymed = tymedHGlobal
	pmedium.data = h
	pmedium.pUnkForRelease = 0
	return sOK
}

// dataGetData is the trace's centre (APP.md §3, "the formats asked for and
// when"): every call is logged as it arrives with the format asked for and
// where it sits against the button's release, and again as it answers, with
// the HRESULT and the time it took — for the drop's own CF_HDROP request
// that time is the extraction's, and the target waited it out inside this
// call.
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
	n := d.calls.Add(1)
	d.log("drag %s: GetData #%d %s, %s", d.dragID(), n, feString(&fe), d.when())
	start := time.Now()
	hr := d.getData(&fe, pmedium)
	d.log("drag %s: GetData #%d -> %s after %s", d.dragID(), n, hrName(hr), time.Since(start).Round(time.Millisecond))
	return hr
}

// dragID names the drag in a log line, or nothing when there is no stage.
func (d *dataObject) dragID() string {
	if d.stage == nil {
		return "-"
	}
	return d.stage.id
}

// when is where a call sits against the button's release, which is the
// axis every question about delayed rendering is asked on.
func (d *dataObject) when() string {
	if d.stage == nil {
		return "no stage"
	}
	return d.stage.timing()
}

func (d *dataObject) getData(pfe *formatEtc, pmedium *stgMedium) uintptr {
	fe := *pfe
	if !d.match(&fe) {
		// Say precisely which part of the FORMATETC was refused: a caller
		// told DV_E_FORMATETC when the real problem was the medium goes off
		// and tries a different format instead of a different medium.
		if !d.offers(fe.cfFormat) {
			return dvEFormatEtc
		}
		if fe.dwAspect != dvAspectContent {
			return dvEDvAspect
		}
		if fe.tymed&tymedHGlobal == 0 {
			return dvETymed
		}
		return dvELIndex
	}
	if d.revoked.Load() {
		// A forced teardown: the drag this object served is over, and the
		// documented way for GetData to say the rendering failed is
		// E_UNEXPECTED — the FORMATETC was fine, and none of the DV_E_ codes
		// is true. The same answer a failed extraction gives.
		d.log("drag: GetData(%s) after the teardown -> E_UNEXPECTED", formatName(fe.cfFormat))
		return eUnexpected
	}

	switch fe.cfFormat {
	case cfHDrop:
		// The state machine decides what this request means — hover, drop,
		// or a repeat — and runs the extraction when it is the drop's own.
		// It can fail, and then so does GetData: handing out the names of
		// files that are not there is the defect 7-Zip's own comments
		// describe.
		paths, ok := d.stage.requestPaths()
		if !ok {
			return eUnexpected
		}
		return renderHGlobal(encodeDropFiles(paths), pmedium)

	case cfPreferredDropEffect:
		return renderHGlobal(encodeDropEffect(preferredEffect), pmedium)
	}
	return dvEFormatEtc
}

// dataGetDataHere would have to render into a medium the caller allocated.
// This object has no such rendering. GetDataHere's documented return values
// do not include E_NOTIMPL — the codes it gives for "I cannot do that" are
// DV_E_FORMATETC and DV_E_TYMED — so it declines with the one that fits.
func dataGetDataHere(this uintptr, pformatetc *formatEtc, pmedium *stgMedium) uintptr {
	// The answer does not depend on the object, but the lookup happens
	// anyway: it is what names a call that arrived on an interface pointer
	// the caller had already released. The same goes for the advisory
	// methods below.
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	return dvEFormatEtc
}

// dataQueryGetData is logged like GetData: a probe for a format this object
// does not offer is as much a part of the drag's trace as a request for one
// it does — which formats a target asks about is what says what it wanted.
func dataQueryGetData(this uintptr, pformatetc *formatEtc) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	if pformatetc == nil {
		return ePointer
	}
	fe := *pformatetc
	n := d.queries.Add(1)
	hr := uintptr(sOK)
	if !d.match(&fe) {
		// DV_E_FORMATETC rather than S_FALSE: S_FALSE is a success code, so
		// a caller testing SUCCEEDED(hr) would read it as "yes, I have
		// that".
		hr = dvEFormatEtc
	}
	d.log("drag %s: QueryGetData #%d %s, %s -> %s", d.dragID(), n, feString(&fe), d.when(), hrName(hr))
	return hr
}

// dataGetCanonicalFormatEtc: this object never renders per device, so the
// documented simplest implementation applies — "copy the input FORMATETC to
// the output FORMATETC, store a NULL in the ptd member of the output
// FORMATETC, and return DATA_S_SAMEFORMATETC".
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
	return dataSSameFormatEtc
}

// feedbackFormats are the formats a drop target uses to report back to the
// source. Nothing here acts on any of them — a move here is the target's
// own rename of the staged copy, and the record is never touched — but
// which arrive, and with what values, is worth the log: CFSTR_PERFORMEDDROPEFFECT
// is how a target says copy or move where DoDragDrop's own effect does not.
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
		// "A data object implements this method if it supports receiving
		// data from another object. If it does not support this, it should
		// be implemented to return E_NOTIMPL." Declining also leaves the
		// medium with its caller: ownership only moves on success.
		return eNotImpl
	}
	// Only now, and only this far. Every feedback format above is documented
	// as a DWORD in an HGLOBAL, so reading the first four bytes of one is
	// reading what the sender sent; reading four bytes of an arbitrary
	// medium the caller allocated is an overread of somebody else's memory,
	// and the format is the only thing that says which of the two this is.
	// GlobalSize is the second half of that: it returns zero for a handle
	// that is not one, so a block it says is shorter than a DWORD is left
	// alone.
	value := ""
	if pmedium.tymed == tymedHGlobal && pmedium.data != 0 && globalSize(pmedium.data) >= 4 {
		if p := globalLock(pmedium.data); p != 0 {
			var b [4]byte
			copyFromNative(b[:], p)
			globalUnlock(pmedium.data)
			v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
			value = " = " + effectName(v)
		}
	}
	if d.stage != nil {
		d.log("drag %s: the target set %s%s", d.stage.id, formatName(pformatetc.cfFormat), value)
	}
	if fRelease != 0 {
		// "If TRUE, the data object called, which implements SetData, owns
		// the storage medium after the call returns. This means it must
		// free the medium after it has been used by calling the
		// ReleaseStgMedium function."
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
		// "E_NOTIMPL: The direction specified by dwDirection is not
		// supported." Nothing can be set on this object, so there is nothing
		// to enumerate.
		return eNotImpl
	default:
		// "E_INVALIDARG: The supplied dwDirection is invalid."
		return eInvalidArg
	}
	o := newEnumFormatEtcObject(d.formats, 0, d.log)
	*ppenumFormatEtc = o.unknown()
	return sOK
}

// Advisory connections are for data that changes underneath the consumer.
// The files this object offers do not change while the drag is up, so it
// supports none of it, which OLE_E_ADVISENOTSUPPORTED is the code for.
func dataDAdvise(this uintptr, pformatetc *formatEtc, advf uintptr, pAdvSink uintptr, pdwConnection *uint32) uintptr {
	if pdwConnection != nil {
		*pdwConnection = 0
	}
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	return oleEAdviseNotSupp
}

func dataDUnadvise(this uintptr, dwConnection uintptr) uintptr {
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
	return oleEAdviseNotSupp
}

func dataEnumDAdvise(this uintptr, ppenumAdvise *uintptr) uintptr {
	if ppenumAdvise != nil {
		*ppenumAdvise = 0
	}
	if _, ok := dataObjectOf(this); !ok {
		return eUnexpected
	}
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
// The documented handshake: the source calls SetAsyncMode(VARIANT_TRUE)
// before the drag; the target, in its Drop, calls GetAsyncMode, and if it
// agrees calls StartOperation, returns from Drop, extracts on a thread of
// its own, and calls EndOperation when it is done. The source calls
// InOperation after DoDragDrop returns to find out which of the two
// happened.
//
// The reference count is the whole point on this side: "If fDoOpAsync is
// set to VARIANT_TRUE, SetAsyncMode must call AddRef, and store the
// interface pointer for use by EndOperation" — that reference is what keeps
// the object alive after the drag loop has gone, and EndOperation is what
// gives it back.

func dataSetAsyncMode(this uintptr, fDoOpAsync uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	on := uint32(fDoOpAsync) != 0
	d.mu.Lock()
	d.asyncMode = on
	// selfRef is the ledger EndOperation, finishSync and comOnlySelfLeft
	// release against, so it must not be possible for it to say a reference
	// was taken when none was. Deciding and taking under the one lock is
	// what makes them agree.
	if on && !d.selfRef && d.self != nil {
		d.selfRef = true
		d.self.addSelfRef()
	}
	d.mu.Unlock()
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
	return sOK
}

func dataStartOperation(this uintptr, pbcReserved uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	d.mu.Lock()
	d.inOp = true
	d.mu.Unlock()
	if d.stage != nil {
		d.stage.noteAsyncStarted()
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
	return sOK
}

func dataEndOperation(this uintptr, hResult uintptr, pbcReserved uintptr, dwEffects uintptr) uintptr {
	d, ok := dataObjectOf(this)
	if !ok {
		return eUnexpected
	}
	d.mu.Lock()
	d.inOp = false
	self := d.self
	give := d.selfRef
	d.selfRef = false
	d.mu.Unlock()
	if d.stage != nil {
		d.log("drag %s: EndOperation(%s, %s)", d.stage.id, hrName(hResult), effectName(uint32(dwEffects)))
		// Recorded here, acted on by the stage's own goroutine: deleting the
		// staged files inside the call the target is waiting to return from
		// would make this program the reason its copy looked slow.
		d.stage.noteEndOperation()
	}
	if give && self != nil {
		// Give back the reference SetAsyncMode took. This may well be the
		// last one, so nothing may touch d afterwards.
		self.releaseSelfRef()
	}
	return sOK
}

// inOperation reports whether the target has called StartOperation and not
// yet EndOperation.
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
		self.releaseSelfRef()
	}
}

// The struct is kept as well as the pinned copy, because the source side of
// the handshake — SetAsyncMode before the drag, InOperation after it — is
// made as a real indirect call through these same function pointers. Going
// the long way round is deliberate: it exercises the vtable in the direction
// COM uses it, so a slot in the wrong place shows up here rather than only
// in Explorer.
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
	log     func(string, ...any)
	mu      sync.Mutex
	idx     int
}

func (e *enumFormatEtc) comDestroy() {}

func newEnumFormatEtcObject(formats []formatEtc, idx int, log func(string, ...any)) *comObject {
	// Copy: the enumerator must not be able to see a later edit of the
	// data object's list.
	cp := make([]formatEtc, len(formats))
	copy(cp, formats)
	e := &enumFormatEtc{formats: cp, idx: idx, log: log}
	return newCOMObject("IEnumFORMATETC", e, true, log, comIface{
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
		// got, not n: the slice is only ever as long as the elements
		// actually being written, so nothing here can name memory past what
		// was fetched.
		copy(unsafe.Slice(rgelt, got), e.formats[e.idx:e.idx+got])
		e.idx += got
	}
	e.mu.Unlock()
	if pceltFetched != nil {
		*pceltFetched = uint32(got)
	}
	if uint32(got) != n {
		return sFALSE
	}
	return sOK
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
	e.mu.Unlock()
	if skipped != n {
		return sFALSE
	}
	return sOK
}

func enumReset(this uintptr) uintptr {
	e, ok := enumOf(this)
	if !ok {
		return eUnexpected
	}
	e.mu.Lock()
	e.idx = 0
	e.mu.Unlock()
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
	o := newEnumFormatEtcObject(formats, idx, e.log)
	*ppenum = o.unknown()
	return sOK
}

// Built inside a Once: Clone makes another enumerator, which needs the
// vtable, and a package-level initialiser could not name itself.
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
	// stage is the drag's staging folder. QueryContinueDrag is where the
	// button coming up is observed, and that release is what arms the
	// extraction: it is the only moment in the whole protocol at which a
	// source learns that the drop is about to happen, before the target
	// asks for anything. It is also where the self-drop is told apart: the
	// window under the cursor at that moment. Fixed at creation and read
	// from the drag thread only — OLE calls IDropSource nowhere else, which
	// is why it is not agile.
	stage  *stage
	window uintptr
}

func (s *dropSource) comDestroy() {}

func newDropSourceObject(s *stage, window uintptr, log func(string, ...any)) *comObject {
	return newCOMObject("IDropSource", &dropSource{stage: s, window: window}, false, log, comIface{
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

// dropSourceQueryContinueDrag is the documented rule, and two things beside
// it: Esc cancels, letting go of the button that started the drag drops,
// anything else carries on — unless Cancel was called, which ends the drag
// as Esc would, or the button came up over the caller's own window, which
// is the self-drop (APP.md §3): the drop still happens, so that the WebView
// receives it and the page can turn it into the Move it always was, but the
// stage is told to extract nothing.
func dropSourceQueryContinueDrag(this uintptr, fEscapePressed uintptr, grfKeyState uintptr) uintptr {
	s, ok := dropSourceOf(this)
	if !ok {
		return dragDropSCancel
	}
	switch {
	case uint32(fEscapePressed) != 0:
		return dragDropSCancel
	case s.stage.ctx.Err() != nil:
		return dragDropSCancel
	case uint32(grfKeyState)&mkLButton == 0:
		// "The drop operation should occur completing the drag operation.
		// This result occurs if grfKeyState indicates that the key that
		// started the drag-and-drop operation has been released." So this
		// is the moment, and the extraction is armed before DRAGDROP_S_DROP
		// is returned — the target's GetData can follow immediately.
		self := s.window != 0 && cursorRootWindow() == s.window
		s.stage.arm(self)
		return dragDropSDrop
	}
	return sOK
}

// dropSourceGiveFeedback asks OLE for the standard cursors.
func dropSourceGiveFeedback(this uintptr, dwEffect uintptr) uintptr {
	if _, ok := dropSourceOf(this); !ok {
		return dragDropSUseDefaultCursors
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
