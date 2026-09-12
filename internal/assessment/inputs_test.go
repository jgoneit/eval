package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestRetainedInputsRejectCoordinatedGradeAndSummaryForgery(t *testing.T) {
	s, attempts := advFixture()
	attempts.Attempts[0].Files[1].Digest = advDigest("7")
	advRefreshArtifact(&attempts.Attempts[0])
	for _, ruleID := range []string{"allowed_files", "protected_files"} {
		t.Run(ruleID, func(t *testing.T) {
			baseline, candidate := advRecord(t, s, attempts), advRecord(t, s, attempts)
			changed := false
			for i := range candidate.Assessment.Cases[0].Rules {
				rule := &candidate.Assessment.Cases[0].Rules[i]
				if rule.ID == ruleID {
					if rule.Status != Fail {
						t.Fatalf("fixture rule is not failed: %+v", rule)
					}
					rule.Status = Pass
					changed = true
				}
			}
			if !changed {
				t.Fatalf("fixture lacks %s", ruleID)
			}
			candidate.Assessment.Cases[0].Process = aggregateFindings(candidate.Assessment.Cases[0].Rules, "")
			candidate.Assessment.Summary.Process = Counts{}
			increment(&candidate.Assessment.Summary.Process, candidate.Assessment.Cases[0].Process)
			if _, err := Compare(baseline, candidate); err == nil {
				t.Fatal("accepted coordinated rule/summary forgery")
			}
		})
	}
}

func TestRetainedInputsRejectReplacementAndInvalidBindings(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*AssessmentRecord)
	}{
		{"replaced-inputs", func(r *AssessmentRecord) { r.Inputs.Attempts.Attempts[0].Checks[0].Status = Fail }},
		{"forged-input-digest", func(r *AssessmentRecord) { r.Assessment.InputsDigest = advDigest("9") }},
		{"replaced-file-and-rehashed-inputs", func(r *AssessmentRecord) {
			r.Inputs.Attempts.Attempts[0].Files[1].Digest = advDigest("7")
			r.Assessment.InputsDigest = DigestInputs(r.Inputs)
		}},
		{"replaced-criteria-and-rehashed-inputs", func(r *AssessmentRecord) {
			r.Inputs.Suite.Cases[0].AllowedFiles = []string{"public_test.go"}
			r.Assessment.InputsDigest = DigestInputs(r.Inputs)
		}},
		{"regraded-outcome-omits-check", func(r *AssessmentRecord) {
			r.Assessment.Cases[0].Checks = r.Assessment.Cases[0].Checks[:1]
		}},
		{"nil-evidence-ids", func(r *AssessmentRecord) { r.Assessment.Cases[0].Rules[2].EvidenceIDs = nil }},
		{"legacy-result", func(r *AssessmentRecord) { r.Assessment.Schema = "eval-assessment/v1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			baseline, candidate := advRecord(t, s, a), advRecord(t, s, a)
			test.mutate(&candidate)
			if _, err := Compare(baseline, candidate); err == nil {
				t.Fatal("accepted altered assessment or private inputs")
			}
		})
	}
}

func TestInputsDigestUsesVersionedCompactTypedJSON(t *testing.T) {
	s, a := advFixture()
	r := advRecord(t, s, a)
	compact, err := json.Marshal(r.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(append([]byte("eval-assessment-inputs/v1\n"), compact...))
	if got := r.Assessment.InputsDigest; got != hex.EncodeToString(want[:]) {
		t.Fatalf("digest rule changed: %s", got)
	}
	pretty, err := json.MarshalIndent(r.Inputs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var decoded AssessmentInputs
	if err := DecodeStrict(pretty, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAssessment(r.Assessment, decoded); err != nil {
		t.Fatal("whitespace changed typed input digest")
	}
}

func TestConfigurationObservationGatesChangesWithoutLosingPopulationOrCost(t *testing.T) {
	for _, status := range []string{"changed", "unavailable", "different-expected", "legacy"} {
		t.Run(status, func(t *testing.T) {
			s, b := advFixture()
			_, c := advFixture()
			missing := s.Cases[0]
			missing.ID = "missing"
			s.Cases = append(s.Cases, missing)
			b.SuiteDigest, c.SuiteDigest = DigestSuite(s), DigestSuite(s)
			b.Attempts[0].Checks[0].Status = Fail
			bd, cd := int64(10), int64(25)
			b.Attempts[0].Measurements = Measurements{Provenance: "host_record", DurationMS: &bd}
			c.Attempts[0].Measurements = Measurements{Provenance: "host_record", DurationMS: &cd}
			x := advDigest("8")
			switch status {
			case "changed":
				c.Attempts[0].ConfigurationObservation.Status = "changed"
				c.Attempts[0].ConfigurationObservation.AfterDigest = &x
			case "unavailable":
				c.Attempts[0].ConfigurationObservation.Status = "unavailable"
				c.Attempts[0].ConfigurationObservation.AfterDigest = nil
			case "different-expected":
				c.Attempts[0].ConfigurationObservation = &ConfigurationObservation{Status: "unchanged", ExpectedDigest: &x, BeforeDigest: &x, AfterDigest: &x}
			case "legacy":
				c.Schema = LegacyAttemptsSchema
				c.Attempts[0].ConfigurationObservation = nil
			}
			candidate := advRecord(t, s, c)
			comparison, err := Compare(advRecord(t, s, b), candidate)
			if err != nil {
				t.Fatal(err)
			}
			row := comparison.Cases[0]
			if candidate.Assessment.Summary.Attempted != 1 || candidate.Assessment.Cases[0].Outcome != Pass || row.Baseline != Fail || row.Candidate != Pass || row.CandidateTermination != "completed" {
				t.Fatal("confirmed findings or attempt count lost")
			}
			if comparison.Summary.Planned != 2 || comparison.Summary.Comparable != 0 || comparison.Summary.Unevaluated != 2 || comparison.Summary.BaselineMissing != 1 || comparison.Summary.CandidateMissing != 1 {
				t.Fatalf("population changed: %+v", comparison.Summary)
			}
			if row.ConfigurationComparable || row.OutcomeChange != "unevaluated" || row.ProcessChange != "unevaluated" {
				t.Fatalf("configuration uncertainty treated as improvement: %+v", row)
			}
			for _, rule := range row.Rules {
				if rule.Change != "unevaluated" {
					t.Fatal("rule compared across unknown configuration")
				}
			}
			if row.Delta.DurationMS == nil || *row.Delta.DurationMS != 15 || comparison.Summary.DurationMeasuredPairs != 1 || row.Delta.InputTokens != nil {
				t.Fatal("observed/missing costs changed")
			}
		})
	}
}

func TestConfigurationObservationContract(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*AttemptSet)
	}{
		{"missing-v2", func(a *AttemptSet) { a.Attempts[0].ConfigurationObservation = nil }},
		{"invented-v1", func(a *AttemptSet) { a.Schema = LegacyAttemptsSchema }},
		{"false-unchanged", func(a *AttemptSet) { x := advDigest("9"); a.Attempts[0].ConfigurationObservation.AfterDigest = &x }},
		{"false-changed", func(a *AttemptSet) { a.Attempts[0].ConfigurationObservation.Status = "changed" }},
		{"false-unavailable", func(a *AttemptSet) { a.Attempts[0].ConfigurationObservation.Status = "unavailable" }},
		{"missing-expected", func(a *AttemptSet) { a.Attempts[0].ConfigurationObservation.ExpectedDigest = nil }},
		{"invalid-digest", func(a *AttemptSet) {
			x := "private/path-canary"
			a.Attempts[0].ConfigurationObservation.AfterDigest = &x
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := advFixture()
			test.mutate(&a)
			if _, err := Assess(s, a); err == nil {
				t.Fatal("accepted inconsistent configuration observation")
			}
		})
	}
}

func TestComparisonKindUsesInstructionDigestAndKeepsOutputReproducible(t *testing.T) {
	for _, same := range []bool{true, false} {
		s, b := advFixture()
		_, c := advFixture()
		c.Condition.ID = "different-label"
		want := "same_instruction"
		if !same {
			c.Condition.InstructionDigest = advDigest("4")
			want = "different_instruction"
		}
		baseline, candidate := advRecord(t, s, b), advRecord(t, s, c)
		first, err := Compare(baseline, candidate)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Compare(baseline, candidate)
		if err != nil {
			t.Fatal(err)
		}
		j1, _ := json.Marshal(first)
		j2, _ := json.Marshal(second)
		md := ComparisonMarkdown(first)
		if first.Kind != want || first.Summary.Comparable != 1 || !strings.Contains(md, want) || string(j1) != string(j2) || md != ComparisonMarkdown(second) {
			t.Fatal("comparison kind or reproducibility mismatch")
		}
		if same && !strings.Contains(md, "variation between samples") {
			t.Fatal("same instruction difference misrepresented")
		}
		for _, path := range []string{"solution.go", "public_test.go", "requirements.md"} {
			if strings.Contains(string(j1)+md, path) {
				t.Fatal("private path leaked")
			}
		}
	}
}
