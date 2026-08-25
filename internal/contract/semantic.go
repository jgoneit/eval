package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

var uuid4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidateObservation checks invariants that JSON Schema cannot express.
func ValidateObservation(observation *Observation) []Issue {
	if observation == nil {
		return []Issue{{Code: "nil_observation", Message: "observation is nil"}}
	}
	issues := make([]Issue, 0)
	if !uuid4Pattern.MatchString(observation.ObservationID) {
		issues = append(issues, observationIssue(observation, "observation_id_invalid", "observation_id must be a lowercase UUIDv4"))
	}
	if !uuid4Pattern.MatchString(observation.TaskID) {
		issues = append(issues, observationIssue(observation, "task_id_invalid", "task_id must be a lowercase UUIDv4"))
	}
	if observation.ObservationID == observation.TaskID {
		issues = append(issues, observationIssue(observation, "identity_collision", "task_id must differ from observation_id"))
	}
	terminal, terminalErr := time.Parse(time.DateOnly, observation.TerminalOn)
	recorded, recordedErr := time.Parse(time.DateOnly, observation.RecordedOn)
	if terminalErr != nil {
		issues = append(issues, observationIssue(observation, "terminal_date_invalid", terminalErr.Error()))
	}
	if recordedErr != nil {
		issues = append(issues, observationIssue(observation, "recorded_date_invalid", recordedErr.Error()))
	}
	if terminalErr == nil && recordedErr == nil && terminal.After(recorded) {
		issues = append(issues, observationIssue(observation, "date_order", "terminal_on must not be after recorded_on"))
	}
	if observation.Revision < 1 {
		issues = append(issues, observationIssue(observation, "revision_invalid", "revision must be at least 1"))
	}
	if observation.Revision == 1 && observation.Supersedes != nil {
		issues = append(issues, observationIssue(observation, "root_supersedes", "revision 1 must not supersede another observation"))
	}
	if observation.Revision > 1 && observation.Supersedes == nil {
		issues = append(issues, observationIssue(observation, "revision_missing_predecessor", "revision greater than 1 must supersede its immediate predecessor"))
	}
	if observation.Supersedes != nil && *observation.Supersedes == observation.ObservationID {
		issues = append(issues, observationIssue(observation, "self_supersedes", "observation must not supersede itself"))
	}
	for moduleID, module := range observation.Modules {
		if module.Used {
			if module.Version.Status != VersionKnownPublic && module.Version.Status != VersionUnavailable {
				issues = append(issues, observationIssue(observation, "used_version_status", fmt.Sprintf("used module %q must have known-public or unavailable version status", moduleID)))
			}
			if module.Version.Status == VersionKnownPublic && (module.Version.Value == nil || *module.Version.Value == "") {
				issues = append(issues, observationIssue(observation, "known_version_missing", fmt.Sprintf("used module %q with known-public status requires a version value", moduleID)))
			}
			if module.Version.Status == VersionUnavailable && module.Version.Value != nil {
				issues = append(issues, observationIssue(observation, "unavailable_version_value", fmt.Sprintf("used module %q with unavailable status must have a null version value", moduleID)))
			}
		} else {
			if module.Version.Status != VersionNotApplicable || module.Version.Value != nil {
				issues = append(issues, observationIssue(observation, "unused_version_status", fmt.Sprintf("unused module %q must have not-applicable/null version", moduleID)))
			}
			if module.Metrics != nil {
				issues = append(issues, observationIssue(observation, "unused_metrics", fmt.Sprintf("unused module %q must not have metrics", moduleID)))
			}
		}
	}
	issues = append(issues, validateObservationValues(observation.Outcome, observation.TaskEffects, observation.Modules)...)
	for index := range issues {
		issues[index] = attachIssue(issues[index], observation, observation.Source, observation.Line)
	}
	sortIssues(issues)
	return issues
}

func validateObservationValues(outcome map[string]any, taskEffects map[string]Extension, modules map[string]Module) []Issue {
	issues := make([]Issue, 0)
	visitBoundedTimes("outcome", outcome, &issues)
	for effectID, effect := range taskEffects {
		visitBoundedTimes("task_effects."+effectID+".values", effect.Values, &issues)
	}
	for moduleID, module := range modules {
		if module.Metrics != nil {
			visitBoundedTimes("modules."+moduleID+".metrics.values", module.Metrics.Values, &issues)
		}
	}

	if requirements, ok := taskEffects["requirements"]; ok {
		if late, ok := requirements.Values["late_material_decisions"].(map[string]any); ok {
			if total, known := integerValue(late["total"]); known {
				for _, category := range []string{"api", "authentication_authorization", "consistency_rules", "data_model", "scope", "user_behavior"} {
					if count, categoryKnown := integerValue(late[category]); categoryKnown && count > total {
						issues = append(issues, Issue{Code: "category_exceeds_total", Message: fmt.Sprintf("requirements late_material_decisions.%s (%d) exceeds total (%d)", category, count, total)})
					}
				}
			}
		}
	}
	if seal, ok := modules["seal"]; ok && seal.Metrics != nil {
		if added, addedKnown := integerValue(seal.Metrics.Values["added_user_interventions"]); addedKnown {
			if total, totalKnown := integerValue(outcome["user_interventions"]); totalKnown && added > total {
				issues = append(issues, Issue{Code: "module_interventions_exceed_total", Message: fmt.Sprintf("Seal added_user_interventions (%d) exceeds outcome user_interventions (%d)", added, total)})
			}
		}
	}
	sortIssues(issues)
	return issues
}

func visitBoundedTimes(path string, value any, issues *[]Issue) {
	switch typed := value.(type) {
	case map[string]any:
		if method, _ := typed["method"].(string); method == "bounded-estimate" {
			lower, lowerKnown := integerValue(typed["lower_seconds"])
			upper, upperKnown := integerValue(typed["upper_seconds"])
			if lowerKnown && upperKnown && lower > upper {
				*issues = append(*issues, Issue{Code: "bounded_range_order", Message: fmt.Sprintf("%s lower_seconds (%d) exceeds upper_seconds (%d)", path, lower, upper)})
			}
		}
		for key, child := range typed {
			visitBoundedTimes(path+"."+key, child, issues)
		}
	case []any:
		for index, child := range typed {
			visitBoundedTimes(path+"["+strconv.Itoa(index)+"]", child, issues)
		}
	}
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		if uint64(typed) > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case uint64:
		if typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func observationIssue(observation *Observation, code, message string) Issue {
	return Issue{
		Code:          code,
		Message:       message,
		Source:        observation.Source,
		Line:          observation.Line,
		ObservationID: observation.ObservationID,
		TaskID:        observation.TaskID,
	}
}
