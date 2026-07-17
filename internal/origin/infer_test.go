package origin

import "testing"

func TestSanitizeDNSLabel(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"My App":      "my-app",
		"portx":       "portx",
		"foo_bar":     "foo-bar",
		"---x---":     "x",
		"API.Gateway": "api-gateway",
		"":            "",
		"...":         "",
	}
	for in, want := range cases {
		if got := sanitizeDNSLabel(in); got != want {
			t.Fatalf("sanitizeDNSLabel(%q)=%q want %q", in, got, want)
		}
	}
}
