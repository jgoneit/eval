package experiment

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeDraftModuleRelationships(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unused with version",
			input: `{"outcome":"completed","ward":{"used":false,"version":"0.1.0"},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "unused with defects",
			input: `{"outcome":"completed","ward":{"used":false,"version":null,"defects_caught_before_terminal":0},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "unused with interventions",
			input: `{"outcome":"completed","ward":{"used":false,"version":null,"added_user_interventions":0},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "unused with interaction",
			input: `{"outcome":"completed","ward":{"used":false,"version":null,"interaction_seconds":0},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "unused with false blocked effect",
			input: `{"outcome":"completed","ward":{"used":false,"version":null,"normal_work_blocked":false},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "negative defects",
			input: `{"outcome":"completed","ward":{"used":true,"version":null,"defects_caught_before_terminal":-1},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "negative interventions",
			input: `{"outcome":"completed","ward":{"used":true,"version":null,"added_user_interventions":-1},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "negative interaction",
			input: `{"outcome":"completed","ward":{"used":true,"version":null,"interaction_seconds":-1},"seal":{"used":false,"version":null}}`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeDraft(strings.NewReader(test.input)); err == nil {
				t.Fatal("DecodeDraft() succeeded, want module relationship rejection")
			}
		})
	}
}

func TestDecodeDraftAllowsUsedWithUnavailableVersion(t *testing.T) {
	t.Parallel()

	draft, err := DecodeDraft(strings.NewReader(validDraftJSON))
	if err != nil {
		t.Fatalf("DecodeDraft() error = %v", err)
	}
	if !draft.Ward.Used || draft.Ward.Version != nil {
		t.Fatalf("ward = %+v, want used with null version", draft.Ward)
	}
}

func TestDecodeDraftOutcomeEnum(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{"completed", "failed", "abandoned"} {
		input := strings.Replace(validDraftJSON, `"completed"`, `"`+outcome+`"`, 1)
		if _, err := DecodeDraft(strings.NewReader(input)); err != nil {
			t.Errorf("DecodeDraft(outcome=%q) error = %v", outcome, err)
		}
	}
	input := strings.Replace(validDraftJSON, `"completed"`, `"running"`, 1)
	if _, err := DecodeDraft(strings.NewReader(input)); err == nil {
		t.Fatal("DecodeDraft(outcome=running) succeeded")
	}
}

func TestDecodeDraftPreservesOptionalEffects(t *testing.T) {
	t.Parallel()

	input := `{"outcome":"failed","rework_required":false,` +
		`"ward":{"used":true,"version":"0.1.0","defects_caught_before_terminal":0,` +
		`"added_user_interventions":2,"interaction_seconds":3,"normal_work_blocked":false},` +
		`"seal":{"used":true,"version":null}}`
	draft, err := DecodeDraft(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeDraft() error = %v", err)
	}
	if draft.ReworkRequired == nil || *draft.ReworkRequired {
		t.Fatalf("rework_required = %v, want present false", draft.ReworkRequired)
	}
	if draft.Ward.DefectsCaughtBeforeTerminal == nil || *draft.Ward.DefectsCaughtBeforeTerminal != 0 {
		t.Fatalf("defects = %v, want present zero", draft.Ward.DefectsCaughtBeforeTerminal)
	}
	if draft.Ward.AddedUserInterventions == nil || *draft.Ward.AddedUserInterventions != 2 {
		t.Fatalf("interventions = %v, want 2", draft.Ward.AddedUserInterventions)
	}
	if draft.Ward.InteractionSeconds == nil || *draft.Ward.InteractionSeconds != 3 {
		t.Fatalf("interaction = %v, want 3", draft.Ward.InteractionSeconds)
	}
	if draft.Ward.NormalWorkBlocked == nil || *draft.Ward.NormalWorkBlocked {
		t.Fatalf("normal_work_blocked = %v, want present false", draft.Ward.NormalWorkBlocked)
	}
	if !draft.Seal.Used || draft.Seal.Version != nil {
		t.Fatalf("seal = %+v, want used with unavailable version", draft.Seal)
	}
}

func TestNewRowNormalizesAndValidatesTimestamp(t *testing.T) {
	t.Parallel()

	draft := validDraft(t)
	zone := time.FixedZone("KST", 9*60*60)
	recorded := time.Date(2026, 8, 26, 12, 34, 56, 120_000_000, zone)
	row, err := NewRow(draft, 1, recorded)
	if err != nil {
		t.Fatalf("NewRow() error = %v", err)
	}
	if row.RecordedAt != "2026-08-26T03:34:56.12Z" {
		t.Fatalf("recorded_at = %q, want canonical UTC", row.RecordedAt)
	}

	invalid := []string{
		"2026-08-26T03:34:56+00:00",
		"2026-08-26T03:34:56.120Z",
		"2026-02-30T03:34:56Z",
		"2026-08-26 03:34:56Z",
	}
	for _, timestamp := range invalid {
		row.RecordedAt = timestamp
		if err := ValidateRow(row); err == nil {
			t.Errorf("ValidateRow(recorded_at=%q) succeeded", timestamp)
		}
	}
}

func validDraft(t *testing.T) Draft {
	t.Helper()
	draft, err := DecodeDraft(strings.NewReader(validDraftJSON))
	if err != nil {
		t.Fatalf("DecodeDraft(valid) error = %v", err)
	}
	return draft
}
