package evaluation

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewFixture() Snapshot {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := Snapshot{Experiment: Experiment{Schema: Schema, ID: "exp-test", StartedAt: at}, Tasks: map[string]Task{}, Events: map[string]Event{}}
	r := Review{Schema: Schema, ExperimentID: s.Experiment.ID, Reviewer: "human"}
	for _, id := range []string{"task-one", "task-two"} {
		s.Tasks[id] = Task{ID: id, RepositoryID: "repo", Profile: "ward", Model: "model-a", Kind: "root", CreatedAt: at}
		r.Tasks = append(r.Tasks, TaskReview{TaskID: id, Revision: 1, Eligibility: "yes", Outcome: "completed", TaskType: "coding", TerminalRecordObserved: Unknown})
	}
	s.Reviews = []Review{r}
	return s
}

func addTestEvent(s *Snapshot, id, module, outcome string) Event {
	at := s.Experiment.StartedAt.Add(time.Hour)
	e := Event{ID: id, SourceID: "source-" + id, Revision: 1, RepositoryID: "repo", Module: module, Version: "1.0.0", Outcome: outcome, SourceTime: &at}
	s.Events[id] = e
	return e
}

func testIncident(id, event, module, expected, actual string) IncidentReview {
	i := unknownIncident(id, 1, module, []string{event})
	i.TaskID = "task-one"
	i.ExpectedAction = expected
	i.ActualAction = actual
	i.Evidence = "human_review"
	return i
}

func reviewWith(s Snapshot, incidents ...IncidentReview) Review {
	return Review{Schema: Schema, ExperimentID: s.Experiment.ID, Reviewer: "human", Incidents: incidents}
}

func TestReviewTemplateKeepsUnknownAndResetsChangedEvidence(t *testing.T) {
	s := reviewFixture()
	e := addTestEvent(&s, "old", "seal", "fail")
	i := testIncident("incident", "old", "seal", "block", "blocked")
	i.AdditionalValue = Confirmed
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	updated := e
	updated.ID = "updated"
	updated.Revision = 2
	s.Events[updated.ID] = updated
	addTestEvent(&s, "new", "ward", "deny")
	r, err := ReviewTemplate(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 2 || r.Tasks[0].Revision != 2 {
		t.Fatalf("task template revisions: %#v", r.Tasks)
	}
	if len(r.Incidents) != 2 {
		t.Fatalf("incidents: %#v", r.Incidents)
	}
	for _, got := range r.Incidents {
		if got.ID == "incident" {
			if got.Revision != 2 || got.EventIDs[0] != "updated" || got.AdditionalValue != Unknown || got.Evidence != "unknown" {
				t.Fatalf("changed evidence retained a grade: %#v", got)
			}
		} else if got.TaskID != "" || incidentReviewed(got) || got.ActualAction != "blocked" {
			t.Fatalf("new template invented a review or a link: %#v", got)
		}
	}
	if err := ValidateReview(s, r); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReviewRejectsContradictionsAndInvalidReferences(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Snapshot, *Review)
	}{
		{"nonhuman", func(s *Snapshot, r *Review) { r.Reviewer = "agent" }},
		{"missing task", func(s *Snapshot, r *Review) { r.Incidents[0].TaskID = "missing" }},
		{"missing event", func(s *Snapshot, r *Review) { r.Incidents[0].EventIDs = []string{"missing"} }},
		{"mismatched module", func(s *Snapshot, r *Review) { r.Incidents[0].Module = "seal" }},
		{"contradictory action", func(s *Snapshot, r *Review) { r.Incidents[0].ActualAction = "allowed" }},
		{"contradictory correctness", func(s *Snapshot, r *Review) { r.Incidents[0].Correctness = Denied }},
		{"contradictory derived correctness", func(s *Snapshot, r *Review) {
			r.Incidents[0].ActualAction = "unknown"
			r.Incidents[0].ExpectedAction = "allow"
			r.Incidents[0].Correctness = Confirmed
		}},
		{"no explicit link", func(s *Snapshot, r *Review) { r.Incidents[0].TaskID = "" }},
		{"no evidence", func(s *Snapshot, r *Review) { r.Incidents[0].Evidence = "unknown" }},
		{"duplicate incident", func(s *Snapshot, r *Review) { r.Incidents = append(r.Incidents, r.Incidents[0]) }},
		{"two owners", func(s *Snapshot, r *Review) {
			i := r.Incidents[0]
			i.ID = "second-owner"
			r.Incidents = append(r.Incidents, i)
		}},
		{"duplicate source revision", func(s *Snapshot, r *Review) {
			e := s.Events["event"]
			e.ID = "new-revision"
			e.Revision = 2
			s.Events[e.ID] = e
			r.Incidents[0].EventIDs = append(r.Incidents[0].EventIDs, e.ID)
		}},
		{"stale review", func(s *Snapshot, r *Review) {
			e := s.Events["event"]
			e.ID = "new-revision"
			e.Revision = 2
			s.Events[e.ID] = e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := reviewFixture()
			addTestEvent(&s, "event", "ward", "deny")
			r := reviewWith(s, testIncident("incident", "event", "ward", "block", "blocked"))
			tc.change(&s, &r)
			if err := ValidateReview(s, r); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestReviewTemplateDoesNotResurrectInactiveIncidents(t *testing.T) {
	s := reviewFixture()
	addTestEvent(&s, "event", "ward", "deny")
	i := testIncident("incident", "event", "ward", "block", "blocked")
	i.Active = false
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	r, err := ReviewTemplate(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Incidents) != 1 || r.Incidents[0].Active {
		t.Fatalf("deactivated source was resurrected: %#v", r.Incidents)
	}
}

func TestReviewCanRegroupAnIncidentAtomically(t *testing.T) {
	s := reviewFixture()
	addTestEvent(&s, "one", "ward", "deny")
	addTestEvent(&s, "two", "ward", "deny")
	i := testIncident("first", "one", "ward", "block", "blocked")
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	i.Revision = 2
	i.Active = false
	j := testIncident("combined", "one", "ward", "block", "blocked")
	j.EventIDs = append(j.EventIDs, "two")
	r := reviewWith(s, i, j)
	if err := ValidateReview(s, r); err != nil {
		t.Fatal(err)
	}
	s.Reviews = append(s.Reviews, r)
	if got := BuildReport(s).Counts; got.ActiveIncidents != 1 || got.InactiveIncidents != 1 || got.UnderlyingUniqueRuns != 2 {
		t.Fatalf("regroup counts: %#v", got)
	}
}

func comparisonFixture() (Snapshot, Review) {
	s := reviewFixture()
	addTestEvent(&s, "event", "seal", "pass")
	i := testIncident("incident", "event", "seal", "allow", "allowed")
	i.TaskID = "task-two"
	s.Reviews = append(s.Reviews, reviewWith(s, i))
	base, tool := 10.0, 14.0
	c := Comparison{ID: "comparison", Revision: 1, BaselineTaskID: "task-one", ToolTaskID: "task-two", Module: "seal", Comparable: true, BaselineSuccess: Denied, ToolSuccess: Confirmed, BaselineSeconds: &base, ToolSeconds: &tool}
	r := reviewWith(s)
	r.Comparisons = []Comparison{c}
	return s, r
}

func TestComparisonValidationEnforcesCohortsAndPairedTimings(t *testing.T) {
	s, r := comparisonFixture()
	if err := ValidateReview(s, r); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*Snapshot, *Review)
	}{
		{"model mismatch", func(s *Snapshot, r *Review) { v := s.Tasks["task-two"]; v.Model = "other-model"; s.Tasks[v.ID] = v }},
		{"profile mismatch", func(s *Snapshot, r *Review) { v := s.Tasks["task-two"]; v.Profile = "other-profile"; s.Tasks[v.ID] = v }},
		{"unknown type", func(s *Snapshot, r *Review) { s.Reviews[0].Tasks[1].TaskType = "unknown" }},
		{"ongoing task", func(s *Snapshot, r *Review) { s.Reviews[0].Tasks[1].Outcome = "ongoing" }},
		{"same task", func(s *Snapshot, r *Review) { r.Comparisons[0].ToolTaskID = r.Comparisons[0].BaselineTaskID }},
		{"unpaired timing", func(s *Snapshot, r *Review) { r.Comparisons[0].BaselineSeconds = nil }},
		{"negative timing", func(s *Snapshot, r *Review) { v := -1.0; r.Comparisons[0].BaselineSeconds = &v }},
		{"infinite timing", func(s *Snapshot, r *Review) { v := math.Inf(1); r.Comparisons[0].BaselineSeconds = &v }},
		{"nan timing", func(s *Snapshot, r *Review) { v := math.NaN(); r.Comparisons[0].BaselineSeconds = &v }},
		{"duplicate paired task", func(s *Snapshot, r *Review) {
			c := r.Comparisons[0]
			c.ID = "another-pair"
			r.Comparisons = append(r.Comparisons, c)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, r := comparisonFixture()
			tc.change(&s, &r)
			if err := ValidateReview(s, r); err == nil {
				t.Fatal("expected invalid comparison")
			}
		})
	}
}

func TestPrivateReviewPacketCanLinkSources(t *testing.T) {
	s := reviewFixture()
	privateRoot := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "private")
	reference := filepath.Join(privateRoot, "source-session.jsonl")
	s.Experiment.Config.SessionDirs = []string{privateRoot}
	s.Bindings = []Binding{{ID: "private-task-id", Kind: "session", Reference: reference}}
	r, err := ReviewTemplate(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ReviewMarkdown(s, r), filepath.ToSlash(reference)) {
		t.Fatal("private reviewer cannot locate source")
	}
}

func TestPrivateReviewPacketIdentifiesExactSealRunWithEncodedLocalLinks(t *testing.T) {
	s := reviewFixture()
	privateRoot := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "private")
	repository := filepath.Join(privateRoot, "review project (alpha)")
	encodedRepository := filepath.ToSlash(privateRoot) + "/review%20project%20%28alpha%29"
	s.Experiment.Config.Repositories = []string{repository}
	e := addTestEvent(&s, "event", "seal", "pass")
	e.SourceID = "source-alias"
	e.EvidenceSHA256 = strings.Repeat("a", 64)
	s.Events[e.ID] = e
	s.Bindings = []Binding{
		{ID: "source-alias", Kind: "seal_run", Key: repository + "\x00TASK-1\x00RUN-1", Reference: repository},
		{ID: "task-alias", Kind: "seal_task", Key: repository + "\x00TASK-1", Reference: repository},
		{ID: "other", Kind: "session", Key: "LEAK_RAW_COMMAND", Reference: "https://untrusted.invalid/private"},
	}
	r := reviewWith(s, unknownIncident("incident", 1, "seal", []string{e.ID}))
	md := ReviewMarkdown(s, r)
	for _, want := range []string{"source-alias", e.EvidenceSHA256, "Task `TASK-1`, Run `RUN-1`", "[Run manifest](<" + encodedRepository + "/.seal/evidence/TASK-1/RUN-1/run-manifest.json>)", "[saved Task](<" + encodedRepository + "/.seal/tasks/TASK-1.json>)"} {
		if !strings.Contains(md, want) {
			t.Fatalf("private packet missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "LEAK_RAW_COMMAND") || strings.Contains(md, "https://untrusted") {
		t.Fatal("raw source key or unsupported link exposed")
	}
}
