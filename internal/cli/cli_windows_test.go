//go:build windows

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgoneit/eval/internal/experiment"
	"github.com/jgoneit/eval/internal/state"
)

func TestObserveUsesWindowsUserProfileByDefault(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", profile)

	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"observe"}, Runtime{
		Stdin:  strings.NewReader(validDraft),
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return time.Date(2026, 8, 26, 12, 34, 56, 123, time.UTC) },
	})
	var result capturedResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode %q: %v (stderr %q)", stdout.String(), err, stderr.String())
	}
	if exit != ExitSuccess || result.Status != "recorded" || result.Slot != 1 {
		t.Fatalf("observe = %+v exit %d (stderr %q)", result, exit, stderr.String())
	}
	if result.Durability != "confirmed" && result.Durability != "unconfirmed" {
		t.Fatalf("durability = %q", result.Durability)
	}

	root := filepath.Join(profile, ".local", "state")
	data, err := os.ReadFile(state.JournalPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := experiment.ParseJournal(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Slot != 1 {
		t.Fatalf("stored rows = %#v", rows)
	}
}
