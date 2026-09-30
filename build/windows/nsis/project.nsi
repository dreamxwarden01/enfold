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
##   4. No, or still absent: the cancelled page (enfold.InstFilesLeave), with
##      the reason in the details in the words of what happened.
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
## Long sentences are split over several DetailPrint lines: the details list
## is one column exactly as wide as the list (Ui.c: lvc.cx = r.right -
## SM_CXVSCROLL) and clips anything longer. ${U+2014} is an em dash: makensis
## reads this file in the ANSI code page, so the source stays ASCII.
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
    DetailPrint "Enfold needs the Microsoft Edge WebView2 Runtime."
    DetailPrint "Get it from"
    DetailPrint "${ENFOLD_WV2_URL}"
    DetailPrint "and run this installer again. Nothing was installed."
    Goto enfold_wv2_cancel

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

    ; (b) It ran, and the runtime is still absent. The code shown is the run
    ;     as the user's - the only one there is (see above).
    DetailPrint "Enfold needs the Microsoft Edge WebView2 Runtime"
    ${If} $R5 == ""
        DetailPrint "and it was not installed."
    ${Else}
        DetailPrint "and it was not installed (Microsoft's installer"
        DetailPrint "reported code $R5 when run as you)."
    ${EndIf}
    DetailPrint "Nothing was installed. Get it from"
    DetailPrint "${ENFOLD_WV2_URL}"
    DetailPrint "and run this installer again."
    Goto enfold_wv2_cancel

enfold_wv2_notlaunched:
    ; (a) The elevated run never started: the permission was declined, or the
    ;     account could not supply an administrator's credentials.
    DetailPrint "Windows did not allow Microsoft's installer to run:"
    DetailPrint "the permission was declined."
    DetailPrint "The WebView2 Runtime is installed for the whole machine,"
    DetailPrint "so an administrator installs it once from"
    DetailPrint "${ENFOLD_WV2_URL} ${U+2014}"
    DetailPrint "after that any account can install Enfold."
    DetailPrint "Nothing was installed."

enfold_wv2_cancel:
    ; Nothing has been written: $EnfoldWritten is still empty, so
    ; enfold.InstFilesLeave turns this abort into the cancelled page. textonly:
    ; the message goes to the status line, not as a blank row into the list
    ; (a message-less Abort would add one - exec.c EW_ABORT, Ui.c
    ; update_status_text).
    SetDetailsPrint textonly
    Abort "Installation cancelled."

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
## Each stop writes its reason into the details and a short status line
## before Abort (SetDetailsPrint textonly + Abort "text": a message-less Abort
## would leave a blank row in the list). In the installer these stops come
## before anything is written, so they end on the cancelled page too.
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
    SetDetailsPrint listonly
    DetailPrint "Enfold is running. Quit it from the tray icon first."
    SetDetailsPrint textonly
    Abort "Cancelled: Enfold is running."

enfold_run_${ID}_denied:
    IntCmp $1 5 0 enfold_run_${ID}_other enfold_run_${ID}_other
    MessageBox MB_OK|MB_ICONSTOP "Enfold's folder cannot be written. Check the permissions on $INSTDIR."
    SetDetailsPrint listonly
    DetailPrint "Enfold's folder cannot be written."
    DetailPrint "Check the permissions on $INSTDIR."
    SetDetailsPrint textonly
    Abort "Stopped: Enfold's folder cannot be written."

enfold_run_${ID}_other:
    MessageBox MB_OK|MB_ICONSTOP "Enfold's folder cannot be written. Check the permissions on $INSTDIR. (Windows error $1.)"
    SetDetailsPrint listonly
    DetailPrint "Enfold's folder cannot be written (Windows error $1)."
    DetailPrint "Check the permissions on $INSTDIR."
    SetDetailsPrint textonly
    Abort "Stopped: Enfold's folder cannot be written."

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
# The cancelled page: see enfold.InstFilesLeave. MUI calls a custom leave
# function first from its own instfiles leave (System.nsh 1494) and !undefs the
# define after one page, so it applies to this page only.
!define MUI_PAGE_CUSTOMFUNCTION_LEAVE enfold.InstFilesLeave
!insertmacro MUI_PAGE_INSTFILES # Installing page.
# MUI_FINISHPAGE_RUN gives the finish page its checkbox; its label defaults to
# MUI_TEXT_FINISH_RUN, "&Run $(^NameDA)" - "Run Enfold", Name being the
# product's. MUI unsets the FINISHPAGE defines once a finish page is declared
# (System.nsh 1062-1070), so this one does not leak into the uninstaller's.
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
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
## "1" once the install section has started writing (set just before
## SetOutPath $INSTDIR). Until then nothing - no file, key, shortcut or
## uninstaller - exists, which is what lets the cancelled page say "Nothing was
## installed." and mean it.
####
Var EnfoldWritten

!define ENFOLD_DM_SETDEFID 0x0401 ; DM_SETDEFID = WM_USER + 1; not in WinMessages.nsh

####
## THE CANCELLED PAGE (APP.md 14, "WebView2"): an install that stopped before
## writing anything ends on this page - header "Installation cancelled",
## "Nothing was installed.", the reason in the details, and one button, Close.
##
## Why it is done here and not in the section. After a section Aborts, NSIS
## moves to the hidden "completed" page that follows every instfiles page, and
## sets the buttons' text for it from the page's own strings (Ui.c:
## SetDlgItemTextFromLang(hwndDlg, IDCANCEL, this_page->cancel)) - which is
## always "Cancel" (build.cpp: every page's cancel defaults to NLF_BTN_CANCEL;
## no script command sets it per page). A WM_SETTEXT sent from the section
## would be overwritten a moment later. That transition runs only if this
## page's leave function lets it: "The leave-function allows you to force the
## user to stay on the current page using Abort" (NSIS manual 4.5.3), and Ui.c
## returns before the completed page when it does. So the leave function sets
## the header, relabels and enables Cancel as Close, hides the disabled Next
## (Back is already hidden on this page), makes Close the default and focused
## button, enables the title bar's close box - everything NSIS itself does for
## an aborted install (Ui.c, the abort branch) - and stays.
##
## The header is set here too, not with MUI_INSTFILESPAGE_ABORTHEADER_TEXT/
## _SUBTEXT: MUI's own header code runs only when this function returns, and
## those defines would (a) put "Nothing was installed." on an install aborted
## AFTER writing - a file error's Abort - and (b) leak into the uninstaller's
## instfiles page, since MUI does not unset them (System.nsh unsets only the
## FINISHHEADER and ABORTWARNING texts). An abort after writing gets MUI's own
## "Installation Aborted" page, unchanged.
##
## Close ends the installer through NSIS's aborted-install path: Cancel with
## the abort flag set runs .onInstFailed and exits with code 2 ("aborted by
## script", NSIS Appendix D.1) - never .onUserAbort, so MUI_ABORTWARNING's "Are
## you sure?" cannot appear (Ui.c, the IDCANCEL branch).
####
Function enfold.InstFilesLeave
    IfAbort 0 enfold_leave_done
    StrCmp $EnfoldWritten "1" enfold_leave_done

    !insertmacro MUI_HEADER_TEXT "Installation cancelled" "Nothing was installed."

    GetDlgItem $0 $HWNDPARENT 1 ; Next
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 3 ; Back
    ShowWindow $0 ${SW_HIDE}

    GetDlgItem $0 $HWNDPARENT 2 ; Cancel -> Close
    SendMessage $0 ${WM_SETTEXT} 0 "STR:Close"
    EnableWindow $0 1
    SendMessage $HWNDPARENT ${ENFOLD_DM_SETDEFID} 2 0
    SendMessage $HWNDPARENT ${WM_NEXTDLGCTL} $0 1

    ; SC_CLOSE 0xF060, MF_BYCOMMAND|MF_ENABLED 0 - as Ui.c does on abort.
    System::Call 'user32::GetSystemMenu(p$HWNDPARENT, i0)p.r1'
    System::Call 'user32::EnableMenuItem(pr1, i0xF060, i0)'

    ; Abort updates the status line wherever it runs (exec.c EW_ABORT calls
    ; update_status_text); "none" (script.cpp: none=6, both bits set) keeps
    ; the section's "Installation cancelled." and adds no blank row.
    SetDetailsPrint none
    Abort ; stay on this page
enfold_leave_done:
FunctionEnd

####
## The uninstaller's finish text, written again in InstallOptions' escaped
## form (see ENFOLD_UNFINISH_TEXT above). MUI has written Field 3 raw by the
## time it calls this (System.nsh 1615, then the custom PRE at 1744) and shows
## the page after it. un.Nsis2Io exists because MUI includes InstallOptions'
## uninstaller conversion functions whenever it uses one of its own default
## texts (System.nsh 55-56), as this page's default title does. On a reboot
## the field holds MUI's reboot text instead, which is left alone - this
## uninstaller never asks for one.
####
Function un.enfold.UnFinishPre
    IfRebootFlag enfold_unfinish_done
    !insertmacro INSTALLOPTIONS_WRITE_UNCONVERT "ioSpecial.ini" "Field 3" "Text" "${ENFOLD_UNFINISH_TEXT}"
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

    # WebView2: as the user, then - asked - elevated; the cancelled page if it
    # is still absent (APP.md 14). See the note above enfold.webview2runtime.
    !insertmacro enfold.webview2runtime

    # A running Enfold is asked to quit, never killed (APP.md 14). Last before
    # the files, so nothing that could abort the install comes after it.
    !insertmacro enfold.checkNotRunning "inst"

    # From here on the install writes; an abort after this is not "nothing was
    # installed" and gets MUI's own aborted page (see enfold.InstFilesLeave).
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
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # A running Enfold, again - nothing is removed until it has quit (14).
    !insertmacro enfold.checkNotRunning "uninst"

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
SectionEnd
