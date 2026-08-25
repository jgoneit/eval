package state

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	V1RelativePath = "jgoneit/eval/v1/observations.jsonl"
	V2RelativePath = "jgoneit/eval/v2/observations.jsonl"
)

// Root resolves the explicit state root or the XDG/HOME fallback without
// creating it. Relative or non-clean roots fail closed.
func Root(explicit string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	root := explicit
	if root == "" {
		if xdg := getenv("XDG_STATE_HOME"); xdg != "" {
			root = xdg
		} else {
			home := getenv("HOME")
			if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
				return "", fmt.Errorf("HOME must be an absolute clean path when XDG_STATE_HOME is unset")
			}
			root = filepath.Join(home, ".local", "state")
		}
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("state root must be an absolute clean path")
	}
	return root, nil
}

func V1Path(root string) string { return filepath.Join(root, filepath.FromSlash(V1RelativePath)) }

func V2Path(root string) string { return filepath.Join(root, filepath.FromSlash(V2RelativePath)) }
