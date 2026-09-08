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
        "entangled": True, "vaultFileSize": 148_480, "tampered": False, "warnings": [], "ops": [], "openArchives": 0,
        "dirtyArchives": 0, "hasPasswordSlot": False, "hasHardwareSlot": True, "pendingTouch": False,
        "setupNeeded": False, "defaultPath": "C:/Users/me/AppData/Local/Enfold/vault.eks", "missingPath": "",
        "keptElsewhere": True, "retiredCopies": 0, "retiredPath": "", "damaged": False, "damagedCopyPath": "",
    },
    # Every record carries its description and its forgotten_at
    # (FORMAT.md 7.1, 18.2): one row is forgotten, so Show hidden, Restore
    # and the retention line have something to render, and one keeps the
    # archive.file_missing note, which is the only thing that says a file
    # is gone (APP.md 13).
    "archives": [
        {"id": "a1" * 16, "name": "Photos 2024", "path": "D:/Archives/photos-2024.efd", "storedSize": 51_700_000_000,
         "lastWrittenAt": NOW - 7200, "keyVersion": 3, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": False, "hashBehind": 2, "files": 12406, "freeSpace": 3_100_000_000,
         "description": "Iceland, the Dolomites, and everything off the phone.", "forgottenAt": 0},
        {"id": "b2" * 16, "name": "Family videos", "path": "/Volumes/Media/family-videos.efd", "storedSize": 227_000_000_000,
         "lastWrittenAt": NOW - 600000, "keyVersion": 1, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": True, "hidden": False, "hashBehind": 0, "files": 96, "freeSpace": 0,
         "description": "", "forgottenAt": 0},
        {"id": "c3" * 16, "name": "Tax returns", "path": "D:/Archives/tax.efd", "storedSize": 193_000_000,
         "lastWrittenAt": NOW - 2000000, "keyVersion": 2, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": True, "hashBehind": 0, "files": 231, "freeSpace": 0,
         "description": "Scans and the filed returns, 2016 onwards.", "forgottenAt": 0},
        {"id": "d4" * 16, "name": "Passport scans", "path": "E:/missing/passports.efd", "storedSize": 10_300_000,
         "lastWrittenAt": NOW - 5000000, "keyVersion": 2, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": False, "hashBehind": 0, "note": "archive.file_missing", "files": 0, "freeSpace": 0,
         "description": "", "forgottenAt": 0},
        {"id": "e5" * 16, "name": "Old laptop backup", "path": "\\\\nas\\backups\\laptop.efd", "storedSize": 8_900_000_000,
         "lastWrittenAt": NOW - 26_000_000, "keyVersion": 1, "open": False, "dirty": 0, "receiptOwed": False,
         "noCompression": False, "hidden": False, "hashBehind": 0, "files": 0, "freeSpace": 0,
         "description": "The 2019 machine, kept until the photos are checked.", "forgottenAt": NOW - 400_000},
    ],
    # One incoming set behind a merge handle, one row per action the core
    # reports (APP.md 13). The mock dispatches no events, so the ceremony
    # itself is driven from the page; this is what IncomingRecords answers.
    "incoming": [
        {"archiveId": "a1" * 16, "name": "Photos 2024", "description": "Iceland, the Dolomites, and everything off the phone.",
         "createdAt": NOW - 40_000_000, "versions": 3, "action": "skip", "forgottenAt": 0, "differs": [], "ticked": False},
        {"archiveId": "b2" * 16, "name": "Family videos", "description": "Everything off the camcorder tapes.",
         "createdAt": NOW - 38_000_000, "versions": 2, "action": "version", "forgottenAt": 0,
         "differs": [{"field": "description", "theirs": "Everything off the camcorder tapes."}], "ticked": True},
        {"archiveId": "f6" * 16, "name": "Recipes", "description": "", "createdAt": NOW - 9_000_000,
         "versions": 1, "action": "add", "forgottenAt": 0, "differs": [], "ticked": True},
        {"archiveId": "e5" * 16, "name": "Old laptop backup", "description": "",
         "createdAt": NOW - 44_000_000, "versions": 1, "action": "forgotten", "forgottenAt": NOW - 400_000,
         "differs": [{"field": "name", "theirs": "Laptop 2019"}], "ticked": False},
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
    # A slot carries no entangled, stale or escrowed since Revision 2: the
    # entangled password is the vault's one switch, no rotation is
    # deferred, and every recovery slot has its kept key (FORMAT.md R38).
    "slots": [
        {"recipientId": "01" * 16, "type": "hardware", "label": "YubiKey 5C — desk", "createdAt": NOW - 600000, "removable": True},
        {"recipientId": "02" * 16, "type": "hardware", "label": "YubiKey 5 NFC — travel", "createdAt": NOW - 500000, "removable": True},
        {"recipientId": "03" * 16, "type": "recovery", "label": "Recovery key — printed, in the safe", "createdAt": NOW - 600000, "recoveryId": "0303-0303", "removable": True},
        {"recipientId": "04" * 16, "type": "recovery", "label": "Recovery key — the first one", "createdAt": NOW - 900000, "recoveryId": "0404-0404", "removable": True},
    ],
    "entangled": {"on": True, "canEnable": True},
    "lastExportAt": NOW - 1_900_000,
    "settings": {"vaultPath": "D:/Vaults/personal.eks", "displayName": "Personal vault", "closeToTray": "destroy", "theme": "system",
                 "look": "native", "recoveryRecordPct": 3, "dictionaryBelow": 262144, "idleMinutes": 0, "absoluteMinutes": 0,
                 "timeoutsFromVault": True, "timeoutsAdjustable": True},
    "text": {"text": "# Iceland, July 2024\n\nDay 1: Reykjavik...\n", "truncated": False},
}

def archive(archive_id):
    for a in state["archives"]:
        if a["id"] == archive_id:
            return a
    return None


def details(archive_id):
    """One registry record read whole (APP.md 13): no wrapped_archive_key,
    no nonce and no offset, so the app's one boundary holds."""
    a = archive(archive_id)
    if a is None:
        return None
    return {
        "archiveId": a["id"], "name": a["name"], "description": a.get("description", ""),
        "createdAt": NOW - 40_000_000, "lastPath": a["path"], "currentKid": a["id"][:32],
        "revision": 7, "lastWriter": "9f" * 8, "lastSeq": 812, "hashAtSeq": 810,
        "lastCiphertextHash": a["id"][::-1], "lastStoredSize": a["storedSize"],
        "lastWrittenAt": a["lastWrittenAt"], "forgottenAt": a.get("forgottenAt", 0),
        "alwaysRequireFullAuth": False, "hidden": a["hidden"], "noCompression": a["noCompression"],
        "versions": [
            {"kid": a["id"][:32], "createdAt": NOW - 900_000, "retiredAt": 0, "state": "current"},
        ] + [
            {"kid": f"{i:02x}" * 16, "createdAt": NOW - 40_000_000 + i * 1_000_000, "retiredAt": NOW - 900_000, "state": "retired"}
            for i in range(1, a["keyVersion"])
        ],
    }


def set_field(args, field):
    a = archive(args[0]) if args else None
    if a is not None:
        a[field] = args[1]
    return None


def forget(args, at):
    a = archive(args[0]) if args else None
    if a is not None:
        a["forgottenAt"] = at
    return None


def listed(show_hidden):
    return [a for a in state["archives"] if show_hidden or (not a["hidden"] and not a.get("forgottenAt"))]


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
    4176692468: lambda a: listed(bool(a and a[0])),             # archives.List
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
    # The vault's entangled password and the last backup (APP.md §13). Each
    # id is FNV-1a/32 over "github.com/dreamxwarden01/enfold/internal/app/
    # api.<Struct>.<Method>" — the name only, so BeginEnroll keeps its id
    # after losing an argument and a rename moves one in silence.
    898464716: lambda a: state["entangled"],                    # keys.EntangledState
    2247223905: lambda a: state["entangled"].update({"on": bool(a[0])}) if a else None,  # keys.SetEntangled
    3747209322: lambda a: None,                                 # keys.ChangeEntangledPassword
    3774546620: lambda a: state["lastExportAt"],                # vault.LastExportAt
    3319062579: lambda a: {"path": a[0], "kind": "backup", "modifiedAt": NOW - 90000, "vaultMatches": True, "slotCount": 1, "hardware": 0, "password": 0, "recovery": 1, "newer": False,
                           "recoverySlots": [{"recipientId": "03" * 16, "label": "Recovery key - printed, in the safe", "createdAt": NOW - 600000}], "generation": 4},  # vault.InspectFile
    # The Archives page as the inspector, and the merge (APP.md 13).
    2187678638: lambda a: set_field(a, "name"),                 # archives.Rename
    48311342: lambda a: set_field(a, "description"),            # archives.SetDescription
    2517846314: lambda a: details(a[0]) if a else None,         # archives.Details
    2605071885: lambda a: forget(a, NOW),                       # archives.Forget
    2523843286: lambda a: forget(a, 0),                         # archives.Restore
    2185012391: lambda a: forget(a, NOW),                       # archives.Delete
    118112595: lambda a: None,                                  # archives.CheckFiles
    1148297629: lambda a: None,                                 # vault.InspectRecords (a ceremony)
    2398325755: lambda a: state["incoming"],                    # vault.IncomingRecords
    1344102321: lambda a: None,                                 # vault.MergeRecords
    3468826983: lambda a: None,                                 # vault.DiscardRecords
    18027300: lambda a: None, 1994498129: lambda a: None, 3093488550: lambda a: None,  # ImportFile, FinishSetup, VerifyBackup
    2652127606: lambda a: state["settings"], 740356410: lambda a: state["settings"].update(a[0]) if a else None,
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
