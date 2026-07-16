package origin

import (
	"net/url"
	"testing"
)

func TestValidateTargetSafetyBlocksMetadata(t *testing.T) {
	t.Parallel()
	u, _ := url.Parse("http://169.254.169.254/latest/meta-data")
	if err := ValidateTargetSafety(u); err == nil {
		t.Fatal("expected block")
	}
	u, _ = url.Parse("http://127.0.0.1:3000")
	if err := ValidateTargetSafety(u); err != nil {
		t.Fatal(err)
	}
}
