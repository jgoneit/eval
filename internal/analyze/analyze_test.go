package analyze

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func stringPointer(value string) *string { return &value }

func sampleRecords() []Record {
	return []Record{
		{
			TaskID: "b", ObservationID: "2", Population: "real", TerminalOn: "2026-08-20",
			Modules:     map[string]Module{"spec": {Used: true, VersionStatus: "unavailable"}},
			Outcome:     map[string]any{"status": "completed", "user_interventions": 1, "rework_required": false, "module_interaction_time": map[string]any{"method": "bounded-estimate", "lower_seconds": 10, "upper_seconds": 20}},
			TaskEffects: map[string]any{"completion": map[string]any{"terminal_completion_invalidated": false}},
		},
		{
			TaskID: "a", ObservationID: "1", Population: "real", TerminalOn: "2026-08-19",
			Modules:     map[string]Module{"spec": {Used: false, VersionStatus: "not-applicable"}},
			Outcome:     map[string]any{"status": "failed", "user_interventions": 0, "rework_required": true, "module_interaction_time": map[string]any{"method": "measured", "seconds": 4}},
			TaskEffects: map[string]any{"completion": map[string]any{"terminal_completion_invalidated": true}},
		},
		{
			TaskID: "c", ObservationID: "3", Population: "real", TerminalOn: "2026-08-21",
			Modules:     map[string]Module{"spec": {Used: true, VersionStatus: "known-public", Version: stringPointer("0.2.0")}},
			Outcome:     map[string]any{"status": "completed", "user_interventions": nil, "rework_required": nil, "module_interaction_time": nil},
			TaskEffects: map[string]any{},
		},
	}
}

func TestVersionUnavailableIncludedInUsageOnly(t *testing.T) {
	records := sampleRecords()
	window := Window{AsOf: "2026-08-25", Through: "2026-08-25"}
	usage, err := BuildComparison(records, window, Exclusions{}, "spec", "usage", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Cohorts[1].Cohort != "used" || usage.Cohorts[1].N != 2 {
		t.Fatalf("used cohort = %#v, want n=2", usage.Cohorts[1])
	}
	versions, err := BuildComparison(records, window, Exclusions{}, "spec", "version", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if versions.Excluded.VersionUnavailable != 1 {
		t.Fatalf("version unavailable = %d, want 1", versions.Excluded.VersionUnavailable)
	}
	if versions.Excluded.VersionNotApplicable != 1 || versions.Excluded.ModuleUnassessed != 0 {
		t.Fatalf("version exclusions = %#v", versions.Excluded)
	}
	if len(versions.Cohorts) != 1 || versions.Cohorts[0].Cohort != "0.2.0" || versions.Cohorts[0].N != 1 {
		t.Fatalf("version cohorts = %#v", versions.Cohorts)
	}
}

func TestCanonicalSummaryIsByteIdentical(t *testing.T) {
	window := Window{AsOf: "2026-08-25", Through: "2026-08-25"}
	first, err := CanonicalJSON(BuildSummary(sampleRecords(), window, Exclusions{}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalJSON(BuildSummary(sampleRecords(), window, Exclusions{}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("summary output differs:\n%s\n%s", first, second)
	}
}

func TestTypedAggregationKeepsMissingAndTimeMethodsSeparate(t *testing.T) {
	summary := BuildSummary(sampleRecords(), Window{AsOf: "2026-08-25", Through: "2026-08-25"}, Exclusions{})
	if got := summary.CommonFacts.Booleans[0]; got.Rate.Denominator != 2 || got.Unavailable != 1 {
		t.Fatalf("boolean aggregate = %#v", got)
	}
	var interaction TimeAggregate
	for _, candidate := range summary.CommonFacts.Times {
		if candidate.Key == "outcome.module_interaction_time" {
			interaction = candidate
		}
	}
	if interaction.Measured.Known != 1 || interaction.Bounded.Known != 1 || interaction.Unavailable != 1 {
		t.Fatalf("time aggregate = %#v", interaction)
	}
}

func TestCategoricalMetricsAreRetained(t *testing.T) {
	rows := []Record{{
		TaskID: "a", ObservationID: "1",
		Modules: map[string]Module{"seal": {Used: true, VersionStatus: "known-public", Version: stringPointer("0.2.0"), Metrics: map[string]any{"completion_decision": "accepted"}}},
		Outcome: map[string]any{"status": "completed"}, TaskEffects: map[string]any{},
	}}
	summary := BuildSummary(rows, Window{AsOf: "2026-08-25", Through: "2026-08-25"}, Exclusions{})
	categories := summary.Modules[0].UsedMetrics.Categories
	if len(categories) != 1 || categories[0].Key != "module_metrics.seal.completion_decision" || categories[0].Values[0].Name != "accepted" {
		t.Fatalf("categories = %#v", categories)
	}
}

func TestAllUnavailableFactsKeepTypedDenominators(t *testing.T) {
	summary := BuildSummary([]Record{{
		TaskID: "a", ObservationID: "1", Modules: map[string]Module{},
		Outcome:     map[string]any{"status": "completed", "user_interventions": nil, "rework_required": nil, "module_interaction_time": nil},
		TaskEffects: map[string]any{},
	}}, Window{AsOf: "2026-08-25", Through: "2026-08-25"}, Exclusions{})
	findBoolean := func(key string) *BooleanAggregate {
		for index := range summary.CommonFacts.Booleans {
			if summary.CommonFacts.Booleans[index].Key == key {
				return &summary.CommonFacts.Booleans[index]
			}
		}
		return nil
	}
	findCount := func(key string) *CountAggregate {
		for index := range summary.CommonFacts.Counts {
			if summary.CommonFacts.Counts[index].Key == key {
				return &summary.CommonFacts.Counts[index]
			}
		}
		return nil
	}
	if value := findBoolean("outcome.rework_required"); value == nil || value.Unavailable != 1 || value.Rate.Denominator != 0 {
		t.Fatalf("rework aggregate = %#v", value)
	}
	if value := findCount("outcome.user_interventions"); value == nil || value.Unavailable != 1 || value.Known != 0 {
		t.Fatalf("intervention aggregate = %#v", value)
	}
	if len(summary.CommonFacts.Times) != 1 || summary.CommonFacts.Times[0].Unavailable != 1 {
		t.Fatalf("time aggregates = %#v", summary.CommonFacts.Times)
	}
}

func TestSelectedVersionOrderAndExclusionAreExplicit(t *testing.T) {
	makeRecord := func(taskID, version string) Record {
		return Record{
			TaskID: taskID, ObservationID: taskID,
			Modules:     map[string]Module{"spec": {Used: true, VersionStatus: "known-public", Version: stringPointer(version)}},
			Outcome:     map[string]any{"status": "completed", "user_interventions": nil, "rework_required": nil, "module_interaction_time": nil},
			TaskEffects: map[string]any{},
		}
	}
	comparison, err := BuildComparison([]Record{
		makeRecord("a", "3.0.0"), makeRecord("b", "2.0.0"), makeRecord("c", "1.0.0"),
	}, Window{AsOf: "2026-08-25", Through: "2026-08-25"}, Exclusions{}, "spec", "version", "2.0.0", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Cohorts) != 2 || comparison.Cohorts[0].Cohort != "2.0.0" || comparison.Cohorts[1].Cohort != "1.0.0" {
		t.Fatalf("cohort order = %#v", comparison.Cohorts)
	}
	if comparison.Excluded.OutsideSelectedVersion != 1 {
		t.Fatalf("outside selected = %d", comparison.Excluded.OutsideSelectedVersion)
	}
}

func TestMarkdownDisclosesTypedFactsAndAllExclusions(t *testing.T) {
	summary := BuildSummary(sampleRecords(), Window{AsOf: "2026-08-25", Through: "2026-08-25"}, Exclusions{
		InvalidRows: 1, InvalidChains: 2, InvalidOrChainRows: 6, SyntheticRows: 3, SupersededRows: 4, OutsideDateWindow: 5,
	})
	markdown := string(RenderSummaryMarkdown(summary))
	for _, expected := range []string{
		"Excluded invalid rows: 1", "Excluded invalid chains: 2", "Excluded synthetic rows: 3",
		"Excluded invalid or chain rows: 6",
		"Excluded superseded rows: 4", "Excluded outside date window: 5",
		"outcome.user_interventions", "outcome.module_interaction_time",
		"#### unused cohort", "#### used cohort", "#### used-only module metrics",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown missing %q:\n%s", expected, markdown)
		}
	}
}

func TestDeterministicOutputGoldenDigests(t *testing.T) {
	window := Window{AsOf: "2026-08-25", Through: "2026-08-25"}
	summary := BuildSummary(sampleRecords(), window, Exclusions{})
	summaryJSON, err := CanonicalJSON(summary)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := BuildComparison(sampleRecords(), window, Exclusions{}, "spec", "version", "", "")
	if err != nil {
		t.Fatal(err)
	}
	comparisonJSON, err := CanonicalJSON(comparison)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"summary-json":     "c2569fb4496c50736c84d046b107908f51a1a24a10dd436260d43f22fe3a214b",
		"comparison-json":  "91608fa396e35ccad248bb5e1509c7fd9b77b58c7890722735ed47f74da984ae",
		"summary-markdown": "bcafab29eddb828e88250473b6a3efdde7253fec817eb03db7c51ac9c2345a54",
	}
	outputs := map[string][]byte{
		"summary-json":     summaryJSON,
		"comparison-json":  comparisonJSON,
		"summary-markdown": RenderSummaryMarkdown(summary),
	}
	for name, output := range outputs {
		digest := sha256.Sum256(output)
		got := hex.EncodeToString(digest[:])
		if got != want[name] {
			t.Errorf("%s digest = %s, want %s", name, got, want[name])
		}
	}
}
