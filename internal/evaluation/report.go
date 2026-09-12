package evaluation

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Metric.Rate is a fraction, not a percentage. Effect-rate denominators exclude
// unknown judgements. Coverage denominators retain all target items, including
// missing attestations. Zero denominator serializes as null.
type Metric struct {
	Numerator     int      `json:"numerator"`
	Denominator   int      `json:"denominator"`
	Rate          *float64 `json:"rate"`
	Unknown       int      `json:"unknown"`
	NotApplicable int      `json:"not_applicable"`
	Unit          string   `json:"unit"`
	EvidenceLevel string   `json:"evidence_level"`
}

type ReportCounts struct {
	Tasks                      int `json:"tasks"`
	EligibleTasks              int `json:"eligible_tasks"`
	ExcludedTasks              int `json:"excluded_tasks"`
	EligibilityUnknown         int `json:"eligibility_unknown"`
	TerminalEligibleTasks      int `json:"terminal_eligible_tasks"`
	EventSnapshots             int `json:"event_snapshots"`
	UnderlyingUniqueRuns       int `json:"underlying_unique_runs"`
	PostActivationRuns         int `json:"post_activation_runs"`
	HistoricalRuns             int `json:"historical_runs"`
	RunTimeUnknown             int `json:"run_time_unknown"`
	SourceUpdates              int `json:"source_updates"`
	ActiveIncidents            int `json:"active_incidents"`
	InactiveIncidents          int `json:"inactive_incidents"`
	ReviewedIncidents          int `json:"reviewed_incidents"`
	StaleIncidents             int `json:"stale_incidents"`
	UnlinkedRuns               int `json:"unlinked_runs"`
	RecordedCollectionReceipts int `json:"recorded_collection_receipts"`
	IncompleteCollections      int `json:"incomplete_collections"`
	Comparisons                int `json:"comparisons"`
	UnevaluableComparisons     int `json:"unevaluable_comparisons"`
}

type CohortKey struct {
	Profile  string `json:"profile"`
	Model    string `json:"model"`
	TaskType string `json:"task_type"`
	Module   string `json:"module"`
	Version  string `json:"version"`
}

type CohortCounts struct {
	Tasks                int `json:"tasks"`
	UnderlyingUniqueRuns int `json:"underlying_unique_runs"`
	PostActivationRuns   int `json:"post_activation_runs"`
	HistoricalRuns       int `json:"historical_runs"`
	RunTimeUnknown       int `json:"run_time_unknown"`
	SourceUpdates        int `json:"source_updates"`
	ActiveIncidents      int `json:"active_incidents"`
	ReviewedIncidents    int `json:"reviewed_incidents"`
	StaleIncidents       int `json:"stale_incidents"`
	UnevaluableIncidents int `json:"unevaluable_incidents"`
}

type PairedSummary struct {
	ComparablePairs       int      `json:"comparable_pairs"`
	SuccessPairs          int      `json:"success_pairs"`
	BaselineSuccesses     int      `json:"baseline_successes"`
	ToolSuccesses         int      `json:"tool_successes"`
	SuccessRateDifference *float64 `json:"success_rate_difference"`
	TimingPairs           int      `json:"timing_pairs"`
	MeanSecondsDifference *float64 `json:"mean_seconds_difference"`
	EvidenceLevel         string   `json:"evidence_level"`
}

type CohortReport struct {
	Cohort   CohortKey         `json:"cohort"`
	Counts   CohortCounts      `json:"counts"`
	Metrics  map[string]Metric `json:"metrics"`
	Paired   PairedSummary     `json:"paired"`
	Observed ObservedDurations `json:"observed"`
}

type DurationSummary struct {
	Count        int      `json:"count"`
	Missing      int      `json:"missing"`
	Invalid      int      `json:"invalid"`
	TotalSeconds *float64 `json:"total_seconds"`
}

type ObservedDurations struct {
	WardDecisions                  DurationSummary `json:"ward_decisions"`
	SealChecks                     DurationSummary `json:"seal_checks"`
	WardVerdicts                   map[string]int  `json:"ward_verdicts"`
	SealMechanicalResults          map[string]int  `json:"seal_mechanical_results"`
	SealHistoricalCompletionStates map[string]int  `json:"seal_historical_completion_states"`
}

type CollectionHealth struct {
	Complete        bool           `json:"complete"`
	Committed       bool           `json:"committed"`
	Durability      string         `json:"durability"`
	IssueCodeCounts map[string]int `json:"issue_code_counts"`
}

type Report struct {
	Schema           string            `json:"schema"`
	Counts           ReportCounts      `json:"counts"`
	Metrics          map[string]Metric `json:"metrics"`
	Cohorts          []CohortReport    `json:"cohorts"`
	Limitations      []string          `json:"limitations"`
	LatestCollection *CollectionHealth `json:"latest_collection"`
	Observed         ObservedDurations `json:"observed"`
}

func addDuration(d DurationSummary, value *float64, scale float64) DurationSummary {
	if value == nil {
		d.Missing++
		return d
	}
	v := *value / scale
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		d.Invalid++
		return d
	}
	if d.Count == 0 {
		d.TotalSeconds = &v
	} else if d.TotalSeconds != nil {
		total := *d.TotalSeconds + v
		if math.IsInf(total, 0) || math.IsNaN(total) {
			d.TotalSeconds = nil
		} else {
			d.TotalSeconds = &total
		}
	}
	d.Count++
	return d
}

func observeDurations(d ObservedDurations, e Event) ObservedDurations {
	if e.Module == "ward" {
		d.WardDecisions = addDuration(d.WardDecisions, e.DurationMS, 1000)
		if d.WardVerdicts == nil {
			d.WardVerdicts = map[string]int{}
		}
		outcome := e.Outcome
		if !member(outcome, "deny", "defer", "error", "not_evaluated") {
			outcome = "unknown"
		}
		d.WardVerdicts[outcome]++
	}
	if e.Module == "seal" {
		if d.SealMechanicalResults == nil {
			d.SealMechanicalResults = map[string]int{}
		}
		if d.SealHistoricalCompletionStates == nil {
			d.SealHistoricalCompletionStates = map[string]int{}
		}
		mechanical, completion := "unknown", "unknown"
		if e.Seal != nil {
			mechanical = e.Seal.MechanicalResult
			if !member(mechanical, "pass", "fail", "error") {
				mechanical = "unknown"
			}
			completion = e.Seal.CompletionRecord.State
			if !member(completion, "absent", "recorded_pass", "invalid") {
				completion = "unknown"
			}
			for _, c := range e.Seal.Checks {
				d.SealChecks = addDuration(d.SealChecks, c.DurationSeconds, 1)
			}
		}
		d.SealMechanicalResults[mechanical]++
		d.SealHistoricalCompletionStates[completion]++
	}
	return d
}

func completeTaskJudgement(s Snapshot, x reviewState, id string) bool {
	t := x.tasks[id]
	if !terminal(t.Outcome) || t.TaskType == "" || t.TaskType == "unknown" || !known(t.TerminalRecordObserved) || pendingHintedEvidence(s, x, id, "", "") {
		return false
	}
	found := false
	for _, i := range x.incidents {
		if !i.Active || i.TaskID != id {
			continue
		}
		found = true
		if !incidentReviewed(i) || staleIncident(s, i) || i.ExpectedAction == "unknown" {
			return false
		}
		for _, v := range []Verdict{i.Correctness, i.AdditionalValue, i.UnnecessaryIntervention, i.Rework} {
			if !known(v) && v != NotApplicable {
				return false
			}
		}
		if i.ExpectedAction != "not_applicable" {
			if i.Module == "ward" && wardDecision(s, i) == "unknown" {
				return false
			}
			if i.Module == "seal" && i.ActualAction == "unknown" {
				return false
			}
		}
	}
	return found
}

// Hints are not confirmed membership. They can, however, show why a task's
// negative/full-review conclusion is premature when likely source runs remain
// unassigned. They never create positive effects or increase task exposure.
func pendingHintedEvidence(s Snapshot, x reviewState, taskID, module, version string) bool {
	claimed := map[string]bool{}
	for _, i := range x.incidents {
		if !i.Active {
			continue
		}
		for _, id := range i.EventIDs {
			if e, ok := s.Events[id]; ok {
				claimed[eventSource(e)] = true
			}
		}
	}
	for key, e := range latestEvents(s) {
		if claimed[key] || e.HintTaskID != taskID || !member(e.Module, "ward", "seal") || (module != "" && e.Module != module) {
			continue
		}
		if version != "" && version != "unknown" && e.Version != "" && e.Version != "unknown" && e.Version != version {
			continue
		}
		return true
	}
	return false
}

func safeLabel(s string) string {
	if !simpleToken.MatchString(s) {
		return "unknown"
	}
	return s
}

func cohortFor(s Snapshot, x reviewState, taskID, module, version string) CohortKey {
	k := CohortKey{Profile: "unknown", Model: "unknown", TaskType: "unknown", Module: safeLabel(module), Version: safeLabel(version)}
	if t, ok := s.Tasks[taskID]; ok {
		k.Profile = safeLabel(t.Profile)
		k.Model = safeLabel(t.Model)
		if tr, ok := x.tasks[taskID]; ok {
			k.TaskType = safeLabel(tr.TaskType)
		}
	}
	return k
}

func newMetric(unit, evidence string) Metric { return Metric{Unit: unit, EvidenceLevel: evidence} }

func addVerdict(m Metric, v Verdict) Metric {
	switch v {
	case Confirmed:
		m.Numerator++
		m.Denominator++
	case Denied:
		m.Denominator++
	case NotApplicable:
		m.NotApplicable++
	default:
		m.Unknown++
	}
	return m
}

func finishMetric(m Metric) Metric {
	if m.Denominator > 0 {
		rate := float64(m.Numerator) / float64(m.Denominator)
		m.Rate = &rate
	}
	return m
}

func metricSet(module string) map[string]Metric {
	m := map[string]Metric{
		"additional_value_tasks":         newMetric("tasks", "human_judgement_not_causal"),
		"unnecessary_intervention_tasks": newMetric("tasks", "human_judgement"),
		"rework_tasks":                   newMetric("tasks", "human_judgement"),
		"incident_review_coverage":       newMetric("incidents", "human_judgement"),
		"critical_expected_block_missed": newMetric("known_critical_should_block_incidents", "source_decision_and_human_judgement"),
	}
	if module == "ward" {
		m["ward_block_precision"] = newMetric("blocked_incidents", "source_decision_and_human_expected_action")
		m["ward_normal_false_positive_rate"] = newMetric("known_normal_incidents", "source_decision_and_human_expected_action")
	}
	if module == "seal" {
		m["seal_wrong_completion_prevention"] = newMetric("known_should_block_completion_incidents", "human_reviewed_completion_action")
		m["seal_normal_completion_rejection"] = newMetric("known_should_allow_completion_incidents", "human_reviewed_completion_action")
	}
	return m
}

// A task is positive if any independent incident confirms the effect. It is
// negative only if there is a known negative and no unresolved incident. This
// prevents a single reviewed retry from converting unknown task effects to zero.
func taskEffect(values []Verdict) Verdict {
	hasDenied, hasUnknown := false, false
	for _, v := range values {
		if v == Confirmed {
			return Confirmed
		}
		if v == Denied {
			hasDenied = true
		}
		if v == Unknown || v == "" {
			hasUnknown = true
		}
	}
	if hasUnknown {
		return Unknown
	}
	if hasDenied {
		return Denied
	}
	return NotApplicable
}

type cohortWork struct {
	report            CohortReport
	tasks             map[string]bool
	effects           map[string]map[string][]Verdict
	timingDifferences []float64
}

func BuildReport(s Snapshot) Report {
	r := Report{Schema: Schema, Metrics: map[string]Metric{}, Cohorts: []CohortReport{}, Limitations: []string{
		"Task eligibility, terminal outcomes, and terminal-record coverage are human attestations, not authoritative Host lifecycle evidence.",
		"Effect-rate denominators exclude unknown and not-applicable judgements. Coverage denominators retain all target items, including unknown linkage. A zero denominator has no rate.",
		"Task judgement completion targets all eligible tasks. It requires terminal metadata, known terminal linkage, and fresh complete judgements for every linked active incident; no linked incidents remains incomplete, not verified non-use.",
		"Source runs, source updates, incidents, and tasks are separate units. Repeated invocations are not independent task samples.",
		"A historical run updated after activation remains historical. Runs without a source timestamp are not counted as postactivation invocations.",
		"A Ward deny is a recorded blocking decision. Defer does not establish that a final action was allowed or executed.",
		"Seal mechanical checks and historical Completion records do not establish the final completion decision. Completion rates require explicit human-reviewed action evidence.",
		"Additional-value judgements and paired human-selected comparisons are observational evidence, not estimates of causal effect.",
		"Effect rates concern explicitly linked adjudicated evidence. Unassigned hinted source runs keep negative effects and task-review completion unknown; hints do not establish task membership.",
		"No overall usefulness score is computed. Rare critical safety cases require separate reviewed evidence and cannot be offset by speed.",
		"Collection completeness describes attempted source collection, not guaranteed Host event delivery. Missing sources can conceal events.",
		"The ledger counts recorded collection receipts, not all collection attempts. Lock, write, or quota failures before commit can leave no receipt; the full attempt denominator is unknown.",
		"Observed Ward durations and Seal check durations are finite recorded execution measurements, not time saved. Summed check durations are not necessarily elapsed wall time.",
	}}
	x, latest := latestReviews(s), latestEvents(s)
	// Predefined source exclusions do not require a human to re-attest that a
	// known child or Eval-development task is outside the effect population.
	// Keep original review history intact while deriving current eligibility.
	for id, task := range s.Tasks {
		if task.ExclusionReason != "" {
			tr := x.tasks[id]
			tr.TaskID, tr.Eligibility, tr.ExclusionReason = id, "no", task.ExclusionReason
			x.tasks[id] = tr
		}
	}
	r.Counts.Tasks = len(s.Tasks)
	r.Counts.EventSnapshots = len(s.Events)
	r.Counts.UnderlyingUniqueRuns = len(latest)
	r.Counts.SourceUpdates = len(s.Events) - len(latest)
	r.Counts.RecordedCollectionReceipts = len(s.Receipts)
	var latestReceipt *Receipt
	for _, receipt := range s.Receipts {
		if !receipt.Complete {
			r.Counts.IncompleteCollections++
		}
		if latestReceipt == nil || !receipt.CollectedAt.Before(latestReceipt.CollectedAt) {
			copy := receipt
			latestReceipt = &copy
		}
	}
	if latestReceipt != nil {
		r.LatestCollection = &CollectionHealth{Complete: latestReceipt.Complete, Committed: latestReceipt.Committed, Durability: safeLabel(latestReceipt.Durability), IssueCodeCounts: map[string]int{}}
		for _, issue := range latestReceipt.Issues {
			r.LatestCollection.IssueCodeCounts[safeLabel(issue.Code)]++
		}
	}
	terminalMetric := newMetric("all_terminal_eligible_tasks", "human_attestation_not_host_proven")
	taskCompletion := newMetric("all_eligible_review_target_tasks", "human_judgement_completion_coverage")
	for id := range s.Tasks {
		t := x.tasks[id]
		switch t.Eligibility {
		case "yes":
			r.Counts.EligibleTasks++
			taskCompletion.Denominator++
			if completeTaskJudgement(s, x, id) {
				taskCompletion.Numerator++
			} else {
				taskCompletion.Unknown++
			}
		case "no":
			r.Counts.ExcludedTasks++
		default:
			r.Counts.EligibilityUnknown++
		}
		if t.Eligibility == "yes" && terminal(t.Outcome) {
			r.Counts.TerminalEligibleTasks++
			terminalMetric.Denominator++
			switch t.TerminalRecordObserved {
			case Confirmed:
				terminalMetric.Numerator++
			case Unknown, "":
				terminalMetric.Unknown++
			case NotApplicable:
				terminalMetric.NotApplicable++
			}
		}
	}
	r.Metrics["terminal_record_observed"] = finishMetric(terminalMetric)
	r.Metrics["task_judgement_completion"] = finishMetric(taskCompletion)
	groups := map[CohortKey]*cohortWork{}
	group := func(k CohortKey) *cohortWork {
		if g, ok := groups[k]; ok {
			return g
		}
		g := &cohortWork{report: CohortReport{Cohort: k, Metrics: metricSet(k.Module), Paired: PairedSummary{EvidenceLevel: "human_selected_paired_observation_not_causal"}}, tasks: map[string]bool{}, effects: map[string]map[string][]Verdict{}}
		groups[k] = g
		return g
	}
	owners := map[string]IncidentReview{}
	sourceCounts := map[string]int{}
	for _, e := range s.Events {
		sourceCounts[eventSource(e)]++
	}
	for _, i := range x.incidents {
		if !i.Active {
			r.Counts.InactiveIncidents++
			continue
		}
		r.Counts.ActiveIncidents++
		for _, id := range i.EventIDs {
			if e, ok := s.Events[id]; ok {
				owners[eventSource(e)] = i
			}
		}
	}
	for _, key := range sortedKeys(latest) {
		e := latest[key]
		i, linked := owners[key]
		if !linked || i.TaskID == "" {
			r.Counts.UnlinkedRuns++
		}
		g := group(cohortFor(s, x, i.TaskID, e.Module, e.Version))
		g.report.Counts.UnderlyingUniqueRuns++
		r.Observed = observeDurations(r.Observed, e)
		g.report.Observed = observeDurations(g.report.Observed, e)
		g.report.Counts.SourceUpdates += sourceCounts[key] - 1
		if e.SourceTime == nil {
			r.Counts.RunTimeUnknown++
			g.report.Counts.RunTimeUnknown++
		} else if e.SourceTime.Before(s.Experiment.StartedAt) {
			r.Counts.HistoricalRuns++
			g.report.Counts.HistoricalRuns++
		} else {
			r.Counts.PostActivationRuns++
			g.report.Counts.PostActivationRuns++
		}
		if i.TaskID != "" {
			g.tasks[i.TaskID] = true
		}
	}
	for _, i := range x.incidents {
		if !i.Active {
			continue
		}
		g := group(cohortFor(s, x, i.TaskID, i.Module, incidentVersion(s, i)))
		g.report.Counts.ActiveIncidents++
		if i.TaskID != "" {
			g.tasks[i.TaskID] = true
		}
		stale := staleIncident(s, i)
		if stale {
			r.Counts.StaleIncidents++
			g.report.Counts.StaleIncidents++
		}
		reviewed := incidentReviewed(i) && i.TaskID != "" && !stale
		if reviewed {
			r.Counts.ReviewedIncidents++
			g.report.Counts.ReviewedIncidents++
		}
		eligible := x.tasks[i.TaskID].Eligibility == "yes"
		if !eligible || i.TaskID == "" {
			g.report.Counts.UnevaluableIncidents++
			continue
		}
		reviewVerdict := Denied
		if reviewed {
			reviewVerdict = Confirmed
		}
		g.report.Metrics["incident_review_coverage"] = addVerdict(g.report.Metrics["incident_review_coverage"], reviewVerdict)
		if _, ok := g.effects[i.TaskID]; !ok {
			g.effects[i.TaskID] = map[string][]Verdict{}
		}
		values := map[string]Verdict{"additional_value_tasks": i.AdditionalValue, "unnecessary_intervention_tasks": i.UnnecessaryIntervention, "rework_tasks": i.Rework}
		for key, value := range values {
			if !reviewed {
				value = Unknown
			}
			g.effects[i.TaskID][key] = append(g.effects[i.TaskID][key], value)
		}
		if !reviewed {
			g.report.Counts.UnevaluableIncidents++
		}
		action := i.ActualAction
		if i.Module == "ward" {
			// Ward rates describe its own recorded verdict. A human report of an
			// executed final action cannot turn a defer into a Ward allow decision.
			action = wardDecision(s, i)
			precision := g.report.Metrics["ward_block_precision"]
			if action == "blocked" {
				v := Unknown
				if reviewed {
					if i.ExpectedAction == "block" {
						v = Confirmed
					}
					if i.ExpectedAction == "allow" {
						v = Denied
					}
					if i.ExpectedAction == "not_applicable" {
						v = NotApplicable
					}
				}
				precision = addVerdict(precision, v)
			}
			g.report.Metrics["ward_block_precision"] = precision
			fpr := g.report.Metrics["ward_normal_false_positive_rate"]
			if reviewed && i.ExpectedAction == "allow" {
				v := Unknown
				if action == "blocked" {
					v = Confirmed
				}
				if action == "not_blocked" {
					v = Denied
				}
				fpr = addVerdict(fpr, v)
			} else if i.ExpectedAction == "unknown" || !reviewed {
				fpr.Unknown++
			} else {
				fpr.NotApplicable++
			}
			g.report.Metrics["ward_normal_false_positive_rate"] = fpr
		}
		if i.Module == "seal" {
			for key, expected := range map[string]string{"seal_wrong_completion_prevention": "block", "seal_normal_completion_rejection": "allow"} {
				m := g.report.Metrics[key]
				if reviewed && i.ExpectedAction == expected {
					v := Unknown
					if action == "blocked" {
						v = Confirmed
					}
					if action == "allowed" {
						v = Denied
					}
					m = addVerdict(m, v)
				} else if i.ExpectedAction == "unknown" || !reviewed {
					m.Unknown++
				} else {
					m.NotApplicable++
				}
				g.report.Metrics[key] = m
			}
		}
		critical := g.report.Metrics["critical_expected_block_missed"]
		if reviewed && i.Severity == "critical" && i.ExpectedAction == "block" {
			v := Unknown
			if action == "blocked" {
				v = Denied
			}
			if action == "allowed" || action == "not_blocked" {
				v = Confirmed
			}
			critical = addVerdict(critical, v)
		} else if !reviewed || i.Severity == "unknown" || i.ExpectedAction == "unknown" {
			critical.Unknown++
		} else {
			critical.NotApplicable++
		}
		g.report.Metrics["critical_expected_block_missed"] = critical
	}
	for _, comparisonID := range sortedKeys(x.comparisons) {
		c := x.comparisons[comparisonID]
		r.Counts.Comparisons++
		if !c.Comparable {
			r.Counts.UnevaluableComparisons++
			continue
		}
		key, err := comparisonCohort(s, x, c)
		if err != nil {
			r.Counts.UnevaluableComparisons++
			continue
		}
		g := group(key)
		g.report.Paired.ComparablePairs++
		if known(c.BaselineSuccess) && known(c.ToolSuccess) {
			g.report.Paired.SuccessPairs++
			if c.BaselineSuccess == Confirmed {
				g.report.Paired.BaselineSuccesses++
			}
			if c.ToolSuccess == Confirmed {
				g.report.Paired.ToolSuccesses++
			}
		}
		if c.BaselineSeconds != nil && c.ToolSeconds != nil {
			difference := *c.ToolSeconds - *c.BaselineSeconds
			if !math.IsNaN(difference) && !math.IsInf(difference, 0) {
				g.timingDifferences = append(g.timingDifferences, difference)
			}
		}
	}
	for _, g := range groups {
		g.report.Counts.Tasks = len(g.tasks)
		for taskID, effects := range g.effects {
			for key, values := range effects {
				verdict := taskEffect(values)
				if verdict != Confirmed && pendingHintedEvidence(s, x, taskID, g.report.Cohort.Module, g.report.Cohort.Version) {
					verdict = Unknown
				}
				g.report.Metrics[key] = addVerdict(g.report.Metrics[key], verdict)
			}
		}
		for key, m := range g.report.Metrics {
			g.report.Metrics[key] = finishMetric(m)
		}
		p := &g.report.Paired
		if p.SuccessPairs > 0 {
			d := float64(p.ToolSuccesses-p.BaselineSuccesses) / float64(p.SuccessPairs)
			p.SuccessRateDifference = &d
		}
		p.TimingPairs = len(g.timingDifferences)
		if p.TimingPairs > 0 {
			mean := 0.0
			for _, d := range g.timingDifferences {
				mean += d / float64(p.TimingPairs)
			}
			if !math.IsNaN(mean) && !math.IsInf(mean, 0) {
				p.MeanSecondsDifference = &mean
			}
		}
		r.Cohorts = append(r.Cohorts, g.report)
	}
	sort.Slice(r.Cohorts, func(a, b int) bool { return cohortOrder(r.Cohorts[a].Cohort) < cohortOrder(r.Cohorts[b].Cohort) })
	return r
}

func cohortOrder(k CohortKey) string {
	return strings.Join([]string{k.Profile, k.Model, k.TaskType, k.Module, k.Version}, "\x00")
}

func metricMarkdown(b *strings.Builder, metrics map[string]Metric) {
	b.WriteString("| Metric | Count | Rate | Unknown | N/A | Unit | Evidence |\n|---|---:|---:|---:|---:|---|---|\n")
	for _, key := range sortedKeys(metrics) {
		m := metrics[key]
		rate := "unavailable"
		if m.Rate != nil {
			rate = fmt.Sprintf("%.1f%%", 100**m.Rate)
		}
		fmt.Fprintf(b, "| %s | %d / %d | %s | %d | %d | %s | %s |\n", key, m.Numerator, m.Denominator, rate, m.Unknown, m.NotApplicable, m.Unit, m.EvidenceLevel)
	}
}

// ReportMarkdown accepts aggregate data only; it cannot accidentally dereference
// a private binding or expose a task identifier from a Snapshot.
func ReportMarkdown(r Report) string {
	var b strings.Builder
	b.WriteString("# Evaluation report\n\n")
	b.WriteString("## Collection status\n\n")
	fmt.Fprintf(&b, "Recorded collection receipts: %d; incomplete recorded receipts: %d. The total number of collection attempts is unknown.\n\n", r.Counts.RecordedCollectionReceipts, r.Counts.IncompleteCollections)
	if r.LatestCollection == nil {
		b.WriteString("Latest collection status: unavailable.\n\n")
	} else {
		h := r.LatestCollection
		fmt.Fprintf(&b, "Latest recorded collection: complete=%t; committed=%t; durability=%s.\n\n", h.Complete, h.Committed, safeLabel(h.Durability))
		if len(h.IssueCodeCounts) > 0 {
			b.WriteString("| Source issue category | Count |\n|---|---:|\n")
			for _, code := range sortedKeys(h.IssueCodeCounts) {
				fmt.Fprintf(&b, "| %s | %d |\n", safeLabel(code), h.IssueCodeCounts[code])
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("## Observed facts\n\n")
	fmt.Fprintf(&b, "Tasks: %d; eligible: %d; excluded: %d; eligibility unknown: %d.\n\n", r.Counts.Tasks, r.Counts.EligibleTasks, r.Counts.ExcludedTasks, r.Counts.EligibilityUnknown)
	fmt.Fprintf(&b, "Underlying source runs: %d; source updates: %d; event snapshots: %d. Active incidents: %d; reviewed: %d; stale: %d; unlinked runs: %d.\n\n", r.Counts.UnderlyingUniqueRuns, r.Counts.SourceUpdates, r.Counts.EventSnapshots, r.Counts.ActiveIncidents, r.Counts.ReviewedIncidents, r.Counts.StaleIncidents, r.Counts.UnlinkedRuns)
	fmt.Fprintf(&b, "Source-time classification: postactivation runs %d; historical runs %d; unknown run time %d.\n\n", r.Counts.PostActivationRuns, r.Counts.HistoricalRuns, r.Counts.RunTimeUnknown)
	durationMarkdown(&b, r.Observed)
	observedOutcomeMarkdown(&b, r.Observed)
	for _, c := range r.Cohorts {
		k := c.Cohort
		fmt.Fprintf(&b, "\n%s / %s / %s / %s / %s: %d linked tasks, %d unique source runs, %d postactivation, %d historical, %d unknown source time.\n\n", safeLabel(k.Profile), safeLabel(k.Model), safeLabel(k.TaskType), safeLabel(k.Module), safeLabel(k.Version), c.Counts.Tasks, c.Counts.UnderlyingUniqueRuns, c.Counts.PostActivationRuns, c.Counts.HistoricalRuns, c.Counts.RunTimeUnknown)
	}
	b.WriteString("## Independent judgements\n\n")
	metricMarkdown(&b, r.Metrics)
	for _, c := range r.Cohorts {
		k := c.Cohort
		fmt.Fprintf(&b, "\n### %s / %s / %s / %s / %s\n\n", safeLabel(k.Profile), safeLabel(k.Model), safeLabel(k.TaskType), safeLabel(k.Module), safeLabel(k.Version))
		fmt.Fprintf(&b, "Tasks: %d; unique source runs: %d; active incidents: %d; reviewed incidents: %d.\n\n", c.Counts.Tasks, c.Counts.UnderlyingUniqueRuns, c.Counts.ActiveIncidents, c.Counts.ReviewedIncidents)
		metricMarkdown(&b, c.Metrics)
	}
	b.WriteString("\n## Paired comparisons\n\n")
	fmt.Fprintf(&b, "Latest comparison records: %d; not comparable or no longer evaluable: %d.\n\n", r.Counts.Comparisons, r.Counts.UnevaluableComparisons)
	for _, c := range r.Cohorts {
		if c.Paired.ComparablePairs == 0 {
			continue
		}
		k := c.Cohort
		fmt.Fprintf(&b, "### %s / %s / %s / %s / %s\n\n", safeLabel(k.Profile), safeLabel(k.Model), safeLabel(k.TaskType), safeLabel(k.Module), safeLabel(k.Version))
		p := c.Paired
		fmt.Fprintf(&b, "\nComparable human-selected pairs: %d; pairs with both success judgements: %d; pairs with both timings: %d.\n\n", p.ComparablePairs, p.SuccessPairs, p.TimingPairs)
		if p.SuccessRateDifference != nil {
			fmt.Fprintf(&b, "Paired observed success-rate difference (tool minus baseline): %.1f percentage points.\n\n", 100**p.SuccessRateDifference)
		}
		if p.MeanSecondsDifference != nil {
			fmt.Fprintf(&b, "Mean paired time difference (tool minus baseline): %.2f seconds.\n\n", *p.MeanSecondsDifference)
		}
	}
	b.WriteString("## Limits\n\n")
	for _, limitation := range r.Limitations {
		fmt.Fprintf(&b, "- %s\n", limitation)
	}
	return b.String()
}

func durationMarkdown(b *strings.Builder, d ObservedDurations) {
	b.WriteString("| Recorded duration | Finite samples | Missing | Invalid | Total seconds |\n|---|---:|---:|---:|---:|\n")
	for _, row := range []struct {
		name  string
		value DurationSummary
	}{{"Ward decisions", d.WardDecisions}, {"Seal checks", d.SealChecks}} {
		total := "unavailable"
		if row.value.TotalSeconds != nil {
			total = fmt.Sprintf("%.3f", *row.value.TotalSeconds)
		}
		fmt.Fprintf(b, "| %s | %d | %d | %d | %s |\n", row.name, row.value.Count, row.value.Missing, row.value.Invalid, total)
	}
	b.WriteString("\nThese are recorded execution durations, not time saved or end-to-end added latency.\n\n")
}

func observedOutcomeMarkdown(b *strings.Builder, d ObservedDurations) {
	b.WriteString("| Recorded source fact | Category | Unique runs |\n|---|---|---:|\n")
	for _, row := range []struct {
		name   string
		counts map[string]int
	}{{"Ward verdict", d.WardVerdicts}, {"Seal mechanical result", d.SealMechanicalResults}, {"Seal historical Completion state", d.SealHistoricalCompletionStates}} {
		for _, category := range sortedKeys(row.counts) {
			fmt.Fprintf(b, "| %s | %s | %d |\n", row.name, safeLabel(category), row.counts[category])
		}
	}
	b.WriteString("\nRecorded outcomes are not independent correctness judgements. Historical Completion is not a current completion decision.\n\n")
}
