package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validDraftJSON = `{
  "terminal_on":"2026-08-25",
  "population":"real",
  "task_type":"backend-feature",
  "agent":"codex",
  "model":null,
  "host_os":"darwin",
  "modules":{
    "spec":{"used":true,"version":{"status":"unavailable","value":null},"metrics":null},
    "ward":{"used":false,"version":{"status":"not-applicable","value":null},"metrics":null}
  },
  "outcome":{"status":"completed","user_interventions":0,"module_interaction_time":null,"rework_required":false},
  "task_effects":{}
}`

func TestDecodeJSONStrict(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{
		"duplicate top level": `{"a":1,"a":2}`,
		"duplicate nested":    `{"a":{"b":1,"b":2}}`,
		"non finite":          `{"a":NaN}`,
		"trailing":            `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeJSONStrict([]byte(input)); err == nil {
				t.Fatalf("DecodeJSONStrict(%q) unexpectedly succeeded", input)
			}
		})
	}
	if _, err := DecodeJSONStrict([]byte(`{"a":[1,true,null,{"b":"c"}]}`)); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
}

func TestParseDraftAndObservationWithUnavailableUsedVersion(t *testing.T) {
	t.Parallel()
	draft, err := ParseDraft([]byte(validDraftJSON))
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if got := draft.Modules["spec"].Version.Status; got != VersionUnavailable {
		t.Fatalf("spec version status = %q, want %q", got, VersionUnavailable)
	}
	observation := draft.BuildObservation(
		"20000000-0000-4000-8000-000000000011",
		"10000000-0000-4000-8000-000000000011",
		1,
		nil,
		"2026-08-25",
	)
	data, err := CanonicalJSON(observation)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRow(data)
	if err != nil {
		t.Fatalf("ParseRow: %v\n%s", err, data)
	}
	if !parsed.Modules["spec"].Used || parsed.Modules["spec"].Version.Value != nil {
		t.Fatalf("used unavailable module was not preserved: %#v", parsed.Modules["spec"])
	}
	withEffect := strings.Replace(validDraftJSON, `"task_effects":{}`, `"task_effects":{"completion":{"schema_version":"completion-effect/v1","values":{"terminal_completion_invalidated":false}}}`, 1)
	if _, err := ParseDraft([]byte(withEffect)); err != nil {
		t.Fatalf("registered task effect rejected: %v", err)
	}
}

func TestDraftRejectsGeneratedFieldsAndInvalidVersionState(t *testing.T) {
	t.Parallel()
	generated := strings.Replace(validDraftJSON, `"terminal_on"`, `"schema_version":"eval-observation/v2","terminal_on"`, 1)
	if _, err := ParseDraft([]byte(generated)); err == nil {
		t.Fatal("draft with schema_version unexpectedly accepted")
	}
	badVersion := strings.Replace(validDraftJSON, `"used":false,"version":{"status":"not-applicable"`, `"used":false,"version":{"status":"unavailable"`, 1)
	if _, err := ParseDraft([]byte(badVersion)); err == nil {
		t.Fatal("unused module with unavailable status unexpectedly accepted")
	}
	closedEffect := strings.Replace(validDraftJSON, `"task_effects":{}`, `"task_effects":{"completion":{"schema_version":"completion-effect/v1","values":{"terminal_completion_invalidated":false,"notes":"forbidden"}}}`, 1)
	if _, err := ParseDraft([]byte(closedEffect)); err == nil {
		t.Fatal("task effect extension with extra field unexpectedly accepted")
	}
	unknownMetrics := strings.Replace(validDraftJSON, `"modules":{`, `"modules":{"new-module":{"used":true,"version":{"status":"unavailable","value":null},"metrics":{"schema_version":"new-metrics/v1","values":{}}},`, 1)
	if _, err := ParseDraft([]byte(unknownMetrics)); err == nil || !strings.Contains(err.Error(), "metrics_not_allowlisted") {
		t.Fatalf("unknown metrics extension error = %v", err)
	}
	unknownEffect := strings.Replace(validDraftJSON, `"task_effects":{}`, `"task_effects":{"new-effect":{"schema_version":"new-effect/v1","values":{}}}`, 1)
	if _, err := ParseDraft([]byte(unknownEffect)); err == nil || !strings.Contains(err.Error(), "effect_not_allowlisted") {
		t.Fatalf("unknown task effect extension error = %v", err)
	}
}

func TestV1ReadCompatibilityAndNormalization(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "valid", "observations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rows := ParseJSONL("observations.jsonl", data)
	validation := ValidateLog(rows)
	if !validation.Valid() {
		t.Fatalf("v1 fixture rejected: %#v", validation.Issues)
	}
	if got, want := len(validation.Latest("synthetic")), 5; got != want {
		t.Fatalf("latest synthetic observations = %d, want %d", got, want)
	}
	var foundSpec bool
	for _, observation := range validation.ValidObservations {
		module := observation.Modules["spec"]
		if module.Used {
			foundSpec = true
			if module.Version.Status != VersionKnownPublic || module.Metrics == nil || module.Metrics.SchemaVersion != "spec-metrics/v1" {
				t.Fatalf("v1 Spec module not normalized: %#v", module)
			}
		}
	}
	if !foundSpec {
		t.Fatal("expected at least one used Spec observation")
	}
}

func TestSemanticRelationships(t *testing.T) {
	t.Parallel()
	observation := validObservation(1)
	observation.RecordedOn = "2026-08-24"
	observation.Outcome["module_interaction_time"] = map[string]any{
		"method": "bounded-estimate", "lower_seconds": 20, "upper_seconds": 10,
	}
	observation.Outcome["user_interventions"] = 1
	observation.Modules["seal"] = Module{
		Used:    true,
		Version: Version{Status: VersionUnavailable},
		Metrics: &Extension{SchemaVersion: "seal-metrics/v1", Values: map[string]any{"added_user_interventions": 2}},
	}
	observation.TaskEffects["requirements"] = Extension{
		SchemaVersion: "requirements-effect/v1",
		Values: map[string]any{"late_material_decisions": map[string]any{
			"total": 1, "api": 2,
		}},
	}
	issues := ValidateObservation(observation)
	for _, code := range []string{"date_order", "bounded_range_order", "category_exceeds_total", "module_interventions_exceed_total"} {
		if !hasIssue(issues, code) {
			t.Errorf("missing %s in %#v", code, issues)
		}
	}
}

func TestValidateLogRejectsMixedChainForkAndDuplicateID(t *testing.T) {
	t.Parallel()

	mixedRoot := validObservation(1)
	mixedRoot.SchemaVersion = ObservationV1
	mixedSecond := validObservation(2)
	mixedSecond.Supersedes = pointer(mixedRoot.ObservationID)
	mixed := ValidateObservations([]*Observation{mixedRoot, mixedSecond})
	if !hasIssue(mixed.Issues, "mixed_schema_chain") || mixed.InvalidChainCount != 1 {
		t.Fatalf("mixed chain result: %#v", mixed)
	}

	forkRoot := validObservation(1)
	forkSecond := validObservation(2)
	forkSecond.Supersedes = pointer(forkRoot.ObservationID)
	forkThird := validObservation(3)
	forkThird.Supersedes = pointer(forkRoot.ObservationID)
	fork := ValidateObservations([]*Observation{forkRoot, forkSecond, forkThird})
	if !hasIssue(fork.Issues, "revision_fork") || !hasIssue(fork.Issues, "non_immediate_predecessor") {
		t.Fatalf("fork result: %#v", fork.Issues)
	}

	duplicateA := validObservation(1)
	duplicateB := validObservation(1)
	duplicateB.TaskID = "10000000-0000-4000-8000-000000000099"
	duplicateB.ObservationID = duplicateA.ObservationID
	duplicate := ValidateObservations([]*Observation{duplicateA, duplicateB})
	if !hasIssue(duplicate.Issues, "duplicate_observation_id") || duplicate.InvalidChainCount != 2 {
		t.Fatalf("duplicate result: %#v", duplicate)
	}
}

func TestParseJSONLRetainsBlankAndMalformedRows(t *testing.T) {
	t.Parallel()
	rows := ParseJSONL("test.jsonl", []byte("{}\n\n{\"broken\":\n"))
	if got, want := len(rows), 3; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	validation := ValidateLog(rows)
	if validation.InvalidRowCount != 3 || validation.ExcludedRowCount != 3 {
		t.Fatalf("validation counts: %#v", validation)
	}
	if !hasIssue(validation.Issues, "blank_row") || !hasIssue(validation.Issues, "invalid_json") {
		t.Fatalf("issues: %#v", validation.Issues)
	}
}

func TestParseJSONLRejectsMissingTerminalNewline(t *testing.T) {
	t.Parallel()
	row, err := CanonicalJSON(validObservation(1))
	if err != nil {
		t.Fatal(err)
	}
	validation := ValidateLog(ParseJSONL("missing-newline.jsonl", row))
	if validation.Valid() || validation.InvalidRowCount != 1 || validation.InvalidChainCount != 1 {
		t.Fatalf("validation = %#v", validation)
	}
	if !hasIssue(validation.Issues, "missing_terminal_newline") {
		t.Fatalf("issues = %#v", validation.Issues)
	}
}

func TestLatestFiltersPopulationAfterRevisionSelection(t *testing.T) {
	t.Parallel()
	root := validObservation(1)
	root.Population = "real"
	correction := validObservation(2)
	correction.Supersedes = pointer(root.ObservationID)
	correction.Population = "synthetic"
	validation := ValidateObservations([]*Observation{root, correction})
	if !validation.Valid() {
		t.Fatalf("validation = %#v", validation)
	}
	if got := validation.Latest("real"); len(got) != 0 {
		t.Fatalf("latest real = %#v, want none", got)
	}
	if got := validation.Latest("synthetic"); len(got) != 1 || got[0].Revision != 2 {
		t.Fatalf("latest synthetic = %#v, want revision 2", got)
	}
}

func TestCorrectionRecordedDateCannotPrecedeImmediatePredecessor(t *testing.T) {
	t.Parallel()
	root := validObservation(1)
	root.RecordedOn = "2026-08-25"
	correction := validObservation(2)
	correction.RecordedOn = "2026-08-24"
	correction.TerminalOn = "2026-08-24"
	correction.Supersedes = pointer(root.ObservationID)
	validation := ValidateObservations([]*Observation{root, correction})
	if validation.Valid() || validation.InvalidChainCount != 1 || !hasIssue(validation.Issues, "recorded_date_order") {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestInvalidSemanticAndChainFixtures(t *testing.T) {
	t.Parallel()
	semantic, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "invalid", "semantic", "v2-terminal-after-recorded.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRow(semantic); err == nil || !strings.Contains(err.Error(), "date_order") {
		t.Fatalf("semantic fixture error = %v, want date_order", err)
	}

	forkData, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "invalid", "chains", "v2-fork.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	fork := ValidateLog(ParseJSONL("v2-fork.jsonl", forkData))
	if !hasIssue(fork.Issues, "revision_fork") || fork.InvalidChainCount != 1 || fork.ExcludedRowCount != 3 {
		t.Fatalf("fork fixture validation: %#v", fork)
	}
}

func TestSchemaInvalidCorrectionHintInvalidatesMatchingChain(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "invalid", "chains", "v2-schema-invalid-correction.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rows := ParseJSONL("v2-schema-invalid-correction.jsonl", data)
	if got, want := len(rows), 2; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	invalid := rows[1]
	if invalid.Observation != nil || invalid.TaskIDHint != "10000000-0000-4000-8000-000000000051" || invalid.ObservationIDHint != "20000000-0000-4000-8000-000000000052" {
		t.Fatalf("invalid row hints = %#v", invalid)
	}
	if invalid.RevisionHint == nil || *invalid.RevisionHint != 2 || invalid.SupersedesHint == nil || *invalid.SupersedesHint != "20000000-0000-4000-8000-000000000051" {
		t.Fatalf("invalid row revision hints = %#v", invalid)
	}
	validation := ValidateLog(rows)
	if validation.InvalidRowCount != 1 || validation.InvalidChainCount != 1 || validation.ExcludedRowCount != 2 {
		t.Fatalf("validation counts = %#v", validation)
	}
	if len(validation.ValidObservations) != 0 || len(validation.Latest("")) != 0 {
		t.Fatalf("schema-invalid correction left task in analysis: %#v", validation.ValidObservations)
	}
}

func TestUnparseableRowRemainsUnattributed(t *testing.T) {
	t.Parallel()
	root, err := CanonicalJSON(validObservation(1))
	if err != nil {
		t.Fatal(err)
	}
	data := append(append(root, '\n'), []byte(`{"task_id":"10000000-0000-4000-8000-000000000021"`)...)
	data = append(data, '\n')
	rows := ParseJSONL("malformed-after-root.jsonl", data)
	if len(rows) != 2 || rows[1].TaskIDHint != "" || rows[1].Observation != nil {
		t.Fatalf("malformed row unexpectedly attributed: %#v", rows)
	}
	validation := ValidateLog(rows)
	if validation.InvalidRowCount != 1 || validation.InvalidChainCount != 0 || validation.ExcludedRowCount != 1 {
		t.Fatalf("validation counts = %#v", validation)
	}
	if len(validation.ValidObservations) != 1 || len(validation.Latest("")) != 1 {
		t.Fatalf("unattributed malformed row invalidated unrelated chain: %#v", validation.ValidObservations)
	}
}

func validObservation(revision int) *Observation {
	observationID := "20000000-0000-4000-8000-000000000021"
	if revision == 2 {
		observationID = "20000000-0000-4000-8000-000000000022"
	}
	if revision == 3 {
		observationID = "20000000-0000-4000-8000-000000000023"
	}
	return &Observation{
		SchemaVersion: ObservationV2,
		ObservationID: observationID,
		TaskID:        "10000000-0000-4000-8000-000000000021",
		Revision:      revision,
		TerminalOn:    "2026-08-25",
		RecordedOn:    "2026-08-25",
		Population:    "real",
		TaskType:      "backend-feature",
		Modules:       map[string]Module{},
		Outcome: map[string]any{
			"status": "completed", "user_interventions": 0,
			"module_interaction_time": nil, "rework_required": false,
		},
		TaskEffects: map[string]Extension{},
	}
}

func pointer(value string) *string { return &value }

func hasIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestCanonicalJSONIsStable(t *testing.T) {
	t.Parallel()
	value := map[string]any{"z": 1, "a": map[string]any{"z": false, "a": true}}
	first, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || string(first) != `{"a":{"a":true,"z":false},"z":1}` {
		t.Fatalf("canonical output = %s / %s", first, second)
	}
}
