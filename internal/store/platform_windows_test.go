//go:build windows

package store

import (
	"errors"
	"testing"
)

func TestWindowsFailsClosedWithoutACLVerification(t *testing.T) {
	_, err := New(`C:\eval-state`, `jgoneit/eval/v2/observations.jsonl`, Options{})
	if !errors.Is(err, ErrPermission) || CategoryOf(err) != CategoryPermission {
		t.Fatalf("New() error = %v, want stable permission failure", err)
	}
}
