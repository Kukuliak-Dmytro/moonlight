Set shell = CreateObject("WScript.Shell")
If WScript.Arguments.Count > 0 Then
    shell.Run """" & WScript.Arguments(0) & """ daemon", 0, True
End If
