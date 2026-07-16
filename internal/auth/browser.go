package auth

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"portx/internal/apperr"
	"portx/internal/cloudflared"
)

// LoginResult is the credential payload from Cloudflare browser login.
type LoginResult struct {
	APIToken  string
	AccountID string
	ZoneID    string
}

// BrowserLogin ensures cloudflared is on PATH, runs `cloudflared tunnel login`,
// then reads ~/.cloudflared/cert.pem.
func BrowserLogin(ctx context.Context) (LoginResult, error) {
	st, err := cloudflared.EnsureInstalled()
	if err != nil {
		return LoginResult{}, err // already polished
	}

	certPath, err := defaultCertPath()
	if err != nil {
		return LoginResult{}, err
	}

	if data, err := os.ReadFile(certPath); err == nil {
		if res, err := decodeOriginCert(data); err == nil && res.APIToken != "" {
			return res, nil
		}
	}

	if stInfo, err := os.Stat(certPath); err == nil && stInfo.Size() > 0 {
		bak := certPath + ".bak." + time.Now().Format("20060102-150405")
		_ = os.Rename(certPath, bak)
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return LoginResult{}, err
	}

	cmd := exec.CommandContext(ctx, st.Path, "tunnel", "login")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return LoginResult{}, apperr.New(apperr.ExitAuth,
			"cloudflared tunnel login failed\n\n"+
				"Open https://dash.cloudflare.com and try again, or paste a token:\n"+
				"  portx setup --token")
	}

	data, err := os.ReadFile(certPath)
	if err != nil {
		return LoginResult{}, apperr.Wrap(apperr.ExitAuth, "login finished but cert.pem missing", err)
	}
	res, err := decodeOriginCert(data)
	if err != nil {
		return LoginResult{}, err
	}
	return res, nil
}

func defaultCertPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cloudflared", "cert.pem"), nil
}

func decodeOriginCert(blocks []byte) (LoginResult, error) {
	if len(blocks) == 0 {
		return LoginResult{}, apperr.New(apperr.ExitAuth, "empty certificate from login")
	}
	var result LoginResult
	block, rest := pem.Decode(blocks)
	for block != nil {
		if block.Type == "ARGO TUNNEL TOKEN" {
			var cert struct {
				ZoneID    string `json:"zoneID"`
				AccountID string `json:"accountID"`
				APIToken  string `json:"apiToken"`
			}
			if err := json.Unmarshal(block.Bytes, &cert); err != nil {
				return LoginResult{}, apperr.Wrap(apperr.ExitAuth, "decode login certificate", err)
			}
			result.ZoneID = cert.ZoneID
			result.AccountID = cert.AccountID
			result.APIToken = cert.APIToken
		}
		block, rest = pem.Decode(rest)
	}
	if result.APIToken == "" {
		return LoginResult{}, apperr.New(apperr.ExitAuth, "login certificate missing API token")
	}
	return result, nil
}
