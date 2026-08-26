package state

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRootPrefersExplicitPath(t *testing.T) {
	t.Parallel()
	explicit := filepath.Join(t.TempDir(), "explicit")
	getenvCalled := false
	homeCalled := false

	got, err := Root(
		explicit,
		func(string) string {
			getenvCalled = true
			return filepath.Join(t.TempDir(), "xdg")
		},
		func() (string, error) {
			homeCalled = true
			return t.TempDir(), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != explicit {
		t.Fatalf("root = %q, want %q", got, explicit)
	}
	if getenvCalled || homeCalled {
		t.Fatalf("explicit path consulted environment: getenv=%t home=%t", getenvCalled, homeCalled)
	}
}

func TestRootPrefersXDGStateHome(t *testing.T) {
	t.Parallel()
	xdg := filepath.Join(t.TempDir(), "xdg")
	homeCalled := false

	got, err := Root(
		"",
		func(name string) string {
			if name == "XDG_STATE_HOME" {
				return xdg
			}
			return ""
		},
		func() (string, error) {
			homeCalled = true
			return t.TempDir(), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != xdg {
		t.Fatalf("root = %q, want %q", got, xdg)
	}
	if homeCalled {
		t.Fatal("XDG resolution consulted user home")
	}
}

func TestRootFallsBackToInjectedUserHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	homeCalls := 0

	got, err := Root(
		"",
		func(string) string { return "" },
		func() (string, error) {
			homeCalls++
			return home, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
	if homeCalls != 1 {
		t.Fatalf("user home calls = %d, want 1", homeCalls)
	}
}

func TestRootRejectsInvalidXDGWithoutHomeFallback(t *testing.T) {
	t.Parallel()
	homeCalled := false

	_, err := Root(
		"",
		func(name string) string {
			if name == "XDG_STATE_HOME" {
				return "relative-state"
			}
			return ""
		},
		func() (string, error) {
			homeCalled = true
			return t.TempDir(), nil
		},
	)
	if err == nil {
		t.Fatal("relative XDG state root was accepted")
	}
	if homeCalled {
		t.Fatal("invalid nonempty XDG state root fell back to user home")
	}
}

func TestRootReportsUserHomeFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("home unavailable")

	_, err := Root(
		"",
		func(string) string { return "" },
		func() (string, error) { return "", wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestRootRejectsInvalidUserHome(t *testing.T) {
	t.Parallel()
	unclean := t.TempDir() + string(filepath.Separator) + "."
	for _, home := range []string{"", "relative-home", unclean} {
		home := home
		t.Run(home, func(t *testing.T) {
			t.Parallel()
			if _, err := Root(
				"",
				func(string) string { return "" },
				func() (string, error) { return home, nil },
			); err == nil {
				t.Fatalf("invalid user home %q was accepted", home)
			}
		})
	}
}
