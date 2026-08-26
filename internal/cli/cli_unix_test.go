//go:build darwin || linux

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jgoneit/eval/internal/store"
)

func TestObservePermissionFailureIsBestEffort(t *testing.T) {
	root := t.TempDir()
	if result, _ := captureObserve(t, root, validDraft, store.Options{}); result.Status != "recorded" {
		t.Fatalf("seed result = %+v", result)
	}
	privateRoot := filepath.Join(root, "jgoneit")
	if err := os.Chmod(privateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	result, exit := captureObserve(t, root, validDraft, store.Options{})
	if exit != ExitSuccess || result.Status != "skipped" || result.Reason != "state-permission-denied" {
		t.Fatalf("result = %+v exit %d", result, exit)
	}
}
