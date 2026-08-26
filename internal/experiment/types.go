package experiment

import "time"

const (
	// SchemaVersion identifies rows produced by the bounded host experiment.
	SchemaVersion = "eval-experiment/v1"
	// MaxInputBytes is the maximum accepted draft or journal size.
	MaxInputBytes int64 = 64 * 1024
	// MaxRows is the fixed number of slots in one experiment journal.
	MaxRows int64 = 20
)

// Outcome is the terminal state of the primary task.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeAbandoned Outcome = "abandoned"
)

// Module records whether a module was used and the bounded effects already
// known when the task reached a terminal outcome. Version is required in JSON
// but may be null when an exact public version is unavailable.
type Module struct {
	Used                        bool    `json:"used"`
	Version                     *string `json:"version"`
	DefectsCaughtBeforeTerminal *int64  `json:"defects_caught_before_terminal,omitempty"`
	AddedUserInterventions      *int64  `json:"added_user_interventions,omitempty"`
	InteractionSeconds          *int64  `json:"interaction_seconds,omitempty"`
	NormalWorkBlocked           *bool   `json:"normal_work_blocked,omitempty"`
}

// Draft contains only facts supplied by the host after a task terminates.
// Identity, ordering, and recording time are assigned by the journal owner.
type Draft struct {
	Outcome        Outcome `json:"outcome"`
	ReworkRequired *bool   `json:"rework_required,omitempty"`
	Ward           Module  `json:"ward"`
	Seal           Module  `json:"seal"`
}

// Row is the canonical stored representation of a validated Draft.
type Row struct {
	SchemaVersion  string  `json:"schema_version"`
	Slot           int64   `json:"slot"`
	RecordedAt     string  `json:"recorded_at"`
	Outcome        Outcome `json:"outcome"`
	ReworkRequired *bool   `json:"rework_required,omitempty"`
	Ward           Module  `json:"ward"`
	Seal           Module  `json:"seal"`
}

// NewRow validates draft and constructs a row for slot at recordedAt. The
// timestamp is normalized to canonical UTC RFC3339 with nanosecond precision.
func NewRow(draft Draft, slot int64, recordedAt time.Time) (Row, error) {
	if err := ValidateDraft(draft); err != nil {
		return Row{}, err
	}

	row := Row{
		SchemaVersion:  SchemaVersion,
		Slot:           slot,
		RecordedAt:     recordedAt.UTC().Format(time.RFC3339Nano),
		Outcome:        draft.Outcome,
		ReworkRequired: draft.ReworkRequired,
		Ward:           draft.Ward,
		Seal:           draft.Seal,
	}
	if err := ValidateRow(row); err != nil {
		return Row{}, err
	}
	return row, nil
}
