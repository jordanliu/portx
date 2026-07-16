package cloudflared

import (
	"os/exec"
)

// Status describes how cloudflared was resolved.
type Status struct {
	Path    string
	Version string
	OnPATH  bool
	Source  string // path
}

// Lookup finds `cloudflared` on PATH. Does not download anything.
func Lookup() (Status, error) {
	p, err := exec.LookPath("cloudflared")
	if err != nil {
		return Status{}, notFoundError()
	}
	ver, err := Version(p)
	if err != nil {
		return Status{}, notFoundError()
	}
	if err := CheckSupported(ver); err != nil {
		return Status{}, err
	}
	return Status{Path: p, Version: ver, OnPATH: true, Source: "path"}, nil
}

// EnsureInstalled is an alias for Lookup (cloudflared must already be on PATH).
func EnsureInstalled() (Status, error) {
	return Lookup()
}
