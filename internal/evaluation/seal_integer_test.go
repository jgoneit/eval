package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSealExitCodeJSONRoundTrip(t *testing.T) {
	for _, raw := range []string{"null", "0", "-1", "9007199254740993", "9223372036854775808", "-9223372036854775809", strings.Repeat("9", 400)} {
		t.Run(raw, func(t *testing.T) {
			input := []byte(`{"exit_code":` + raw + `}`)
			var check SealCheck
			if err := DecodeStrict(input, &check); err != nil {
				t.Fatalf("decode integer: %v", err)
			}
			if raw == "null" {
				if check.ExitCode != nil {
					t.Fatal("unknown exit code became a value")
				}
			} else if check.ExitCode == nil || string(*check.ExitCode) != raw {
				t.Fatal("exit code lost precision")
			}
			data, err := json.Marshal(check)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil || string(fields["exit_code"]) != raw {
				t.Fatalf("integer was changed or quoted: %s %v", data, err)
			}
		})
	}
}

func TestSealExitCodeRejectsNonIntegerJSON(t *testing.T) {
	for _, raw := range []string{`"0"`, `"9223372036854775808"`, "0.0", "1e3", "true", "[]", "{}", "01", "+1", "NaN"} {
		var check SealCheck
		if DecodeStrict([]byte(`{"exit_code":`+raw+`}`), &check) == nil {
			t.Fatalf("accepted non-integer %s", raw)
		}
	}
	for _, raw := range []string{"", "1.5", "1e3", "01", "null", "CANARY_PRIVATE_ERROR"} {
		n := SealExitCode(raw)
		if _, err := json.Marshal(SealCheck{ExitCode: &n}); err == nil {
			t.Fatalf("serialized invalid integer %q", raw)
		}
	}
}

func TestCollectSealExitCodesPersistAndReloadExactly(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	rawValues := []string{"0", "9007199254740993", "9223372036854775808", "-9223372036854775809", strings.Repeat("9", 400)}
	runs := []any{}
	for i, value := range rawValues {
		run := sealFixture()["runs"].([]any)[0].(map[string]any)
		run["run_id"] = "private-run-" + string(rune('a'+i))
		check := run["checks"].([]any)[0].(map[string]any)
		check["exit_code"] = json.Number(value)
		if value != "0" {
			check["passed"] = false
			run["required_checks_pass"] = false
			run["mechanical_result"] = "fail"
		}
		runs = append(runs, run)
	}
	fixture["runs"] = runs
	writeJSON(t, file, fixture)
	m := Manager{Root: filepath.Join(t.TempDir(), "state")}
	snap.Experiment.Config.SchemaVersion = 1
	experiment, _, err := m.Init(context.Background(), snap.Experiment.Config, snap.Experiment.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	now := experiment.StartedAt.Add(2 * time.Hour)
	receipt, err := m.Collect(context.Background(), experiment.ID, now)
	if err != nil || !receipt.Complete || !receipt.Committed || receipt.EventsAdded != len(rawValues) || receipt.Durability != "confirmed" {
		t.Fatalf("collect: %+v %v", receipt, err)
	}
	// Reopen the private store through a new Manager to exercise the ledger
	// decoder as well as the exporter decoder.
	reopened := Manager{Root: m.Root}
	loaded, err := reopened.Load(experiment.ID)
	if err != nil || len(loaded.Events) != len(rawValues) {
		t.Fatalf("reload: %d events, %v", len(loaded.Events), err)
	}
	seen := map[string]bool{}
	for _, event := range loaded.Events {
		if event.Seal == nil || len(event.Seal.Checks) != 1 || event.Seal.Checks[0].ExitCode == nil {
			t.Fatal("stored check missing")
		}
		seen[string(*event.Seal.Checks[0].ExitCode)] = true
	}
	journal, _, err := reopened.stores(experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(journal.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range rawValues {
		if !seen[value] || !bytes.Contains(data, []byte(`"exit_code":`+value+`,`)) {
			t.Fatalf("exact numeric value not preserved: %s", value)
		}
	}
	receipt, err = reopened.Collect(context.Background(), experiment.ID, now.Add(time.Hour))
	if err != nil || !receipt.Complete || receipt.EventsAdded != 0 || receipt.Duplicates != len(rawValues) {
		t.Fatalf("repeat collect: %+v %v", receipt, err)
	}
	if _, err := reopened.Load(experiment.ID); err != nil {
		t.Fatalf("reload after deduplication: %v", err)
	}
	requirePrivate(t, string(data), loaded.Events, receipt)
}
