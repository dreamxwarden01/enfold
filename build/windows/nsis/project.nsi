Unicode true

####
## Enfold's installer. Its semantics are ruled in docs/APP.md 14 "Install,
## upgrade, uninstall" (reasoning: docs/DECISIONS.md, 2026-09-21): one
## installer, per user, no elevation; Welcome, the files, Finish; no directory
## page, no licence page, no desktop shortcut; the WebView2 bootstrapper when
## the runtime is absent, and a stop when it fails; never a kill of a running
## Enfold; .efd registered for the user; the uninstaller removes the program,
## the shortcut, the association, the entry and the WebView2 profile, and
## nothing else under %LOCALAPPDATA%\Enfold.
####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
##
## Ordinarily the installer is built from the repo root with
## > wails3 task windows:package
## which builds bin\enfold.exe, fetches Microsoft's WebView2 bootstrapper into
## this folder and runs makensis with the scope defines. To run makensis by
## hand instead, build first and then:
## > makensis -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user -DARG_WAILS_AMD64_BINARY=..\..\..\bin\enfold.exe project.nsi
## (both -D are optional: this file defaults the scope to "user" below.)
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "enfold"
## !define INFO_COMPANYNAME    "My Company" # Default "dreamxwarden01"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "Enfold"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "0.1.0" - set in build/config.yml, which the release bumps
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "(c) 2026, dreamxwarden01"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####

####
## Per-user scope, no UAC prompt (APP.md 14, decision 1). build/windows/Taskfile.yml
## passes -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user; this guard makes a
## makensis run without them agree instead of falling back to the template's "machine".
## REQUEST_EXECUTION_LEVEL is deliberately left to wails_tools.nsh, which derives "user"
## from the scope, so overriding the scope alone keeps the two consistent.
####
!ifndef WAILS_INSTALL_SCOPE
    !define WAILS_INSTALL_SCOPE "user"
!endif

####
## Include the wails tools
####
!include "wails_tools.nsh"

####
## ENFOLD'S OWN MACROS - and why they are here rather than in wails_tools.nsh.
##
## wails_tools.nsh is GENERATED: `wails3 task common:update:build-assets`
## rewrites it from build/config.yml, so a correction made there is silently
## undone by the next regeneration. The four macros below are copies of the
## template's with the bugs fixed, and this file is never regenerated. The
## template's originals are left where they are and simply not inserted.
##
## build/config.yml's `fileAssociations:` entry stays the one declaration of
## the association - ENFOLD_EXT and ENFOLD_PROGID below must match its `ext`
## and `name`, and the strings must match its `description`.
####
!define ENFOLD_EXT        "efd"
!define ENFOLD_PROGID     "Enfold.efd"
!define ENFOLD_TYPENAME   "Enfold archive"
!define ENFOLD_WV2_CLIENT "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

####
## Is a suitable WebView2 Evergreen runtime installed? Sets ${OUT} to "1".
##
## The template's macro treats any non-empty `pv` as installed. Microsoft is
## explicit that it is not ("Detect if a WebView2 Runtime is already
## installed"): "at least one of these regkeys must be present and defined
## with a version greater than 0.0.0.0. If neither regkey exists, or if only
## one of these regkeys exists but its value is null, an empty string, or
## 0.0.0.0, this means that the WebView2 Runtime isn't installed on the
## client." 0.0.0.0 is what EdgeUpdate leaves behind when the runtime has been
## removed, which is exactly the machine that needs the bootstrapper.
##
## SetRegView 64 because makensis builds a 32-bit installer and these keys are
## read from the 64-bit view; the HKLM path names WOW6432Node itself, as
## Microsoft's 64-bit Windows listing does.
####
!macro enfold.webview2.detect OUT
    SetRegView 64
    StrCpy ${OUT} "0"
    ReadRegStr $R2 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\${ENFOLD_WV2_CLIENT}" "pv"
    ${If} $R2 != ""
    ${AndIf} $R2 != "0.0.0.0"
        StrCpy ${OUT} "1"
    ${Else}
        ReadRegStr $R2 HKCU "Software\Microsoft\EdgeUpdate\Clients\${ENFOLD_WV2_CLIENT}" "pv"
        ${If} $R2 != ""
        ${AndIf} $R2 != "0.0.0.0"
            StrCpy ${OUT} "1"
        ${EndIf}
    ${EndIf}
!macroend

####
## The runtime (APP.md 14, "WebView2"; ruled again 2026-09-30 after the first
## clean-machine test, a Windows 10 guest with Microsoft Edge installed and
## the runtime removed):
##
##   1. Absent: Microsoft's bootstrapper, silently, AS THE USER - a per-user
##      runtime, no prompt. Its exit code goes into the details.
##   2. Still absent: that is Edge's machine-level updater taking the install
##      over - Microsoft, "Installing the Runtime as per-machine or per-user":
##      "a per-user install is automatically replaced by a per-machine
##      install, if a per-machine Microsoft Edge Updater is in place" - and a
##      per-machine install needs the machine's rights. So ASK, and on Yes run
##      the same bootstrapper elevated ("runas": the one UAC prompt this
##      install can show, and it is Microsoft's signed installer's, not ours).
##   3. Present after either run: on to the files.
##   4. No, or still absent: a STOP. The case goes into $EnfoldStop (and the
##      code, when there is one, into $EnfoldStopCode); the section writes
##      nothing and lets the wizard run on to the finish page, which
##      enfold.FinishPre turns into "Enfold was not installed" and the reason.
##      The details keep a one-line trail for the log.
##
## What each run can report, from NSIS's own source (Source/exehead/exec.c):
## ExecWait (EW_EXECUTE) stores the exit code in its variable and sets the
## error flag only when the process could not be started. ExecShellWait
## (EW_SHELLEXEC with SEE_MASK_NOCLOSEPROCESS) calls ShellExecuteEx: when that
## FAILS it sets the error flag - a declined UAC prompt, or an account that
## could not supply an administrator's credentials, is exactly such a failure
## (ERROR_CANCELLED) - and when it SUCCEEDS it waits for the process and
## DISCARDS the exit code. No variable, $0 included, carries anything. So the
## elevated run reports "did not start" or "ran", never a code; the one code
## there is to show is the run as the user's.
##
## ${U+2014} is an em dash: makensis reads this file in the ANSI code page, so
## the source stays ASCII.
##
## Inserted exactly once (in the install section), so plain labels are safe.
####
!define ENFOLD_WV2_URL   "https://developer.microsoft.com/microsoft-edge/webview2/"
!define ENFOLD_WV2_SETUP "$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe"

!macro enfold.webview2runtime
    !insertmacro enfold.webview2.detect $R3
    StrCmp $R3 "1" enfold_wv2_present

    SetDetailsPrint both
    DetailPrint "Installing: WebView2 Runtime"
    SetDetailsPrint listonly

    InitPluginsDir
    CreateDirectory "$pluginsdir\webview2bootstrapper"
    SetOutPath "$pluginsdir\webview2bootstrapper"
    File "MicrosoftEdgeWebview2Setup.exe"

    ; 1. As the user.
    ClearErrors
    ExecWait '"${ENFOLD_WV2_SETUP}" /silent /install' $R4
    ${If} ${Errors}
        ; The variable is undefined when the process did not start (NSIS
        ; manual, ExecWait), so it is not shown as a code.
        StrCpy $R5 ""
        DetailPrint "WebView2 bootstrapper (as user) did not start."
    ${Else}
        ; ExecWait writes the code as a signed decimal (exec.c EW_EXECUTE:
        ; myitoa = util.c iptrtostr, wsprintf "%d"), so an HRESULT such as
        ; 0x80040C01 would read -2147218431. Hexadecimal matches Microsoft's
        ; documented codes: IntFmt is wsprintf(format, (UINT)value) (exec.c
        ; EW_INTFMT), so "0x%08X" prints the 32-bit value unsigned.
        IntFmt $R5 "0x%08X" $R4
        DetailPrint "WebView2 bootstrapper (as user) exit code: $R5"
    ${EndIf}

    !insertmacro enfold.webview2.detect $R3
    StrCmp $R3 "1" enfold_wv2_installed

    ; 2. Ask. MessageBox is Win32's MessageBoxW, whose button sets are fixed:
    ;    plain NSIS cannot relabel Yes/No as Install/Cancel.
    MessageBox MB_YESNO|MB_ICONQUESTION "Enfold needs the Microsoft Edge WebView2 Runtime. Install it now? Windows will ask for permission ${U+2014} it is Microsoft's installer." IDYES enfold_wv2_elevate

    ; (c) No.
    DetailPrint "Stopped: the WebView2 Runtime was not installed (No was chosen)."
    StrCpy $EnfoldStop "wv2-no"
    Goto enfold_wv2_stop

enfold_wv2_elevate:
    SetDetailsPrint both
    DetailPrint "Installing: WebView2 Runtime (elevated)"
    SetDetailsPrint listonly
    ClearErrors
    ExecShellWait "runas" "${ENFOLD_WV2_SETUP}" "/silent /install" SW_SHOWNORMAL
    IfErrors enfold_wv2_notlaunched
    DetailPrint "WebView2 bootstrapper (elevated) ran; Windows does not report its exit code to this installer."

    !insertmacro enfold.webview2.detect $R3
    StrCmp $R3 "1" enfold_wv2_installed

    ; (b) It ran, and the runtime is still absent. The code kept is the run as
    ;     the user's - the only one there is (see above); "" if that run did
    ;     not start.
    DetailPrint "Stopped: the WebView2 Runtime is still absent after the elevated run."
    StrCpy $EnfoldStop "wv2-failed"
    StrCpy $EnfoldStopCode $R5
    Goto enfold_wv2_stop

enfold_wv2_notlaunched:
    ; (a) The elevated run never started: the permission was declined, or the
    ;     account could not supply an administrator's credentials.
    DetailPrint "Stopped: Windows did not allow the elevated run (the permission was declined)."
    StrCpy $EnfoldStop "wv2-notallowed"

enfold_wv2_stop:
    SetDetailsPrint both
    Goto enfold_wv2_present

enfold_wv2_installed:
    SetDetailsPrint both
    DetailPrint "WebView2 Runtime installed."
enfold_wv2_present:
!macroend

####
## Is Enfold running? (APP.md 14: the installer never kills the process.)
##
## The executable of a running process cannot be opened for writing, so that is
## the test - but "cannot be opened for writing" is two different answers, and
## NSIS's FileOpen only sets the error flag, which cannot tell them apart. A
## stopped enfold.exe whose ACL refuses writing would loop "Enfold is running"
## for ever. So: CreateFileW through the System plug-in with the "?e" option,
## which per System.html means "Call GetLastError() after procedure end and
## push result on stack" - read inside the plug-in, immediately after the call,
## with nothing in between for NSIS to clobber. (A separate GetLastError call
## after FileOpen has no such guarantee, which is why it is not used.)
##
##   32 ERROR_SHARING_VIOLATION - a live process holds the image: Retry.
##    5 ERROR_ACCESS_DENIED     - the folder is not writable: stop and say so.
##
## OPEN_EXISTING neither creates nor truncates, so nothing is damaged whatever
## the answer, and on a fresh install the file is not there at all - which is
## not "running". The handle comes back as "i" (int32) because makensis builds
## a 32-bit installer, so INVALID_HANDLE_VALUE reads exactly "-1".
##
## ID makes the labels unique: this macro is inserted in both sections.
##
## A stop does not Abort: it records the case in $EnfoldStop (the Windows
## error in $EnfoldStopCode) and returns; the section then skips everything
## that writes or removes, and the finish page says why - "Enfold was not
## installed" / "Enfold was not removed" (APP.md 14). The running case keeps
## its Retry/Cancel box, since that is a choice; the permissions case has no
## box of its own any more - the finish page says the same sentence, and
## saying it twice in a row helps nobody.
####
!macro enfold.checkNotRunning ID
enfold_run_${ID}_retry:
    IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 enfold_run_${ID}_ok

    ; GENERIC_WRITE 0x40000000 | share READ|WRITE|DELETE 7 | OPEN_EXISTING 3 |
    ; FILE_ATTRIBUTE_NORMAL 0x80.
    System::Call 'kernel32::CreateFileW(w "$INSTDIR\${PRODUCT_EXECUTABLE}", i0x40000000, i7, p0, i3, i0x80, p0) i.r0 ?e'
    Pop $1
    StrCmp $0 "-1" enfold_run_${ID}_failed 0
    System::Call 'kernel32::CloseHandle(ir0)'
    Goto enfold_run_${ID}_ok

enfold_run_${ID}_failed:
    IntCmp $1 32 0 enfold_run_${ID}_denied enfold_run_${ID}_denied
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "Enfold is running. Quit it from the tray icon, then Retry." IDRETRY enfold_run_${ID}_retry
    DetailPrint "Stopped: Enfold is running, and Cancel was chosen."
    StrCpy $EnfoldStop "running"
    Goto enfold_run_${ID}_ok

enfold_run_${ID}_denied:
    IntCmp $1 5 0 enfold_run_${ID}_other enfold_run_${ID}_other
    DetailPrint "Stopped: $INSTDIR cannot be written (access denied)."
    StrCpy $EnfoldStop "denied"
    Goto enfold_run_${ID}_ok

enfold_run_${ID}_other:
    DetailPrint "Stopped: $INSTDIR cannot be written (Windows error $1)."
    StrCpy $EnfoldStop "error"
    StrCpy $EnfoldStopCode $1

enfold_run_${ID}_ok:
!macroend

####
## The .efd association. Two corrections to the template's APP_ASSOCIATE:
##
## 1. THE BACKUP. The template saves the current default of .efd on EVERY
##    install. The first install saves whatever held .efd; the second saves
##    "Enfold.efd", our own ProgID, over it - so the uninstaller then
##    "restores" the very class it has just deleted and .efd points at
##    nothing. Back up only what belongs to somebody else.
## 2. THE OPEN COMMAND. A per-user install path always contains the user's own
##    name, so it can contain a space: C:\Users\Jane Doe\AppData\Local\
##    Programs\Enfold. Microsoft, "Extending Shortcut Menus": "If any element
##    of the command string contains or might contain spaces, it must be
##    enclosed in quotation marks. Otherwise, if the element contains a space,
##    it will not parse correctly." The template quotes "%1" but leaves the
##    executable bare, so Jane's install would try to run C:\Users\Jane. The
##    DefaultIcon is quoted too and given its index, the documented
##    "path",index form. $\" is NSIS for a literal quotation mark.
####
!macro enfold.associateFiles
    ReadRegStr $R0 SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" ""
    ${If} $R0 != "${ENFOLD_PROGID}"
        WriteRegStr SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" "${ENFOLD_PROGID}_backup" "$R0"
    ${EndIf}

    WriteRegStr SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" "" "${ENFOLD_PROGID}"

    WriteRegStr SHELL_CONTEXT "Software\Classes\${ENFOLD_PROGID}" "" "${ENFOLD_TYPENAME}"
    WriteRegStr SHELL_CONTEXT "Software\Classes\${ENFOLD_PROGID}\DefaultIcon" "" '$\"$INSTDIR\icon.ico$\",0'
    WriteRegStr SHELL_CONTEXT "Software\Classes\${ENFOLD_PROGID}\shell" "" "open"
    WriteRegStr SHELL_CONTEXT "Software\Classes\${ENFOLD_PROGID}\shell\open" "" "Open with ${INFO_PRODUCTNAME}"
    WriteRegStr SHELL_CONTEXT "Software\Classes\${ENFOLD_PROGID}\shell\open\command" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'

    File "..\icon.ico"
!macroend

####
## Giving .efd back. Only when it is still ours: if another program has taken
## it since, that was the user's choice and the uninstaller does not undo it.
####
!macro enfold.unassociateFiles
    ReadRegStr $R0 SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" ""
    ${If} $R0 == "${ENFOLD_PROGID}"
        ReadRegStr $R1 SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" "${ENFOLD_PROGID}_backup"
        ${If} $R1 == ""
        ${OrIf} $R1 == "${ENFOLD_PROGID}"
            ; Nothing held .efd before Enfold - or a version of this installer
            ; that still had bug 1 wrote our own ProgID as the backup. Either
            ; way, leave .efd unclaimed rather than pointing at a class that is
            ; about to be deleted.
            DeleteRegValue SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" ""
        ${Else}
            WriteRegStr SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" "" "$R1"
        ${EndIf}
    ${EndIf}

    DeleteRegValue SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}" "${ENFOLD_PROGID}_backup"
    DeleteRegKey /ifempty SHELL_CONTEXT "Software\Classes\.${ENFOLD_EXT}"
    DeleteRegKey SHELL_CONTEXT "Software\Classes\${ENFOLD_PROGID}"

    Delete "$INSTDIR\icon.ico"
!macroend

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

####
## Pages: Welcome, the files, Finish with "Run Enfold" (APP.md 14, decision 2).
## No directory page: there is one place to install to, and the one path the
## product lets a user choose is the vault's, inside the application. No
## licence page.
##
## NOTE: "MUI.nsh" is Modern UI 1 (NSIS\Include\MUI.nsh includes
## Contrib\Modern UI\System.nsh), as the Wails template has it. Every MUI line
## number cited in this file is System.nsh's.
####
!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
# MUI_FINISHPAGE_RUN gives the finish page its checkbox; its label defaults to
# MUI_TEXT_FINISH_RUN, "&Run $(^NameDA)" - "Run Enfold", Name being the
# product's. MUI unsets the FINISHPAGE defines once a finish page is declared
# (System.nsh 1062-1070), so this one does not leak into the uninstaller's.
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
# A stop before the first file ends on THIS page, rewritten by enfold.FinishPre
# into "Enfold was not installed" (APP.md 14). MUI !undefs the custom-function
# define after one page (System.nsh 1227), so it applies to the installer's
# finish page only.
!define MUI_PAGE_CUSTOMFUNCTION_PRE enfold.FinishPre
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page
####
## The uninstaller's finish page says what was kept (APP.md 14, decision 5).
## MUI_UNPAGE_FINISH (System.nsh 1205) uses the installer's finish-page
## declaration, which reads MUI_FINISHPAGE_TEXT (1023) - there is no
## MUI_UNFINISHPAGE_TEXT. $LOCALAPPDATA expands to this user's folder.
##
## But MUI 1's finish page is an InstallOptions page, and InstallOptions
## DECODES \r \n \t \\ in a label's text (InstallOptions readme, "Escaped
## values"; InstallerOptions.cpp ConvertNewLines). MUI writes a script-supplied
## text into ioSpecial.ini raw (System.nsh 2072-2087 converts only its own
## defaults), so a profile folder that starts with r, n or t - C:\Users\rober,
## as Microsoft-account profiles are often named - would put a carriage
## return, line feed or tab where "\r", "\n" or "\t" was. un.enfold.UnFinishPre
## writes the text again through InstallOptions' own un.Nsis2Io, which doubles
## every backslash.
####
!define ENFOLD_UNFINISH_TEXT "Your vault and settings stay in $LOCALAPPDATA\Enfold. Delete that folder yourself if you mean to."
!define MUI_FINISHPAGE_TEXT "${ENFOLD_UNFINISH_TEXT}"
!define MUI_PAGE_CUSTOMFUNCTION_PRE un.enfold.UnFinishPre
!insertmacro MUI_UNPAGE_FINISH

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## 1.0 ships unsigned (docs/SCOPE.md, ruled 2026-09-13); the release notes say so
## and what SmartScreen shows. The following two statements can be used to sign the
## installer and the uninstaller once there is a certificate. The path to the
## binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

####
## THE STOP (APP.md 14, "WebView2" and "A running Enfold"). A stop before the
## first file does not Abort - an aborted install stands on its progress page,
## which reads as a hang. Instead:
##
##   * the macro that stops records the case in $EnfoldStop ("wv2-no",
##     "wv2-failed", "wv2-notallowed", "running", "denied", "error") and the
##     code, when there is one, in $EnfoldStopCode;
##   * the section skips everything that writes or removes, calls SetAutoClose
##     true - the progress page then moves on by itself (Ui.c: at the page
##     after instfiles, "if (!g_exec_flags.abort && g_exec_flags.autoclose)
##     goto nextPage") - and SetErrorLevel 2, so the process still exits with
##     "aborted by script" (NSIS Appendix D.1; Main.c: a set error level
##     replaces the exit code);
##   * the finish page's PRE function (enfold.FinishPre / un.enfold.UnFinishPre)
##     rewrites the page's InstallOptions fields: the title, the text, the Run
##     box gone, the one button "Close".
##
## "1" once the install section has started writing (set just before
## SetOutPath $INSTDIR). Every stop is recorded before that point; the finish
## page checks it anyway before it says "Nothing was installed". An abort after
## writing - a file error's - is not a stop: it keeps Modern UI's own
## "Installation Aborted" page, which is then the truth.
####
Var EnfoldWritten
Var EnfoldStop
Var EnfoldStopCode

####
## The failure page's words. The text goes through InstallOptions' Nsis2Io
## (INSTALLOPTIONS_WRITE_CONVERT / _UNCONVERT): a label decodes \r \n \t \\
## (see ENFOLD_UNFINISH_TEXT below), and these texts carry $INSTDIR; Nsis2Io
## also turns $\r$\n into the "\r\n" a label shows as a line break. The
## paragraphs follow Modern UI's own finish text ("...installed on your
## computer.$\r$\n$\r$\nClick Finish..."); the address stands on a line of its
## own so the label never breaks it.
####
!define ENFOLD_PARA "$\r$\n$\r$\n"
!define ENFOLD_WV2_NEEDS "Enfold needs the Microsoft Edge WebView2 Runtime, and it was not installed: "
!define ENFOLD_WV2_FIX "Install the runtime from$\r$\n${ENFOLD_WV2_URL}$\r$\nand run this installer again."
!define ENFOLD_NOTHING_INSTALLED "Nothing was installed on this computer."
!define ENFOLD_NOTHING_CHANGED "Nothing was changed on this computer."
!define ENFOLD_NOTHING_REMOVED "Nothing was removed from this computer."

####
## The installer's finish page when the install STOPPED (see above); on success
## it leaves the page exactly as Modern UI built it - "Run Enfold" ticked,
## "Finish".
##
## Order, in System.nsh's finish-page PRE: MUI writes ioSpecial.ini -
## NextButtonText "Finish" (1509), the title as Field 2 (1523), the text as
## Field 3 (1615), the Run box as Field 4 (1617-1640), NumFields (1737) - then
## calls this function (1744), and only then builds the dialog from the ini
## (INSTALLOPTIONS_INITDIALOG, 1769; the custom SHOW is later, 1848). So what
## this function writes is what the page shows:
##
##   Field 2 Text         the title, "Enfold was not installed"
##   Field 3 Text/Bottom  the reason (escaped), the full height MUI gives the
##                        text when there is no box (1541)
##   NumFields 3          the Run box (Field 4) is not created at all
##   Field 4 State 0      and MUI's LEAVE, which runs the program when Field 4
##                        reads "1" (1904-1915), does not
##   NextButtonText       "&Close" - InstallOptions sets the Next button's text
##                        from it when it builds the page (InstallerOptions.cpp,
##                        mySetWindowText(hNextButton, pszNextButtonText)), the
##                        same way MUI's "Finish" gets there
##   CancelShow 0         the disabled Cancel is hidden: one button
####
Function enfold.FinishPre
    StrCmp $EnfoldStop "" enfold_finish_done
    StrCmp $EnfoldWritten "1" enfold_finish_done

    ${If} $EnfoldStop == "wv2-no"
        StrCpy $R0 "${ENFOLD_WV2_NEEDS}you chose not to install it.${ENFOLD_PARA}${ENFOLD_WV2_FIX}${ENFOLD_PARA}${ENFOLD_NOTHING_INSTALLED}"
    ${ElseIf} $EnfoldStop == "wv2-failed"
        ${If} $EnfoldStopCode == ""
            StrCpy $R0 "${ENFOLD_WV2_NEEDS}Microsoft's installer did not install it.${ENFOLD_PARA}${ENFOLD_WV2_FIX}${ENFOLD_PARA}${ENFOLD_NOTHING_INSTALLED}"
        ${Else}
            StrCpy $R0 "${ENFOLD_WV2_NEEDS}Microsoft's installer reported code $EnfoldStopCode when run as you.${ENFOLD_PARA}${ENFOLD_WV2_FIX}${ENFOLD_PARA}${ENFOLD_NOTHING_INSTALLED}"
        ${EndIf}
    ${ElseIf} $EnfoldStop == "wv2-notallowed"
        StrCpy $R0 "${ENFOLD_WV2_NEEDS}Windows did not allow Microsoft's installer to run (the permission was declined).${ENFOLD_PARA}The runtime is installed for the whole machine, so an administrator installs it once from$\r$\n${ENFOLD_WV2_URL}$\r$\nand after that any account can install Enfold.${ENFOLD_PARA}${ENFOLD_NOTHING_INSTALLED}"
    ${ElseIf} $EnfoldStop == "running"
        StrCpy $R0 "Enfold is running, and Cancel was chosen.${ENFOLD_PARA}Quit Enfold from the tray icon, then run this installer again.${ENFOLD_PARA}${ENFOLD_NOTHING_CHANGED}"
    ${ElseIf} $EnfoldStop == "denied"
        StrCpy $R0 "Enfold's folder cannot be written:$\r$\n$INSTDIR${ENFOLD_PARA}Check the permissions on that folder, then run this installer again.${ENFOLD_PARA}${ENFOLD_NOTHING_CHANGED}"
    ${Else}
        StrCpy $R0 "Enfold's folder cannot be written (Windows error $EnfoldStopCode):$\r$\n$INSTDIR${ENFOLD_PARA}Check the permissions on that folder, then run this installer again.${ENFOLD_PARA}${ENFOLD_NOTHING_CHANGED}"
    ${EndIf}

    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Field 2" "Text" "Enfold was not installed"
    !insertmacro INSTALLOPTIONS_WRITE_CONVERT "ioSpecial.ini" "Field 3" "Text" "$R0"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Field 3" "Bottom" "185"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Settings" "NumFields" "3"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Field 4" "State" "0"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Settings" "NextButtonText" "&Close"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Settings" "CancelShow" "0"
enfold_finish_done:
FunctionEnd

####
## The uninstaller's finish page, both ways.
##
## Removed: the text says what was kept, written again in InstallOptions'
## escaped form (see ENFOLD_UNFINISH_TEXT above). MUI has written Field 3 raw
## by the time it calls this (System.nsh 1615, then the custom PRE at 1744) and
## builds the page after it. un.Nsis2Io exists because MUI includes
## InstallOptions' uninstaller conversion functions whenever it uses one of its
## own default texts (System.nsh 55-56), as this page's default title does.
##
## Stopped (Enfold running and Cancel chosen, or a folder it cannot write):
## "Enfold was not removed", the reason, "Close" - the same rewrite as
## enfold.FinishPre, less the Run box this page never had.
##
## On a reboot the field holds MUI's reboot text instead, which is left alone -
## this uninstaller never asks for one.
####
Function un.enfold.UnFinishPre
    IfRebootFlag enfold_unfinish_done
    StrCmp $EnfoldStop "" 0 enfold_unfinish_stop
    !insertmacro INSTALLOPTIONS_WRITE_UNCONVERT "ioSpecial.ini" "Field 3" "Text" "${ENFOLD_UNFINISH_TEXT}"
    Goto enfold_unfinish_done

enfold_unfinish_stop:
    ${If} $EnfoldStop == "running"
        StrCpy $R0 "Enfold is running, and Cancel was chosen.${ENFOLD_PARA}Quit Enfold from the tray icon, then run the uninstaller again.${ENFOLD_PARA}${ENFOLD_NOTHING_REMOVED}"
    ${ElseIf} $EnfoldStop == "denied"
        StrCpy $R0 "Enfold's folder cannot be written:$\r$\n$INSTDIR${ENFOLD_PARA}Check the permissions on that folder, then run the uninstaller again.${ENFOLD_PARA}${ENFOLD_NOTHING_REMOVED}"
    ${Else}
        StrCpy $R0 "Enfold's folder cannot be written (Windows error $EnfoldStopCode):$\r$\n$INSTDIR${ENFOLD_PARA}Check the permissions on that folder, then run the uninstaller again.${ENFOLD_PARA}${ENFOLD_NOTHING_REMOVED}"
    ${EndIf}

    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Field 2" "Text" "Enfold was not removed"
    !insertmacro INSTALLOPTIONS_WRITE_UNCONVERT "ioSpecial.ini" "Field 3" "Text" "$R0"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Settings" "NextButtonText" "&Close"
    !insertmacro INSTALLOPTIONS_WRITE "ioSpecial.ini" "Settings" "CancelShow" "0"
enfold_unfinish_done:
FunctionEnd

Function .onInit
   !insertmacro wails.checkArchitecture

   ####
   ## The maintenance page goes HERE, in the next release (APP.md 14,
   ## "Maintenance - the next release's, designed now"). It reads the uninstall
   ## entry - SetRegView 64; ReadRegStr HKCU "${UNINST_KEY}" "DisplayVersion" -
   ## and, when one is there, opens on a page offering Update / Repair /
   ## Uninstall by comparing it with ${INFO_PRODUCTVERSION}, in place of
   ## MUI_PAGE_WELCOME; the entry then also gains ModifyPath, and the installer
   ## copies itself into $INSTDIR so that Modify in Apps & features finds it.
   ## 1.0 has no predecessor to tell apart, so it simply installs.
   ####
FunctionEnd

Section
    !insertmacro wails.setShellContext

    # Nothing above this point in the section, nor in .onInit, writes anything:
    # wails.setShellContext is SetShellVarContext; the WebView2 macro writes
    # only into $PLUGINSDIR, which NSIS deletes on every exit (Main.c,
    # CleanUp); the running check opens with OPEN_EXISTING and writes nothing.

    # WebView2: as the user, then - asked - elevated; a stop if it is still
    # absent (APP.md 14). See the note above enfold.webview2runtime.
    !insertmacro enfold.webview2runtime
    StrCmp $EnfoldStop "" 0 enfold_inst_stop

    # A running Enfold is asked to quit, never killed (APP.md 14). Last before
    # the files, so nothing that could stop the install comes after it.
    !insertmacro enfold.checkNotRunning "inst"
    StrCmp $EnfoldStop "" 0 enfold_inst_stop

    # From here on the install writes; an abort after this is not a stop and
    # gets Modern UI's own "Installation Aborted" page (see THE STOP above).
    StrCpy $EnfoldWritten "1"
    SetOutPath $INSTDIR

    !insertmacro wails.files

    # The Start-menu shortcut only - Enfold lives in the tray, and a desktop
    # shortcut is a right-click away for whoever wants one (14, decision 2).
    # SetShellVarContext current puts it in this user's Start menu.
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    # SHELL_CONTEXT is HKCU here, so .efd is registered for this user alone
    # (14, decision 3). SetRegView 64 keeps HKCU\Software\Classes out of the
    # 32-bit Wow6432Node view, where 64-bit Explorer would not look for it -
    # makensis builds a 32-bit installer.
    SetRegView 64
    !insertmacro enfold.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
    Goto enfold_inst_end

enfold_inst_stop:
    # Nothing has been written (see THE STOP above). The progress page moves on
    # by itself to the finish page, which enfold.FinishPre rewrites; the exit
    # code still says the install did not happen.
    SetAutoClose true
    SetErrorLevel 2

enfold_inst_end:
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # A running Enfold, again - nothing is removed until it has quit (14). A
    # stop skips every removal below and ends on "Enfold was not removed".
    !insertmacro enfold.checkNotRunning "uninst"
    StrCmp $EnfoldStop "" 0 enfold_uninst_stop

    # The association, given back to whatever held .efd before (14, decision 3).
    SetRegView 64
    !insertmacro enfold.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"

    ####
    ## The WebView2 profile, and NOTHING else under %LOCALAPPDATA%\Enfold
    ## (14, decision 5): the vault, settings.json and the log stay, and the
    ## finish page says so. The profile is recreatable and holds nothing
    ## decrypted, caching being off. main_windows.go puts it exactly here.
    ####
    RMDir /r "$LOCALAPPDATA\Enfold\WebView2"

    # The uninstall entry (HKCU in user scope) and uninstall.exe.
    !insertmacro wails.deleteUninstaller

    ####
    ## The program folder, and with it uninstall.exe. $INSTDIR is the program
    ## folder written above - enfold.exe, icon.ico, uninstall.exe and nothing
    ## else - and no self-delete dance is needed to take it: an NSIS uninstaller
    ## copies ITSELF to the temporary directory and runs from there, passing its
    ## real directory back as $INSTDIR, so the file being deleted here is not the
    ## image that is executing. The manual says so under the _?= switch, which
    ## exists precisely to suppress that copy ("It also stops the uninstaller
    ## from copying itself to the temporary directory and running from there",
    ## NSIS manual 3.2.2). Do NOT add _?= here: it would disable the mechanism
    ## that makes this line work.
    ####
    RMDir /r "$INSTDIR"
    Goto enfold_uninst_end

enfold_uninst_stop:
    # Nothing has been removed. The uninstaller's progress page already moves
    # on by itself (Modern UI sets SetAutoClose true unless
    # MUI_UNFINISHPAGE_NOAUTOCLOSE, System.nsh 406); said again here so the
    # stop does not depend on it. un.enfold.UnFinishPre rewrites the page.
    SetAutoClose true
    SetErrorLevel 2

enfold_uninst_end:
SectionEnd
