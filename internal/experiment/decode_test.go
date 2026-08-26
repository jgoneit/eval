package experiment

import (
	"errors"
	"strings"
	"testing"
)

const validDraftJSON = `{"outcome":"completed","ward":{"used":true,"version":null},"seal":{"used":false,"version":null}}`

func TestDecodeDraftRejectsDuplicateKeysAtEveryDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "top level",
			input: `{"outcome":"completed","outcome":"failed","ward":{"used":true,"version":null},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "module",
			input: `{"outcome":"completed","ward":{"used":true,"used":false,"version":null},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "unknown nested object",
			input: `{"outcome":"completed","ward":{"used":true,"version":null,"unknown":{"value":1,"value":2}},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "object nested in array",
			input: `{"outcome":"completed","ward":{"used":true,"version":null,"unknown":[{"value":1,"value":2}]},"seal":{"used":false,"version":null}}`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeDraft(strings.NewReader(test.input))
			if err == nil || !strings.Contains(err.Error(), "duplicate object key") {
				t.Fatalf("DecodeDraft() error = %v, want duplicate-key rejection", err)
			}
		})
	}
}

func TestDecodeDraftRejectsTrailingValues(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{` {}`, ` null`, ` []`} {
		_, err := DecodeDraft(strings.NewReader(validDraftJSON + suffix))
		if err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
			t.Errorf("DecodeDraft(%q) error = %v, want trailing-value rejection", suffix, err)
		}
	}
}

func TestDecodeDraftRejectsUnknownAndMissingFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unknown top level",
			input: `{"outcome":"completed","ward":{"used":true,"version":null},"seal":{"used":false,"version":null},"unknown":true}`,
		},
		{
			name:  "unknown module field",
			input: `{"outcome":"completed","ward":{"used":true,"version":null,"unknown":true},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "missing outcome",
			input: `{"ward":{"used":true,"version":null},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "missing ward",
			input: `{"outcome":"completed","seal":{"used":false,"version":null}}`,
		},
		{
			name:  "missing seal",
			input: `{"outcome":"completed","ward":{"used":true,"version":null}}`,
		},
		{
			name:  "missing used",
			input: `{"outcome":"completed","ward":{"version":null},"seal":{"used":false,"version":null}}`,
		},
		{
			name:  "missing version",
			input: `{"outcome":"completed","ward":{"used":true},"seal":{"used":false,"version":null}}`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeDraft(strings.NewReader(test.input)); err == nil {
				t.Fatal("DecodeDraft() succeeded, want strict shape rejection")
			}
		})
	}
}

func TestDecodeDraftRejectsNonIntegerRepresentations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "fractional defects", field: "defects_caught_before_terminal", value: "1.0"},
		{name: "exponent defects", field: "defects_caught_before_terminal", value: "1e0"},
		{name: "fractional interventions", field: "added_user_interventions", value: "2.5"},
		{name: "exponent interaction", field: "interaction_seconds", value: "6E1"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := `{"outcome":"completed","ward":{"used":true,"version":null,"` +
				test.field + `":` + test.value + `},"seal":{"used":false,"version":null}}`
			if _, err := DecodeDraft(strings.NewReader(input)); err == nil {
				t.Fatal("DecodeDraft() succeeded, want int64 lexical rejection")
			}
		})
	}
}

func TestDecodeDraftInputLimit(t *testing.T) {
	t.Parallel()

	exact := validDraftJSON + strings.Repeat(" ", int(MaxInputBytes)-len(validDraftJSON))
	if got := int64(len(exact)); got != MaxInputBytes {
		t.Fatalf("test input length = %d, want %d", got, MaxInputBytes)
	}
	if _, err := DecodeDraft(strings.NewReader(exact)); err != nil {
		t.Fatalf("DecodeDraft() at limit error = %v", err)
	}

	over := exact + " "
	_, err := DecodeDraft(strings.NewReader(over))
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("DecodeDraft() over limit error = %v, want ErrInputTooLarge", err)
	}
}
