package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"iptv-client/internal/desktop"
)

// windowsPlatform installs the NSIS setup.exe.
type windowsPlatform struct{}

func (windowsPlatform) AssetName() string { return "iptv-client-windows-amd64-setup.exe" }

// windowsHelper waits for the app to exit (so its files are not locked), runs
// the NSIS installer silently into the current install folder and starts the
// new version. The installer asks for elevation through UAC; the app is
// restarted from this unelevated script so it does not run as admin. If the
// installer cannot start (UAC declined, blocked by Smart App Control) or
// fails, a message says so and the old version is started again.
const windowsHelper = `
Wait-Process -Id $env:UPDATE_PID -ErrorAction SilentlyContinue
$failure = $null
try {
  $p = Start-Process -FilePath $env:UPDATE_SETUP -ArgumentList '/S', "/D=$env:UPDATE_DIR" -Verb RunAs -Wait -PassThru
  if ($p.ExitCode -ne 0) { $failure = "The installer failed (exit code $($p.ExitCode))." }
} catch {
  $failure = "The installer could not be started: $($_.Exception.Message)"
}
if ($failure) {
  Add-Type -AssemblyName PresentationFramework
  [System.Windows.MessageBox]::Show("$failure The previous version will be started again.", 'IPTV Client update', 'OK', 'Warning') | Out-Null
}
Start-Process -FilePath $env:UPDATE_APP
Remove-Item -LiteralPath (Split-Path $env:UPDATE_SETUP) -Recurse -Force -ErrorAction SilentlyContinue
`

func (windowsPlatform) Install(setupPath string) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	ps := filepath.Join(desktop.WindowsDir(), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsHelper)
	cmd.Env = append(os.Environ(),
		"UPDATE_PID="+strconv.Itoa(os.Getpid()),
		"UPDATE_SETUP="+setupPath,
		"UPDATE_DIR="+filepath.Dir(exe),
		"UPDATE_APP="+exe,
	)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start the installer: %w", err)
	}
	return cmd.Process.Release()
}
