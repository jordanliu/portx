package origin

import "testing"

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"3000", "http://127.0.0.1:3000"},
		{"localhost:3000", "http://localhost:3000"},
		{"http://localhost:3000", "http://localhost:3000"},
		{"https://localhost:8443", "https://localhost:8443"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
	}
	for _, tc := range cases {
		u, err := Normalize(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if u.String() != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, u.String(), tc.want)
		}
	}
}

func TestNormalizeReject(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "ftp://x", "99999", "not-a-target"} {
		if _, err := Normalize(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
