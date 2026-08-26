package experiment

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var publicVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// ValidateDraft checks the cross-field experiment contract independently of
// JSON decoding so callers constructing typed drafts receive the same rules.
func ValidateDraft(draft Draft) error {
	if !draft.Outcome.valid() {
		return fmt.Errorf("unsupported outcome %q", draft.Outcome)
	}
	if err := validateModule("ward", draft.Ward); err != nil {
		return err
	}
	if err := validateModule("seal", draft.Seal); err != nil {
		return err
	}
	return nil
}

// ValidateRow checks one typed stored row. Journal ordering is validated by
// ValidateJournal.
func ValidateRow(row Row) error {
	if row.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", row.SchemaVersion)
	}
	if row.Slot < 1 || row.Slot > MaxRows {
		return fmt.Errorf("slot must be between 1 and %d", MaxRows)
	}
	if err := validateRecordedAt(row.RecordedAt); err != nil {
		return err
	}
	return ValidateDraft(Draft{
		Outcome:        row.Outcome,
		ReworkRequired: row.ReworkRequired,
		Ward:           row.Ward,
		Seal:           row.Seal,
	})
}

func (outcome Outcome) valid() bool {
	switch outcome {
	case OutcomeCompleted, OutcomeFailed, OutcomeAbandoned:
		return true
	default:
		return false
	}
}

func validateModule(name string, module Module) error {
	if module.Version != nil {
		if len(*module.Version) == 0 || len(*module.Version) > 128 ||
			!publicVersionPattern.MatchString(*module.Version) {
			return fmt.Errorf("%s.version is not a bounded public identifier", name)
		}
	}

	if !module.Used {
		if module.Version != nil {
			return fmt.Errorf("%s.version must be null when used is false", name)
		}
		if hasModuleEffect(module) {
			return fmt.Errorf("%s effects are forbidden when used is false", name)
		}
		return nil
	}

	for field, value := range map[string]*int64{
		"defects_caught_before_terminal": module.DefectsCaughtBeforeTerminal,
		"added_user_interventions":       module.AddedUserInterventions,
		"interaction_seconds":            module.InteractionSeconds,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s.%s must be non-negative", name, field)
		}
	}
	return nil
}

func hasModuleEffect(module Module) bool {
	return module.DefectsCaughtBeforeTerminal != nil ||
		module.AddedUserInterventions != nil ||
		module.InteractionSeconds != nil ||
		module.NormalWorkBlocked != nil
}

func validateRecordedAt(value string) error {
	if !strings.HasSuffix(value, "Z") {
		return errors.New("recorded_at must use canonical UTC RFC3339")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("recorded_at must use canonical UTC RFC3339: %w", err)
	}
	if parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return errors.New("recorded_at must use canonical UTC RFC3339")
	}
	return nil
}
