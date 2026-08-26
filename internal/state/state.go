package state

import (
	"fmt"
	"os"
	"path/filepath"
)

const JournalRelativePath = "jgoneit/eval-experiment/v1/journal.jsonl"

// Root resolves the recorder state root without creating it. Store owns
// creation and security checks; this package only defines the location contract.
func Root(explicit string, getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if userHomeDir == nil {
		userHomeDir = os.UserHomeDir
	}
	root := explicit
	if root == "" {
		root = getenv("XDG_STATE_HOME")
		if root == "" {
			home, err := userHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve user home: %w", err)
			}
			if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
				return "", fmt.Errorf("user home must be an absolute clean path")
			}
			root = filepath.Join(home, ".local", "state")
		}
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Dir(root) == root {
		return "", fmt.Errorf("state root must be an absolute non-root clean path")
	}
	return root, nil
}

func JournalPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(JournalRelativePath))
}
