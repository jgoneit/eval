// Package evaluation implements private, evidence-linked observational studies.
// It never controls the evaluated task or treats a tool verdict as ground truth.
package evaluation

import "time"

const Schema = "eval-ledger/v1"
const MaxBytes int64 = 64 << 20

type Config struct {
	SchemaVersion        int      `json:"schema_version"`
	Repositories         []string `json:"repositories"`
	SessionDirs          []string `json:"session_dirs"`
	WardDirs             []string `json:"ward_dirs"`
	SealBinary           string   `json:"seal_binary"`
	SelfRepository       string   `json:"self_repository,omitempty"`
	ExportTimeoutSeconds int      `json:"export_timeout_seconds,omitempty"`
}

type Experiment struct {
	Schema    string    `json:"schema"`
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	Config    Config    `json:"config"`
}

// Binding is stored only in private.jsonl, never in the fact ledger or reports.
type Binding struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	Reference string `json:"reference,omitempty"`
}

type Task struct {
	ID              string    `json:"id"`
	Revision        int       `json:"revision"`
	RepositoryID    string    `json:"repository_id"`
	ParentID        string    `json:"parent_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	Profile         string    `json:"profile"`
	Model           string    `json:"model"`
	Kind            string    `json:"kind"`
	ExclusionReason string    `json:"exclusion_reason,omitempty"`
}

type SealCheck struct {
	Index           int           `json:"index"`
	Required        bool          `json:"required"`
	Passed          bool          `json:"passed"`
	TimedOut        bool          `json:"timed_out"`
	ExitCode        *SealExitCode `json:"exit_code"`
	DurationSeconds *float64      `json:"duration_seconds"`
}

type Completion struct {
	State       string  `json:"state"`
	CompletedAt *string `json:"completed_at"`
}

type SealFacts struct {
	MechanicalResult         string      `json:"mechanical_result"`
	RequiredChecksPass       bool        `json:"required_checks_pass"`
	ScopePass                bool        `json:"scope_pass"`
	SourceStableDuringChecks bool        `json:"source_stable_during_checks"`
	ScopeViolationCount      int         `json:"scope_violation_count"`
	Checks                   []SealCheck `json:"checks"`
	CompletionRecord         Completion  `json:"completion_record"`
}

type Event struct {
	ID             string     `json:"id"`
	SourceID       string     `json:"source_id"`
	Revision       int        `json:"revision"`
	RepositoryID   string     `json:"repository_id,omitempty"`
	HintTaskID     string     `json:"hint_task_id,omitempty"`
	Module         string     `json:"module"`
	Version        string     `json:"version"`
	Source         string     `json:"source"`
	ObservedAt     time.Time  `json:"observed_at"`
	SourceTime     *time.Time `json:"source_time"`
	Outcome        string     `json:"outcome"`
	Stage          string     `json:"stage,omitempty"`
	DurationMS     *float64   `json:"duration_ms"`
	RuleID         string     `json:"rule_id,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	GapCode        string     `json:"gap_code,omitempty"`
	Fingerprint    string     `json:"fingerprint"`
	EvidenceSHA256 string     `json:"evidence_sha256,omitempty"`
	Seal           *SealFacts `json:"seal,omitempty"`
}

type Issue struct {
	SourceID string `json:"source_id"`
	Code     string `json:"code"`
}

type Receipt struct {
	Schema       string    `json:"schema"`
	ExperimentID string    `json:"experiment_id"`
	CollectedAt  time.Time `json:"collected_at"`
	TasksAdded   int       `json:"tasks_added"`
	TasksUpdated int       `json:"tasks_updated"`
	EventsAdded  int       `json:"events_added"`
	Duplicates   int       `json:"duplicates"`
	Unmatched    int       `json:"unmatched"`
	Complete     bool      `json:"complete"`
	Issues       []Issue   `json:"issues"`
	Committed    bool      `json:"committed"`
	Durability   string    `json:"durability"`
}

type Verdict string

const (
	Confirmed     Verdict = "confirmed"
	Denied        Verdict = "denied"
	Unknown       Verdict = "unknown"
	NotApplicable Verdict = "not_applicable"
)

type TaskReview struct {
	TaskID                 string  `json:"task_id"`
	Revision               int     `json:"revision"`
	Eligibility            string  `json:"eligibility"`
	ExclusionReason        string  `json:"exclusion_reason,omitempty"`
	Outcome                string  `json:"outcome"`
	TaskType               string  `json:"task_type"`
	TerminalRecordObserved Verdict `json:"terminal_record_observed"`
}

type IncidentReview struct {
	ID                      string   `json:"id"`
	Revision                int      `json:"revision"`
	Active                  bool     `json:"active"`
	TaskID                  string   `json:"task_id"`
	EventIDs                []string `json:"event_ids"`
	Module                  string   `json:"module"`
	ExpectedAction          string   `json:"expected_action"`
	ActualAction            string   `json:"actual_action"`
	Correctness             Verdict  `json:"correctness"`
	AdditionalValue         Verdict  `json:"additional_value"`
	UnnecessaryIntervention Verdict  `json:"unnecessary_intervention"`
	Rework                  Verdict  `json:"rework"`
	Severity                string   `json:"severity"`
	Evidence                string   `json:"evidence"`
}

type Comparison struct {
	ID              string   `json:"id"`
	Revision        int      `json:"revision"`
	BaselineTaskID  string   `json:"baseline_task_id"`
	ToolTaskID      string   `json:"tool_task_id"`
	Module          string   `json:"module"`
	Comparable      bool     `json:"comparable"`
	BaselineSuccess Verdict  `json:"baseline_success"`
	ToolSuccess     Verdict  `json:"tool_success"`
	BaselineSeconds *float64 `json:"baseline_seconds"`
	ToolSeconds     *float64 `json:"tool_seconds"`
}

type Review struct {
	Schema       string           `json:"schema"`
	ExperimentID string           `json:"experiment_id"`
	Reviewer     string           `json:"reviewer"`
	Tasks        []TaskReview     `json:"tasks"`
	Incidents    []IncidentReview `json:"incidents"`
	Comparisons  []Comparison     `json:"comparisons"`
}

type Batch struct {
	Schema  string    `json:"schema"`
	At      time.Time `json:"at"`
	Tasks   []Task    `json:"tasks,omitempty"`
	Events  []Event   `json:"events,omitempty"`
	Receipt *Receipt  `json:"receipt,omitempty"`
	Review  *Review   `json:"review,omitempty"`
}

type PrivateBatch struct {
	Schema     string      `json:"schema"`
	Experiment *Experiment `json:"experiment,omitempty"`
	Bindings   []Binding   `json:"bindings,omitempty"`
}

type Snapshot struct {
	Experiment Experiment
	Bindings   []Binding
	Tasks      map[string]Task
	Events     map[string]Event
	Receipts   []Receipt
	Reviews    []Review
}
