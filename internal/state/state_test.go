package state

import (
	"path/filepath"
	"testing"
)

func TestRootSelection(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		env      map[string]string
		want     string
		wantErr  bool
	}{
		{name: "explicit", explicit: filepath.Join(string(filepath.Separator), "tmp", "eval-state"), want: filepath.Join(string(filepath.Separator), "tmp", "eval-state")},
		{name: "xdg", env: map[string]string{"XDG_STATE_HOME": filepath.Join(string(filepath.Separator), "tmp", "xdg")}, want: filepath.Join(string(filepath.Separator), "tmp", "xdg")},
		{name: "home", env: map[string]string{"HOME": filepath.Join(string(filepath.Separator), "tmp", "home")}, want: filepath.Join(string(filepath.Separator), "tmp", "home", ".local", "state")},
		{name: "relative xdg", env: map[string]string{"XDG_STATE_HOME": "state"}, wantErr: true},
		{name: "missing", env: map[string]string{}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Root(test.explicit, func(key string) string { return test.env[key] })
			if (err != nil) != test.wantErr {
				t.Fatalf("Root() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("Root() = %q, want %q", got, test.want)
			}
		})
	}
}
