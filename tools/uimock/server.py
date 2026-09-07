"""A stand-in for the Wails backend so the built page can be exercised in a
browser: serves frontend/dist and answers POST /wails/runtime with canned
data. POST /mock/state replaces parts of the state from the outside; events
are dispatched from the page itself via window._wails.dispatchWailsEvent."""
import json, os, sys, time
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

DIST = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "frontend", "dist")
NOW = int(time.time())

state = {
    "vault": {
        "seq": 1, "state": "locked", "path": "D:/Vaults/personal.eks", "displayName": "Personal vault",
        "lastUnlockedAt": NOW - 86400, "locksAt": 0, "absoluteAt": 0, "modifiedAt": NOW - 3600,
        "rotationPending": False, "tampered": False, "warnings": [], "ops": [], "openArchives": 0,
        "dirtyArchives": 0, "hasPasswordSlot": False, "hasHardwareSlot": True, "pendingTouch": False,
        "setupNeeded": False, "defaultPath": "C:/Users/me/AppData/Local/Enfold/vault.eks", "missingPath": "",
        "keptElsewhere": True, "retiredCopies": 0, "retiredPath": "", "damaged": False, "damagedCopyPath": "",
    },
    "archives": [
        {"id": "a1" * 16, "name": "Photos 2024", "path": "D:/Archives/photos-2024.efd", "storedSize": 51_700_000_000,
         "lastWrittenAt": NOW - 7200, "keyVersion": 3, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": False, "hashBehind": 2, "files": 12406, "freeSpace": 3_100_000_000},
        {"id": "b2" * 16, "name": "Family videos", "path": "D:/Archives/family-videos.efd", "storedSize": 227_000_000_000,
         "lastWrittenAt": NOW - 600000, "keyVersion": 1, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": True, "hidden": False, "hashBehind": 0, "files": 96, "freeSpace": 0},
        {"id": "c3" * 16, "name": "Tax returns", "path": "D:/Archives/tax.efd", "storedSize": 193_000_000,
         "lastWrittenAt": NOW - 2000000, "keyVersion": 2, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": False, "hashBehind": 0, "files": 231, "freeSpace": 0},
        {"id": "d4" * 16, "name": "Passport scans", "path": "E:/missing/passports.efd", "storedSize": 10_300_000,
         "lastWrittenAt": NOW - 5000000, "keyVersion": 2, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": False, "hashBehind": 0, "note": "archive.file_missing", "files": 0, "freeSpace": 0},
    ],
    "stat": {"seq": 1, "id": "a1" * 16, "name": "Photos 2024", "size": 51_700_000_000, "files": 12406, "freeSpace": 3_100_000_000,
             "keyVersion": 3, "lastSavedAt": NOW - 7200, "dirty": 3, "state": "dirty", "expiresAt": NOW + 540, "capAt": NOW + 3500,
             "receiptOwed": False, "sessionAlive": True},
    "rows": {
        "": [
            {"fileId": "f0" * 16, "path": "2024", "name": "2024", "size": 0, "storage": "", "savedPercent": 0, "modifiedAt": 0, "isFolder": True, "files": 12400},
            {"fileId": "f1" * 16, "path": "IMG_7201.HEIC", "name": "IMG_7201.HEIC", "size": 4_300_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 90000, "isFolder": False, "files": 0},
            {"fileId": "f2" * 16, "path": "IMG_7202.HEIC", "name": "IMG_7202.HEIC", "size": 4_090_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 90000, "isFolder": False, "files": 0},
            {"fileId": "f3" * 16, "path": "DJI_0042.MP4", "name": "DJI_0042.MP4", "size": 851_000_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 80000, "isFolder": False, "files": 0},
            {"fileId": "f4" * 16, "path": "trip-notes.md", "name": "trip-notes.md", "size": 12_300, "storage": "zstd+dict", "savedPercent": 71, "modifiedAt": NOW - 70000, "isFolder": False, "files": 0, "pending": "added"},
            {"fileId": "f5" * 16, "path": "itinerary.pdf", "name": "itinerary.pdf", "size": 1_260_000, "storage": "zstd", "savedPercent": 18, "modifiedAt": NOW - 60000, "isFolder": False, "files": 0},
            {"fileId": "f6" * 16, "path": "receipts.csv", "name": "receipts.csv", "size": 49_000, "storage": "zstd+dict", "savedPercent": 84, "modifiedAt": NOW - 50000, "isFolder": False, "files": 0, "pending": "deleted"},
        ],
    },
    "slots": [
        {"recipientId": "01" * 16, "type": "hardware", "label": "YubiKey 5C — desk", "createdAt": NOW - 600000, "entangled": False, "stale": False, "removable": True},
        {"recipientId": "02" * 16, "type": "hardware", "label": "YubiKey 5 NFC — travel", "createdAt": NOW - 500000, "entangled": True, "stale": True, "removable": True},
        {"recipientId": "03" * 16, "type": "recovery", "label": "Recovery key — printed, in the safe", "createdAt": NOW - 600000, "entangled": False, "stale": False, "escrowed": True, "removable": True},
        {"recipientId": "04" * 16, "type": "recovery", "label": "Recovery key — the first one", "createdAt": NOW - 900000, "entangled": False, "stale": False, "escrowed": False, "removable": True},
    ],
    "settings": {"vaultPath": "D:/Vaults/personal.eks", "displayName": "Personal vault", "closeToTray": "destroy", "theme": "system",
                 "look": "native", "recoveryRecordPct": 3, "dictionaryBelow": 262144, "idleMinutes": 0, "absoluteMinutes": 0,
                 "timeoutsFromVault": True, "timeoutsAdjustable": True},
    "text": {"text": "# Iceland, July 2024\n\nDay 1: Reykjavik...\n", "truncated": False},
}

def handle(method_id, args):
    v = state["vault"]
    m = METHODS.get(method_id)
    if m is None:
        return None
    return m(args)

METHODS = {
    3940765069: lambda a: state["vault"],                       # vault.Status
    3956196437: lambda a: [{"name": "Yubico YubiKey OTP+FIDO+CCID 0"}],
    1779776360: lambda a: None,                                 # BeginUnlock
    3770426637: lambda a: None, 951839700: lambda a: None, 4094929714: lambda a: None, 2320277474: lambda a: None,
    2953146167: lambda a: None, 882388909: lambda a: None,
    4176692468: lambda a: state["archives"],                    # archives.List
    923201420: lambda a: state["stat"],                         # archives.Open
    1388822288: lambda a: None, 2300343171: lambda a: [], 2169725132: lambda a: None, 474530495: lambda a: None,
    422512670: lambda a: None, 1162996984: lambda a: "e5" * 16, 2111968017: lambda a: "op1", 3689812034: lambda a: "op2",
    3359801409: lambda a: "op3",
    2601627082: lambda a: {"seq": 1, "folder": a[1], "rows": state["rows"].get(a[1], []), "total": len(state["rows"].get(a[1], []))},
    2565212395: lambda a: state["stat"],
    2769288047: lambda a: "op4", 3008447636: lambda a: "op5", 3231340199: lambda a: "op6", 3839303214: lambda a: None,
    3028646727: lambda a: None, 585645538: lambda a: "op7", 3912183196: lambda a: "op8", 2715197693: lambda a: None,
    2470395052: lambda a: None, 913354260: lambda a: "http://127.0.0.1:1/p/x/y", 723007364: lambda a: state["text"],
    1850767145: lambda a: [], 2446376312: lambda a: None, 448053830: lambda a: {"id": a[0], "kind": "add", "done": 1, "total": 1, "phase": "", "startedAt": NOW, "finished": True},
    632849442: lambda a: state["slots"], 309727738: lambda a: None, 3159373965: lambda a: None, 1280438677: lambda a: None,
    207819850: lambda a: None, 3463005426: lambda a: None, 3166408438: lambda a: None,  # RevealRecoveryKey, SaveRecoveryKey, DropRecoveryKey
    332274822: lambda a: None,
    3319062579: lambda a: {"path": a[0], "kind": "backup", "modifiedAt": NOW - 90000, "vaultMatches": True, "slotCount": 1, "hardware": 0, "password": 0, "recovery": 1, "newer": False},  # vault.InspectFile
    18027300: lambda a: None, 1994498129: lambda a: None, 3093488550: lambda a: None,  # ImportFile, FinishSetup, VerifyBackup
    2652127606: lambda a: state["settings"], 740356410: lambda a: None,
    3606391931: lambda a: None, 3229291943: lambda a: ["D:/Pictures/a.jpg"], 2529646972: lambda a: "D:/Pictures", 2079207478: lambda a: None,
    842300112: lambda a: None, 1923582270: lambda a: "D:/new.efd", 3130426784: lambda a: None,
}

class H(SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=DIST, **k)

    def log_message(self, *a):
        pass

    def do_GET(self):
        # The one-time recovery URL the core would mint: 48 digits, once.
        if self.path.startswith("/s/"):
            data = b"1234 5678 9012 3456 7890 1234 5678 9012 3456 7890 1234 5678"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        super().do_GET()

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n) or b"{}")
        if self.path.startswith("/mock/state"):
            for k, v in body.items():
                if isinstance(state.get(k), dict) and isinstance(v, dict):
                    state[k].update(v)
                else:
                    state[k] = v
            return self.reply({"ok": True})
        args = body.get("args") or {}
        if isinstance(args, dict) and "methodID" in args:
            return self.reply(handle(args["methodID"], args.get("args") or []))
        return self.reply({})

    def reply(self, obj):
        data = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()

if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8125
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
