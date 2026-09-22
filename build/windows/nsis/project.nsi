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
## The runtime: looked for, Microsoft's bootstrapper run silently when it is
## absent (online only, as SCOPE says), then looked for again - a bootstrapper
## that failed leaves the keys as they were, and Enfold cannot start without
## the runtime, so the installer stops with the address to get it from.
####
!macro enfold.webview2runtime
    !insertmacro enfold.webview2.detect $R3
    ${If} $R3 != "1"
        SetDetailsPrint both
        DetailPrint "Installing: WebView2 Runtime"
        SetDetailsPrint listonly

        InitPluginsDir
        CreateDirectory "$pluginsdir\webview2bootstrapper"
        SetOutPath "$pluginsdir\webview2bootstrapper"
        File "MicrosoftEdgeWebview2Setup.exe"
        ExecWait '"$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe" /silent /install'

        SetDetailsPrint both

        !insertmacro enfold.webview2.detect $R3
        ${If} $R3 != "1"
            MessageBox MB_OK|MB_ICONSTOP "Enfold needs the Microsoft Edge WebView2 Runtime and it could not be installed. Install it from https://developer.microsoft.com/microsoft-edge/webview2/ and run this installer again."
            Abort
        ${EndIf}
    ${EndIf}
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
    Abort

enfold_run_${ID}_denied:
    IntCmp $1 5 0 enfold_run_${ID}_other enfold_run_${ID}_other
    MessageBox MB_OK|MB_ICONSTOP "Enfold's folder cannot be written. Check the permissions on $INSTDIR."
    Abort

enfold_run_${ID}_other:
    MessageBox MB_OK|MB_ICONSTOP "Enfold's folder cannot be written. Check the permissions on $INSTDIR. (Windows error $1.)"
    Abort

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
####
!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
# MUI_FINISHPAGE_RUN gives the finish page its checkbox; its label defaults to
# MUI_TEXT_FINISH_RUN, "&Run $(^NameDA)" - "Run Enfold", Name being the
# product's. MUI2 unsets the FINISHPAGE defines when a finish page is declared,
# so this one does not leak into the uninstaller's finish page below.
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page
# The uninstaller's finish page says what was kept (APP.md 14, decision 5).
# MUI2's MUI_UNPAGE_FINISH shares MUI_PAGEDECLARATION_FINISH with the
# installer's, and that macro applies MUI_FINISHPAGE_TEXT through
# !insertmacro MUI_DEFAULT - so the text define for the uninstaller's page is
# MUI_FINISHPAGE_TEXT, not a MUI_UNFINISHPAGE_* one (there is none).
# $LOCALAPPDATA expands to this user's folder.
!define MUI_FINISHPAGE_TEXT "Your vault and settings stay in $LOCALAPPDATA\Enfold. Delete that folder yourself if you mean to."
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

    # WebView2, and the stop if the bootstrapper fails (APP.md 14). Enfold's
    # macro, not the template's: see the note above enfold.webview2.detect.
    !insertmacro enfold.webview2runtime

    # A running Enfold is asked to quit, never killed (APP.md 14). Last before
    # the files, so nothing that could abort the install comes after it.
    !insertmacro enfold.checkNotRunning "inst"

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
