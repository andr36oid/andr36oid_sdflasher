package platform

// uacScript is the PowerShell that starts the helper (exe and args already quoted)
// with administrator rights and ends with its exit code, or with 1223 when the UAC
// prompt was cancelled (1 for any other error of Start-Process).
func uacScript(exe, args string) string {
	return "try { $p=Start-Process -FilePath " + exe + " -ArgumentList " + args + " -Verb RunAs -WindowStyle Hidden -PassThru -Wait -ErrorAction Stop; exit $p.ExitCode } " +
		"catch { $c=1; $x=$_.Exception; while ($x) { if ($x.NativeErrorCode -eq 1223) { $c=1223 }; $x=$x.InnerException }; exit $c }"
}
