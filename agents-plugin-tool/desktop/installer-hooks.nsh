; The NSIS installer only overwrites the files it ships. A file an older
; release shipped under daemon/ and a newer one dropped would survive the
; upgrade, and the app rejects any daemon/ file its manifests do not list
; ("Invalid daemon package"). Clear the whole tree before the new files land.
; Running daemons use staged copies, never these files, so none hold a lock.
!macro NSIS_HOOK_PREINSTALL
  RMDir /r "$INSTDIR\daemon"
!macroend
