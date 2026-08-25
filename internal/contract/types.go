package contract

import (
	"fmt"
	"sort"
)

const (
	ObservationV1 = "eval-observation/v1"
	ObservationV2 = "eval-observation/v2"

	VersionKnownPublic   = "known-public"
	VersionUnavailable   = "unavailable"
	VersionNotApplicable = "not-applicable"
)

// Version records whether an exact public module version is known. A module
// can be used with an unavailable version; that observation remains in usage
// cohorts and is excluded only from version cohorts.
type Version struct {
	Status string  `json:"status"`
	Value  *string `json:"value"`
}

// Extension is a locally allowlisted, versioned metrics or task-effect
// envelope. Values intentionally remain generic so analysis can flatten new
// registered extensions without changing the observation envelope.
type Extension struct {
	SchemaVersion string         `json:"schema_version"`
	Values        map[string]any `json:"values"`
}

// Module is the module-neutral v2 identity and optional metrics record.
type Module struct {
	Used    bool       `json:"used"`
	Version Version    `json:"version"`
	Metrics *Extension `json:"metrics"`
}

// Observation is the normalized model used by validation and analysis. v1
// rows are converted to this shape while retaining SchemaVersion. Source and
// Line identify the original JSONL row and are never serialized.
type Observation struct {
	SchemaVersion string               `json:"schema_version"`
	ObservationID string               `json:"observation_id"`
	TaskID        string               `json:"task_id"`
	Revision      int                  `json:"revision"`
	Supersedes    *string              `json:"supersedes"`
	TerminalOn    string               `json:"terminal_on"`
	RecordedOn    string               `json:"recorded_on"`
	Population    string               `json:"population"`
	TaskType      string               `json:"task_type"`
	Agent         *string              `json:"agent"`
	Model         *string              `json:"model"`
	HostOS        *string              `json:"host_os"`
	Modules       map[string]Module    `json:"modules"`
	Outcome       map[string]any       `json:"outcome"`
	TaskEffects   map[string]Extension `json:"task_effects"`

	Source string `json:"-"`
	Line   int    `json:"-"`
}

// Draft is the only input shape accepted by observe. Core supplies all
// identity, revision, and recorded-on fields.
type Draft struct {
	TaskID      *string              `json:"task_id,omitempty"`
	TerminalOn  string               `json:"terminal_on"`
	Population  string               `json:"population"`
	TaskType    string               `json:"task_type"`
	Agent       *string              `json:"agent"`
	Model       *string              `json:"model"`
	HostOS      *string              `json:"host_os"`
	Modules     map[string]Module    `json:"modules"`
	Outcome     map[string]any       `json:"outcome"`
	TaskEffects map[string]Extension `json:"task_effects"`
}

// BuildObservation adds Core-owned fields to a validated draft.
func (d *Draft) BuildObservation(observationID, taskID string, revision int, supersedes *string, recordedOn string) *Observation {
	return &Observation{
		SchemaVersion: ObservationV2,
		ObservationID: observationID,
		TaskID:        taskID,
		Revision:      revision,
		Supersedes:    supersedes,
		TerminalOn:    d.TerminalOn,
		RecordedOn:    recordedOn,
		Population:    d.Population,
		TaskType:      d.TaskType,
		Agent:         d.Agent,
		Model:         d.Model,
		HostOS:        d.HostOS,
		Modules:       d.Modules,
		Outcome:       d.Outcome,
		TaskEffects:   d.TaskEffects,
	}
}

// Issue is a machine-readable validation finding. Source and Line are filled
// when an issue is attached to a JSONL row.
type Issue struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Source        string `json:"source,omitempty"`
	Line          int    `json:"line,omitempty"`
	ObservationID string `json:"observation_id,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
}

func (i Issue) Error() string {
	where := ""
	if i.Source != "" {
		where = i.Source
		if i.Line > 0 {
			where += fmt.Sprintf(":%d", i.Line)
		}
		where += ": "
	}
	return where + i.Code + ": " + i.Message
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.TaskID != right.TaskID {
			return left.TaskID < right.TaskID
		}
		if left.ObservationID != right.ObservationID {
			return left.ObservationID < right.ObservationID
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}

// ParsedRow retains a row even when strict decoding or row validation fails.
// Index is zero-based physical order; Line is one-based source line number.
type ParsedRow struct {
	Index       int          `json:"index"`
	Line        int          `json:"line"`
	Source      string       `json:"source,omitempty"`
	Raw         []byte       `json:"-"`
	Observation *Observation `json:"-"`
	Issues      []Issue      `json:"issues,omitempty"`

	// Identity hints are populated only after strict JSON decoding succeeds and
	// each hinted value independently satisfies its canonical bounded form. They
	// are used solely to attribute an invalid row to an existing chain; they do
	// not make the row or any of its other values trustworthy.
	TaskIDHint          string  `json:"-"`
	ObservationIDHint   string  `json:"-"`
	RevisionHint        *int    `json:"-"`
	SupersedesHint      *string `json:"-"`
	SupersedesHintKnown bool    `json:"-"`
}

func (r ParsedRow) Valid() bool { return r.Observation != nil && len(r.Issues) == 0 }

// LogValidation separates valid rows from rows excluded by structural,
// semantic, or revision-chain failures.
type LogValidation struct {
	Rows              []ParsedRow    `json:"-"`
	ValidObservations []*Observation `json:"-"`
	Issues            []Issue        `json:"issues,omitempty"`
	InvalidRowCount   int            `json:"invalid_rows"`
	InvalidChainCount int            `json:"invalid_chains"`
	ExcludedRowCount  int            `json:"excluded_rows"`
}

func (v LogValidation) Valid() bool {
	return v.InvalidRowCount == 0 && v.InvalidChainCount == 0 && len(v.Issues) == 0
}

// Latest returns the latest revision of each valid chain, optionally filtered
// by the latest revision's population. An empty population selects both real
// and synthetic rows. Population filtering happens after revision selection so
// an older real revision cannot survive a newer synthetic correction.
func (v LogValidation) Latest(population string) []*Observation {
	latest := make(map[string]*Observation)
	for _, observation := range v.ValidObservations {
		if current := latest[observation.TaskID]; current == nil || observation.Revision > current.Revision {
			latest[observation.TaskID] = observation
		}
	}
	result := make([]*Observation, 0, len(latest))
	for _, observation := range latest {
		if population != "" && observation.Population != population {
			continue
		}
		result = append(result, observation)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TaskID != result[j].TaskID {
			return result[i].TaskID < result[j].TaskID
		}
		return result[i].Revision < result[j].Revision
	})
	return result
}
