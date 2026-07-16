package cli

import "testing"

func TestExpandBareURLFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want []string
	}{
		{
			in:   []string{"portx", "http", "3000", "--url"},
			want: []string{"portx", "http", "3000", "--url="},
		},
		{
			in:   []string{"portx", "http", "3000", "--url", "--json"},
			want: []string{"portx", "http", "3000", "--url=", "--json"},
		},
		{
			in:   []string{"portx", "http", "3000", "--url", "api"},
			want: []string{"portx", "http", "3000", "--url", "api"},
		},
		{
			in:   []string{"portx", "http", "3000", "--url=api"},
			want: []string{"portx", "http", "3000", "--url=api"},
		},
		{
			in:   []string{"portx", "http", "3000"},
			want: []string{"portx", "http", "3000"},
		},
	}
	for _, tc := range cases {
		got := expandBareURLFlag(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("in=%v got=%v want=%v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("in=%v got=%v want=%v", tc.in, got, tc.want)
			}
		}
	}
}
