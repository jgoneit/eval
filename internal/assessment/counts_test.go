package assessment

import "testing"

func TestUnavailableChecksDoNotCountAsVerificationAttempts(t *testing.T) {
	s, a := advFixture()
	for index := range a.Attempts[0].Checks {
		a.Attempts[0].Checks[index].Status = Unavailable
	}
	agent := a.Attempts[0].Checks[0]
	agent.EvidenceID, agent.Executor, agent.Provenance = "agent-unavailable", "agent", "host_record"
	a.Attempts[0].Checks = append(a.Attempts[0].Checks, agent)
	r := advGrade(t, s, a)
	if got := r.Cases[0].Verification; got != (VerificationCounts{}) {
		t.Fatalf("unavailable assertions counted as execution: %+v", got)
	}
	if err := ValidateAssessment(r); err != nil {
		t.Fatal(err)
	}
	a.Attempts[0].Events = []Event{{ID: "observed-verification", Sequence: 1, Kind: "verification", Provenance: "host_record", Tool: "shell", Status: "unknown"}}
	r = advGrade(t, s, a)
	if got := r.Cases[0].Verification; got != (VerificationCounts{Agent: 1}) {
		t.Fatalf("explicit Host verification attempt lost: %+v", got)
	}
	if err := ValidateAssessment(r); err != nil {
		t.Fatal(err)
	}
	a.Attempts[0].Events[0].ID = agent.EvidenceID
	if _, err := Assess(s, a); err == nil {
		t.Fatal("accepted unavailable assertion sharing an execution event ID")
	}
}
