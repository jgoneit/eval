package assessment

import (
	"reflect"
	"sort"
)

// ValidateAssessment regrades retained private inputs and verifies the entire
// derived result. Matching digests do not authenticate the input producer.
func ValidateAssessment(a Assessment, inputs AssessmentInputs) error {
	if inputs.Schema != InputsSchema || a.Schema != AssessmentSchema || a.EvaluatorVersion != EvaluatorVersion || !digest(a.InputsDigest) || a.InputsDigest != DigestInputs(inputs) {
		return ErrInvalid
	}
	expected, err := Assess(inputs.Suite, inputs.Attempts)
	if err != nil || !reflect.DeepEqual(a, expected) {
		return ErrInvalid
	}
	return nil
}

func configurationsComparable(b, c ConfigurationObservation) bool {
	return b.Status == "unchanged" && c.Status == "unchanged" && b.ExpectedDigest != nil && c.ExpectedDigest != nil && *b.ExpectedDigest == *c.ExpectedDigest
}

func Compare(baselineRecord, candidateRecord AssessmentRecord) (Comparison, error) {
	baseline, candidate := baselineRecord.Assessment, candidateRecord.Assessment
	if ValidateAssessment(baseline, baselineRecord.Inputs) != nil || ValidateAssessment(candidate, candidateRecord.Inputs) != nil || baseline.SuiteID != candidate.SuiteID || baseline.SuiteVersion != candidate.SuiteVersion || baseline.SuiteDigest != candidate.SuiteDigest || baseline.Environment != candidate.Environment || len(baseline.Cases) != len(candidate.Cases) {
		return Comparison{}, ErrInvalid
	}
	r := Comparison{Schema: ComparisonSchema, EvaluatorVersion: EvaluatorVersion, SuiteID: baseline.SuiteID, SuiteVersion: baseline.SuiteVersion, SuiteDigest: baseline.SuiteDigest, Baseline: baseline.Condition, Candidate: candidate.Condition, Environment: baseline.Environment, Cases: []CaseComparison{}}
	r.Kind = "different_instruction"
	if baseline.Condition.InstructionDigest == candidate.Condition.InstructionDigest {
		r.Kind = "same_instruction"
	}
	base := map[string]CaseAssessment{}
	for _, c := range baseline.Cases {
		base[c.CaseID] = c
	}
	cases := append([]CaseAssessment(nil), candidate.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].CaseID < cases[j].CaseID })
	for _, c := range cases {
		b, ok := base[c.CaseID]
		if !ok || b.InputDigest != c.InputDigest || b.CriteriaDigest != c.CriteriaDigest {
			return Comparison{}, ErrInvalid
		}
		row := CaseComparison{BaselineProcess: b.Process, CandidateProcess: c.Process, Rules: []RuleComparison{}, CaseID: c.CaseID, Baseline: b.Outcome, Candidate: c.Outcome, OutcomeChange: completedChange(b.Outcome, c.Outcome, b.Termination, c.Termination), ProcessChange: completedChange(b.Process, c.Process, b.Termination, c.Termination), BaselineTermination: b.Termination, CandidateTermination: c.Termination, Delta: MeasurementDelta{DurationMS: delta(b.Measurements.DurationMS, c.Measurements.DurationMS), InputTokens: delta(b.Measurements.InputTokens, c.Measurements.InputTokens), OutputTokens: delta(b.Measurements.OutputTokens, c.Measurements.OutputTokens), CachedInputTokens: delta(b.Measurements.CachedInputTokens, c.Measurements.CachedInputTokens)}}
		row.BaselineConfiguration = b.ConfigurationObservation.Status
		row.CandidateConfiguration = c.ConfigurationObservation.Status
		row.ConfigurationComparable = configurationsComparable(b.ConfigurationObservation, c.ConfigurationObservation)
		if !row.ConfigurationComparable {
			row.OutcomeChange, row.ProcessChange = "unevaluated", "unevaluated"
		}
		baseRules := map[string]Status{}
		for _, f := range b.Rules {
			baseRules[f.ID] = f.Status
		}
		for _, f := range c.Rules {
			ruleChange := completedChange(baseRules[f.ID], f.Status, b.Termination, c.Termination)
			if !row.ConfigurationComparable {
				ruleChange = "unevaluated"
			}
			row.Rules = append(row.Rules, RuleComparison{ID: f.ID, Baseline: baseRules[f.ID], Candidate: f.Status, Change: ruleChange})
		}
		sort.Slice(row.Rules, func(i, j int) bool { return row.Rules[i].ID < row.Rules[j].ID })
		r.Cases = append(r.Cases, row)
		r.Summary.Planned++
		switch row.OutcomeChange {
		case "improved":
			r.Summary.Comparable++
			r.Summary.Improved++
		case "regressed":
			r.Summary.Comparable++
			r.Summary.Regressed++
		case "unchanged":
			r.Summary.Comparable++
			r.Summary.Unchanged++
		default:
			r.Summary.Unevaluated++
		}
		if b.Termination == "missing" {
			r.Summary.BaselineMissing++
		}
		if c.Termination == "missing" {
			r.Summary.CandidateMissing++
		}
		anyMeasurement := false
		if row.Delta.DurationMS != nil {
			r.Summary.DurationMeasuredPairs++
			anyMeasurement = true
		}
		if row.Delta.InputTokens != nil {
			r.Summary.InputTokensMeasuredPairs++
			anyMeasurement = true
		}
		if row.Delta.OutputTokens != nil {
			r.Summary.OutputTokensMeasuredPairs++
			anyMeasurement = true
		}
		if row.Delta.CachedInputTokens != nil {
			r.Summary.CachedInputTokensMeasuredPairs++
			anyMeasurement = true
		}
		if anyMeasurement {
			r.Summary.MeasuredPairs++
		}
	}
	return r, nil
}
func completedChange(b, c Status, baselineTermination, candidateTermination string) string {
	if baselineTermination != "completed" || candidateTermination != "completed" {
		return "unevaluated"
	}
	return change(b, c)
}
func change(b, c Status) string {
	if (b != Pass && b != Fail) || (c != Pass && c != Fail) {
		return "unevaluated"
	}
	if b == c {
		return "unchanged"
	}
	if c == Pass {
		return "improved"
	}
	return "regressed"
}
func delta(b, c *int64) *int64 {
	if b == nil || c == nil {
		return nil
	}
	v := *c - *b
	return &v
}
