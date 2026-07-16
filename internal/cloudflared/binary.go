package cloudflared

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"portx/internal/apperr"
)

func Version(bin string) (string, error) {
	cmd := exec.Command(bin, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1], nil
		}
		if strings.Count(f, ".") >= 2 && f[0] >= '0' && f[0] <= '9' {
			return f, nil
		}
	}
	return line, nil
}

func supportCutoff() time.Time {
	return time.Now().UTC().AddDate(-1, 0, 0)
}

// CheckSupported returns an error if the version is older than Cloudflare's ~1-year support window.
func CheckSupported(version string) error {
	version = strings.TrimSpace(version)
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return nil
	}
	year, err1 := strconv.Atoi(parts[0])
	month, err2 := strconv.Atoi(parts[1])
	day := 1
	if len(parts) > 2 {
		day, _ = strconv.Atoi(parts[2])
	}
	if err1 != nil || err2 != nil {
		return nil
	}
	built := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if built.Before(supportCutoff()) {
		return apperr.New(apperr.ExitCloudflared, fmt.Sprintf(
			"cloudflared %s is too old (Cloudflare supports ~1 year of releases)\n\nUpgrade:\n  %s",
			version, strings.TrimSpace(InstallCommand())))
	}
	return nil
}

const downloadsURL = "https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/downloads/"

// InstallCommand is the primary install hint for this platform.
// On Windows this is the downloads page URL (no package-manager command).
func InstallCommand() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install cloudflared"
	case "windows":
		return downloadsURL
	default:
		return "brew install cloudflared   # or see Cloudflare docs"
	}
}

func notFoundError() error {
	cmd := InstallCommand()
	if runtime.GOOS == "windows" {
		return apperr.New(apperr.ExitCloudflared, fmt.Sprintf(
			"cloudflared is not installed (or not on PATH)\n\nDownload and install:\n  %s\n\nThen re-run your command.",
			cmd))
	}
	return apperr.New(apperr.ExitCloudflared, fmt.Sprintf(
		"cloudflared is not installed (or not on PATH)\n\n  %s\n\nThen run:  cloudflared version",
		cmd))
}
