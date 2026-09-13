"""A stand-in for the Wails backend so the built page can be exercised in a
browser: serves frontend/dist and answers POST /wails/runtime with canned
data. POST /mock/state replaces parts of the state from the outside.

Events. The runtime has no socket here, so the mock queues what the core
would emit and the page drains it. GET /mock/events returns the queue and
empties it; paste this into the page once to have it dispatch them:

    setInterval(async () => {
      for (const e of await (await fetch("/mock/events")).json())
        window._wails.dispatchWailsEvent(e);
    }, 200);

That is what makes an operation visible: an add, a replace or an extract
here runs for a few seconds, ticking op.progress by bytes the way the core
does, and publishes nothing until it commits at its end (APP.md 2.3).
POST /mock/preview {"name": "reclaim"} plays the in-place compaction the
core runs itself after a commit whose plan would give enough of the tail
back (APP.md 2.3, FORMAT.md R40) - the strip's *Reclaiming space*,
cancellable, the space back on the status line and "Reclaimed 1.2 GB" said
when it ends.

An archive's lifetime (APP.md 2.3, ruled 2026-09-10) is played too: the
page's Archives.Open mounts one archive and mints a preview token; every
Archive-service method answers archive.not_open for an archive that is not
mounted, and the preview URL - served by this mock at /p/<token>/<id> -
answers 404 the moment the token is gone. Archives.Leave unmounts at once
and leaves the archive DRAINING while one of its own operations still runs
(the row keeps open: true until it ends, then archives.changed with open:
false); Archives.Close is the kill switch and cancels that operation.

The drag out of the window (APP.md 3, ruled 2026-09-10/11) is played as
far as a browser can play it: Shell.DragOut answers a fake dragout
operation labelled *Extracting N items* throughout - the label alone while
it hovers, then the byte bar with Cancel while it stages, then that bar
standing full while the target copies inside its own Drop - and ends moved
when the fake DoDragDrop returns, which is when the call answers.
POST /mock/state {"drag": {"skip": true}} is the target answering Skip to
its own conflict dialog: no effect, and the operation ends cancelled with
the strip going at once (the drag that hung before the drop was made
synchronous). POST /mock/state
{"drag": {"selfDrop": true}} makes the next drag a self-drop - the call
answers selfDrop with nothing extracted, and the page performs the Move of
the ids it kept in flight when the WebView's drop lands on a folder row
(dispatch a drop event on one, since a native drag cannot run here: the
page tells its own drop by the names it carries, one File per dragged
record, so build the event's dataTransfer with a File of each selected
row's name - a drop of other names is Explorer's and stays an add) - and
{"drag": {"fail": "file.name_refused"}} fails the extraction half-way
through the preparing phase, which fails the drop's request (dragResult
failed).

The close question (APP.md 2.4, ruled 2026-09-13) is played too: POST
/mock/preview {"name": "close"} queues the shell.close the shell emits when
it has cancelled a window close, so the page shows its close question here;
Shell.CloseDecided records the answer in state["closeDecided"] and, with
remember, writes closeAction into the settings the page reads back."""
import json, os, re, secrets, sys, threading, time
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

DIST = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "frontend", "dist")
NOW = int(time.time())
PORT = 8125

# Ids are 32 lowercase hex digits and the all-zero id is the archive's
# root directory - the same value the core, the page and the generated
# bindings spell (APP.md 3, FORMAT.md R39).
ROOT_ID = "0" * 32

state = {
    "vault": {
        "seq": 1, "state": "locked", "path": "D:/Vaults/personal.eks", "displayName": "Personal vault",
        "lastUnlockedAt": NOW - 86400, "locksAt": 0, "absoluteAt": 0, "modifiedAt": NOW - 3600,
        "entangled": True, "vaultFileSize": 148_480, "tampered": False, "warnings": [], "ops": [], "openArchives": 0,
        "hasPasswordSlot": False, "hasHardwareSlot": True, "pendingTouch": False,
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
         "lastWrittenAt": NOW - 7200, "keyVersion": 3, "open": False, "receiptOwed": False,
         "method": "normal", "hidden": False, "hashBehind": 2, "files": 12406, "freeSpace": 3_100_000_000,
         "description": "Iceland, the Dolomites, and everything off the phone.", "forgottenAt": 0},
        {"id": "b2" * 16, "name": "Family videos", "path": "/Volumes/Media/family-videos.efd", "storedSize": 227_000_000_000,
         "lastWrittenAt": NOW - 600000, "keyVersion": 1, "open": False, "receiptOwed": False,
         "method": "store", "hidden": False, "hashBehind": 0, "files": 96, "freeSpace": 0,
         "description": "", "forgottenAt": 0},
        {"id": "c3" * 16, "name": "Tax returns", "path": "D:/Archives/tax.efd", "storedSize": 193_000_000,
         "lastWrittenAt": NOW - 2000000, "keyVersion": 2, "open": False, "receiptOwed": False,
         "method": "best", "hidden": True, "hashBehind": 0, "files": 231, "freeSpace": 0,
         "description": "Scans and the filed returns, 2016 onwards.", "forgottenAt": 0},
        {"id": "d4" * 16, "name": "Passport scans", "path": "E:/missing/passports.efd", "storedSize": 10_300_000,
         "lastWrittenAt": NOW - 5000000, "keyVersion": 2, "open": False, "receiptOwed": False,
         "method": "fastest", "hidden": False, "hashBehind": 0, "note": "archive.file_missing", "files": 0, "freeSpace": 0,
         "description": "", "forgottenAt": 0},
        {"id": "e5" * 16, "name": "Old laptop backup", "path": "\\\\nas\\backups\\laptop.efd", "storedSize": 8_900_000_000,
         "lastWrittenAt": NOW - 26_000_000, "keyVersion": 1, "open": False, "receiptOwed": False,
         "method": "better", "hidden": False, "hashBehind": 0, "files": 0, "freeSpace": 0,
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
    # ArchiveStat lost Dirty, CapAt and SessionAlive on 2026-09-09 and
    # gained Records: an archive is clean between operations, and Extract
    # all is greyed on a record count of zero (APP.md 2.3, 3). It lost
    # ExpiresAt on 2026-09-10: an open archive has no timeout of its own,
    # so there is no deadline to carry.
    "stat": {"seq": 1, "id": "a1" * 16, "name": "Photos 2024", "size": 51_700_000_000, "files": 12406, "freeSpace": 3_100_000_000,
             "records": 0, "keyVersion": 3, "lastSavedAt": NOW - 7200, "state": "open",
             "receiptOwed": False, "copyMismatch": False},
    # The index is a tree (APP.md 3, FORMAT.md R39): one record per
    # directory and per file, hanging off its parent by id, the root the
    # all-zero id and the path a derived thing. Every row is committed -
    # there is no pending vocabulary any more - so what is worth looking
    # at is an empty folder, a folder inside a folder, and a name longer
    # than any column can hold.
    "records": [
        {"id": "f0" * 16, "parentId": ROOT_ID, "isDir": True, "name": "2024", "size": 0, "storage": "", "savedPercent": 0, "modifiedAt": NOW - 900000},
        {"id": "fa" * 16, "parentId": ROOT_ID, "isDir": True, "name": "Empty folder", "size": 0, "storage": "", "savedPercent": 0, "modifiedAt": NOW - 30000},
        {"id": "f1" * 16, "parentId": ROOT_ID, "isDir": False, "name": "IMG_7201.HEIC", "size": 4_300_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 90000},
        {"id": "f2" * 16, "parentId": ROOT_ID, "isDir": False, "name": "IMG_7202.HEIC", "size": 4_090_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 90000},
        {"id": "f3" * 16, "parentId": ROOT_ID, "isDir": False, "name": "DJI_0042.MP4", "size": 851_000_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 80000},
        {"id": "f4" * 16, "parentId": ROOT_ID, "isDir": False, "name": "trip-notes.md", "size": 12_300, "storage": "zstd+dict", "savedPercent": 71, "modifiedAt": NOW - 70000},
        {"id": "f5" * 16, "parentId": ROOT_ID, "isDir": False, "name": "itinerary.pdf", "size": 1_260_000, "storage": "zstd", "savedPercent": 18, "modifiedAt": NOW - 60000},
        {"id": "f6" * 16, "parentId": ROOT_ID, "isDir": False, "name": "receipts.csv", "size": 49_000, "storage": "zstd+dict", "savedPercent": 84, "modifiedAt": NOW - 50000},
        # A name longer than any column can hold: it ellipsizes, and the
        # cell carries the whole of it for the hover (APP.md 6).
        {"id": "f7" * 16, "parentId": ROOT_ID, "isDir": False,
         "name": "2024-07-14 Reykjavik to Vik - the long way round, with the puffins.HEIC", "size": 6_820_000,
         "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 40000},
        {"id": "e0" * 16, "parentId": "f0" * 16, "isDir": True, "name": "Trips", "size": 0, "storage": "", "savedPercent": 0, "modifiedAt": NOW - 880000},
        {"id": "e1" * 16, "parentId": "f0" * 16, "isDir": False, "name": "IMG_0001.HEIC", "size": 3_900_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 900000},
        {"id": "e2" * 16, "parentId": "f0" * 16, "isDir": False, "name": "IMG_0002.HEIC", "size": 4_120_000, "storage": "raw", "savedPercent": 0, "modifiedAt": NOW - 890000},
        {"id": "e3" * 16, "parentId": "f0" * 16, "isDir": False, "name": "packing.txt", "size": 2_100, "storage": "zstd", "savedPercent": 79, "modifiedAt": NOW - 880000},
        {"id": "e4" * 16, "parentId": "f0" * 16, "isDir": False, "name": "budget.csv", "size": 31_000, "storage": "zstd+dict", "savedPercent": 84, "modifiedAt": NOW - 870000},
        {"id": "d0" * 16, "parentId": "e0" * 16, "isDir": True, "name": "Day one", "size": 0, "storage": "", "savedPercent": 0, "modifiedAt": NOW - 860000},
        {"id": "d1" * 16, "parentId": "e0" * 16, "isDir": False, "name": "day-one.md", "size": 4_400, "storage": "zstd", "savedPercent": 66, "modifiedAt": NOW - 860000},
        {"id": "d2" * 16, "parentId": "e0" * 16, "isDir": False, "name": "day-two.md", "size": 5_100, "storage": "zstd", "savedPercent": 68, "modifiedAt": NOW - 850000},
    ],
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
    "settings": {"vaultPath": "D:/Vaults/personal.eks", "displayName": "Personal vault", "closeAction": "ask", "theme": "system",
                 "look": "native", "recoveryRecordPct": 3, "dictionaryBelow": 262144, "idleMinutes": 0, "absoluteMinutes": 0,
                 "timeoutsFromVault": True, "timeoutsAdjustable": True, "lastArchiveFolder": "D:/Archives"},
    "text": {"text": "# Iceland, July 2024\n\nDay 1: Reykjavik...\n", "truncated": False},
    # What the destination already holds, keyed by the record's path inside
    # the archive: the mock's stand-in for the exclusive create the core
    # cannot do here. Under the `ask` policy each one comes back as a
    # `conflict` outcome carrying this stat, which is taken after the
    # refusal and never to decide it (APP.md 3, ruled 2026-09-10). The
    # first matches the archive's own copy exactly, so the compare list's
    # "Skip N files with the same date and size" has something to tick.
    "inDestination": {
        "IMG_7201.HEIC": {"size": 4_300_000, "modifiedAt": NOW - 90000},
        "itinerary.pdf": {"size": 990_000, "modifiedAt": NOW - 200000},
        "2024/IMG_0001.HEIC": {"size": 1_200_000, "modifiedAt": NOW - 400000},
    },
    # The destination's limits, the mock's stand-in for a volume that
    # refuses a name (APP.md 3, ruled 2026-09-10): a leaf longer than
    # nameOver code units is name_refused on placement - the puffins
    # photo above is 71 - and a full path longer than pathOver is
    # path_refused (a file's folder, or a folder itself, reported once
    # with nothing beneath it). POST /mock/state {"refuse": {...}} moves
    # them; the core has no such numbers, only the volume's refusal.
    "refuse": {"nameOver": 64, "pathOver": 200},
    # The operations running now, and the events the page has not drained.
    "ops": {},
    "events": [],
    # The archive the page has mounted (Archives.Open), and the preview
    # token minted for it - forgotten at Leave and at Close, after which
    # the URL is 404 whatever it says (APP.md 2.3, 4).
    "mounted": None,
    "token": "",
    # The drag out's switches (drag_out): a self-drop, an extraction that
    # fails while preparing, or a target that answers Skip. All three are
    # read at the next DragOut.
    "drag": {"selfDrop": False, "fail": "", "skip": False},
    # What the close question was answered with (APP.md 2.4): every
    # Shell.CloseDecided(action, remember) in order, so the pane can be
    # checked against what the shell would have been told. There is no
    # window to close here, so the call records and answers.
    "closeDecided": [],
}

# Scenes the lock screen cannot reach on its own here (the mock dispatches
# no events): POST /mock/preview {"name": "..."} puts one on the vault
# status, then reload the page. "pin" is the merged card of the ruling of
# 2026-09-08 — an entangled vault, a token ceremony parked at the PIN, with
# the vault's password waiting in the field beside it; "deadline" is the
# note the status carries when the five minutes from the touch ran out
# (APP.md 2.2); "locked" puts it back.
# "reclaim" is not a vault state but an operation: it plays the in-place
# compaction the core runs itself after a commit whose plan would give
# enough of the tail back (APP.md 2.3, FORMAT.md R40). It is in the same
# set because it is the same question - "show me that" - and the route
# tells the two apart by name.
# "close" is neither a vault state nor an operation but the one event the
# pane cannot raise for itself: the shell's cancelled window close (APP.md
# 2.4), which the page answers with its close question. It is queued like
# every other event and drained by GET /mock/events.
PREVIEW_OPS = {"reclaim": lambda: start_reclaim(), "close": lambda: emit("shell.close", None)}

PREVIEWS = {
    "pin": {
        "state": "unlocking", "entangled": True, "note": "",
        "ceremony": {
            "seq": 2, "kind": "unlock", "method": "token", "step": "pin", "promptId": "pin-1",
            "choose": False, "slotLabel": "YubiKey 5C - desk", "recoveryId": "", "retries": 3,
            "retriesKnown": True, "verified": False, "readerCount": 1, "n": 0, "pinAsked": False,
            "error": "", "removeLabel": "", "insertLabel": "", "archives": 0,
        },
    },
    "wrong_pin": {
        "state": "unlocking", "entangled": True, "note": "",
        "ceremony": {
            "seq": 3, "kind": "unlock", "method": "token", "step": "pin", "promptId": "pin-2",
            "choose": False, "slotLabel": "YubiKey 5C - desk", "recoveryId": "", "retries": 2,
            "retriesKnown": True, "verified": False, "readerCount": 1, "n": 0, "pinAsked": False,
            "error": "token.pin", "removeLabel": "", "insertLabel": "", "archives": 0,
        },
    },
    "wrong_password": {
        "state": "unlocking", "entangled": True, "note": "",
        "ceremony": {
            "seq": 4, "kind": "unlock", "method": "token", "step": "password", "promptId": "pw-2",
            "choose": False, "slotLabel": "YubiKey 5C - desk", "recoveryId": "", "retries": 0,
            "retriesKnown": False, "verified": True, "readerCount": 1, "n": 1, "pinAsked": True,
            "error": "vault.auth", "removeLabel": "", "insertLabel": "", "archives": 0,
        },
    },
    "deadline": {"state": "locked", "entangled": True, "note": "token.password_deadline", "ceremony": None},
    "locked": {"state": "locked", "entangled": True, "note": "", "ceremony": None},
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
        # A ciphertext hash is 64 hex digits and takes a second line in the
        # modal rather than being cut; all zeros means no commit yet, which
        # reads N/A there (APP.md 13). The record with no file to hash is
        # the one that has never been written.
        "lastCiphertextHash": "0" * 64 if a.get("note") == "archive.file_missing" else (a["id"] + a["id"][::-1]),
        "lastStoredSize": a["storedSize"],
        "lastWrittenAt": a["lastWrittenAt"], "forgottenAt": a.get("forgottenAt", 0),
        "alwaysRequireFullAuth": False, "hidden": a["hidden"], "method": a["method"],
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


class Err:
    """A coded refusal, answered the way the Wails runtime reads one: a
    non-2xx JSON body whose `cause` carries the app's code (APP.md 3)."""

    def __init__(self, code):
        self.code = code


# ---- the archive's tree (APP.md 3, FORMAT.md R39) ----------------------
#
# One record per directory and per file, hanging off its parent by id. The
# page names a place by id and never by a path: Page's dirID, the parentID
# of CreateFolder / AddFiles / AddFolder / CheckNames / Move, and the root
# among Extract's recordIDs. A path is derived, for the row and the pane.

def record(rid):
    for r in state["records"]:
        if r["id"] == rid:
            return r
    return None


def children(pid):
    return [r for r in state["records"] if r["parentId"] == pid]


def subtree(rid):
    """The ids beneath rid, rid itself excluded."""
    out, stack = [], [rid]
    while stack:
        for c in children(stack.pop()):
            out.append(c["id"])
            stack.append(c["id"])
    return out


def path_of(r):
    parts, at = [r["name"]], r["parentId"]
    while at != ROOT_ID:
        up = record(at)
        if up is None:
            break
        parts.append(up["name"])
        at = up["parentId"]
    return "/".join(reversed(parts))


def size_of(r):
    """A directory's size is the sum beneath it."""
    if not r["isDir"]:
        return r["size"]
    return sum(record(i)["size"] for i in subtree(r["id"]) if not record(i)["isDir"])


def row(r):
    return {
        "id": r["id"], "parentId": r["parentId"], "isDir": r["isDir"], "name": r["name"],
        "path": path_of(r), "size": size_of(r), "storage": r["storage"],
        "savedPercent": r["savedPercent"], "modifiedAt": r["modifiedAt"],
    }


def crumbs_to(dir_id):
    """Root-inclusive and never empty: the first entry is the root, named
    with the archive's own name, the last is the folder shown."""
    chain, at = [], dir_id
    while at != ROOT_ID:
        rec = record(at)
        if rec is None:
            break
        chain.append({"id": rec["id"], "name": rec["name"]})
        at = rec["parentId"]
    chain.append({"id": ROOT_ID, "name": state["stat"]["name"]})
    return list(reversed(chain))


def stat():
    """ArchiveStat, with Records counted live: files and directories
    together, which is what Extract all is greyed on (APP.md 3)."""
    s = dict(state["stat"])
    s["records"] = len(state["records"])
    s["files"] = len([r for r in state["records"] if not r["isDir"]])
    return s


def bump(archive_id=None):
    """One commit: the archive's seq advances and the page is told
    (APP.md 2.3, an operation is a transaction). An operation the Archives
    page started on an archive whose page is not open advances no tree the
    page is showing, so only the list is told."""
    if archive_id is not None and archive_id != state["stat"]["id"]:
        emit("archives.changed", {"purged": []})
        return
    state["stat"]["seq"] += 1
    state["stat"]["lastSavedAt"] = int(time.time())
    emit("archive.changed", {"id": state["stat"]["id"], "seq": state["stat"]["seq"]})
    emit("archives.changed", {"purged": []})


def new_id():
    state["nextId"] = state.get("nextId", 0) + 1
    return ("%032x" % (0xC0FFEE0000 + state["nextId"]))[-32:]


def live_dir(dir_id):
    """Is dir_id a directory that can be listed or written into: itself a
    live record, and every ancestor live with it. A folder whose ancestor
    was deleted went with it, and is file.not_found (APP.md 3)."""
    at = dir_id
    seen = set()
    while at != ROOT_ID:
        if at in seen:
            return False  # a cycle the mock's own state made
        seen.add(at)
        rec = record(at)
        if rec is None or not rec["isDir"]:
            return False
        at = rec["parentId"]
    return True


def parse_sort(sort_by):
    """Page's sort grammar (APP.md 3, ruled 2026-09-10), the core's rule:
    one or two signed keys, comma-separated - the column (name, size,
    type or modified, a leading "-" for descending), then optionally
    "name" or "-name" for the tie-break's direction; the empty string is
    name; a second name key must agree with a first. Anything else is
    None, and the caller answers params."""
    if sort_by == "":
        return ("name", False, False)
    parts = sort_by.split(",")
    if len(parts) > 2:
        return None
    key, desc = (parts[0][1:], True) if parts[0].startswith("-") else (parts[0], False)
    if key not in ("name", "size", "type", "modified"):
        return None
    name_desc = desc if key == "name" else False
    if len(parts) == 2:
        tie, tie_desc = (parts[1][1:], True) if parts[1].startswith("-") else (parts[1], False)
        if tie != "name" or (key == "name" and tie_desc != desc):
            return None
        name_desc = tie_desc
    return (key, desc, name_desc)


def name_key(name):
    """A stand-in for the core's collation: case ignored, runs of digits
    by value, then the name itself by code point so the order is total."""
    parts = re.split(r"(\d+)", name.casefold())
    return (tuple((0, int(x)) if x.isdigit() else (1, x) for x in parts if x != ""), name)


def ext_of(name):
    """The type key: the part after the last dot, folded; none when the
    dot is first or absent (APP.md 6)."""
    i = name.rfind(".")
    return "" if i <= 0 else name[i + 1:].lower()


def sorted_children(dir_id, order):
    """The children of dir_id in Page's order (APP.md 3): directories
    before files under every key and both directions, then the key, then
    the name in the tie-break's direction. Under size the directories,
    which show none, fall straight to the name; under type a file with no
    extension is first in both directions."""
    key, desc, name_desc = order
    dirs = [r for r in children(dir_id) if r["isDir"]]
    files = [r for r in children(dir_id) if not r["isDir"]]
    for group in (dirs, files):
        group.sort(key=lambda r: name_key(r["name"]), reverse=name_desc)
    if key == "size":
        files.sort(key=lambda r: r["size"], reverse=desc)
    elif key == "modified":
        dirs.sort(key=lambda r: r["modifiedAt"], reverse=desc)
        files.sort(key=lambda r: r["modifiedAt"], reverse=desc)
    elif key == "type":
        none = [r for r in files if ext_of(r["name"]) == ""]
        typed = [r for r in files if ext_of(r["name"]) != ""]
        typed.sort(key=lambda r: ext_of(r["name"]), reverse=desc)
        files = none + typed
    return dirs + files


def page(args):
    """Page(id, dirID, sortBy, offset, limit). A dirID that no longer names
    a live directory is file.not_found, never an empty listing under a
    breadcrumb that still names the place; a live empty directory answers
    zero rows with total 0. The rows come in sortBy's order (parse_sort),
    a page at a time: limit <= 0 is 200, over 1000 is 1000, and the Seq
    is the archive's, so the page can tell a listing that moved."""
    _, dir_id, sort_by, offset, limit = (list(args) + ["", ROOT_ID, "", 0, 0])[:5]
    order = parse_sort(sort_by or "")
    if order is None:
        return Err("params")
    if not live_dir(dir_id):
        return Err("file.not_found")
    kids = sorted_children(dir_id, order)
    offset = max(0, min(int(offset or 0), len(kids)))
    limit = int(limit or 0)
    limit = 200 if limit <= 0 else min(limit, 1000)
    rows = [row(r) for r in kids[offset:offset + limit]]
    return {"seq": state["stat"]["seq"], "rows": rows, "total": len(kids), "crumbs": crumbs_to(dir_id)}


def children_ids(args):
    """Children(id, dirID, sortBy): every live child of dirID in Page's
    order, its id and its kind and nothing else - what the header's tick
    and Ctrl+A select, the folder and not the rows loaded so far, and what
    a Delete's question counts, files and folders apart, past the rows
    loaded (APP.md 3)."""
    _, dir_id, sort_by = (list(args) + ["", ROOT_ID, ""])[:3]
    order = parse_sort(sort_by or "")
    if order is None:
        return Err("params")
    if not live_dir(dir_id):
        return Err("file.not_found")
    return [{"id": r["id"], "isDir": r["isDir"]} for r in sorted_children(dir_id, order)]


def a_dir(pid):
    """The parent a change may name: the root, or a directory live in the
    merged view."""
    return None if live_dir(pid) else Err("file.not_found")


def emit(name, data):
    """Queue what the core would emit; GET /mock/events drains it."""
    state["events"].append({"name": name, "data": data})


def op_view(op):
    return {k: v for k, v in op.items() if k != "cancelled"}


def start_op(kind, total, commit, seconds=4.0, steps=24, items=0, phase="", archive_id=None,
             policy="", destination=""):
    """An operation is a transaction (APP.md 2.3): it begins, ticks
    op.progress by *bytes* - Done against Total, both plaintext - and
    commits at its end. A Cancel aborts it and publishes nothing: the
    records the commit would have made are never made. Items is what the
    operation planned - the files an add, a replace or an extract will
    write - which is what the strip counts: "Adding 3 files", singular at
    one (APP.md 3, ruled 2026-09-09). An extract's OpView carries its
    Policy and Destination from its first event, so the conflict question
    is the operation's own (APP.md 3, ruled 2026-09-10)."""
    state["nextOp"] = state.get("nextOp", 0) + 1
    op_id = "op%d" % state["nextOp"]
    aid = archive_id or state["stat"]["id"]
    op = {"id": op_id, "kind": kind, "archiveId": aid, "done": 0, "total": total,
          "items": items, "phase": phase, "startedAt": int(time.time()), "finished": False, "results": [],
          "returned": 0}
    if policy:
        op["policy"] = policy
        op["destination"] = destination
    state["ops"][op_id] = op

    def run():
        for i in range(1, steps + 1):
            time.sleep(seconds / steps)
            if op.get("cancelled"):
                op["finished"] = True
                op["error"] = "op.cancelled"
                emit("op.done", op_view(op))
                finish_drain(aid)
                return
            op["done"] = total * i // steps
            emit("op.progress", op_view(op))
        # A commit with something to report answers its FileOutcomes; the
        # rest answer None and the operation reports nothing.
        out = commit()
        if out is not None:
            op["results"] = out
        op["finished"] = True
        emit("op.done", op_view(op))
        bump(aid)
        finish_drain(aid)

    threading.Thread(target=run, daemon=True).start()
    return op_id


def running_ops(archive_id):
    return [o for o in state["ops"].values() if o["archiveId"] == archive_id and not o["finished"]]


def finish_drain(archive_id):
    """The last of a left archive's own operations has ended: it closes
    itself now (APP.md 2.3) and the list is told."""
    a = archive(archive_id)
    if a is None or not a.get("draining") or running_ops(archive_id):
        return
    a["draining"] = False
    a["open"] = False
    state["vault"]["openArchives"] = open_count()
    emit("archives.changed", {"purged": []})


def cancel_op(args):
    op = state["ops"].get(args[0] if args else "")
    if op is None:
        return Err("op.not_found")
    op["cancelled"] = True
    return None


def create_folder(args):
    """CreateFolder(id, parentID, name) returns the new record's id at
    once: the folder is immediately a real parent (FORMAT.md R39)."""
    _, parent, name = (list(args) + ["", ROOT_ID, ""])[:3]
    bad = a_dir(parent)
    if bad:
        return bad
    if not name or "/" in name:
        return Err("file.name")
    if any(c["name"].lower() == name.lower() for c in children(parent)):
        return Err("file.exists")
    rid = new_id()
    state["records"].append({"id": rid, "parentId": parent, "isDir": True, "name": name, "size": 0,
                             "storage": "", "savedPercent": 0, "modifiedAt": NOW})
    bump()
    return rid


def move(args):
    """Move(id, recordIDs, parentID), refused whole and in place: nothing
    is staged when any item fails, so the page can say why against the
    drag (APP.md 3)."""
    _, ids, parent = (list(args) + ["", [], ROOT_ID])[:3]
    ids = ids or []
    bad = a_dir(parent)
    if bad:
        return bad
    for rid in ids:
        r = record(rid)
        if r is None:
            return Err("file.not_found")
        if r["isDir"] and (parent == rid or parent in subtree(rid)):
            return Err("file.move_into_self")
    taken = {c["name"].lower() for c in children(parent) if c["id"] not in ids}
    for rid in ids:
        name = record(rid)["name"].lower()
        if name in taken:
            return Err("file.exists")
        taken.add(name)
    moved = False
    for rid in ids:
        r = record(rid)
        if r["parentId"] == parent:
            continue  # already there: a no-op
        r["parentId"] = parent
        moved = True
    if moved:
        bump()
    return None


def delete_records(args):
    """Delete(id, recordIDs): the page asks first and the operation then
    commits at once - a directory takes its subtree in the same commit,
    and there is no undo (APP.md 3, FORMAT.md R32)."""
    ids = (list(args) + ["", []])[1] or []
    gone = set()
    for rid in ids:
        if record(rid) is None:
            continue
        gone |= set(subtree(rid)) | {rid}
    if not gone:
        return None
    freed = sum(x["size"] for x in state["records"] if x["id"] in gone and not x["isDir"])
    state["records"][:] = [x for x in state["records"] if x["id"] not in gone]
    state["stat"]["freeSpace"] += freed
    bump()
    # The commit that freed the tail has truncated it; what is left is the
    # holes, and the core takes them itself once they are worth it.
    if reclaim_due():
        start_reclaim()
    return None


# The rule the core reclaims on (APP.md 2.3, FORMAT.md R40): a run of
# moves happens when it would give the file system back 64 MiB or more of
# the tail, and that is at least a quarter of what it would have to move.
# The mock has no layout to plan on, so it takes the free space for the
# tail and the live bytes for the move; what the core measures is bytes
# returned, never the size of a hole.
RECLAIM_FLOOR = 64 * 1024 * 1024


def reclaim_due():
    st = state["stat"]
    live = max(0, st["size"] - st["freeSpace"])
    return st["freeSpace"] >= RECLAIM_FLOOR and st["freeSpace"] * 4 >= live


def running_reclaim():
    """The run on the books, or None. The core plans no second run over a
    first (ops.go reclaimDueLocked): a run is an operation of the archive,
    and an operation of the archive is what holds the next one off."""
    for o in running_ops(state["stat"]["id"]):
        if o["kind"] == "reclaim":
            return o
    return None


def start_reclaim():
    """*Reclaiming space* (APP.md 2.3, 6): the in-place compaction the user
    did not ask for, cancellable, which lowers the live data into the holes
    a budget per commit and gives the tail back as it comes free. Its Items
    is zero - it moves bytes, not files - its Total is the bytes to move,
    its phase the copies it is made of, which the strip does not repeat,
    and what came back is said apart from what moved: Returned is the
    file's own shrinking, which the page reports as "Reclaimed 1.2 GB".

    A run already on the books is answered instead of a second being begun,
    and what this one reports is measured on its own run alone: the file's
    size when it began against the size it leaves, never the free map read
    at its commit, which a delete made while it ran has already grown."""
    running = running_reclaim()
    if running is not None:
        return running["id"]
    st = state["stat"]
    before = st["size"]
    holes = st["freeSpace"]           # what this run set out to give back
    live = max(0, before - holes)     # and the bytes it has to move for it

    def commit():
        # This run's own holes, and no more: space freed under it belongs to
        # the run after, which the next commit plans.
        give = min(holes, st["freeSpace"])
        st["size"] = max(0, st["size"] - give)
        st["freeSpace"] -= give
        # The commit runs on the operation's thread, once start_op has
        # answered the id below.
        state["ops"][op_id]["returned"] = max(0, before - st["size"])

    op_id = start_op("reclaim", live, commit, seconds=6.0, phase="moving")
    return op_id


def rename_record(args):
    _, rid, name = (list(args) + ["", "", ""])[:3]
    r = record(rid)
    if r is None:
        return Err("file.not_found")
    if not name or "/" in name:
        return Err("file.name")
    if any(c["name"].lower() == name.lower() and c["id"] != rid for c in children(r["parentId"])):
        return Err("file.exists")
    r["name"] = name
    bump()
    return None


def check_names(args):
    """CheckNames(id, parentID, names): a name offered with a trailing "/"
    is offered as a directory, and the collision carries the kind on both
    sides."""
    _, parent, names = (list(args) + ["", ROOT_ID, []])[:3]
    live = children(parent)
    out = []
    for n in names or []:
        is_dir = n.endswith("/")
        bare = n[:-1] if is_dir else n
        for c in live:
            if c["name"].lower() == bare.lower():
                out.append({"name": bare, "isDir": is_dir, "existing": c["id"],
                            "existingIsDir": c["isDir"]})
                break
    return out


ADDED_SIZE = 14_800_000  # one added file's plaintext, so the bar has a run


def add_one(parent, path, is_dir):
    name = [p for p in path.replace("\\", "/").split("/") if p][-1]
    state["records"].append({"id": new_id(), "parentId": parent, "isDir": is_dir, "name": name,
                             "size": 0 if is_dir else ADDED_SIZE, "storage": "" if is_dir else "zstd",
                             "savedPercent": 0 if is_dir else 31, "modifiedAt": NOW})


def add_files(args):
    """AddFiles(id, parentID, paths, policy) is one transaction: it runs
    for a few seconds, ticking by bytes, and the records appear at its
    commit - a cancel publishes nothing (APP.md 2.3)."""
    _, parent, paths = (list(args) + ["", ROOT_ID, []])[:3]
    bad = a_dir(parent)
    if bad:
        return bad
    paths = list(paths or [])
    if not paths:
        return Err("params")

    def commit():
        for p in paths:
            add_one(parent, p, False)

    return start_op("add", ADDED_SIZE * len(paths), commit, items=len(paths))


def add_folder(args):
    _, parent, path = (list(args) + ["", ROOT_ID, ""])[:3]
    bad = a_dir(parent)
    if bad:
        return bad
    return start_op("add", ADDED_SIZE, lambda: add_one(parent, path, True), items=1)


def replace_file(args):
    """Replace(id, fileID, path): the in-place edit, cancellable like an
    add."""
    _, rid, _path = (list(args) + ["", "", ""])[:3]
    if record(rid) is None:
        return Err("file.not_found")
    return start_op("replace", ADDED_SIZE, lambda: None, items=1)


# The four policies of APP.md 3 (ruled 2026-09-10). Replace is the default
# when none is given - the wizard default of every archiver - and anything
# else is params: the old code treated an unknown word as skip, which would
# now silently mean "not replace".
EXTRACT_POLICIES = ("replace", "skip", "rename", "ask")


def extract_plan(ids):
    """The records an Extract would write, parents first: each selected
    record, everything live beneath a selected directory and the ancestors
    of all of them, the all-zero id meaning the whole archive. An empty
    plan is file.not_found."""
    if ROOT_ID in (ids or []):
        want = {r["id"] for r in state["records"]}
    else:
        want = set()
        for rid in ids or []:
            r = record(rid)
            if r is None:
                continue
            want |= {rid} | set(subtree(rid))
            at = r["parentId"]
            while at != ROOT_ID and record(at) is not None:
                want.add(at)
                at = record(at)["parentId"]
    out = []

    def walk(pid):
        for c in children(pid):
            if c["id"] in want:
                out.append(c)
                if c["isDir"]:
                    walk(c["id"])
    walk(ROOT_ID)
    return out


def under(dest, p):
    """Where a record lands: the destination plus its path in the archive,
    which is what the core joins (APP.md 3)."""
    return dest.rstrip("/\\") + "\\" + p.replace("/", "\\")


def valid_name(name):
    """R20 as far as the mock judges it: one non-empty element, no slash
    or other forbidden character, no trailing space or dot, at most 255
    code units."""
    if not name or name in (".", "..") or len(name.encode("utf-16-le")) // 2 > 255:
        return False
    if any(c in '\\/:*?"<>|' or ord(c) < 32 for c in name):
        return False
    return name[-1] not in " ."


def extract(args):
    """Extract(id, recordIDs, dir, policy, names): the all-zero id among
    the records is the whole archive; an empty list is params, never
    everything. Nothing is recorded about the destination - the dialog is
    prefilled from the archive's own folder, and no folder is kept from the
    last time (ruled 2026-09-10). `ask` extracts everything that collides
    with nothing and reports each collision as a `conflict` outcome
    carrying the existing file's size and date, for the page to ask about
    and re-issue. `names` maps a record id to the one path element it is
    written under in this extract only - what Shorten and Rename... send
    after a name_refused - a directory's new name carrying its subtree; a
    name that breaks R20, or an id that is not a record's, is params. The
    destination's refusals (state["refuse"]) are per record: a leaf too
    long is name_refused, a path too long path_refused - a folder's once,
    with nothing beneath it - and the rest is written."""
    ids, dest, policy, names = (list(args) + ["", [], "", "", None])[1:5]
    if not ids:
        return Err("params")
    policy = policy or "replace"
    if policy not in EXTRACT_POLICIES:
        return Err("params")
    names = dict(names or {})
    for k, v in names.items():
        if k == ROOT_ID or record(k) is None or not valid_name(v):
            return Err("params")
    plan = extract_plan(ids)
    files = [r for r in plan if not r["isDir"]]
    if not files:
        return Err("file.not_found")
    total = sum(r["size"] for r in files) or ADDED_SIZE
    here = state["inDestination"]
    limits = state["refuse"]

    def out_path(r):
        """The path written under the destination: the archive path with
        every renamed element - the record's own, or an ancestor's -
        replaced for this extract."""
        parts, at = [], r
        while at is not None and at["id"] != ROOT_ID:
            parts.append(names.get(at["id"], at["name"]))
            at = record(at["parentId"])
        return "/".join(reversed(parts))

    def commit():
        out = []
        refused_dirs = set()
        for r in plan:
            p = path_of(r)
            rel = out_path(r)
            full = under(dest, rel)
            # A folder the destination refused takes its subtree with it:
            # reported once, for its top (APP.md 3).
            at = r["parentId"]
            skip = False
            while at != ROOT_ID:
                if at in refused_dirs:
                    skip = True
                    break
                at = record(at)["parentId"] if record(at) else ROOT_ID
            if skip:
                continue
            # Every extract outcome carries the record's id and the archive
            # copy's size and date (APP.md 3, ruled 2026-09-10): the page
            # walks nothing for its compare list or its re-issue.
            res = {"path": full, "name": p, "isDir": r["isDir"], "outcome": "created" if r["isDir"] else "extracted",
                   "id": r["id"], "size": 0 if r["isDir"] else r["size"], "modifiedAt": r["modifiedAt"]}
            leaf = rel.split("/")[-1]
            if r["isDir"]:
                if len(full) > limits["pathOver"]:
                    res["outcome"], res["code"] = "path_refused", "file.path_refused"
                    refused_dirs.add(r["id"])
                out.append(res)
                continue
            if len(full) - len(leaf) > limits["pathOver"]:
                # The temporary in that folder is what the volume refuses:
                # the path, and no name helps.
                res["outcome"], res["code"] = "path_refused", "file.path_refused"
            elif len(leaf) > limits["nameOver"]:
                res["outcome"], res["code"] = "name_refused", "file.name_refused"
            else:
                existing = here.get(rel)
                if existing is not None:
                    if policy == "skip":
                        res["outcome"] = "skipped"
                    elif policy == "ask":
                        res["outcome"] = "conflict"
                        res["existing"] = dict(existing)
                    elif policy == "rename":
                        res["path"] = full + " (2)"
                    else:
                        here.pop(rel, None)  # replaced: the destination holds ours now
            out.append(res)
        return out

    return start_op("extract", total, commit, items=len(files), policy=policy, destination=dest)


# ---- the drag out of the window (APP.md 3, ruled 2026-09-10/11) --------
#
# The staged route: one native drag per gesture, the staging folder under
# %LOCALAPPDATA%\Enfold\drag, the files written by the drop's own request
# after the button's release - the strip's byte bar - and then that same bar
# standing full while the target finishes the drop. The drop is synchronous
# (APP.md 3, ruled 2026-09-11: the data object offers no
# IDataObjectAsyncCapability, so the target has to finish inside Drop),
# which here means Shell.DragOut does not answer until the whole thing is
# over, and the operation ends when it does. The mock has no OLE and no
# Explorer, so the phases run on a clock.

DRAG_ROOT = "C:\\Users\\me\\AppData\\Local\\Enfold\\drag\\"
DRAG_HOVER = 1.0       # the hover, before the fake release
DRAG_PREPARING = 3.0   # the drop's request running the extraction
DRAG_AWAITING = 2.5    # the target's own copy, inside its Drop
DRAG_TICK = 0.1        # how often a waiting phase looks for a cancel

# One native drag at a time (APP.md 3 "One gesture"): a second DragOut is
# drag.busy while DoDragDrop would still be running, which the synchronous
# drop makes the whole of the drag - the call returns when the target has
# finished (the Go core's Begin/Run contract).
drag_running = threading.Lock()


def drag_plan(ids):
    """The records a drag stages, each with its path under the items
    folder: every selected record at the top under its own name, wherever
    it sits in the tree, and a selected directory's whole subtree beneath
    it - never the ancestors up to the root, which an extract writes and a
    drag does not (APP.md 3, dragout.go). The walk goes from the root
    through every folder, selected or not, so a selection inside an
    unselected parent is staged too (the outside review's finding 10 on the
    mock: it was skipped, and the plan came out empty)."""
    want = set(ids)
    out = []

    def walk(pid, under):
        for c in children(pid):
            if under is not None:
                rel = under + "\\" + c["name"]
            elif c["id"] in want:
                rel = c["name"]
            else:
                rel = None
            if rel is not None:
                out.append((c, rel))
            if c["isDir"]:
                walk(c["id"], rel)
    walk(ROOT_ID, None)
    return out


def drag_out(args):
    """Shell.DragOut(archiveID, recordIDs): blocks until DoDragDrop would
    return, which under the synchronous drop is when the target has
    finished - at once for a self-drop - and answers the DragOutResult. The
    dragout operation it registers is the strip's: "dragging" during the
    hover, "preparing" by bytes, "awaiting" with the bar full while the
    target copies, and op.done with dragResult saying how it ended -
    moved, copied, cancelled, refused, self_drop or failed. DragItems is
    what the gesture named, folders included, which is what the strip
    counts ("Extracting 2 items"); Items is the files the extraction
    writes, which is what the bar measures. An empty selection or the root
    is params, an id that is not live file.not_found, two records that
    would take one name at the top file.exists, and a second drag while the
    first has not returned drag.busy."""
    archive_id, ids = (list(args) + ["", []])[:2]
    if state.get("mounted") != archive_id:
        return Err("archive.not_open")
    ids = ids or []
    if not ids or ROOT_ID in ids:
        return Err("params")
    if not drag_running.acquire(blocking=False):
        return Err("drag.busy")
    try:
        return run_drag(archive_id, ids)
    finally:
        drag_running.release()


def run_drag(archive_id, ids):
    """The drag itself, under the running lock: what drag_out answers."""
    tops = []
    for rid in ids:
        r = record(rid)
        if r is None:
            return Err("file.not_found")
        tops.append(r["name"].lower())
    if len(set(tops)) != len(tops):
        return Err("file.exists")
    plan = drag_plan(ids)
    files = [r for r, _ in plan if not r["isDir"]]
    total = sum(r["size"] for r in files)
    folder = DRAG_ROOT + secrets.token_hex(4)
    self_drop = bool(state["drag"].get("selfDrop"))
    fail = state["drag"].get("fail") or ""
    skip = bool(state["drag"].get("skip"))

    state["nextOp"] = state.get("nextOp", 0) + 1
    op_id = "op%d" % state["nextOp"]
    op = {"id": op_id, "kind": "dragout", "archiveId": archive_id, "done": 0, "total": 0,
          "items": len(files), "dragItems": len(ids), "phase": "dragging",
          "startedAt": int(time.time()), "finished": False,
          "results": [], "returned": 0}
    state["ops"][op_id] = op
    emit("op.progress", op_view(op))

    def outcome(planned, code=""):
        # The path is the drag's folder, then items, then the record's
        # place beneath a selected top (dragout.Result.Folder).
        r, rel = planned
        res = {"path": folder + "\\items\\" + rel, "name": path_of(r), "isDir": r["isDir"],
               "outcome": "created" if r["isDir"] else "extracted",
               "id": r["id"], "size": 0 if r["isDir"] else r["size"], "modifiedAt": r["modifiedAt"]}
        if code:
            res["outcome"], res["code"] = "failed", code
        return res

    returned = threading.Event()
    result = {"selfDrop": False, "extracted": False, "effect": 0, "folder": folder, "opId": op_id}

    def end(reason, error="", results=None):
        op["finished"] = True
        op["dragResult"] = reason
        if error:
            op["error"] = error
        if results is not None:
            op["results"] = results
        emit("op.done", op_view(op))
        returned.set()
        finish_drain(archive_id)

    def run():
        time.sleep(DRAG_HOVER)
        if op.get("cancelled"):
            return end("cancelled", "op.cancelled")
        if self_drop:
            # The button came up over Enfold's own window: nothing is
            # extracted, the folder goes at once, and the page performs
            # the Move (APP.md 3).
            result["selfDrop"] = True
            return end("self_drop")
        op["phase"], op["total"] = "preparing", total
        emit("op.progress", op_view(op))
        steps = 24
        for i in range(1, steps + 1):
            time.sleep(DRAG_PREPARING / steps)
            if op.get("cancelled"):
                # The cancel fails the drop's request and Explorer
                # abandons the drop.
                return end("cancelled", "op.cancelled")
            if fail and i == steps // 2:
                # A record could not be written: the request fails rather
                # than hand out half-written files, and the drag ends
                # failed with the extraction's code - refused is a target
                # that let the data object go without ever asking.
                out = [outcome(pl) for pl in plan[:-1]] + [outcome(plan[-1], fail)] if plan else []
                return end("failed", fail, out)
            op["done"] = total * i // steps
            emit("op.progress", op_view(op))
        # The staging is done and the target has the paths: the bar stays
        # where it is, full, with nothing to cancel, while the target
        # finishes the drop inside its own Drop (APP.md 3). DoDragDrop has
        # not returned yet, so neither has the call.
        op["phase"], op["done"] = "awaiting", total
        emit("op.progress", op_view(op))
        result["extracted"] = True
        # A cancel or a Close while awaiting ends the operation there and
        # then as cancelled, like the core: nothing of the drag is left to
        # end - the files are Explorer's - and the strip must not linger
        # behind a page that has closed (dragout.go wait).
        waited = 0.0
        while waited < DRAG_AWAITING:
            time.sleep(DRAG_TICK)
            waited += DRAG_TICK
            if op.get("cancelled"):
                return end("cancelled", "op.cancelled")
        if skip:
            # The user answered Skip to the target's conflict dialog, or
            # cancelled it: DoDragDrop comes back with DROPEFFECT_NONE and
            # the drop is over - cancelled, and the strip goes (APP.md 3,
            # ruled 2026-09-11 after the drag that hung on exactly this).
            return end("cancelled")
        result["effect"] = 2  # DROPEFFECT_MOVE: a same-volume drop is one rename
        end("moved", results=[outcome(pl) for pl in plan])

    threading.Thread(target=run, daemon=True).start()
    returned.wait()
    return result


# ---- an archive's lifetime (APP.md 2.3, ruled 2026-09-10) --------------
#
# An open archive has no timeout of its own. Leaving its page closes it at
# once - Archives.Leave - or, while one of its own operations still runs,
# when that operation ends (draining: nothing new is admitted meanwhile);
# *Close archive* is the kill switch, which closes it whatever is reading it
# and cancels the operation. The Archives page's own operations never ask
# for it to be opened first: they open it themselves, run, and close it
# again.

def open_count():
    return len([a for a in state["archives"] if a.get("open")])


def mounted_only(method):
    """Every Archive-service method is the mounted page's (APP.md 2.3):
    an archive that is not mounted - never opened, left, closed, draining -
    answers archive.not_open, the token notwithstanding. CancelOp and Op
    are not wrapped: they stay allowed."""
    def guarded(args):
        if not args or state.get("mounted") != args[0]:
            return Err("archive.not_open")
        return method(args)
    guarded.__name__ = getattr(method, "__name__", "method")
    return guarded


def preview_url(args):
    """PreviewURL(id, fileID): the loopback URL the mock itself serves, under
    the token minted at Open (APP.md 4)."""
    return "http://127.0.0.1:%d/p/%s/%s" % (PORT, state["token"], (list(args) + ["", ""])[1])


def open_archive(args):
    """Archives.Open(id): the page has it now. It needs the session
    (APP.md 2.3, "What stays usable after a lock") - unless this page
    already has it, in which case the lock preserved it and Open answers
    Stat as before. A token is minted for the previews; any earlier one is
    forgotten with the archive it belonged to."""
    a = archive(args[0]) if args else None
    if a is None:
        return Err("archive.not_found")
    if state["vault"]["state"] != "unlocked" and state.get("mounted") != a["id"]:
        return Err("vault.needs_unlock")
    if state.get("mounted") != a["id"]:
        state["mounted"] = a["id"]
        state["token"] = secrets.token_hex(16)
    a["open"] = True
    a["draining"] = False
    state["vault"]["openArchives"] = open_count()
    return stat()


def leave_archive(args):
    """Archives.Leave(id): the page left it. Nothing new is admitted from
    this moment - the methods answer archive.not_open and the preview URL
    404 - and it closes at once, its keys gone, unless one of its own
    operations still runs, in which case it is DRAINING: the row keeps
    open: true until that operation ends, then archives.changed says
    open: false (APP.md 2.3). An archive that is not open answers nil: it
    had already been left."""
    a = archive(args[0]) if args else None
    if a is None:
        return None
    if state.get("mounted") == a["id"]:
        state["mounted"] = None
        state["token"] = ""
    if a.get("open") and running_ops(a["id"]):
        a["draining"] = True
    else:
        a["draining"] = False
        a["open"] = False
    state["vault"]["openArchives"] = open_count()
    return None


def close_archive(args):
    """Archives.Close(id): the kill switch. It closes now, readers or not,
    dropping every in-flight body and cancelling an operation of its own
    that is running - the op ends with op.cancelled - and never waits
    behind one (APP.md 2.3)."""
    a = archive(args[0]) if args else None
    if a is None:
        return None
    for o in running_ops(a["id"]):
        o["cancelled"] = True
    if state.get("mounted") == a["id"]:
        state["mounted"] = None
        state["token"] = ""
    a["draining"] = False
    a["open"] = False
    state["vault"]["openArchives"] = open_count()
    return None


def record_op(kind, args, total, seconds=4.0, phase=""):
    """A record-level operation from the Archives page - Verify, Compact,
    Rotate key. It runs whether or not the archive's page is open: while
    the vault is unlocked the core unwraps the key with the session's KWK,
    opens the file for the operation and closes it again (APP.md 2.3, ruled
    2026-09-10). An archive left closed stays closed."""
    a = archive(args[0]) if args else None
    if a is None:
        return Err("archive.not_found")
    if state["vault"]["state"] != "unlocked":
        return Err("vault.needs_unlock")

    def commit():
        if kind == "rotate":
            a["keyVersion"] += 1
        if kind == "compact":
            if a["id"] == state["stat"]["id"]:
                st = state["stat"]
                st["size"] = max(0, st["size"] - st["freeSpace"])
                st["freeSpace"] = 0
            a["freeSpace"] = 0
        if kind == "verify":
            a["hashBehind"] = 0
        return None

    return start_op(kind, total, commit, seconds=seconds, phase=phase, archive_id=a["id"])


def vault_status(args=None):
    """Status carries Ops so a window recreated mid-operation recovers the
    progress it was showing (APP.md 2.4). Named, not a lambda, so that
    POST /mock/state {"failNext": {"vault_status": "internal"}} can refuse
    the first Status once - the boot's one plain line with Retry (2.4)."""
    v = dict(state["vault"])
    v["ops"] = [op_view(o) for o in state["ops"].values() if not o["finished"]]
    v["openArchives"] = state["vault"]["openArchives"]
    return v


def create_archive(args):
    """Archives.Create(path, name, method) since the ruling of 2026-09-08:
    the compression method is the record's, chosen once and obeyed by every
    later writer (FORMAT.md 7.1). A path that already holds a file is
    refused with archive.exists, as the core's O_EXCL create is - Enfold
    never overwrites a file it did not make."""
    path, name, method = (list(args) + ["", "", "normal"])[:3]
    if any(a["path"] == path for a in state["archives"]):
        return Err("archive.exists")
    a = {
        "id": ("%02x" % (len(state["archives"]) + 16)) * 16, "name": name or "New archive",
        "path": path or "D:/Archives/new.efd", "storedSize": 0, "lastWrittenAt": NOW,
        "keyVersion": 1, "open": False, "receiptOwed": False,
        "method": method or "normal", "hidden": False, "hashBehind": 0, "files": 0,
        "freeSpace": 0, "description": "", "forgottenAt": 0,
    }
    state["archives"].append(a)
    return a["id"]


def listed(show_hidden):
    return [a for a in state["archives"] if show_hidden or (not a["hidden"] and not a.get("forgottenAt"))]


def close_decided(args):
    """Shell.CloseDecided(action, remember): the close question's answer
    (APP.md 2.4). The service takes "tray" or "quit" and nothing else; with
    remember the answer is written to closeAction through the core's own
    settings, which here is this dict. No window closes and nothing quits -
    the answer is recorded in state["closeDecided"] so the pane can be
    checked against what the shell would have been told."""
    action, remember = (list(args) + ["", False])[:2]
    if action not in ("tray", "quit"):
        return Err("params")
    if remember:
        state["settings"]["closeAction"] = action
    state["closeDecided"].append({"action": action, "remember": bool(remember)})
    return None


def handle(method_id, args):
    v = state["vault"]
    m = METHODS.get(method_id)
    if m is None:
        return None
    # A refusal staged from outside, for the paths a happy mock cannot
    # reach: POST /mock/state {"failNext": {"page": "archive.compacting"}}
    # makes the next call of that method answer that code once. Mock
    # scaffolding; the core has nothing of the kind.
    pending = state.get("failNext") or {}
    name = getattr(m, "__name__", "")
    if name in pending:
        return Err(pending.pop(name))
    return m(args)

METHODS = {
    3940765069: vault_status,                                   # vault.Status
    3956196437: lambda a: [{"name": "Yubico YubiKey OTP+FIDO+CCID 0"}],
    1779776360: lambda a: None,                                 # BeginUnlock
    3770426637: lambda a: None, 951839700: lambda a: None, 4094929714: lambda a: None, 2320277474: lambda a: None,
    2953146167: lambda a: None, 882388909: lambda a: None,
    4176692468: lambda a: listed(bool(a and a[0])),             # archives.List
    923201420: open_archive,                                    # archives.Open
    660127987: leave_archive,                                   # archives.Leave (the page left)
    1388822288: close_archive,                                  # archives.Close (the kill switch)
    2300343171: lambda a: [], 2169725132: lambda a: None, 474530495: lambda a: None,
    422512670: lambda a: None, 1162996984: create_archive,
    # Verify, Compact and Rotate key run on a closed archive: the core
    # opens it for the operation and closes it again (APP.md 2.3).
    2111968017: lambda a: record_op("compact", a, 8_000_000_000, seconds=6.0, phase="compacting"),
    3689812034: lambda a: record_op("rotate", a, 1_000_000),
    3359801409: lambda a: record_op("verify", a, 4_000_000_000, seconds=5.0),
    # The archive's tree, and its operations. Save, Discard and KeepOpen
    # went on 2026-09-09: each operation is its own transaction, committed
    # at its end, and CancelOp aborts a running add or replace.
    # Each of these is the mounted page's: an archive that is not mounted
    # - left, closed, draining - answers archive.not_open (APP.md 2.3).
    2601627082: mounted_only(page),                             # archive.Page
    1305430966: mounted_only(children_ids),                     # archive.Children
    2565212395: mounted_only(lambda a: stat()),                 # archive.Stat
    3241529081: mounted_only(create_folder),                    # archive.CreateFolder
    191579688: mounted_only(move),                              # archive.Move
    2769288047: mounted_only(add_files), 3008447636: mounted_only(add_folder),
    3231340199: mounted_only(replace_file), 3839303214: mounted_only(delete_records),
    3028646727: mounted_only(rename_record), 585645538: mounted_only(extract),
    913354260: mounted_only(preview_url), 723007364: mounted_only(lambda a: state["text"]),
    1850767145: mounted_only(check_names),
    2446376312: cancel_op,                                      # archive.CancelOp (allowed while draining)
    448053830: lambda a: op_view(state["ops"].get(a[0] if a else "", {"id": "", "kind": "add", "archiveId": "", "done": 0, "total": 0, "items": 0, "phase": "", "startedAt": NOW, "finished": True})),
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
    3606391931: lambda a: None, 3229291943: lambda a: ["D:/Pictures/a.jpg", "D:/Pictures/b.jpg"], 2529646972: lambda a: "D:/Pictures", 2079207478: lambda a: None,
    842300112: lambda a: None, 1923582270: lambda a: "D:/new.efd", 3130426784: lambda a: None,
    945813141: drag_out,                                        # shell.DragOut (APP.md 3)
    2169987085: close_decided,                                  # shell.CloseDecided (APP.md 2.4)
}

class H(SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=DIST, **k)

    def log_message(self, *a):
        pass

    def do_GET(self):
        # What the core would have emitted, drained by the page (see the
        # module docstring for the one line that dispatches them).
        if self.path.startswith("/mock/events"):
            queued, state["events"] = state["events"], []
            return self.reply(queued)
        # The preview transport (APP.md 4): a body under the token minted
        # at Open, and 404 for anything else - a token that was forgotten
        # at Leave or Close, or one the mount does not know - so the page's
        # <img> shows what the core would: nothing, once the page is left.
        if self.path.startswith("/p/"):
            parts = self.path.split("/")
            token = parts[2] if len(parts) > 2 else ""
            if not state.get("mounted") or not token or token != state["token"]:
                self.send_response(404)
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            data = (b'<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400" viewBox="0 0 640 400">'
                    b'<rect width="640" height="400" fill="#c9d9d5"/>'
                    b'<circle cx="480" cy="120" r="48" fill="#f2efe4"/>'
                    b'<path d="M0 400 L200 190 L330 320 L420 240 L640 400 Z" fill="#4c7c72"/></svg>')
            self.send_response(200)
            self.send_header("Content-Type", "image/svg+xml")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
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
        if self.path.startswith("/mock/preview"):
            name = body.get("name", "")
            if name in PREVIEW_OPS:
                return self.reply({"ok": True, "op": PREVIEW_OPS[name]()})
            over = PREVIEWS.get(name)
            if over is None:
                return self.reply({"ok": False, "names": sorted(list(PREVIEWS) + list(PREVIEW_OPS))})
            state["vault"].update(over)
            return self.reply({"ok": True})
        if self.path.startswith("/mock/state"):
            for k, v in body.items():
                if isinstance(state.get(k), dict) and isinstance(v, dict):
                    state[k].update(v)
                else:
                    state[k] = v
            return self.reply({"ok": True})
        args = body.get("args") or {}
        if isinstance(args, dict) and "methodID" in args:
            out = handle(args["methodID"], args.get("args") or [])
            if isinstance(out, Err):
                return self.refuse(out.code)
            return self.reply(out)
        return self.reply({})

    def refuse(self, code):
        data = json.dumps({"kind": "Error", "message": code, "cause": {"code": code}}).encode()
        self.send_response(500)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

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
    PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8125
    ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
