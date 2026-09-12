package assessment

import (
	"reflect"
	"sort"
)

// ValidateAssessment verifies its public shape and internal arithmetic. It does
// not authenticate a producer, or independently recover the original files.
func ValidateAssessment(a Assessment) error {
	if a.Schema != AssessmentSchema || a.EvaluatorVersion != EvaluatorVersion || !token(a.SuiteID) || !token(a.SuiteVersion) || !digest(a.SuiteDigest) || validateCondition(a.Condition) != nil || validateEnvironment(a.Environment) != nil || a.ProvenancePolicy != "declared-not-authenticated" || len(a.Cases) == 0 || len(a.Cases) > 1000 {
		return ErrInvalid
	}
	s := Suite{Schema: SuiteSchema, ID: a.SuiteID, Version: a.SuiteVersion}
	seen := map[string]bool{}
	attempts := map[string]bool{}
	for _, c := range a.Cases {
		if !token(c.CaseID) || seen[c.CaseID] || !digest(c.InputDigest) || !digest(c.CriteriaDigest) || validateCoverage(c.Coverage) != nil || validateMeasurements(c.Measurements) != nil || c.Measurements.Provenance == "agent_report" {
			return ErrInvalid
		}
		seen[c.CaseID] = true
		if c.AttemptID == "" {
			if c.Termination != "missing" || c.ArtifactDigest != "" || c.Coverage != missingCoverage() || len(c.Evidence) != 0 || len(c.Tools) != 0 || c.Verification != (VerificationCounts{}) || !reflect.DeepEqual(c.Measurements, emptyMeasurements()) {
				return ErrInvalid
			}
		} else {
			if !token(c.AttemptID) || attempts[c.AttemptID] || !digest(c.ArtifactDigest) || !oneOf(c.Termination, "completed", "timeout", "interrupted", "agent_error", "environment_error", "authentication_error") {
				return ErrInvalid
			}
			attempts[c.AttemptID] = true
		}
		evidence := map[string]Evidence{}
		retained := Attempt{Coverage: c.Coverage}
		sequences := map[int]bool{}
		manifestID := ""
		for _, e := range c.Evidence {
			if !token(e.ID) || !provenance(e.Provenance) || !oneOf(e.Kind, "manifest", "check", "tool_call", "verification", "permission", "observation_gap") {
				return ErrInvalid
			}
			if _, ok := evidence[e.ID]; ok {
				return ErrInvalid
			}
			evidence[e.ID] = e
			switch e.Kind {
			case "check":
				if !validStatus(Status(e.Status)) || !oneOf(e.Executor, "agent", "independent") || !token(e.CriterionID) || e.Tool != "" || e.Fingerprint != "" || e.Sequence != 0 {
					return ErrInvalid
				}
				retained.Checks = append(retained.Checks, Check{ID: e.CriterionID, EvidenceID: e.ID, Executor: e.Executor, Provenance: e.Provenance, Status: Status(e.Status)})
			case "manifest":
				if e.Status != "" || e.Executor != "" || e.CriterionID != "" || e.Tool != "" || e.Fingerprint != "" || e.Sequence != 0 || manifestID != "" {
					return ErrInvalid
				}
				manifestID = e.ID
			case "permission":
				if !oneOf(e.Status, "allowed", "denied", "violation", "unknown") || e.Executor != "" || e.CriterionID != "" || e.Tool != "" || e.Fingerprint != "" {
					return ErrInvalid
				}
			default:
				if !oneOf(e.Status, "succeeded", "failed", "unknown") || e.Executor != "" || e.CriterionID != "" {
					return ErrInvalid
				}
				if oneOf(e.Kind, "tool_call", "verification") {
					if !oneOf(e.Tool, "shell", "file_change", "mcp", "web", "plan", "other") || (e.Fingerprint != "" && !digest(e.Fingerprint)) {
						return ErrInvalid
					}
				} else if e.Tool != "" || e.Fingerprint != "" || c.Coverage.Tools == "complete" || c.Coverage.Permissions == "complete" {
					return ErrInvalid
				}
			}
			if e.Kind != "manifest" && e.Kind != "check" {
				if e.Sequence <= 0 || sequences[e.Sequence] {
					return ErrInvalid
				}
				sequences[e.Sequence] = true
				retained.Events = append(retained.Events, Event{ID: e.ID, Sequence: e.Sequence, Kind: e.Kind, Provenance: e.Provenance, Status: e.Status, Tool: e.Tool, Fingerprint: e.Fingerprint})
			}
		}
		if c.AttemptID != "" && manifestID == "" {
			return ErrInvalid
		}
		checkIDs := map[string]bool{}
		checkReferences := map[string]bool{}
		kinds := map[string]bool{}
		for _, f := range c.Checks {
			if !token(f.ID) || checkIDs[f.ID] || !oneOf(f.Kind, "requirement", "regression") || !validStatus(f.Status) {
				return ErrInvalid
			}
			checkIDs[f.ID] = true
			kinds[f.Kind] = true
			if validateReferences(f.EvidenceIDs, evidence) != nil {
				return ErrInvalid
			}
			status := Unavailable
			independent := 0
			for _, id := range f.EvidenceIDs {
				e := evidence[id]
				if e.Kind != "check" || e.CriterionID != f.ID || checkReferences[id] {
					return ErrInvalid
				}
				checkReferences[id] = true
				if trusted(e.Provenance) && e.Executor == "independent" {
					independent++
					status = Status(e.Status)
				}
			}
			if independent > 1 || status != f.Status {
				return ErrInvalid
			}
		}
		for id, e := range evidence {
			if e.Kind == "check" && !checkReferences[id] {
				return ErrInvalid
			}
		}
		if !kinds["requirement"] || !kinds["regression"] || len(c.Checks) > 1000 {
			return ErrInvalid
		}
		if len(c.Rules) != 3 {
			return ErrInvalid
		}
		ruleIDs := map[string]bool{}
		for _, f := range c.Rules {
			if !oneOf(f.ID, "allowed_files", "protected_files", "permissions") || ruleIDs[f.ID] || f.Kind != "" || !validStatus(f.Status) || f.Status == Error || validateReferences(f.EvidenceIDs, evidence) != nil {
				return ErrInvalid
			}
			ruleIDs[f.ID] = true
			if f.ID == "permissions" {
				expected := permissionFinding(retained)
				if expected.Status != f.Status || !sameStrings(expected.EvidenceIDs, f.EvidenceIDs) {
					return ErrInvalid
				}
			} else {
				if (manifestID == "" && len(f.EvidenceIDs) != 0) || (manifestID != "" && (len(f.EvidenceIDs) != 1 || f.EvidenceIDs[0] != manifestID)) {
					return ErrInvalid
				}
				for _, id := range f.EvidenceIDs {
					if evidence[id].Kind != "manifest" {
						return ErrInvalid
					}
				}
				if f.Status != Unavailable {
					if c.Coverage.Manifest != "complete" || len(f.EvidenceIDs) != 1 || !trusted(evidence[f.EvidenceIDs[0]].Provenance) {
						return ErrInvalid
					}
				}
			}
		}
		if c.Requirements != aggregateFindings(c.Checks, "requirement") || c.Regressions != aggregateFindings(c.Checks, "regression") || c.Outcome != outcome(c.Requirements, c.Regressions, c.Termination) || c.Process != aggregateFindings(c.Rules, "") {
			return ErrInvalid
		}
		tools := map[string]bool{}
		for _, t := range c.Tools {
			if !oneOf(t.Tool, "shell", "file_change", "mcp", "web", "plan", "other") || tools[t.Tool] || t.Calls <= 0 || t.Failures < 0 || t.RepeatedCalls < 0 || t.Failures > t.Calls || t.RepeatedCalls >= t.Calls {
				return ErrInvalid
			}
			tools[t.Tool] = true
		}
		expectedTools, expectedVerification := eventCounts(retained)
		if !reflect.DeepEqual(expectedTools, c.Tools) || expectedVerification != c.Verification {
			return ErrInvalid
		}
		s.Cases = append(s.Cases, Case{ID: c.CaseID, InputDigest: c.InputDigest, CriteriaDigest: c.CriteriaDigest})
	}
	if a.SuiteDigest != DigestSuite(s) || a.Summary != summarize(a.Cases) {
		return ErrInvalid
	}
	return nil
}
func sameStrings(a, b []string) bool {
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}
func sameCheckCriteria(a, b []Finding) bool {
	aa := []string{}
	bb := []string{}
	for _, f := range a {
		aa = append(aa, f.ID+"/"+f.Kind)
	}
	for _, f := range b {
		bb = append(bb, f.ID+"/"+f.Kind)
	}
	return sameStrings(aa, bb)
}
func validateReferences(ids []string, evidence map[string]Evidence) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := evidence[id]; !ok || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

func Compare(baseline, candidate Assessment) (Comparison, error) {
	if ValidateAssessment(baseline) != nil || ValidateAssessment(candidate) != nil || baseline.SuiteID != candidate.SuiteID || baseline.SuiteVersion != candidate.SuiteVersion || baseline.SuiteDigest != candidate.SuiteDigest || baseline.Environment != candidate.Environment || len(baseline.Cases) != len(candidate.Cases) {
		return Comparison{}, ErrInvalid
	}
	r := Comparison{Schema: ComparisonSchema, EvaluatorVersion: EvaluatorVersion, SuiteID: baseline.SuiteID, SuiteVersion: baseline.SuiteVersion, SuiteDigest: baseline.SuiteDigest, Baseline: baseline.Condition, Candidate: candidate.Condition, Environment: baseline.Environment, Cases: []CaseComparison{}}
	base := map[string]CaseAssessment{}
	for _, c := range baseline.Cases {
		base[c.CaseID] = c
	}
	cases := append([]CaseAssessment(nil), candidate.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].CaseID < cases[j].CaseID })
	for _, c := range cases {
		b, ok := base[c.CaseID]
		if !ok || b.InputDigest != c.InputDigest || b.CriteriaDigest != c.CriteriaDigest || !sameCheckCriteria(b.Checks, c.Checks) {
			return Comparison{}, ErrInvalid
		}
		row := CaseComparison{BaselineProcess: b.Process, CandidateProcess: c.Process, Rules: []RuleComparison{}, CaseID: c.CaseID, Baseline: b.Outcome, Candidate: c.Outcome, OutcomeChange: completedChange(b.Outcome, c.Outcome, b.Termination, c.Termination), ProcessChange: completedChange(b.Process, c.Process, b.Termination, c.Termination), BaselineTermination: b.Termination, CandidateTermination: c.Termination, Delta: MeasurementDelta{DurationMS: delta(b.Measurements.DurationMS, c.Measurements.DurationMS), InputTokens: delta(b.Measurements.InputTokens, c.Measurements.InputTokens), OutputTokens: delta(b.Measurements.OutputTokens, c.Measurements.OutputTokens), CachedInputTokens: delta(b.Measurements.CachedInputTokens, c.Measurements.CachedInputTokens)}}
		baseRules := map[string]Status{}
		for _, f := range b.Rules {
			baseRules[f.ID] = f.Status
		}
		for _, f := range c.Rules {
			row.Rules = append(row.Rules, RuleComparison{ID: f.ID, Baseline: baseRules[f.ID], Candidate: f.Status, Change: completedChange(baseRules[f.ID], f.Status, b.Termination, c.Termination)})
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
