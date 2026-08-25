package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

var metricsExtensionByModule = map[string]string{
	"seal": "seal-metrics/v1",
	"spec": "spec-metrics/v1",
	"ward": "ward-metrics/v1",
}

var effectExtensionByName = map[string]string{
	"completion":   "completion-effect/v1",
	"requirements": "requirements-effect/v1",
	"security":     "security-effect/v1",
}

type issueError struct {
	issues []Issue
}

func (e *issueError) Error() string {
	parts := make([]string, 0, len(e.issues))
	for _, issue := range e.issues {
		parts = append(parts, issue.Error())
	}
	return strings.Join(parts, "; ")
}

// DecodeObservation performs strict JSON decoding, structural schema
// validation, v1 normalization, extension validation, and row semantics.
func DecodeObservation(data []byte) (*Observation, error) {
	observation, _, issues := decodeObservation(data)
	if len(issues) > 0 {
		return nil, &issueError{issues: issues}
	}
	return observation, nil
}

// ParseRow is an alias for DecodeObservation for callers that validate a
// single unframed JSON object.
func ParseRow(data []byte) (*Observation, error) {
	return DecodeObservation(data)
}

type identityHints struct {
	taskID          string
	observationID   string
	revision        *int
	supersedes      *string
	supersedesKnown bool
}

func decodeObservation(data []byte) (*Observation, identityHints, []Issue) {
	value, err := DecodeJSONStrict(data)
	if err != nil {
		return nil, identityHints{}, []Issue{{Code: "invalid_json", Message: err.Error()}}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, identityHints{}, []Issue{{Code: "schema_invalid", Message: "observation must be a JSON object"}}
	}
	hints := extractIdentityHints(object)
	version, ok := object["schema_version"].(string)
	if !ok {
		return nil, hints, []Issue{{Code: "schema_invalid", Message: "schema_version must be a string"}}
	}
	compiled, err := getSchemas()
	if err != nil {
		return nil, hints, []Issue{{Code: "schema_unavailable", Message: err.Error()}}
	}
	schema := compiled.observation[version]
	if schema == nil {
		return nil, hints, []Issue{{Code: "unsupported_schema", Message: fmt.Sprintf("unsupported schema_version %q", version)}}
	}
	if err := validateWith(schema, value); err != nil {
		return nil, hints, []Issue{{Code: "schema_invalid", Message: err.Error()}}
	}

	var observation *Observation
	switch version {
	case ObservationV2:
		observation = new(Observation)
		if err := json.Unmarshal(data, observation); err != nil {
			return nil, hints, []Issue{{Code: "decode_failed", Message: err.Error()}}
		}
	case ObservationV1:
		observation, err = normalizeV1(data)
		if err != nil {
			return nil, hints, []Issue{{Code: "decode_failed", Message: err.Error()}}
		}
	}

	issues := validateRegisteredExtensions(observation.Modules, observation.TaskEffects)
	issues = append(issues, ValidateObservation(observation)...)
	return observation, hints, issues
}

func extractIdentityHints(object map[string]any) identityHints {
	var hints identityHints
	if taskID, ok := object["task_id"].(string); ok && uuid4Pattern.MatchString(taskID) {
		hints.taskID = taskID
	}
	if observationID, ok := object["observation_id"].(string); ok && uuid4Pattern.MatchString(observationID) {
		hints.observationID = observationID
	}
	if revision, ok := integerValue(object["revision"]); ok && revision >= 1 && revision <= 1000000 {
		value := int(revision)
		hints.revision = &value
	}
	if supersedes, exists := object["supersedes"]; exists {
		if supersedes == nil {
			hints.supersedesKnown = true
		} else if predecessor, ok := supersedes.(string); ok && uuid4Pattern.MatchString(predecessor) {
			hints.supersedes = &predecessor
			hints.supersedesKnown = true
		}
	}
	return hints
}

// ParseDraft validates the generated-field-free input accepted by observe.
func ParseDraft(data []byte) (*Draft, error) {
	value, err := DecodeJSONStrict(data)
	if err != nil {
		return nil, &issueError{issues: []Issue{{Code: "invalid_json", Message: err.Error()}}}
	}
	compiled, err := getSchemas()
	if err != nil {
		return nil, err
	}
	if err := validateWith(compiled.draft, value); err != nil {
		return nil, &issueError{issues: []Issue{{Code: "schema_invalid", Message: err.Error()}}}
	}
	var draft Draft
	if err := json.Unmarshal(data, &draft); err != nil {
		return nil, err
	}
	issues := validateRegisteredExtensions(draft.Modules, draft.TaskEffects)
	issues = append(issues, validateObservationValues(draft.Outcome, draft.TaskEffects, draft.Modules)...)
	if len(issues) > 0 {
		return nil, &issueError{issues: issues}
	}
	return &draft, nil
}

// ParseJSONL retains invalid rows so callers can report exact exclusion
// counts. A single final line terminator is framing, not a blank row.
func ParseJSONL(source string, data []byte) []ParsedRow {
	hasTerminalNewline := len(data) == 0 || data[len(data)-1] == '\n'
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	rows := make([]ParsedRow, 0, len(lines))
	for index, line := range lines {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		row := ParsedRow{
			Index:  index,
			Line:   index + 1,
			Source: source,
			Raw:    bytes.Clone(line),
		}
		if len(bytes.TrimSpace(line)) == 0 {
			row.Issues = []Issue{{Code: "blank_row", Message: "blank JSONL rows are not allowed", Source: source, Line: row.Line}}
			rows = append(rows, row)
			continue
		}
		observation, hints, issues := decodeObservation(line)
		row.Observation = observation
		row.TaskIDHint = hints.taskID
		row.ObservationIDHint = hints.observationID
		row.RevisionHint = hints.revision
		row.SupersedesHint = hints.supersedes
		row.SupersedesHintKnown = hints.supersedesKnown
		if observation != nil {
			observation.Source = source
			observation.Line = row.Line
		}
		for issueIndex := range issues {
			issues[issueIndex] = attachIssue(issues[issueIndex], observation, source, row.Line)
		}
		if index == len(lines)-1 && !hasTerminalNewline {
			issues = append(issues, attachIssue(Issue{
				Code: "missing_terminal_newline", Message: "JSONL data must end with a newline",
			}, observation, source, row.Line))
		}
		row.Issues = issues
		rows = append(rows, row)
	}
	return rows
}

func attachIssue(issue Issue, observation *Observation, source string, line int) Issue {
	issue.Source = source
	issue.Line = line
	if observation != nil {
		issue.ObservationID = observation.ObservationID
		issue.TaskID = observation.TaskID
	}
	return issue
}

func validateRegisteredExtensions(modules map[string]Module, taskEffects map[string]Extension) []Issue {
	compiled, err := getSchemas()
	if err != nil {
		return []Issue{{Code: "schema_unavailable", Message: err.Error()}}
	}
	issues := make([]Issue, 0)
	for moduleID, module := range modules {
		if module.Metrics == nil {
			continue
		}
		expected, allowed := metricsExtensionByModule[moduleID]
		if !allowed {
			issues = append(issues, Issue{Code: "metrics_not_allowlisted", Message: fmt.Sprintf("module %q has no registered metrics extension", moduleID)})
			continue
		}
		if module.Metrics.SchemaVersion != expected {
			issues = append(issues, Issue{Code: "metrics_schema_mismatch", Message: fmt.Sprintf("module %q requires %q, got %q", moduleID, expected, module.Metrics.SchemaVersion)})
			continue
		}
		if err := validateExtension(compiled, *module.Metrics); err != nil {
			issues = append(issues, Issue{Code: "metrics_invalid", Message: fmt.Sprintf("module %q: %v", moduleID, err)})
		}
	}
	for effectID, effect := range taskEffects {
		expected, allowed := effectExtensionByName[effectID]
		if !allowed {
			issues = append(issues, Issue{Code: "effect_not_allowlisted", Message: fmt.Sprintf("task effect %q is not registered", effectID)})
			continue
		}
		if effect.SchemaVersion != expected {
			issues = append(issues, Issue{Code: "effect_schema_mismatch", Message: fmt.Sprintf("task effect %q requires %q, got %q", effectID, expected, effect.SchemaVersion)})
			continue
		}
		if err := validateExtension(compiled, effect); err != nil {
			issues = append(issues, Issue{Code: "effect_invalid", Message: fmt.Sprintf("task effect %q: %v", effectID, err)})
		}
	}
	sortIssues(issues)
	return issues
}

func validateExtension(compiled *compiledSchemas, extension Extension) error {
	schema := compiled.extension[extension.SchemaVersion]
	if schema == nil {
		return fmt.Errorf("extension schema %q is not registered", extension.SchemaVersion)
	}
	data, err := CanonicalJSON(extension)
	if err != nil {
		return err
	}
	value, err := DecodeJSONStrict(data)
	if err != nil {
		return err
	}
	return validateWith(schema, value)
}

type v1Module struct {
	Used    bool    `json:"used"`
	Version *string `json:"version"`
}

type v1Observation struct {
	SchemaVersion string                     `json:"schema_version"`
	ObservationID string                     `json:"observation_id"`
	TaskID        string                     `json:"task_id"`
	Revision      int                        `json:"revision"`
	Supersedes    *string                    `json:"supersedes"`
	TerminalOn    string                     `json:"terminal_on"`
	RecordedOn    string                     `json:"recorded_on"`
	Population    string                     `json:"population"`
	TaskType      string                     `json:"task_type"`
	Agent         *string                    `json:"agent"`
	Model         *string                    `json:"model"`
	HostOS        *string                    `json:"host_os"`
	Modules       map[string]v1Module        `json:"modules"`
	Outcome       map[string]any             `json:"outcome"`
	TaskEffects   map[string]map[string]any  `json:"task_effects"`
	ModuleMetrics map[string]json.RawMessage `json:"module_metrics"`
}

func normalizeV1(data []byte) (*Observation, error) {
	var legacy v1Observation
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	modules := make(map[string]Module, len(legacy.Modules))
	for moduleID, identity := range legacy.Modules {
		version := Version{Status: VersionNotApplicable, Value: nil}
		if identity.Used {
			version = Version{Status: VersionKnownPublic, Value: identity.Version}
		}
		module := Module{Used: identity.Used, Version: version}
		if raw, ok := legacy.ModuleMetrics[moduleID]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			values := make(map[string]any)
			if err := json.Unmarshal(raw, &values); err != nil {
				return nil, err
			}
			schemaVersion, registered := metricsExtensionByModule[moduleID]
			if !registered {
				return nil, fmt.Errorf("legacy metrics module %q is not registered", moduleID)
			}
			module.Metrics = &Extension{SchemaVersion: schemaVersion, Values: values}
		}
		modules[moduleID] = module
	}
	taskEffects := make(map[string]Extension, len(legacy.TaskEffects))
	for effectID, values := range legacy.TaskEffects {
		schemaVersion, registered := effectExtensionByName[effectID]
		if !registered {
			return nil, fmt.Errorf("legacy task effect %q is not registered", effectID)
		}
		taskEffects[effectID] = Extension{SchemaVersion: schemaVersion, Values: values}
	}
	return &Observation{
		SchemaVersion: legacy.SchemaVersion,
		ObservationID: legacy.ObservationID,
		TaskID:        legacy.TaskID,
		Revision:      legacy.Revision,
		Supersedes:    legacy.Supersedes,
		TerminalOn:    legacy.TerminalOn,
		RecordedOn:    legacy.RecordedOn,
		Population:    legacy.Population,
		TaskType:      legacy.TaskType,
		Agent:         legacy.Agent,
		Model:         legacy.Model,
		HostOS:        legacy.HostOS,
		Modules:       modules,
		Outcome:       legacy.Outcome,
		TaskEffects:   taskEffects,
	}, nil
}
