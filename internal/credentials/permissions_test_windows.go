//go:build windows

package credentials

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPrivateCredentialPath(t *testing.T, path string, wantDir bool) {
	t.Helper()

	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sd == nil {
		t.Fatal("credential path has no security descriptor")
	}
	sddl := sd.String()
	if !strings.Contains(sddl, "D:P") {
		t.Fatalf("credential path DACL is not protected: %q", sddl)
	}
	for _, trustee := range []string{"WD", "BU", "AU", "BG"} {
		if strings.Contains(sddl, trustee) {
			t.Fatalf("credential path grants access to %s: %q", trustee, sddl)
		}
	}
	if wantDir && !strings.Contains(sddl, "OICI") {
		t.Fatalf("credential directory DACL is not inheritable: %q", sddl)
	}
}
