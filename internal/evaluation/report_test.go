package evaluation

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func metricFrom(t *testing.T, r Report, module, name string) Metric {
	t.Helper()
	for _, c := range r.Cohorts {
		if c.Cohort.Module == module {
			m, ok := c.Metrics[name]
			if !ok {
				t.Fatalf("missing metric %s", name)
			}
			return m
		}
	}
	t.Fatalf("missing module %s", module)
	return Metric{}
}

func assertMetric(t *testing.T, m Metric, n, d, u int) {
	t.Helper()
	if m.Numerator != n || m.Denominator != d || m.Unknown != u {
		t.Fatalf("metric got %#v; want %d/%d, unknown %d", m, n, d, u)
	}
	if d == 0 {
		if m.Rate != nil {
			t.Fatal("zero denominator must have a null rate")
		}
		return
	}
	if m.Rate == nil || math.Abs(*m.Rate-float64(n)/float64(d)) > 1e-12 {
		t.Fatalf("incorrect metric rate: %#v", m)
	}
}

func TestWardPrecisionAndFPRHaveDifferentDenominators(t *testing.T) {
	s := reviewFixture()
	rows := []struct{ outcome, expected string }{{"deny", "block"}, {"deny", "block"}, {"deny", "allow"}, {"defer", "allow"}, {"defer", "allow"}, {"error", "allow"}}
	r := reviewWith(s)
	for n, row := range rows {
		id := string(rune('a' + n))
		e := addTestEvent(&s, id, "ward", row.outcome)
		i := testIncident("incident-"+id, e.ID, "ward", row.expected, sourceAction(e))
		r.Incidents = append(r.Incidents, i)
	}
	if err := ValidateReview(s, r); err != nil {
		t.Fatal(err)
	}
	s.Reviews = append(s.Reviews, r)
	report := BuildReport(s)
	assertMetric(t, metricFrom(t, report, "ward", "ward_block_precision"), 2, 3, 0)
	assertMetric(t, metricFrom(t, report, "ward", "ward_normal_false_positive_rate"), 1, 3, 1)
	if report.Counts.ReviewedIncidents != 6 {
		t.Fatalf("review count: %#v", report.Counts)
	}
}

func TestNoNormalCasesDoesNotMeanZeroFalsePositiveRate(t *testing.T) {
	s := reviewFixture()
	addTestEvent(&s, "event", "ward", "deny")
	i := testIncident("incident", "event", "ward", "block", "blocked")
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	m := metricFrom(t, BuildReport(s), "ward", "ward_normal_false_positive_rate")
	assertMetric(t, m, 0, 0, 0)
	if m.NotApplicable != 1 {
		t.Fatalf("N/A not counted: %#v", m)
	}
}

func TestRepeatedIncidentsDoNotMultiplyTaskBenefit(t *testing.T) {
	s := reviewFixture()
	r := reviewWith(s)
	for _, id := range []string{"one", "two"} {
		addTestEvent(&s, id, "ward", "deny")
		i := testIncident("incident-"+id, id, "ward", "block", "blocked")
		i.AdditionalValue = Confirmed
		r.Incidents = append(r.Incidents, i)
	}
	s.Reviews = append(s.Reviews, r)
	report := BuildReport(s)
	assertMetric(t, metricFrom(t, report, "ward", "additional_value_tasks"), 1, 1, 0)
	if report.Counts.ActiveIncidents != 2 || report.Counts.UnderlyingUniqueRuns != 2 {
		t.Fatal("different units collapsed")
	}
	updated := r.Incidents[0]
	updated.Revision = 2
	updated.AdditionalValue = Denied
	s.Reviews = append(s.Reviews, reviewWith(s, updated))
	assertMetric(t, metricFrom(t, BuildReport(s), "ward", "additional_value_tasks"), 1, 1, 0)
	updated = r.Incidents[1]
	updated.Revision = 2
	updated.AdditionalValue = Unknown
	s.Reviews = append(s.Reviews, reviewWith(s, updated))
	assertMetric(t, metricFrom(t, BuildReport(s), "ward", "additional_value_tasks"), 0, 0, 1)
}

func TestSealMechanicalAndHistoricalPassDoNotProveCompletionDecision(t *testing.T) {
	s := reviewFixture()
	e := addTestEvent(&s, "event", "seal", "fail")
	e.Seal = &SealFacts{MechanicalResult: "fail", CompletionRecord: Completion{State: "recorded_pass"}}
	s.Events[e.ID] = e
	i := testIncident("incident", e.ID, "seal", "block", "unknown")
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	assertMetric(t, metricFrom(t, BuildReport(s), "seal", "seal_wrong_completion_prevention"), 0, 0, 1)
	i.Revision = 2
	i.ActualAction = "blocked"
	i.Evidence = "independent_test"
	r := reviewWith(s, i)
	if err := ValidateReview(s, r); err != nil {
		t.Fatal(err)
	}
	s.Reviews = append(s.Reviews, r)
	assertMetric(t, metricFrom(t, BuildReport(s), "seal", "seal_wrong_completion_prevention"), 1, 1, 0)
}

func TestSourceUpdateDoesNotCreateInvocationOrKeepStaleGrade(t *testing.T) {
	s := reviewFixture()
	e := addTestEvent(&s, "old", "seal", "pass")
	historical := s.Experiment.StartedAt.Add(-1)
	e.SourceTime = &historical
	s.Events[e.ID] = e
	i := testIncident("incident", e.ID, "seal", "allow", "allowed")
	i.AdditionalValue = Confirmed
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	updated := e
	updated.ID = "later-completion"
	updated.Revision = 2
	updated.Seal = &SealFacts{CompletionRecord: Completion{State: "recorded_pass"}}
	s.Events[updated.ID] = updated
	r := BuildReport(s)
	if r.Counts.UnderlyingUniqueRuns != 1 || r.Counts.SourceUpdates != 1 || r.Counts.PostActivationRuns != 0 || r.Counts.HistoricalRuns != 1 || r.Counts.StaleIncidents != 1 {
		t.Fatalf("source revision accounting: %#v", r.Counts)
	}
	assertMetric(t, metricFrom(t, r, "seal", "additional_value_tasks"), 0, 0, 1)
}

func TestPairedReportUsesBothKnownEndpointsAndTimingDelta(t *testing.T) {
	s, r := comparisonFixture()
	s.Reviews = append(s.Reviews, r)
	report := BuildReport(s)
	var p PairedSummary
	for _, c := range report.Cohorts {
		if c.Paired.ComparablePairs > 0 {
			p = c.Paired
		}
	}
	if p.SuccessPairs != 1 || p.BaselineSuccesses != 0 || p.ToolSuccesses != 1 || p.SuccessRateDifference == nil || *p.SuccessRateDifference != 1 || p.TimingPairs != 1 || p.MeanSecondsDifference == nil || *p.MeanSecondsDifference != 4 {
		t.Fatalf("paired calculation: %#v", p)
	}
	c := r.Comparisons[0]
	c.Revision = 2
	c.BaselineSuccess = Unknown
	r.Comparisons = []Comparison{c}
	s.Reviews = append(s.Reviews, r)
	for _, cohort := range BuildReport(s).Cohorts {
		if cohort.Paired.ComparablePairs > 0 && cohort.Paired.SuccessPairs != 0 {
			t.Fatal("unknown endpoint entered denominator")
		}
	}
}

func TestAggregateJSONAndMarkdownExcludePrivateIdentifiers(t *testing.T) {
	s := reviewFixture()
	s.Bindings = []Binding{{ID: "original-source-id", Kind: "session", Key: "SECRET PROMPT", Reference: "/private/user/session.jsonl"}}
	e := addTestEvent(&s, "private-event-id", "seal", "pass")
	e.SourceID = "original-source-id"
	e.RuleID = "SECRET RULE"
	s.Events[e.ID] = e
	s.Experiment.Config.Repositories = []string{"/private/user/project"}
	v := s.Tasks["task-one"]
	v.Model = "/private/model-path"
	s.Tasks[v.ID] = v
	i := testIncident("private-incident-id", e.ID, "seal", "allow", "allowed")
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	r := BuildReport(s)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(data) + ReportMarkdown(r)
	for _, secret := range []string{"task-one", "task-two", "private-event-id", "original-source-id", "private-incident-id", "SECRET", "/private/"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("aggregate exposed private material %q", secret)
		}
	}
}

func TestManualTerminalCoverageRetainsUnknownLinkageInDenominator(t *testing.T) {
	s := reviewFixture()
	s.Reviews[0].Tasks[0].TerminalRecordObserved = Confirmed
	m := BuildReport(s).Metrics["terminal_record_observed"]
	assertMetric(t, m, 1, 2, 1)
	if m.EvidenceLevel != "human_attestation_not_host_proven" {
		t.Fatal("terminal evidence overstated")
	}
}

func TestTaskJudgementCompletionDoesNotPromotePartialReview(t *testing.T) {
	s := reviewFixture()
	s.Reviews[0].Tasks[0].TerminalRecordObserved = Confirmed
	addTestEvent(&s, "event", "ward", "deny")
	i := testIncident("incident", "event", "ward", "block", "blocked")
	i.Correctness = Confirmed
	i.AdditionalValue = Confirmed
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	assertMetric(t, BuildReport(s).Metrics["task_judgement_completion"], 0, 2, 2)
	i.Revision = 2
	i.UnnecessaryIntervention = Denied
	i.Rework = NotApplicable
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	assertMetric(t, BuildReport(s).Metrics["task_judgement_completion"], 1, 2, 1)
}

func TestPendingHintPreventsNegativeTaskEffectWithoutConfirmingMembership(t *testing.T) {
	s := reviewFixture()
	s.Reviews[0].Tasks[0].TerminalRecordObserved = Confirmed
	addTestEvent(&s, "known", "ward", "deny")
	i := testIncident("incident", "known", "ward", "block", "blocked")
	i.Correctness = Confirmed
	i.AdditionalValue = Denied
	i.UnnecessaryIntervention = Denied
	i.Rework = Denied
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	pending := addTestEvent(&s, "pending", "ward", "defer")
	pending.HintTaskID = "task-one"
	s.Events[pending.ID] = pending
	r := BuildReport(s)
	for _, c := range r.Cohorts {
		if c.Cohort.Profile == "ward" {
			assertMetric(t, c.Metrics["additional_value_tasks"], 0, 0, 1)
			if c.Counts.Tasks != 1 || c.Counts.UnderlyingUniqueRuns != 1 {
				t.Fatalf("hint became confirmed membership: %#v", c.Counts)
			}
		}
	}
	assertMetric(t, r.Metrics["task_judgement_completion"], 0, 2, 2)
	if r.Counts.UnlinkedRuns != 1 {
		t.Fatal("hinted source should remain unlinked")
	}
}

func TestObservedDurationsUseLatestSourceAndNeverInventTimeSaved(t *testing.T) {
	s := reviewFixture()
	e := addTestEvent(&s, "ward-old", "ward", "defer")
	ms := 125.0
	e.DurationMS = &ms
	s.Events[e.ID] = e
	updated := e
	updated.ID = "ward-later"
	updated.Revision = 2
	s.Events[updated.ID] = updated
	addTestEvent(&s, "ward-no-time", "ward", "error")
	seal := addTestEvent(&s, "seal", "seal", "fail")
	seconds := 2.5
	bad := math.Inf(1)
	seal.Seal = &SealFacts{Checks: []SealCheck{{DurationSeconds: &seconds}, {DurationSeconds: nil}, {DurationSeconds: &bad}}}
	s.Events[seal.ID] = seal
	r := BuildReport(s)
	if r.Observed.WardDecisions.Count != 1 || r.Observed.WardDecisions.Missing != 1 || r.Observed.WardDecisions.TotalSeconds == nil || *r.Observed.WardDecisions.TotalSeconds != .125 {
		t.Fatalf("Ward duration accounting: %#v", r.Observed.WardDecisions)
	}
	if r.Observed.SealChecks.Count != 1 || r.Observed.SealChecks.Missing != 1 || r.Observed.SealChecks.Invalid != 1 || r.Observed.SealChecks.TotalSeconds == nil || *r.Observed.SealChecks.TotalSeconds != 2.5 {
		t.Fatalf("Seal duration accounting: %#v", r.Observed.SealChecks)
	}
	if r.Observed.WardVerdicts["defer"] != 1 || r.Observed.WardVerdicts["error"] != 1 {
		t.Fatalf("revisions counted as more verdicts: %#v", r.Observed.WardVerdicts)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatalf("invalid source duration leaked into JSON: %v", err)
	}
	if !strings.Contains(ReportMarkdown(r), "not time saved") {
		t.Fatal("observed timing was not qualified")
	}
}

func TestObservedOutcomeCategoriesAreControlledAndNotJudgements(t *testing.T) {
	s := reviewFixture()
	e := addTestEvent(&s, "event", "seal", "pass")
	e.Seal = &SealFacts{MechanicalResult: "pass", CompletionRecord: Completion{State: "recorded_pass"}}
	s.Events[e.ID] = e
	addTestEvent(&s, "ward", "ward", "private-user-value")
	r := BuildReport(s)
	if r.Observed.SealMechanicalResults["pass"] != 1 || r.Observed.SealHistoricalCompletionStates["recorded_pass"] != 1 || r.Observed.WardVerdicts["unknown"] != 1 {
		t.Fatalf("observed fact categories: %#v", r.Observed)
	}
	if r.Counts.ReviewedIncidents != 0 {
		t.Fatal("facts were promoted to independent judgements")
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-user-value") {
		t.Fatal("uncontrolled source category leaked")
	}
}

func TestCollectionHealthShowsLatestReceiptWithoutPrivateSourceIDs(t *testing.T) {
	s := reviewFixture()
	s.Receipts = []Receipt{
		{CollectedAt: s.Experiment.StartedAt, Complete: true, Committed: true, Durability: "confirmed"},
		{CollectedAt: s.Experiment.StartedAt.Add(1), Complete: false, Committed: true, Durability: "confirmed", Issues: []Issue{{SourceID: "secret-source-id", Code: "ward_parse_failed"}, {SourceID: "another-private-id", Code: "ward_parse_failed"}}},
	}
	r := BuildReport(s)
	if r.Counts.RecordedCollectionReceipts != 2 || r.LatestCollection == nil || r.LatestCollection.Complete || r.LatestCollection.IssueCodeCounts["ward_parse_failed"] != 2 {
		t.Fatalf("source health: %#v", r.LatestCollection)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	md := ReportMarkdown(r)
	if strings.Contains(string(data)+md, "secret-source-id") || strings.Contains(string(data)+md, "another-private-id") {
		t.Fatal("source identities leaked")
	}
	previous := -1
	for _, heading := range []string{"## Collection status", "## Observed facts", "## Independent judgements", "## Paired comparisons", "## Limits"} {
		index := strings.Index(md, heading)
		if index <= previous {
			t.Fatalf("report section order: %s", md)
		}
		previous = index
	}
}

func TestCriticalMissRemainsVisibleWithoutAnOverallScore(t *testing.T) {
	s := reviewFixture()
	addTestEvent(&s, "event", "ward", "defer")
	i := testIncident("incident", "event", "ward", "block", "unknown")
	i.Severity = "critical"
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	r := BuildReport(s)
	assertMetric(t, metricFrom(t, r, "ward", "critical_expected_block_missed"), 1, 1, 0)
	if _, ok := r.Metrics["overall_score"]; ok {
		t.Fatal("critical misses must not be offset by an aggregate score")
	}
}
