package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRootHelpIncludesQuickStart(t *testing.T) {
	t.Parallel()

	app := newApp()
	var output bytes.Buffer
	app.Writer = &output
	app.ErrWriter = &output

	if err := app.Run(context.Background(), []string{"portx", "--help"}); err != nil {
		t.Fatalf("run help: %v", err)
	}

	for _, want := range []string{
		"Public URLs for local apps, powered by Cloudflare Tunnel",
		"QUICK START:",
		"portx http 3000",
		"portx setup",
		"portx http 3000 --url=api",
		"DIAGNOSTICS:",
		"portx doctor",
		`Use "portx <command> --help"`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("help output missing %q:\n%s", want, output.String())
		}
	}
}

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
