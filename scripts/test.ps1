# Runs the test suite with symbols stripped from the test binaries.
#
# Why: Kaspersky's cloud heuristics (KSN, "VHO:Trojan-Downloader.Win32.Convagent.gen") flag
# unstripped Go test binaries and delete them before they can run - a documented Go false
# positive triggered by the symbol table and DWARF sections, not by anything the tests do.
# Stripping (-s -w) avoids the heuristic. This is a build flag, not a security setting; no
# AV exclusion is required. Drop the flag only when you need to attach a debugger to a test.
$env:CGO_ENABLED = "0"
go test -ldflags="-s -w" @args ./...
