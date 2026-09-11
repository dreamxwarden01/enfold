//go:build windows

package main

import (
	"reflect"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A vtable is an array of function pointers indexed by position: a method in
// the wrong slot is not a compile error, not a crash at the call site, but a
// jump into an unrelated function with the wrong arguments. The orders below
// were read out of the Windows SDK 10.0.26100.0 headers -- objidl.h, oleidl.h,
// ShlDisp.h -- by listing each *Vtbl's STDMETHODCALLTYPE entries in declaration
// order, and this test is what holds the Go structs to them.

var sdkVtableOrder = map[string][]string{
	// objidl.h, IUnknownVtbl
	"iUnknownVtbl": {"QueryInterface", "AddRef", "Release"},
	// objidl.h, IDataObjectVtbl
	"iDataObjectVtbl": {
		"QueryInterface", "AddRef", "Release",
		"GetData", "GetDataHere", "QueryGetData", "GetCanonicalFormatEtc",
		"SetData", "EnumFormatEtc", "DAdvise", "DUnadvise", "EnumDAdvise",
	},
	// ShlDisp.h, IDataObjectAsyncCapabilityVtbl
	"iDataObjectAsyncCapabilityVtbl": {
		"QueryInterface", "AddRef", "Release",
		"SetAsyncMode", "GetAsyncMode", "StartOperation", "InOperation", "EndOperation",
	},
	// objidl.h, IEnumFORMATETCVtbl
	"iEnumFORMATETCVtbl": {
		"QueryInterface", "AddRef", "Release",
		"Next", "Skip", "Reset", "Clone",
	},
	// oleidl.h, IDropSourceVtbl
	"iDropSourceVtbl": {
		"QueryInterface", "AddRef", "Release",
		"QueryContinueDrag", "GiveFeedback",
	},
	// objidl.h, IStreamVtbl. Read and Write come from ISequentialStream, which
	// IStream derives from, so they precede IStream's own methods.
	"iStreamVtbl": {
		"QueryInterface", "AddRef", "Release",
		"Read", "Write",
		"Seek", "SetSize", "CopyTo", "Commit", "Revert",
		"LockRegion", "UnlockRegion", "Stat", "Clone",
	},
}

func TestVtableMethodOrder(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(iUnknownVtbl{}),
		reflect.TypeOf(iDataObjectVtbl{}),
		reflect.TypeOf(iDataObjectAsyncCapabilityVtbl{}),
		reflect.TypeOf(iEnumFORMATETCVtbl{}),
		reflect.TypeOf(iDropSourceVtbl{}),
		reflect.TypeOf(iStreamVtbl{}),
	}
	seen := map[string]bool{}
	for _, rt := range types {
		name := rt.Name()
		seen[name] = true
		want, ok := sdkVtableOrder[name]
		if !ok {
			t.Errorf("no SDK order recorded for %s", name)
			continue
		}
		if rt.NumField() != len(want) {
			t.Errorf("%s has %d slots, the SDK declares %d", name, rt.NumField(), len(want))
			continue
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Name != want[i] {
				t.Errorf("%s slot %d is %s, the SDK declares %s", name, i, f.Name, want[i])
			}
			if f.Type.Kind() != reflect.Uintptr {
				t.Errorf("%s.%s is %s, must be uintptr: COM dereferences this as a function pointer",
					name, f.Name, f.Type)
			}
			// No padding is allowed between slots: the vtable is a dense array.
			if f.Offset != uintptr(i)*unsafe.Sizeof(uintptr(0)) {
				t.Errorf("%s.%s is at offset %d, want %d", name, f.Name, f.Offset,
					uintptr(i)*unsafe.Sizeof(uintptr(0)))
			}
		}
		if rt.Size() != uintptr(len(want))*unsafe.Sizeof(uintptr(0)) {
			t.Errorf("%s is %d bytes, want %d", name, rt.Size(),
				uintptr(len(want))*unsafe.Sizeof(uintptr(0)))
		}
	}
	for name := range sdkVtableOrder {
		if !seen[name] {
			t.Errorf("SDK order recorded for %s but no Go struct was checked", name)
		}
	}
}

// TestVtablesAreFullyPopulated makes sure every slot of every vtable the
// prototype actually installs holds a real callback. A zero slot would be a
// null function pointer COM would jump through.
func TestVtablesAreFullyPopulated(t *testing.T) {
	for _, addr := range []struct {
		name string
		v    uintptr
	}{
		{"IDataObject", vtblIDataObject},
		{"IDataObjectAsyncCapability", vtblIDataObjectAsyncCapability},
		{"IDropSource", vtblIDropSource},
		{"IEnumFORMATETC", vtblIEnumFORMATETC()},
		{"IStream", vtblIStream()},
	} {
		if addr.v == 0 {
			t.Errorf("%s vtable was never built", addr.name)
		}
	}
	// pinVtbl panics on a zero slot, so reaching here with non-zero addresses
	// means every slot of every installed vtable is populated. Prove the guard
	// works rather than trusting it.
	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("pinVtbl accepted a vtable with a nil slot")
			}
		}()
		pinVtbl(iUnknownVtbl{QueryInterface: cbQueryInterface, AddRef: 0, Release: cbRelease})
	}()
}

// TestWin32StructLayouts holds the Go mirrors to the sizes and offsets printed
// from the SDK headers for x64. Everything below came from compiling
// sizeof/offsetof against windows.h, objidl.h, shlobj.h and psapi.h.
func TestWin32StructLayouts(t *testing.T) {
	check := func(name string, got, want uintptr) {
		if got != want {
			t.Errorf("%s = %d, SDK says %d", name, got, want)
		}
	}

	var fe formatEtc
	check("sizeof(FORMATETC)", unsafe.Sizeof(fe), 32)
	check("FORMATETC.cfFormat", unsafe.Offsetof(fe.cfFormat), 0)
	check("FORMATETC.ptd", unsafe.Offsetof(fe.ptd), 8)
	check("FORMATETC.dwAspect", unsafe.Offsetof(fe.dwAspect), 16)
	check("FORMATETC.lindex", unsafe.Offsetof(fe.lindex), 20)
	check("FORMATETC.tymed", unsafe.Offsetof(fe.tymed), 24)

	var sm stgMedium
	check("sizeof(STGMEDIUM)", unsafe.Sizeof(sm), 24)
	check("STGMEDIUM.tymed", unsafe.Offsetof(sm.tymed), 0)
	check("STGMEDIUM.hGlobal/pstm", unsafe.Offsetof(sm.data), 8)
	check("STGMEDIUM.pUnkForRelease", unsafe.Offsetof(sm.pUnkForRelease), 16)

	var st statStg
	check("sizeof(STATSTG)", unsafe.Sizeof(st), 80)
	check("STATSTG.pwcsName", unsafe.Offsetof(st.pwcsName), 0)
	check("STATSTG.type", unsafe.Offsetof(st.typ), 8)
	check("STATSTG.cbSize", unsafe.Offsetof(st.cbSize), 16)
	check("STATSTG.mtime", unsafe.Offsetof(st.mtime), 24)
	check("STATSTG.ctime", unsafe.Offsetof(st.ctime), 32)
	check("STATSTG.atime", unsafe.Offsetof(st.atime), 40)
	check("STATSTG.grfMode", unsafe.Offsetof(st.grfMode), 48)
	check("STATSTG.grfLocksSupported", unsafe.Offsetof(st.grfLocksSupported), 52)
	check("STATSTG.clsid", unsafe.Offsetof(st.clsid), 56)
	check("STATSTG.grfStateBits", unsafe.Offsetof(st.grfStateBits), 72)
	check("STATSTG.reserved", unsafe.Offsetof(st.reserved), 76)

	var pmc processMemoryCounters
	check("sizeof(PROCESS_MEMORY_COUNTERS)", unsafe.Sizeof(pmc), 72)
	check("PROCESS_MEMORY_COUNTERS.cb", unsafe.Offsetof(pmc.cb), 0)
	check("PROCESS_MEMORY_COUNTERS.PageFaultCount", unsafe.Offsetof(pmc.pageFaultCount), 4)
	check("PROCESS_MEMORY_COUNTERS.PeakWorkingSetSize", unsafe.Offsetof(pmc.peakWorkingSetSize), 8)
	check("PROCESS_MEMORY_COUNTERS.WorkingSetSize", unsafe.Offsetof(pmc.workingSetSize), 16)
	check("PROCESS_MEMORY_COUNTERS.PagefileUsage", unsafe.Offsetof(pmc.pagefileUsage), 56)
	check("PROCESS_MEMORY_COUNTERS.PeakPagefileUsage", unsafe.Offsetof(pmc.peakPagefileUsage), 64)

	var wc wndClassExW
	check("sizeof(WNDCLASSEXW)", unsafe.Sizeof(wc), 80)
	var m msgW
	check("sizeof(MSG)", unsafe.Sizeof(m), 48)
	check("MSG.wParam", unsafe.Offsetof(m.wParam), 16)
	check("MSG.lParam", unsafe.Offsetof(m.lParam), 24)
	check("MSG.pt", unsafe.Offsetof(m.pt), 36)
	var ps paintStruct
	check("sizeof(PAINTSTRUCT)", unsafe.Sizeof(ps), 72)
	check("sizeof(GUID)", unsafe.Sizeof(windows.GUID{}), 16)
	check("sizeof(FILETIME)", unsafe.Sizeof(windows.Filetime{}), 8)
}

// TestIIDBytes checks the GUIDs byte for byte against what the SDK's uuid
// library holds. A GUID written down in the wrong byte order still looks like a
// GUID; QueryInterface just never matches, and the failure surfaces as Explorer
// quietly declining the drag.
func TestIIDBytes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		iid   windows.GUID
		bytes string
	}{
		{"IID_IUnknown", iidIUnknown, "0000000000000000C000000000000046"},
		{"IID_IDataObject", iidIDataObject, "0E01000000000000C000000000000046"},
		{"IID_IEnumFORMATETC", iidIEnumFORMATETC, "0301000000000000C000000000000046"},
		{"IID_IDropSource", iidIDropSource, "2101000000000000C000000000000046"},
		{"IID_IStream", iidIStream, "0C00000000000000C000000000000046"},
		{"IID_ISequentialStream", iidISequentialStream, "303A730C1C2ACE11ADE500AA0044773D"},
		{"IID_IMarshal", iidIMarshal, "0300000000000000C000000000000046"},
		{"IID_IAgileObject", iidIAgileObject, "942BEA94CCE9E049C0FFEE64CA8F5B90"},
		{"IID_IDataObjectAsyncCapability", iidIDataObjectAsyncCapability, "90058B3D91F6D2118EA9006097DF5BD4"},
	} {
		if got := guidBytes(tc.iid); got != tc.bytes {
			t.Errorf("%s = %s, SDK says %s", tc.name, got, tc.bytes)
		}
	}
	// All distinct: two interfaces sharing an IID would make QueryInterface
	// hand out the wrong vtable.
	all := []windows.GUID{
		iidIUnknown, iidIDataObject, iidIEnumFORMATETC, iidIDropSource,
		iidIStream, iidISequentialStream, iidIMarshal, iidIAgileObject,
		iidIDataObjectAsyncCapability,
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Errorf("IIDs %d and %d are the same", i, j)
			}
		}
	}
}

// guidBytes renders a GUID in memory order, which is how it arrives from COM:
// Data1 and Data2/Data3 little-endian, Data4 as written.
func guidBytes(g windows.GUID) string {
	const hex = "0123456789ABCDEF"
	b := make([]byte, 0, 32)
	put := func(v byte) { b = append(b, hex[v>>4], hex[v&0xF]) }
	put(byte(g.Data1))
	put(byte(g.Data1 >> 8))
	put(byte(g.Data1 >> 16))
	put(byte(g.Data1 >> 24))
	put(byte(g.Data2))
	put(byte(g.Data2 >> 8))
	put(byte(g.Data3))
	put(byte(g.Data3 >> 8))
	for _, v := range g.Data4 {
		put(v)
	}
	return string(b)
}
