package analyze

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	SummarySchema    = "eval-summary/v1"
	ComparisonSchema = "eval-comparison/v1"
)

// Record is the module-neutral input accepted by the deterministic analysis
// layer. Contract-version normalization happens before this boundary.
type Record struct {
	TaskID        string
	ObservationID string
	SchemaVersion string
	TerminalOn    string
	Population    string
	TaskType      string
	Modules       map[string]Module
	Outcome       map[string]any
	TaskEffects   map[string]any
}

type Module struct {
	Used          bool
	VersionStatus string
	Version       *string
	Metrics       map[string]any
}

type Exclusions struct {
	InvalidRows        int `json:"invalid_rows"`
	InvalidChains      int `json:"invalid_chains"`
	InvalidOrChainRows int `json:"invalid_or_chain_rows"`
	SyntheticRows      int `json:"synthetic_rows"`
	SupersededRows     int `json:"superseded_rows"`
	OutsideDateWindow  int `json:"outside_date_window"`
}

type Window struct {
	From    *string `json:"from"`
	Through string  `json:"through"`
	AsOf    string  `json:"as_of"`
}

type Rational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

type BooleanAggregate struct {
	Key         string   `json:"key"`
	True        int64    `json:"true"`
	False       int64    `json:"false"`
	Unavailable int64    `json:"unavailable"`
	Rate        Rational `json:"rate"`
}

type CountAggregate struct {
	Key         string   `json:"key"`
	Sum         int64    `json:"sum"`
	Known       int64    `json:"known"`
	Unavailable int64    `json:"unavailable"`
	Mean        Rational `json:"mean"`
}

type MeasuredAggregate struct {
	SumSeconds int64    `json:"sum_seconds"`
	Known      int64    `json:"known"`
	Mean       Rational `json:"mean_seconds"`
}

type BoundedAggregate struct {
	LowerSumSeconds int64    `json:"lower_sum_seconds"`
	UpperSumSeconds int64    `json:"upper_sum_seconds"`
	Known           int64    `json:"known"`
	LowerMean       Rational `json:"lower_mean_seconds"`
	UpperMean       Rational `json:"upper_mean_seconds"`
}

type TimeAggregate struct {
	Key         string            `json:"key"`
	Measured    MeasuredAggregate `json:"measured"`
	Bounded     BoundedAggregate  `json:"bounded_estimate"`
	Unavailable int64             `json:"unavailable"`
}

type CategoryAggregate struct {
	Key         string       `json:"key"`
	Values      []NamedCount `json:"values"`
	Unavailable int64        `json:"unavailable"`
}

type AggregateSet struct {
	Booleans   []BooleanAggregate  `json:"booleans"`
	Categories []CategoryAggregate `json:"categories"`
	Counts     []CountAggregate    `json:"counts"`
	Times      []TimeAggregate     `json:"times"`
}

type NamedCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type VersionCount struct {
	Version string `json:"version"`
	Count   int64  `json:"count"`
}

type CohortSummary struct {
	Cohort string       `json:"cohort"`
	N      int64        `json:"n"`
	Facts  AggregateSet `json:"facts"`
}

type ModuleSummary struct {
	ModuleID            string          `json:"module_id"`
	UsedN               int64           `json:"used_n"`
	UnusedN             int64           `json:"unused_n"`
	UnassessedN         int64           `json:"unassessed_n"`
	KnownVersionN       int64           `json:"known_version_n"`
	UnavailableVersionN int64           `json:"unavailable_version_n"`
	Versions            []VersionCount  `json:"versions"`
	UsageCohorts        []CohortSummary `json:"usage_cohorts"`
	UsedMetrics         AggregateSet    `json:"used_metrics"`
}

type Dataset struct {
	IncludedTasks int64      `json:"included_real_tasks"`
	Exclusions    Exclusions `json:"excluded"`
}

type Summary struct {
	SchemaVersion string          `json:"schema_version"`
	Window        Window          `json:"window"`
	Dataset       Dataset         `json:"dataset"`
	Outcomes      []NamedCount    `json:"outcomes"`
	CommonFacts   AggregateSet    `json:"common_facts"`
	Modules       []ModuleSummary `json:"modules"`
	Limitations   []string        `json:"limitations"`
}

type ComparisonCohort struct {
	Cohort        string       `json:"cohort"`
	N             int64        `json:"n"`
	Facts         AggregateSet `json:"facts"`
	ModuleMetrics AggregateSet `json:"module_metrics"`
}

type ComparisonExclusions struct {
	InvalidRows            int   `json:"invalid_rows"`
	InvalidChains          int   `json:"invalid_chains"`
	InvalidOrChainRows     int   `json:"invalid_or_chain_rows"`
	SyntheticRows          int   `json:"synthetic_rows"`
	SupersededRows         int   `json:"superseded_rows"`
	OutsideDateWindow      int   `json:"outside_date_window"`
	ModuleUnassessed       int64 `json:"module_unassessed"`
	VersionNotApplicable   int64 `json:"version_not_applicable"`
	VersionUnavailable     int64 `json:"version_unavailable"`
	OutsideSelectedVersion int64 `json:"outside_selected_versions"`
}

type Comparison struct {
	SchemaVersion  string               `json:"schema_version"`
	Window         Window               `json:"window"`
	ModuleID       string               `json:"module_id"`
	By             string               `json:"by"`
	Cohorts        []ComparisonCohort   `json:"cohorts"`
	Excluded       ComparisonExclusions `json:"excluded"`
	Interpretation string               `json:"interpretation"`
}

func BuildSummary(records []Record, window Window, exclusions Exclusions) Summary {
	records = sortedRecords(records)
	statusCounts := map[string]int64{"abandoned": 0, "completed": 0, "failed": 0}
	commonFacts := make([]map[string]any, 0, len(records))
	moduleIDs := make(map[string]struct{})
	for _, record := range records {
		if status, _ := record.Outcome["status"].(string); status != "" {
			statusCounts[status]++
		}
		commonFacts = append(commonFacts, common(record))
		for id := range record.Modules {
			moduleIDs[id] = struct{}{}
		}
	}

	outcomes := make([]NamedCount, 0, len(statusCounts))
	for _, status := range []string{"abandoned", "completed", "failed"} {
		outcomes = append(outcomes, NamedCount{Name: status, Count: statusCounts[status]})
	}

	ids := sortedKeys(moduleIDs)
	modules := make([]ModuleSummary, 0, len(ids))
	for _, id := range ids {
		modules = append(modules, summarizeModule(id, records))
	}

	return Summary{
		SchemaVersion: SummarySchema,
		Window:        window,
		Dataset:       Dataset{IncludedTasks: int64(len(records)), Exclusions: exclusions},
		Outcomes:      outcomes,
		CommonFacts:   aggregateWithKinds(commonFacts, inferKindsWithBase(commonFacts, commonFactKinds())),
		Modules:       modules,
		Limitations: []string{
			"caller-selected observations are not randomized",
			"comparisons are observational and do not establish causality",
			"missing values are excluded only from their typed denominator",
			"no product, retention, promotion, removal, or release decision is generated",
		},
	}
}

func BuildComparison(records []Record, window Window, exclusions Exclusions, moduleID, by, left, right string) (Comparison, error) {
	records = sortedRecords(records)
	result := Comparison{
		SchemaVersion: ComparisonSchema,
		Window:        window,
		ModuleID:      moduleID,
		By:            by,
		Excluded: ComparisonExclusions{
			InvalidRows: exclusions.InvalidRows, InvalidChains: exclusions.InvalidChains,
			InvalidOrChainRows: exclusions.InvalidOrChainRows,
			SyntheticRows:      exclusions.SyntheticRows, SupersededRows: exclusions.SupersededRows,
			OutsideDateWindow: exclusions.OutsideDateWindow,
		},
		Interpretation: "observational-only; no causal or product decision",
	}

	type bucket struct {
		facts   []map[string]any
		metrics []map[string]any
	}
	buckets := map[string]*bucket{}
	for _, record := range records {
		module, ok := record.Modules[moduleID]
		if !ok {
			result.Excluded.ModuleUnassessed++
			continue
		}
		var cohort string
		switch by {
		case "usage":
			if module.Used {
				cohort = "used"
			} else {
				cohort = "unused"
			}
		case "version":
			if !module.Used {
				result.Excluded.VersionNotApplicable++
				continue
			}
			if module.VersionStatus != "known-public" || module.Version == nil {
				result.Excluded.VersionUnavailable++
				continue
			}
			cohort = *module.Version
			if left != "" && cohort != left && cohort != right {
				result.Excluded.OutsideSelectedVersion++
				continue
			}
		default:
			return Comparison{}, fmt.Errorf("unsupported comparison axis %q", by)
		}
		b := buckets[cohort]
		if b == nil {
			b = &bucket{}
			buckets[cohort] = b
		}
		b.facts = append(b.facts, common(record))
		b.metrics = append(b.metrics, prefixed("module_metrics."+moduleID, module.Metrics))
	}

	if by == "version" && (left == "") != (right == "") {
		return Comparison{}, fmt.Errorf("left and right versions must be provided together")
	}
	labels := make([]string, 0, len(buckets))
	for label := range buckets {
		labels = append(labels, label)
	}
	if by == "usage" {
		labels = []string{"unused", "used"}
	} else if left != "" {
		labels = []string{left, right}
	} else {
		sort.Strings(labels)
	}
	var allFacts []map[string]any
	var allMetrics []map[string]any
	for _, label := range labels {
		if b := buckets[label]; b != nil {
			allFacts = append(allFacts, b.facts...)
			allMetrics = append(allMetrics, b.metrics...)
		}
	}
	factKinds := inferKindsWithBase(allFacts, commonFactKinds())
	metricKinds := inferKindsWithBase(allMetrics, moduleFactKinds(moduleID))
	for _, label := range labels {
		b := buckets[label]
		if b == nil {
			b = &bucket{}
		}
		result.Cohorts = append(result.Cohorts, ComparisonCohort{
			Cohort: label, N: int64(len(b.facts)), Facts: aggregateWithKinds(b.facts, factKinds), ModuleMetrics: aggregateWithKinds(b.metrics, metricKinds),
		})
	}
	return result, nil
}

func summarizeModule(id string, records []Record) ModuleSummary {
	result := ModuleSummary{ModuleID: id}
	versions := map[string]int64{}
	cohortFacts := map[string][]map[string]any{"unused": {}, "used": {}}
	var usedMetrics []map[string]any
	for _, record := range records {
		module, ok := record.Modules[id]
		if !ok {
			result.UnassessedN++
			continue
		}
		if !module.Used {
			result.UnusedN++
			cohortFacts["unused"] = append(cohortFacts["unused"], common(record))
			continue
		}
		result.UsedN++
		cohortFacts["used"] = append(cohortFacts["used"], common(record))
		usedMetrics = append(usedMetrics, prefixed("module_metrics."+id, module.Metrics))
		if module.VersionStatus == "known-public" && module.Version != nil {
			result.KnownVersionN++
			versions[*module.Version]++
		} else {
			result.UnavailableVersionN++
		}
	}
	for _, version := range sortedKeys(versions) {
		result.Versions = append(result.Versions, VersionCount{Version: version, Count: versions[version]})
	}
	for _, cohort := range []string{"unused", "used"} {
		facts := cohortFacts[cohort]
		allFacts := append(append([]map[string]any{}, cohortFacts["unused"]...), cohortFacts["used"]...)
		result.UsageCohorts = append(result.UsageCohorts, CohortSummary{Cohort: cohort, N: int64(len(facts)), Facts: aggregateWithKinds(facts, inferKindsWithBase(allFacts, commonFactKinds()))})
	}
	result.UsedMetrics = aggregateWithKinds(usedMetrics, inferKindsWithBase(usedMetrics, moduleFactKinds(id)))
	return result
}

func common(record Record) map[string]any {
	result := prefixed("outcome", record.Outcome)
	delete(result, "outcome.status")
	for key, value := range prefixed("task_effects", record.TaskEffects) {
		result[key] = value
	}
	return result
}

func prefixed(prefix string, input map[string]any) map[string]any {
	result := make(map[string]any)
	flatten(result, prefix, input)
	return result
}

func flatten(result map[string]any, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["method"].(string); ok {
			result[prefix] = typed
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flatten(result, next, typed[key])
		}
	case nil, bool, string, int, int32, int64, float64, json.Number:
		result[prefix] = typed
	}
}

type metricKind uint8

const (
	kindUnknown metricKind = iota
	kindBoolean
	kindCategory
	kindCount
	kindTime
)

func aggregate(rows []map[string]any) AggregateSet {
	return aggregateWithKinds(rows, inferKinds(rows))
}

func inferKinds(rows []map[string]any) map[string]metricKind {
	kinds := make(map[string]metricKind)
	for _, row := range rows {
		for key, value := range row {
			kind := classify(value)
			if kind != kindUnknown {
				kinds[key] = kind
			}
		}
	}
	return kinds
}

func inferKindsWithBase(rows []map[string]any, base map[string]metricKind) map[string]metricKind {
	kinds := make(map[string]metricKind, len(base))
	for key, kind := range base {
		kinds[key] = kind
	}
	for key, kind := range inferKinds(rows) {
		kinds[key] = kind
	}
	return kinds
}

func commonFactKinds() map[string]metricKind {
	return map[string]metricKind{
		"outcome.module_interaction_time":                                                kindTime,
		"outcome.rework_required":                                                        kindBoolean,
		"outcome.user_interventions":                                                     kindCount,
		"task_effects.completion.terminal_completion_invalidated":                        kindBoolean,
		"task_effects.security.destructive_escape":                                       kindBoolean,
		"task_effects.security.protected_secret_escape":                                  kindBoolean,
		"task_effects.requirements.acceptance_criteria_gap_delayed_defect":               kindBoolean,
		"task_effects.requirements.agent_requirements_questions":                         kindCount,
		"task_effects.requirements.api_contract_changes_after_implementation":            kindCount,
		"task_effects.requirements.data_model_changes_after_implementation":              kindCount,
		"task_effects.requirements.late_material_decisions.api":                          kindCount,
		"task_effects.requirements.late_material_decisions.authentication_authorization": kindCount,
		"task_effects.requirements.late_material_decisions.consistency_rules":            kindCount,
		"task_effects.requirements.late_material_decisions.data_model":                   kindCount,
		"task_effects.requirements.late_material_decisions.scope":                        kindCount,
		"task_effects.requirements.late_material_decisions.total":                        kindCount,
		"task_effects.requirements.late_material_decisions.user_behavior":                kindCount,
		"task_effects.requirements.requirements_misunderstanding_rework":                 kindBoolean,
		"task_effects.requirements.user_confirmation_questions":                          kindCount,
	}
}

func moduleFactKinds(moduleID string) map[string]metricKind {
	prefix := "module_metrics." + moduleID + "."
	fields := map[string]metricKind{}
	add := func(name string, kind metricKind) { fields[prefix+name] = kind }
	switch moduleID {
	case "spec":
		for _, name := range []string{
			"appropriately_sized", "artifact_created", "directly_usable_by_agent",
			"important_decisions_distinguished", "readable_by_user",
			"spec_implementation_divergence", "would_reuse",
		} {
			add(name, kindBoolean)
		}
		add("unnecessary_artifacts", kindCount)
		add("drafting_time", kindTime)
	case "ward":
		for _, name := range []string{
			"added_model_visible_prompt", "defer_mutated_output_or_audit",
			"normal_workflow_false_deny", "required_disablement", "safe_recovery_after_deny",
		} {
			add(name, kindBoolean)
		}
		add("hook_latency.median_ms", kindCount)
		add("hook_latency.sample_count", kindCount)
	case "seal":
		add("completion_decision", kindCategory)
		for _, name := range []string{
			"completion_refusal_understood", "evidence_corruption_bypass",
			"false_acceptance", "false_source_mismatch", "source_binding_bypass",
		} {
			add(name, kindBoolean)
		}
		add("added_user_interventions", kindCount)
		add("task_authoring_time", kindTime)
	}
	return fields
}

func aggregateWithKinds(rows []map[string]any, kinds map[string]metricKind) AggregateSet {
	keys := sortedKeys(kinds)
	result := AggregateSet{Booleans: []BooleanAggregate{}, Categories: []CategoryAggregate{}, Counts: []CountAggregate{}, Times: []TimeAggregate{}}
	for _, key := range keys {
		switch kinds[key] {
		case kindBoolean:
			item := BooleanAggregate{Key: key}
			for _, row := range rows {
				value, ok := row[key]
				boolean, typed := value.(bool)
				if !ok || !typed {
					item.Unavailable++
				} else if boolean {
					item.True++
				} else {
					item.False++
				}
			}
			item.Rate = Rational{Numerator: item.True, Denominator: item.True + item.False}
			result.Booleans = append(result.Booleans, item)
		case kindCategory:
			item := CategoryAggregate{Key: key, Values: []NamedCount{}}
			counts := make(map[string]int64)
			for _, row := range rows {
				value, ok := row[key].(string)
				if !ok {
					item.Unavailable++
					continue
				}
				counts[value]++
			}
			for _, value := range sortedKeys(counts) {
				item.Values = append(item.Values, NamedCount{Name: value, Count: counts[value]})
			}
			result.Categories = append(result.Categories, item)
		case kindCount:
			item := CountAggregate{Key: key}
			for _, row := range rows {
				value, ok := integer(row[key])
				if !ok {
					item.Unavailable++
					continue
				}
				item.Known++
				item.Sum += value
			}
			item.Mean = Rational{Numerator: item.Sum, Denominator: item.Known}
			result.Counts = append(result.Counts, item)
		case kindTime:
			item := TimeAggregate{Key: key}
			for _, row := range rows {
				measurement, ok := row[key].(map[string]any)
				if !ok {
					item.Unavailable++
					continue
				}
				switch measurement["method"] {
				case "measured":
					seconds, ok := integer(measurement["seconds"])
					if !ok {
						item.Unavailable++
						continue
					}
					item.Measured.Known++
					item.Measured.SumSeconds += seconds
				case "bounded-estimate":
					lower, lowerOK := integer(measurement["lower_seconds"])
					upper, upperOK := integer(measurement["upper_seconds"])
					if !lowerOK || !upperOK {
						item.Unavailable++
						continue
					}
					item.Bounded.Known++
					item.Bounded.LowerSumSeconds += lower
					item.Bounded.UpperSumSeconds += upper
				default:
					item.Unavailable++
				}
			}
			item.Measured.Mean = Rational{Numerator: item.Measured.SumSeconds, Denominator: item.Measured.Known}
			item.Bounded.LowerMean = Rational{Numerator: item.Bounded.LowerSumSeconds, Denominator: item.Bounded.Known}
			item.Bounded.UpperMean = Rational{Numerator: item.Bounded.UpperSumSeconds, Denominator: item.Bounded.Known}
			result.Times = append(result.Times, item)
		}
	}
	return result
}

func classify(value any) metricKind {
	switch typed := value.(type) {
	case bool:
		return kindBoolean
	case string:
		return kindCategory
	case int, int32, int64, float64, json.Number:
		return kindCount
	case map[string]any:
		if method, _ := typed["method"].(string); method == "measured" || method == "bounded-estimate" {
			return kindTime
		}
	}
	return kindUnknown
}

func integer(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		converted := int64(typed)
		return converted, float64(converted) == typed
	case json.Number:
		converted, err := typed.Int64()
		return converted, err == nil
	default:
		return 0, false
	}
}

func sortedRecords(records []Record) []Record {
	result := append([]Record(nil), records...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].TaskID == result[j].TaskID {
			return result[i].ObservationID < result[j].ObservationID
		}
		return result[i].TaskID < result[j].TaskID
	})
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func CanonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func RenderSummaryMarkdown(summary Summary) []byte {
	var output strings.Builder
	fmt.Fprintf(&output, "# Eval summary\n\n")
	fmt.Fprintf(&output, "- Schema: `%s`\n", summary.SchemaVersion)
	fmt.Fprintf(&output, "- As of: `%s`\n", summary.Window.AsOf)
	fmt.Fprintf(&output, "- Through: `%s`\n", summary.Window.Through)
	if summary.Window.From != nil {
		fmt.Fprintf(&output, "- From: `%s`\n", *summary.Window.From)
	}
	fmt.Fprintf(&output, "- Included real tasks: %d\n", summary.Dataset.IncludedTasks)
	fmt.Fprintf(&output, "- Excluded invalid rows: %d\n", summary.Dataset.Exclusions.InvalidRows)
	fmt.Fprintf(&output, "- Excluded invalid chains: %d\n", summary.Dataset.Exclusions.InvalidChains)
	fmt.Fprintf(&output, "- Excluded invalid or chain rows: %d\n", summary.Dataset.Exclusions.InvalidOrChainRows)
	fmt.Fprintf(&output, "- Excluded synthetic rows: %d\n", summary.Dataset.Exclusions.SyntheticRows)
	fmt.Fprintf(&output, "- Excluded superseded rows: %d\n", summary.Dataset.Exclusions.SupersededRows)
	fmt.Fprintf(&output, "- Excluded outside date window: %d\n\n", summary.Dataset.Exclusions.OutsideDateWindow)
	output.WriteString("## Outcomes\n\n")
	for _, outcome := range summary.Outcomes {
		fmt.Fprintf(&output, "- %s: %d\n", outcome.Name, outcome.Count)
	}
	output.WriteString("\n## Common facts\n\n")
	renderAggregateSet(&output, summary.CommonFacts)
	output.WriteString("\n## Modules\n\n")
	for _, module := range summary.Modules {
		fmt.Fprintf(&output, "### %s\n\n", module.ModuleID)
		fmt.Fprintf(&output, "- used / unused / unassessed: %d / %d / %d\n", module.UsedN, module.UnusedN, module.UnassessedN)
		fmt.Fprintf(&output, "- known / unavailable version: %d / %d\n", module.KnownVersionN, module.UnavailableVersionN)
		if len(module.Versions) == 0 {
			output.WriteString("- versions: none\n")
		} else {
			output.WriteString("- versions:\n")
			for _, version := range module.Versions {
				fmt.Fprintf(&output, "  - %s: %d\n", version.Version, version.Count)
			}
		}
		for _, cohort := range module.UsageCohorts {
			fmt.Fprintf(&output, "\n#### %s cohort (n=%d)\n\n", cohort.Cohort, cohort.N)
			renderAggregateSet(&output, cohort.Facts)
		}
		output.WriteString("\n#### used-only module metrics\n\n")
		renderAggregateSet(&output, module.UsedMetrics)
		output.WriteByte('\n')
	}
	output.WriteString("## Limitations\n\n")
	for _, limitation := range summary.Limitations {
		fmt.Fprintf(&output, "- %s\n", limitation)
	}
	return []byte(output.String())
}

func renderAggregateSet(output *strings.Builder, set AggregateSet) {
	for _, metric := range set.Booleans {
		fmt.Fprintf(output, "- %s: %s (%d/%d; true %d, false %d, unavailable %d)\n", metric.Key, percent(metric.Rate), metric.Rate.Numerator, metric.Rate.Denominator, metric.True, metric.False, metric.Unavailable)
	}
	for _, metric := range set.Categories {
		fmt.Fprintf(output, "- %s: unavailable %d", metric.Key, metric.Unavailable)
		for _, value := range metric.Values {
			fmt.Fprintf(output, "; %s %d", value.Name, value.Count)
		}
		output.WriteByte('\n')
	}
	for _, metric := range set.Counts {
		fmt.Fprintf(output, "- %s: sum %d; known %d; unavailable %d; mean %d/%d\n", metric.Key, metric.Sum, metric.Known, metric.Unavailable, metric.Mean.Numerator, metric.Mean.Denominator)
	}
	for _, metric := range set.Times {
		fmt.Fprintf(output, "- %s: measured sum %d s, known %d, mean %d/%d s; bounded lower/upper sums %d/%d s, known %d, means %d/%d and %d/%d s; unavailable %d\n",
			metric.Key,
			metric.Measured.SumSeconds, metric.Measured.Known, metric.Measured.Mean.Numerator, metric.Measured.Mean.Denominator,
			metric.Bounded.LowerSumSeconds, metric.Bounded.UpperSumSeconds, metric.Bounded.Known,
			metric.Bounded.LowerMean.Numerator, metric.Bounded.LowerMean.Denominator,
			metric.Bounded.UpperMean.Numerator, metric.Bounded.UpperMean.Denominator,
			metric.Unavailable,
		)
	}
}

func percent(value Rational) string {
	if value.Denominator == 0 {
		return "unavailable"
	}
	return fmt.Sprintf("%.2f%%", float64(value.Numerator)*100/float64(value.Denominator))
}
