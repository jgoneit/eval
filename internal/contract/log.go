package contract

import (
	"fmt"
	"time"
)

// ValidateLog validates global identity and per-task revision chains. Any row
// failure invalidates its task chain; analysis must not retain a correction
// whose predecessor was excluded.
func ValidateLog(rows []ParsedRow) LogValidation {
	result := LogValidation{Rows: rows}
	taskRows := make(map[string][]ParsedRow)
	invalidTasks := make(map[string]bool)
	observationRows := make(map[string][]ParsedRow)

	for _, row := range rows {
		if len(row.Issues) > 0 || row.Observation == nil {
			result.InvalidRowCount++
			result.Issues = append(result.Issues, row.Issues...)
		}
		if row.Observation == nil {
			if row.TaskIDHint != "" {
				invalidTasks[row.TaskIDHint] = true
			}
			continue
		}
		observation := row.Observation
		taskRows[observation.TaskID] = append(taskRows[observation.TaskID], row)
		observationRows[observation.ObservationID] = append(observationRows[observation.ObservationID], row)
		if len(row.Issues) > 0 {
			invalidTasks[observation.TaskID] = true
		}
	}
	// A structurally invalid row may lack a trustworthy task_id while retaining
	// a canonical observation_id or predecessor link. Attribute those hints only
	// through already decoded observations; never treat the invalid row itself as
	// a chain member.
	for _, row := range rows {
		if row.Observation != nil || len(row.Issues) == 0 {
			continue
		}
		for _, hintedID := range []string{row.ObservationIDHint, valueOrEmpty(row.SupersedesHint)} {
			if hintedID == "" {
				continue
			}
			for _, matched := range observationRows[hintedID] {
				invalidTasks[matched.Observation.TaskID] = true
			}
		}
	}

	for observationID, duplicates := range observationRows {
		if len(duplicates) < 2 {
			continue
		}
		for _, row := range duplicates {
			invalidTasks[row.Observation.TaskID] = true
		}
		first := duplicates[0]
		result.Issues = append(result.Issues, attachIssue(Issue{
			Code:    "duplicate_observation_id",
			Message: fmt.Sprintf("observation_id %q occurs %d times", observationID, len(duplicates)),
		}, first.Observation, first.Source, first.Line))
	}

	for taskID, chain := range taskRows {
		if issues := validateChain(chain, observationRows); len(issues) > 0 {
			invalidTasks[taskID] = true
			result.Issues = append(result.Issues, issues...)
		}
	}

	for taskID := range invalidTasks {
		if _, exists := taskRows[taskID]; exists {
			result.InvalidChainCount++
		}
	}
	for _, row := range rows {
		if row.Valid() && !invalidTasks[row.Observation.TaskID] {
			result.ValidObservations = append(result.ValidObservations, row.Observation)
		}
	}
	result.ExcludedRowCount = len(rows) - len(result.ValidObservations)
	sortIssues(result.Issues)
	return result
}

// ValidateObservations is a convenience adapter for in-memory observations.
func ValidateObservations(observations []*Observation) LogValidation {
	rows := make([]ParsedRow, 0, len(observations))
	for index, observation := range observations {
		row := ParsedRow{Index: index, Line: index + 1, Observation: observation}
		if observation != nil {
			row.Source = observation.Source
			if observation.Line > 0 {
				row.Line = observation.Line
			}
			row.Issues = ValidateObservation(observation)
			row.TaskIDHint = observation.TaskID
			row.ObservationIDHint = observation.ObservationID
			row.RevisionHint = &observation.Revision
			row.SupersedesHint = observation.Supersedes
			row.SupersedesHintKnown = true
		} else {
			row.Issues = []Issue{{Code: "nil_observation", Message: "observation is nil", Line: row.Line}}
		}
		rows = append(rows, row)
	}
	return ValidateLog(rows)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateChain(chain []ParsedRow, global map[string][]ParsedRow) []Issue {
	if len(chain) == 0 {
		return nil
	}
	issues := make([]Issue, 0)
	taskID := chain[0].Observation.TaskID
	versions := make(map[string]struct{})
	roots := 0
	revisions := make(map[int]ParsedRow)
	positions := make(map[string]int)
	supersededBy := make(map[string][]ParsedRow)

	for position, row := range chain {
		observation := row.Observation
		versions[observation.SchemaVersion] = struct{}{}
		positions[observation.ObservationID] = position
		if observation.Revision == 1 && observation.Supersedes == nil {
			roots++
		}
		if previous, exists := revisions[observation.Revision]; exists {
			issues = append(issues, attachIssue(Issue{
				Code:    "duplicate_revision",
				Message: fmt.Sprintf("task %q has revision %d more than once (first at line %d)", taskID, observation.Revision, previous.Line),
			}, observation, row.Source, row.Line))
		} else {
			revisions[observation.Revision] = row
		}
		if observation.Supersedes != nil {
			supersededBy[*observation.Supersedes] = append(supersededBy[*observation.Supersedes], row)
		}
	}
	if len(versions) > 1 {
		first := chain[0]
		issues = append(issues, attachIssue(Issue{
			Code:    "mixed_schema_chain",
			Message: "v1 and v2 observations cannot be mixed in one correction chain",
		}, first.Observation, first.Source, first.Line))
	}
	if roots != 1 {
		first := chain[0]
		issues = append(issues, attachIssue(Issue{
			Code:    "root_count",
			Message: fmt.Sprintf("task %q must have exactly one root, got %d", taskID, roots),
		}, first.Observation, first.Source, first.Line))
	}
	for target, children := range supersededBy {
		if len(children) > 1 {
			first := children[0]
			issues = append(issues, attachIssue(Issue{
				Code:    "revision_fork",
				Message: fmt.Sprintf("observation %q is superseded by %d revisions", target, len(children)),
			}, first.Observation, first.Source, first.Line))
		}
	}

	for position, row := range chain {
		observation := row.Observation
		expectedRevision := position + 1
		if observation.Revision != expectedRevision {
			issues = append(issues, attachIssue(Issue{
				Code:    "revision_order",
				Message: fmt.Sprintf("physical chain position %d requires revision %d, got %d", position+1, expectedRevision, observation.Revision),
			}, observation, row.Source, row.Line))
		}
		if position == 0 {
			continue
		}
		if observation.Supersedes == nil {
			continue
		}
		targetID := *observation.Supersedes
		if targetID == observation.ObservationID {
			issues = append(issues, attachIssue(Issue{Code: "self_supersedes", Message: "observation must not supersede itself"}, observation, row.Source, row.Line))
			continue
		}
		targetRows := global[targetID]
		if len(targetRows) == 0 {
			issues = append(issues, attachIssue(Issue{Code: "predecessor_missing", Message: fmt.Sprintf("supersedes target %q does not exist", targetID)}, observation, row.Source, row.Line))
			continue
		}
		if targetRows[0].Observation.TaskID != taskID {
			issues = append(issues, attachIssue(Issue{Code: "cross_task_predecessor", Message: fmt.Sprintf("supersedes target %q belongs to another task", targetID)}, observation, row.Source, row.Line))
			continue
		}
		targetPosition := positions[targetID]
		if targetPosition >= position {
			issues = append(issues, attachIssue(Issue{Code: "forward_supersedes", Message: fmt.Sprintf("supersedes target %q is not earlier in physical order", targetID)}, observation, row.Source, row.Line))
		}
		immediate := chain[position-1].Observation
		currentRecorded, currentErr := time.Parse(time.DateOnly, observation.RecordedOn)
		predecessorRecorded, predecessorErr := time.Parse(time.DateOnly, immediate.RecordedOn)
		if currentErr == nil && predecessorErr == nil && currentRecorded.Before(predecessorRecorded) {
			issues = append(issues, attachIssue(Issue{
				Code:    "recorded_date_order",
				Message: fmt.Sprintf("revision %d recorded_on must not precede immediate predecessor", observation.Revision),
			}, observation, row.Source, row.Line))
		}
		if targetID != immediate.ObservationID {
			issues = append(issues, attachIssue(Issue{
				Code:    "non_immediate_predecessor",
				Message: fmt.Sprintf("revision %d must supersede immediate predecessor %q, got %q", observation.Revision, immediate.ObservationID, targetID),
			}, observation, row.Source, row.Line))
		}
	}
	return issues
}
