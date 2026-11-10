//go:build windows

package lcu

import (
	"os/exec"
	"syscall"
)

// hidden runs a console tool without flashing a window over the game.
func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

// fromProcess reads LeagueClientUx.exe's command line via WMI.
func fromProcess() (Creds, error) {
	out, err := hidden(exec.Command("wmic", "process", "where", "name='LeagueClientUx.exe'", "get", "commandline")).Output()
	if err != nil {
		// wmic is absent on newer Windows builds; fall back to PowerShell CIM.
		out, err = hidden(exec.Command("powershell", "-NoProfile", "-Command",
			"Get-CimInstance Win32_Process -Filter \"name='LeagueClientUx.exe'\" | Select-Object -ExpandProperty CommandLine")).Output()
		if err != nil {
			return Creds{}, err
		}
	}
	return parseProcessList(string(out))
}
