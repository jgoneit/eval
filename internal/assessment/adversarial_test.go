package assessment

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func advDigest(c string) string { return strings.Repeat(c, 64) }

func advFixture() (Suite, AttemptSet) {
	c := Case{
		ID:             "boundary",
		InitialFiles:   []File{{Path: "solution.go", Digest: advDigest("a")}, {Path: "public_test.go", Digest: advDigest("b")}, {Path: "requirements.md", Digest: advDigest("c")}},
		AllowedFiles:   []string{"solution.go"},
		ProtectedFiles: []string{"public_test.go", "requirements.md"},
		RequiredChecks: []CheckCriterion{{ID: "requirements", Kind: "requirement", CheckerDigest: advDigest("d")}, {ID: "compatibility", Kind: "regression", CheckerDigest: advDigest("e")}},
	}
	c.InputDigest, c.CriteriaDigest = DigestFiles(c.InitialFiles), DigestCriteria(c)
	s := Suite{Schema: SuiteSchema, ID: "adversarial", Version: "1", Cases: []Case{c}}
	files := append([]File(nil), c.InitialFiles...)
	files[0].Digest = advDigest("f")
	config := advDigest("6")
	a := Attempt{ConfigurationObservation: &ConfigurationObservation{Status: "unchanged", ExpectedDigest: &config, BeforeDigest: &config, AfterDigest: &config}, ID: "attempt-1", CaseID: c.ID, Termination: "completed", Files: files, ArtifactDigest: DigestFiles(files), ManifestProvenance: "host_record", ManifestEvidenceID: "manifest-1", Coverage: Coverage{Manifest: "complete", Tools: "complete", Permissions: "unavailable"}, Measurements: Measurements{Provenance: "unavailable"}}
	for _, criterion := range c.RequiredChecks {
		a.Checks = append(a.Checks, Check{ID: criterion.ID, EvidenceID: "check-" + criterion.ID, Provenance: "independent_check", Executor: "independent", CheckerDigest: criterion.CheckerDigest, ArtifactDigest: a.ArtifactDigest, ArtifactAfterDigest: a.ArtifactDigest, Status: Pass})
	}
	return s, AttemptSet{Schema: AttemptsSchema, SuiteID: s.ID, SuiteVersion: s.Version, SuiteDigest: DigestSuite(s), Condition: Condition{ID: "baseline", InstructionDigest: advDigest("1")}, Environment: Environment{Model: "test-model", Reasoning: "high", PermissionProfile: "workspace-write", EnvironmentDigest: advDigest("2"), ToolDigest: advDigest("3")}, Attempts: []Attempt{a}}
}

func advGrade(t *testing.T, s Suite, attempts AttemptSet) Assessment {
	t.Helper()
	r, err := Assess(s, attempts)
	if err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	return r
}

// advRecord freezes retained inputs before subsequent test mutations.
func advRecord(t *testing.T, s Suite, attempts AttemptSet) AssessmentRecord {
	t.Helper()
	data, err := json.Marshal(AssessmentInputs{Schema: InputsSchema, Suite: s, Attempts: attempts})
	if err != nil {
		t.Fatal(err)
	}
	var inputs AssessmentInputs
	if err := json.Unmarshal(data, &inputs); err != nil {
		t.Fatal(err)
	}
	return AssessmentRecord{Assessment: advGrade(t, s, attempts), Inputs: inputs}
}

func advRefreshArtifact(a *Attempt) {
	a.ArtifactDigest = DigestFiles(a.Files)
	for i := range a.Checks {
		a.Checks[i].ArtifactDigest, a.Checks[i].ArtifactAfterDigest = a.ArtifactDigest, a.ArtifactDigest
	}
}

func TestAdversarialKnownFailureMissingAndCheckerErrorsRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Attempt)
		want   Status
	}{
		{"verified", func(*Attempt) {}, Pass},
		{"missing-required-check", func(a *Attempt) { a.Checks = a.Checks[:1] }, Unavailable},
		{"failure-with-missing-check", func(a *Attempt) { a.Checks = a.Checks[:1]; a.Checks[0].Status = Fail }, Fail},
		{"checker-error", func(a *Attempt) { a.Checks[0].Status = Error }, Error},
		{"failure-with-checker-error", func(a *Attempt) { a.Checks[0].Status = Fail; a.Checks[1].Status = Error }, Fail},
		{"self-report-success", func(a *Attempt) { a.Checks[0].Provenance = "agent_report"; a.Checks[0].Executor = "agent" }, Unavailable},
		{"host-recorded-independent-check", func(a *Attempt) { a.Checks[0].Provenance = "host_record" }, Pass},
		{"interrupted", func(a *Attempt) { a.Termination = "interrupted" }, Unavailable},
		{"environment-error", func(a *Attempt) { a.Termination = "environment_error" }, Error},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			test.mutate(&a.Attempts[0])
			got := advGrade(t, s, a).Cases[0]
			if got.Outcome != test.want {
				t.Fatalf("outcome=%s want=%s", got.Outcome, test.want)
			}
		})
	}
}

func TestAdversarialRejectsAmbiguousAndMismatchedEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Suite, *AttemptSet)
	}{
		{"duplicate-case", func(s *Suite, a *AttemptSet) { s.Cases = append(s.Cases, s.Cases[0]); a.SuiteDigest = DigestSuite(*s) }},
		{"duplicate-attempt-id", func(s *Suite, a *AttemptSet) {
			other := s.Cases[0]
			other.ID = "other"
			s.Cases = append(s.Cases, other)
			a.SuiteDigest = DigestSuite(*s)
			attempt := a.Attempts[0]
			attempt.CaseID = "other"
			a.Attempts = append(a.Attempts, attempt)
		}},
		{"second-attempt-same-case", func(s *Suite, a *AttemptSet) {
			other := s.Cases[0]
			other.ID = "missing"
			s.Cases = append(s.Cases, other)
			a.SuiteDigest = DigestSuite(*s)
			retry := a.Attempts[0]
			retry.ID = "retry-success"
			a.Attempts = append(a.Attempts, retry)
		}},
		{"duplicate-manifest-path", func(_ *Suite, a *AttemptSet) {
			a.Attempts[0].Files = append(a.Attempts[0].Files, a.Attempts[0].Files[0])
			advRefreshArtifact(&a.Attempts[0])
		}},
		{"absolute-manifest-path", func(_ *Suite, a *AttemptSet) {
			a.Attempts[0].Files[0].Path = "/private/path-canary/solution.go"
			advRefreshArtifact(&a.Attempts[0])
		}},
		{"traversal-manifest-path", func(_ *Suite, a *AttemptSet) {
			a.Attempts[0].Files[0].Path = "src/../solution.go"
			advRefreshArtifact(&a.Attempts[0])
		}},
		{"wrong-artifact", func(_ *Suite, a *AttemptSet) { a.Attempts[0].ArtifactDigest = advDigest("9") }},
		{"check-bound-to-old-artifact", func(_ *Suite, a *AttemptSet) { a.Attempts[0].Checks[0].ArtifactDigest = advDigest("9") }},
		{"artifact-changed-after-check", func(_ *Suite, a *AttemptSet) { a.Attempts[0].Checks[0].ArtifactAfterDigest = advDigest("9") }},
		{"checker-substitution", func(_ *Suite, a *AttemptSet) { a.Attempts[0].Checks[0].CheckerDigest = advDigest("9") }},
		{"duplicate-evidence-id", func(_ *Suite, a *AttemptSet) { a.Attempts[0].Checks[1].EvidenceID = a.Attempts[0].Checks[0].EvidenceID }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			test.mutate(&s, &a)
			if _, err := Assess(s, a); err == nil {
				t.Fatal("accepted ambiguous or mismatched evidence")
			}
		})
	}
}

func TestAdversarialScopeUsesAllChangesEvenWhenResultPasses(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Attempt)
	}{
		{"modified-public-test", func(a *Attempt) { a.Files[1].Digest = advDigest("7") }},
		{"deleted-public-test", func(a *Attempt) { a.Files = append(a.Files[:1], a.Files[2:]...) }},
		{"added-outside-allowlist", func(a *Attempt) { a.Files = append(a.Files, File{Path: "solution.go.extra", Digest: advDigest("7")}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			test.mutate(&a.Attempts[0])
			advRefreshArtifact(&a.Attempts[0])
			got := advGrade(t, s, a).Cases[0]
			if got.Outcome != Pass || got.Process != Fail {
				t.Fatalf("result and scope conflated: outcome=%s process=%s", got.Outcome, got.Process)
			}
		})
	}
}

func TestAdversarialMissingCasesStayInPlannedPopulation(t *testing.T) {
	s, a := advFixture()
	for _, id := range []string{"missing-one", "missing-two"} {
		c := s.Cases[0]
		c.ID = id
		s.Cases = append(s.Cases, c)
	}
	a.SuiteDigest = DigestSuite(s)
	r := advGrade(t, s, a)
	if r.Summary.Planned != 3 || r.Summary.Attempted != 1 || r.Summary.Missing != 2 || r.Summary.Outcome.Pass != 1 || r.Summary.Outcome.Unavailable != 2 {
		t.Fatalf("wrong denominator: %+v", r.Summary)
	}
	for _, c := range r.Cases {
		if c.Termination == "missing" && (c.Outcome != Unavailable || c.Measurements.DurationMS != nil || c.Measurements.InputTokens != nil) {
			t.Fatalf("missing case became success or zero measurement: %+v", c)
		}
	}
}

func TestAdversarialSelfReportsCannotInventHostActivityOrCosts(t *testing.T) {
	s, a := advFixture()
	a.Attempts[0].Events = []Event{
		{ID: "tool-1", Sequence: 1, Kind: "tool_call", Provenance: "host_record", Tool: "shell", Status: "succeeded"},
		{ID: "tool-2", Sequence: 2, Kind: "tool_call", Provenance: "host_record", Tool: "shell", Status: "succeeded"},
		{ID: "tool-3", Sequence: 3, Kind: "tool_call", Provenance: "host_record", Tool: "shell", Status: "failed", Fingerprint: advDigest("7")},
		{ID: "tool-4", Sequence: 4, Kind: "tool_call", Provenance: "host_record", Tool: "shell", Status: "succeeded", Fingerprint: advDigest("7")},
		{ID: "claimed-tool", Sequence: 5, Kind: "tool_call", Provenance: "agent_report", Tool: "shell", Status: "failed", Fingerprint: advDigest("7")},
		{ID: "claimed-check", Sequence: 6, Kind: "verification", Provenance: "agent_report", Tool: "shell", Status: "succeeded"},
		{ID: "claimed-permission", Sequence: 7, Kind: "permission", Provenance: "agent_report", Status: "violation"},
	}
	value := int64(10)
	a.Attempts[0].Measurements = Measurements{Provenance: "agent_report", DurationMS: &value, InputTokens: &value}
	r := advGrade(t, s, a).Cases[0]
	want := []ToolCount{{Tool: "shell", Calls: 4, Failures: 1, RepeatedCalls: 1}}
	if !reflect.DeepEqual(r.Tools, want) {
		t.Fatalf("invented calls or repeats: %+v", r.Tools)
	}
	if r.Verification.Agent != 0 || r.Verification.Independent != 2 {
		t.Fatalf("invented verification: %+v", r.Verification)
	}
	if r.Process != Unavailable {
		t.Fatalf("self-report changed permission judgment: %s", r.Process)
	}
	if r.Measurements.DurationMS != nil || r.Measurements.InputTokens != nil {
		t.Fatal("self-reported costs presented as observed")
	}
}

func TestAdversarialPermissionJudgmentNeedsExplicitHostEvidence(t *testing.T) {
	for _, test := range []struct {
		name, coverage, provenance, status string
		want                               Status
	}{
		{"no-events", "complete", "", "", Unavailable},
		{"denied-with-complete-coverage", "complete", "host_record", "denied", Pass},
		{"denied-with-partial-coverage", "partial", "host_record", "denied", Unavailable},
		{"explicit-violation-with-partial-coverage", "partial", "host_record", "violation", Fail},
		{"unknown-host-event", "complete", "host_record", "unknown", Unavailable},
		{"independent-check-is-not-host", "complete", "independent_check", "violation", Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			a.Attempts[0].Coverage.Permissions = test.coverage
			if test.provenance != "" {
				a.Attempts[0].Events = []Event{{ID: "permission-1", Sequence: 1, Kind: "permission", Provenance: test.provenance, Status: test.status}}
			}
			r := advGrade(t, s, a).Cases[0]
			if r.Process != test.want {
				t.Fatalf("process=%s want=%s", r.Process, test.want)
			}
		})
	}
}

func TestAdversarialUnknownCostNeverBecomesZeroAndReportsAreStable(t *testing.T) {
	s, a := advFixture()
	baseline := advRecord(t, s, a)
	a.Condition = Condition{ID: "candidate", InstructionDigest: advDigest("4")}
	zero := int64(0)
	a.Attempts[0].Measurements = Measurements{Provenance: "host_record", DurationMS: &zero, InputTokens: &zero}
	candidate := advRecord(t, s, a)
	comparison, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Cases[0].Delta.DurationMS != nil || comparison.Cases[0].Delta.InputTokens != nil || comparison.Summary.DurationMeasuredPairs != 0 {
		t.Fatal("missing baseline treated as zero")
	}
	first, _ := json.Marshal(comparison)
	secondComparison, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(secondComparison)
	if string(first) != string(second) || ComparisonMarkdown(comparison) != ComparisonMarkdown(secondComparison) || Markdown(baseline.Assessment) != Markdown(advGrade(t, s, func() AttemptSet { _, original := advFixture(); return original }())) {
		t.Fatal("same input changed report bytes")
	}
	if strings.Contains(string(first), "solution.go") || strings.Contains(string(first), "public_test.go") {
		t.Fatal("file paths leaked into comparison")
	}
}

func TestAdversarialComparisonRejectsChangedCriteriaOrEnvironment(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Assessment)
	}{
		{"criteria", func(a *Assessment) { a.Cases[0].CriteriaDigest = advDigest("6") }},
		{"input", func(a *Assessment) { a.Cases[0].InputDigest = advDigest("6") }},
		{"model", func(a *Assessment) { a.Environment.Model = "other-model" }},
		{"toolchain", func(a *Assessment) { a.Environment.ToolDigest = advDigest("6") }},
		{"permissions", func(a *Assessment) { a.Environment.PermissionProfile = "read-only" }},
		{"summary", func(a *Assessment) { a.Summary.Planned++ }},
		{"forged-outcome", func(a *Assessment) { a.Cases[0].Outcome = Fail }},
		{"forged-tool-count", func(a *Assessment) { a.Cases[0].Tools = []ToolCount{{Tool: "shell", Calls: 1}} }},
		{"forged-verification-count", func(a *Assessment) { a.Cases[0].Verification.Independent++ }},
		{"same-evidence-for-different-checks", func(a *Assessment) { a.Cases[0].Checks[1].EvidenceIDs = a.Cases[0].Checks[0].EvidenceIDs }},
		{"cross-attached-check-evidence", func(a *Assessment) {
			a.Cases[0].Checks[0].EvidenceIDs, a.Cases[0].Checks[1].EvidenceIDs = a.Cases[0].Checks[1].EvidenceIDs, a.Cases[0].Checks[0].EvidenceIDs
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			baseline := advRecord(t, s, a)
			a.Condition = Condition{ID: "candidate", InstructionDigest: advDigest("4")}
			candidate := advRecord(t, s, a)
			test.mutate(&candidate.Assessment)
			if _, err := Compare(baseline, candidate); err == nil {
				t.Fatal("accepted incompatible or inconsistent assessment")
			}
		})
	}
}

func TestAdversarialComparisonCannotDiscardRetainedPermissionViolation(t *testing.T) {
	s, attempts := advFixture()
	attempts.Attempts[0].Events = []Event{{ID: "violation-1", Sequence: 1, Kind: "permission", Provenance: "host_record", Status: "violation"}}
	baseline := advRecord(t, s, attempts)
	attempts.Condition = Condition{ID: "candidate", InstructionDigest: advDigest("4")}
	candidate := advRecord(t, s, attempts)
	for index := range candidate.Assessment.Cases[0].Rules {
		if candidate.Assessment.Cases[0].Rules[index].ID == "permissions" {
			candidate.Assessment.Cases[0].Rules[index].EvidenceIDs = []string{}
			candidate.Assessment.Cases[0].Rules[index].Status = Unavailable
		}
	}
	candidate.Assessment.Cases[0].Process = Unavailable
	candidate.Assessment.Summary.Process = Counts{Unavailable: 1}
	if _, err := Compare(baseline, candidate); err == nil {
		t.Fatal("omitted retained violation evidence to forge process grade")
	}
}

func TestAdversarialComparisonCannotDropARequiredCheckWithUnchangedDigest(t *testing.T) {
	s, attempts := advFixture()
	s.Cases[0].RequiredChecks = append(s.Cases[0].RequiredChecks, CheckCriterion{ID: "additional", Kind: "requirement", CheckerDigest: advDigest("8")})
	s.Cases[0].CriteriaDigest = DigestCriteria(s.Cases[0])
	attempts.SuiteDigest = DigestSuite(s)
	additional := attempts.Attempts[0].Checks[0]
	additional.ID, additional.EvidenceID, additional.CheckerDigest = "additional", "additional-evidence", advDigest("8")
	attempts.Attempts[0].Checks = append(attempts.Attempts[0].Checks, additional)
	baseline := advRecord(t, s, attempts)
	attempts.Condition = Condition{ID: "candidate", InstructionDigest: advDigest("4")}
	candidate := advRecord(t, s, attempts)
	for index, check := range candidate.Assessment.Cases[0].Checks {
		if check.ID == "additional" {
			candidate.Assessment.Cases[0].Checks = append(candidate.Assessment.Cases[0].Checks[:index], candidate.Assessment.Cases[0].Checks[index+1:]...)
			break
		}
	}
	if _, err := Compare(baseline, candidate); err == nil {
		t.Fatal("compared different check sets with unchanged declared criteria digest")
	}
}

func TestAdversarialComparisonMatchesHandCalculatedFourCasePopulation(t *testing.T) {
	s, baselineAttempts := advFixture()
	_, candidateAttempts := advFixture()
	s.Cases, baselineAttempts.Attempts, candidateAttempts.Attempts = nil, nil, nil
	candidateAttempts.Condition = Condition{ID: "candidate", InstructionDigest: advDigest("4")}
	for index, id := range []string{"improvement", "regression", "checker-error", "missing"} {
		oneSuite, oneBaseline := advFixture()
		_, oneCandidate := advFixture()
		c := oneSuite.Cases[0]
		c.ID = id
		s.Cases = append(s.Cases, c)
		b, candidate := oneBaseline.Attempts[0], oneCandidate.Attempts[0]
		b.ID, candidate.ID, b.CaseID, candidate.CaseID = "baseline-"+id, "candidate-"+id, id, id
		switch index {
		case 0:
			b.Checks[0].Status = Fail
			bd, cd := int64(10), int64(15)
			b.Measurements = Measurements{Provenance: "host_record", DurationMS: &bd}
			candidate.Measurements = Measurements{Provenance: "host_record", DurationMS: &cd}
		case 1:
			candidate.Checks[0].Status = Fail
			bd, cd := int64(20), int64(10)
			b.Measurements = Measurements{Provenance: "host_record", DurationMS: &bd}
			candidate.Measurements = Measurements{Provenance: "host_record", DurationMS: &cd}
		case 2:
			b.Checks[0].Status = Error
		case 3:
			candidate.Checks[0].Status = Fail
		}
		if index != 3 {
			baselineAttempts.Attempts = append(baselineAttempts.Attempts, b)
		}
		candidateAttempts.Attempts = append(candidateAttempts.Attempts, candidate)
	}
	baselineAttempts.SuiteDigest, candidateAttempts.SuiteDigest = DigestSuite(s), DigestSuite(s)
	comparison, err := Compare(advRecord(t, s, baselineAttempts), advRecord(t, s, candidateAttempts))
	if err != nil {
		t.Fatal(err)
	}
	got := comparison.Summary
	if got.Planned != 4 || got.Comparable != 2 || got.Improved != 1 || got.Regressed != 1 || got.Unchanged != 0 || got.Unevaluated != 2 || got.BaselineMissing != 1 || got.CandidateMissing != 0 || got.DurationMeasuredPairs != 2 || got.InputTokensMeasuredPairs != 0 {
		t.Fatalf("comparison disagrees with hand calculation: %+v", got)
	}
	for _, c := range comparison.Cases {
		switch c.CaseID {
		case "improvement":
			if c.Delta.DurationMS == nil || *c.Delta.DurationMS != 5 {
				t.Fatalf("observed increase lost: %+v", c.Delta)
			}
		case "regression":
			if c.Delta.DurationMS == nil || *c.Delta.DurationMS != -10 {
				t.Fatalf("observed decrease lost: %+v", c.Delta)
			}
		default:
			if c.Delta.DurationMS != nil {
				t.Fatal("unknown time compared as zero")
			}
		}
	}
}

func TestAdversarialNoncompletedAttemptsCannotEstablishStrategyImprovement(t *testing.T) {
	for _, termination := range []string{"environment_error", "authentication_error", "timeout", "interrupted", "agent_error"} {
		t.Run(termination, func(t *testing.T) {
			s, baselineAttempts := advFixture()
			_, candidateAttempts := advFixture()
			candidateAttempts.Condition = Condition{ID: "candidate", InstructionDigest: advDigest("4")}
			baselineAttempt, candidateAttempt := &baselineAttempts.Attempts[0], &candidateAttempts.Attempts[0]
			baselineAttempt.Termination = termination
			baselineAttempt.Files[1].Digest = advDigest("7")
			advRefreshArtifact(baselineAttempt)
			baselineAttempt.Checks[0].Status = Fail
			baselineDuration, candidateDuration := int64(10), int64(20)
			baselineAttempt.Measurements = Measurements{Provenance: "host_record", DurationMS: &baselineDuration}
			candidateAttempt.Measurements = Measurements{Provenance: "host_record", DurationMS: &candidateDuration}
			for _, attempt := range []*Attempt{baselineAttempt, candidateAttempt} {
				attempt.Coverage.Permissions = "complete"
				attempt.Events = []Event{{ID: "permission-record", Sequence: 1, Kind: "permission", Provenance: "host_record", Status: "denied"}}
			}
			baseline, candidate := advRecord(t, s, baselineAttempts), advRecord(t, s, candidateAttempts)
			if baseline.Assessment.Cases[0].Outcome != Fail || baseline.Assessment.Cases[0].Process != Fail || candidate.Assessment.Cases[0].Outcome != Pass || candidate.Assessment.Cases[0].Process != Pass {
				t.Fatal("known findings must remain visible independently of comparison eligibility")
			}
			for _, pair := range []struct {
				baseline, candidate AssessmentRecord
				wantDelta           int64
			}{{baseline, candidate, 10}, {candidate, baseline, -10}} {
				comparison, err := Compare(pair.baseline, pair.candidate)
				if err != nil {
					t.Fatal(err)
				}
				row, summary := comparison.Cases[0], comparison.Summary
				if summary.Planned != 1 || summary.Comparable != 0 || summary.Improved != 0 || summary.Regressed != 0 || summary.Unchanged != 0 || summary.Unevaluated != 1 || summary.BaselineMissing != 0 || summary.CandidateMissing != 0 {
					t.Fatalf("noncompleted attempt changed performance population: %+v", summary)
				}
				if row.OutcomeChange != "unevaluated" || row.ProcessChange != "unevaluated" {
					t.Fatalf("noncompleted attempt established strategy change: %+v", row)
				}
				for _, rule := range row.Rules {
					if rule.Change != "unevaluated" {
						t.Fatalf("process rule implied strategy change: %+v", rule)
					}
				}
				if row.Delta.DurationMS == nil || *row.Delta.DurationMS != pair.wantDelta || summary.DurationMeasuredPairs != 1 {
					t.Fatal("observed duration lost with performance exclusion")
				}
				if row.BaselineTermination != pair.baseline.Assessment.Cases[0].Termination || row.CandidateTermination != pair.candidate.Assessment.Cases[0].Termination || row.Baseline != pair.baseline.Assessment.Cases[0].Outcome || row.Candidate != pair.candidate.Assessment.Cases[0].Outcome || row.BaselineProcess != pair.baseline.Assessment.Cases[0].Process || row.CandidateProcess != pair.candidate.Assessment.Cases[0].Process {
					t.Fatal("comparison concealed termination or confirmed findings")
				}
			}
		})
	}
}

func TestAdversarialStrictJSONRejectsRawAndAmbiguousFields(t *testing.T) {
	for _, data := range []string{
		`{"schema":"eval-attempts/v1","schema":"eval-attempts/v1"}`,
		`{"attempts":[{"events":[{"command":"raw-command-canary","text":"raw-conversation-canary"}]}]}`,
		`{"attempts":[]} {"attempts":[]}`,
	} {
		var a AttemptSet
		if err := DecodeStrict([]byte(data), &a); err == nil {
			t.Fatalf("accepted raw or ambiguous payload: %s", data)
		}
	}
	s, a := advFixture()
	before, _ := json.Marshal(a)
	r := advGrade(t, s, a)
	after, _ := json.Marshal(a)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("assessment mutated supplied evidence")
	}
	data, _ := json.Marshal(r)
	for _, raw := range []string{"solution.go", "public_test.go", "requirements.md"} {
		if strings.Contains(string(data), raw) || strings.Contains(Markdown(r), raw) {
			t.Fatalf("aggregate leaked path %s", raw)
		}
	}
}
