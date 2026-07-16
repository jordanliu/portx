package origin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"portx/internal/apperr"
)

// InferLabel returns a single DNS label from the git repo root or current directory.
func InferLabel() (string, error) {
	base, err := projectBaseName()
	if err != nil {
		return "", err
	}
	label := sanitizeDNSLabel(base)
	if label == "" {
		return "", apperr.New(apperr.ExitInvalidArgs,
			"could not infer a hostname from the current directory\n\nPass an explicit label:  portx http --url=my-app 3000")
	}
	return label, nil
}

func projectBaseName() (string, error) {
	if root, err := gitTopLevel(); err == nil && root != "" {
		return filepath.Base(root), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Base(wd), nil
}

func gitTopLevel() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func sanitizeDNSLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if b.Len() == 0 || lastHyphen {
				continue
			}
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}
