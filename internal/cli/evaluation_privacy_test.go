package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLedgerFlagErrorsDoNotEchoSuppliedArguments(t *testing.T) {
	secret := "sensitive-source-marker"
	for _, args := range [][]string{
		{"collect", "--" + secret},
		{"review", "export", "--" + secret + "=/private/secret"},
		{"collect", "--experiment=" + secret, "--unsupported"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, Runtime{Stdout: &stdout, Stderr: &stderr})
		if code != ExitUsage || stderr.Len() != 0 || strings.Contains(stdout.String(), secret) {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), `"reason":"invalid-argument"`) {
			t.Fatalf("missing static parse error: %q", stdout.String())
		}
	}
}

func TestLedgerHelpPreservesUsageWithoutArgumentValues(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"review", "export", "--out", "/private/sensitive-source-marker", "--help"}, Runtime{Stdout: &stdout, Stderr: &stderr})
	if code != 0 || !strings.Contains(stderr.String(), "Usage of review export:") || !strings.Contains(stderr.String(), "-experiment") || strings.Contains(stderr.String(), "sensitive-source-marker") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
