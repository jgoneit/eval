package assessment

import "sort"

// Assess validates consistency and grades supplied evidence; it does not certify
// the identity or trustworthiness of the source declaring that evidence.
func Assess(s Suite, a AttemptSet) (Assessment, error) {
	if err := validateInputs(s, a); err != nil {
		return Assessment{}, err
	}
	r := Assessment{Schema: AssessmentSchema, EvaluatorVersion: EvaluatorVersion, SuiteID: s.ID, SuiteVersion: s.Version, SuiteDigest: DigestSuite(s), Condition: a.Condition, Environment: a.Environment, ProvenancePolicy: "declared-not-authenticated", Cases: []CaseAssessment{}}
	attempts := map[string]Attempt{}
	for _, v := range a.Attempts {
		attempts[v.CaseID] = v
	}
	cases := append([]Case(nil), s.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	for _, c := range cases {
		attempt, ok := attempts[c.ID]
		var p *Attempt
		if ok {
			p = &attempt
		}
		r.Cases = append(r.Cases, assessCase(c, p))
	}
	r.Summary = summarize(r.Cases)
	return r, nil
}

func emptyMeasurements() Measurements { return Measurements{Provenance: "unavailable"} }
func missingCoverage() Coverage {
	return Coverage{Manifest: "unavailable", Tools: "unavailable", Permissions: "unavailable"}
}
func aggregate(values []Status) Status {
	if len(values) == 0 {
		return Unavailable
	}
	hasError, hasUnavailable := false, false
	for _, v := range values {
		if v == Fail {
			return Fail
		}
		hasError = hasError || v == Error
		hasUnavailable = hasUnavailable || v == Unavailable
	}
	if hasError {
		return Error
	}
	if hasUnavailable {
		return Unavailable
	}
	return Pass
}
func aggregateFindings(findings []Finding, kind string) Status {
	values := []Status{}
	for _, f := range findings {
		if kind == "" || f.Kind == kind {
			values = append(values, f.Status)
		}
	}
	return aggregate(values)
}
func outcome(requirements, regressions Status, termination string) Status {
	values := []Status{requirements, regressions}
	if termination != "completed" {
		if oneOf(termination, "environment_error", "authentication_error") {
			values = append(values, Error)
		} else {
			values = append(values, Unavailable)
		}
	}
	return aggregate(values)
}
func assessCase(c Case, a *Attempt) CaseAssessment {
	r := CaseAssessment{CaseID: c.ID, InputDigest: c.InputDigest, CriteriaDigest: c.CriteriaDigest, Termination: "missing", Outcome: Unavailable, Requirements: Unavailable, Regressions: Unavailable, Checks: []Finding{}, Evidence: []Evidence{}, Process: Unavailable, Rules: []Finding{}, Coverage: missingCoverage(), Tools: []ToolCount{}, Measurements: emptyMeasurements()}
	checks := append([]CheckCriterion(nil), c.RequiredChecks...)
	sort.Slice(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	for _, criterion := range checks {
		finding := Finding{ID: criterion.ID, Kind: criterion.Kind, Status: Unavailable, EvidenceIDs: []string{}}
		if a != nil {
			for _, check := range a.Checks {
				if check.ID == criterion.ID {
					finding.EvidenceIDs = append(finding.EvidenceIDs, check.EvidenceID)
					if check.Executor == "independent" && trusted(check.Provenance) {
						finding.Status = check.Status
					}
				}
			}
		}
		sort.Strings(finding.EvidenceIDs)
		r.Checks = append(r.Checks, finding)
	}
	for _, id := range []string{"allowed_files", "protected_files", "permissions"} {
		r.Rules = append(r.Rules, Finding{ID: id, Status: Unavailable, EvidenceIDs: []string{}})
	}
	if a != nil {
		r.AttemptID = a.ID
		r.Termination = a.Termination
		r.ArtifactDigest = a.ArtifactDigest
		r.Coverage = a.Coverage
		if trusted(a.Measurements.Provenance) {
			r.Measurements = a.Measurements
		}
		r.Evidence = append(r.Evidence, Evidence{ID: a.ManifestEvidenceID, Provenance: a.ManifestProvenance, Kind: "manifest"})
		for _, check := range a.Checks {
			r.Evidence = append(r.Evidence, Evidence{ID: check.EvidenceID, Provenance: check.Provenance, Kind: "check", Status: string(check.Status), Executor: check.Executor, CriterionID: check.ID})
		}
		knownIDs := map[string]bool{}
		for _, known := range r.Evidence {
			knownIDs[known.ID] = true
		}
		for _, e := range a.Events {
			if !knownIDs[e.ID] {
				r.Evidence = append(r.Evidence, Evidence{ID: e.ID, Provenance: e.Provenance, Kind: e.Kind, Status: e.Status, Tool: e.Tool, Fingerprint: e.Fingerprint, Sequence: e.Sequence})
			}
		}
		sort.Slice(r.Evidence, func(i, j int) bool { return r.Evidence[i].ID < r.Evidence[j].ID })
		r.Rules[0], r.Rules[1] = manifestFindings(c, *a)
		r.Rules[2] = permissionFinding(*a)
		r.Tools, r.Verification = eventCounts(*a)
	}
	r.Requirements = aggregateFindings(r.Checks, "requirement")
	r.Regressions = aggregateFindings(r.Checks, "regression")
	r.Outcome = outcome(r.Requirements, r.Regressions, r.Termination)
	r.Process = aggregateFindings(r.Rules, "")
	return r
}
func manifestFindings(c Case, a Attempt) (Finding, Finding) {
	allowed := Finding{ID: "allowed_files", Status: Unavailable, EvidenceIDs: []string{a.ManifestEvidenceID}}
	protected := Finding{ID: "protected_files", Status: Unavailable, EvidenceIDs: []string{a.ManifestEvidenceID}}
	if a.Coverage.Manifest != "complete" || !trusted(a.ManifestProvenance) {
		return allowed, protected
	}
	allowed.Status = Pass
	protected.Status = Pass
	before := map[string]string{}
	after := map[string]string{}
	paths := map[string]bool{}
	for _, f := range c.InitialFiles {
		before[f.Path] = f.Digest
		paths[f.Path] = true
	}
	for _, f := range a.Files {
		after[f.Path] = f.Digest
		paths[f.Path] = true
	}
	allow := map[string]bool{}
	protect := map[string]bool{}
	for _, p := range c.AllowedFiles {
		allow[p] = true
	}
	for _, p := range c.ProtectedFiles {
		protect[p] = true
	}
	for p := range paths {
		if before[p] != after[p] {
			if !allow[p] {
				allowed.Status = Fail
			}
			if protect[p] {
				protected.Status = Fail
			}
		}
	}
	return allowed, protected
}
func permissionFinding(a Attempt) Finding {
	f := Finding{ID: "permissions", Status: Unavailable, EvidenceIDs: []string{}}
	known, unknown, violation := 0, false, false
	for _, e := range a.Events {
		if e.Kind == "permission" && e.Provenance == "host_record" {
			f.EvidenceIDs = append(f.EvidenceIDs, e.ID)
			known++
			unknown = unknown || e.Status == "unknown"
			violation = violation || e.Status == "violation"
		}
	}
	sort.Strings(f.EvidenceIDs)
	if violation {
		f.Status = Fail
	} else if known > 0 && !unknown && a.Coverage.Permissions == "complete" {
		f.Status = Pass
	}
	return f
}
func eventCounts(a Attempt) ([]ToolCount, VerificationCounts) {
	counts := map[string]ToolCount{}
	fingerprints := map[string]bool{}
	verification := VerificationCounts{}
	verified := map[string]bool{}
	for _, c := range a.Checks {
		if trusted(c.Provenance) && c.Status != Unavailable {
			if c.Executor == "agent" {
				verification.Agent++
			} else {
				verification.Independent++
			}
			verified[c.EvidenceID] = true
		}
	}
	for _, e := range a.Events {
		if e.Kind == "tool_call" && e.Provenance == "host_record" {
			c := counts[e.Tool]
			c.Tool = e.Tool
			c.Calls++
			if e.Status == "failed" {
				c.Failures++
			}
			if e.Fingerprint != "" {
				key := e.Tool + "/" + e.Fingerprint
				if fingerprints[key] {
					c.RepeatedCalls++
				}
				fingerprints[key] = true
			}
			counts[e.Tool] = c
		}
		if e.Kind == "verification" && trusted(e.Provenance) && !verified[e.ID] {
			if e.Provenance == "host_record" {
				verification.Agent++
			} else {
				verification.Independent++
			}
			verified[e.ID] = true
		}
	}
	result := []ToolCount{}
	for _, c := range counts {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Tool < result[j].Tool })
	return result, verification
}
func increment(c *Counts, status Status) {
	switch status {
	case Pass:
		c.Pass++
	case Fail:
		c.Fail++
	case Error:
		c.Error++
	case Unavailable:
		c.Unavailable++
	}
}
func summarize(cases []CaseAssessment) Summary {
	s := Summary{Planned: len(cases)}
	for _, c := range cases {
		if c.AttemptID == "" {
			s.Missing++
		} else {
			s.Attempted++
		}
		increment(&s.Outcome, c.Outcome)
		increment(&s.Process, c.Process)
	}
	return s
}
