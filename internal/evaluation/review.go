package evaluation

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type reviewState struct {
	tasks       map[string]TaskReview
	incidents   map[string]IncidentReview
	comparisons map[string]Comparison
}

func latestReviews(s Snapshot) reviewState {
	x := reviewState{map[string]TaskReview{}, map[string]IncidentReview{}, map[string]Comparison{}}
	for _, r := range s.Reviews {
		for _, t := range r.Tasks {
			if t.Revision > x.tasks[t.TaskID].Revision {
				x.tasks[t.TaskID] = t
			}
		}
		for _, i := range r.Incidents {
			if i.Revision > x.incidents[i.ID].Revision {
				x.incidents[i.ID] = i
			}
		}
		for _, c := range r.Comparisons {
			if c.Revision > x.comparisons[c.ID].Revision {
				x.comparisons[c.ID] = c
			}
		}
	}
	return x
}

func eventSource(e Event) string {
	if e.SourceID != "" {
		return e.SourceID
	}
	return e.ID
}

func latestEvents(s Snapshot) map[string]Event {
	x := map[string]Event{}
	for _, e := range s.Events {
		key := eventSource(e)
		old, ok := x[key]
		if !ok || e.Revision > old.Revision || (e.Revision == old.Revision && e.ID < old.ID) {
			x[key] = e
		}
	}
	return x
}

func unknownIncident(id string, revision int, module string, events []string) IncidentReview {
	return IncidentReview{ID: id, Revision: revision, Active: true, EventIDs: events, Module: module,
		ExpectedAction: "unknown", ActualAction: "unknown", Correctness: Unknown, AdditionalValue: Unknown,
		UnnecessaryIntervention: Unknown, Rework: Unknown, Severity: "unknown", Evidence: "unknown"}
}

// ReviewTemplate produces editable human judgements. Source hints never silently
// establish task membership. Changed source revisions invalidate old judgements.
func ReviewTemplate(s Snapshot) (Review, error) {
	r := Review{Schema: Schema, ExperimentID: s.Experiment.ID, Reviewer: "human", Tasks: []TaskReview{}, Incidents: []IncidentReview{}, Comparisons: []Comparison{}}
	x, events := latestReviews(s), latestEvents(s)
	for _, id := range sortedKeys(s.Tasks) {
		t, ok := x.tasks[id]
		if !ok {
			t = TaskReview{TaskID: id, Eligibility: "unknown", Outcome: "unknown", TaskType: "unknown", TerminalRecordObserved: Unknown}
			if s.Tasks[id].ExclusionReason != "" {
				t.Eligibility = "no"
				t.ExclusionReason = s.Tasks[id].ExclusionReason
			}
		}
		t.Revision++
		r.Tasks = append(r.Tasks, t)
	}
	assigned := map[string]bool{}
	changedTasks := map[string]bool{}
	for _, id := range sortedKeys(x.incidents) {
		i := x.incidents[id]
		i.Revision++
		i.EventIDs = append([]string(nil), i.EventIDs...)
		changed := false
		for n, eid := range i.EventIDs {
			e, ok := s.Events[eid]
			if !ok {
				continue
			}
			assigned[eventSource(e)] = true
			if current, ok := events[eventSource(e)]; ok && current.ID != eid {
				i.EventIDs[n] = current.ID
				changed = true
			}
		}
		if changed {
			if i.TaskID != "" {
				changedTasks[i.TaskID] = true
			}
			fresh := unknownIncident(i.ID, i.Revision, i.Module, i.EventIDs)
			fresh.TaskID, fresh.Active = i.TaskID, i.Active
			i = fresh
		}
		r.Incidents = append(r.Incidents, i)
	}
	for _, key := range sortedKeys(events) {
		e := events[key]
		if assigned[key] || (e.Module != "ward" && e.Module != "seal") {
			continue
		}
		id, err := NewID()
		if err != nil {
			return Review{}, err
		}
		i := unknownIncident(id, 1, e.Module, []string{e.ID})
		i.ActualAction = sourceAction(e)
		r.Incidents = append(r.Incidents, i)
	}
	for _, id := range sortedKeys(x.comparisons) {
		c := x.comparisons[id]
		c.Revision++
		if changedTasks[c.BaselineTaskID] || changedTasks[c.ToolTaskID] {
			c.Comparable = false
			c.BaselineSuccess = Unknown
			c.ToolSuccess = Unknown
			c.BaselineSeconds = nil
			c.ToolSeconds = nil
		}
		r.Comparisons = append(r.Comparisons, c)
	}
	return r, nil
}

var simpleToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:+-]{0,127}$`)

func member(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func validVerdict(v Verdict) bool {
	return member(string(v), string(Confirmed), string(Denied), string(Unknown), string(NotApplicable))
}
func terminal(v string) bool { return member(v, "completed", "failed", "abandoned") }
func known(v Verdict) bool   { return v == Confirmed || v == Denied }

// sourceAction describes the recorded decision, never an inferred successful
// side effect. A Ward defer and a Seal mechanical failure are not completion.
func sourceAction(e Event) string {
	if e.Module == "ward" && e.Outcome == "deny" {
		return "blocked"
	}
	return "unknown"
}

func wardDecision(s Snapshot, i IncidentReview) string {
	decision := "unknown"
	for _, id := range i.EventIDs {
		d := "unknown"
		switch s.Events[id].Outcome {
		case "deny":
			d = "blocked"
		case "defer":
			d = "not_blocked"
		}
		if d == "unknown" {
			continue
		}
		if decision != "unknown" && decision != d {
			return "mixed"
		}
		decision = d
	}
	return decision
}

func incidentSourceAction(s Snapshot, i IncidentReview) string {
	action := "unknown"
	for _, id := range i.EventIDs {
		a := sourceAction(s.Events[id])
		if a == "unknown" {
			continue
		}
		if action != "unknown" && action != a {
			return "mixed"
		}
		action = a
	}
	return action
}

func incidentVersion(s Snapshot, i IncidentReview) string {
	version := ""
	for _, id := range i.EventIDs {
		e, ok := s.Events[id]
		if !ok || e.Version == "" || e.Version == "unknown" {
			return "unknown"
		}
		if version != "" && version != e.Version {
			return "mixed"
		}
		version = e.Version
	}
	if version == "" {
		return "unknown"
	}
	return version
}

func staleIncident(s Snapshot, i IncidentReview) bool {
	latest := latestEvents(s)
	for _, id := range i.EventIDs {
		e, ok := s.Events[id]
		if !ok || latest[eventSource(e)].ID != id {
			return true
		}
	}
	return false
}

func incidentReviewed(i IncidentReview) bool {
	return i.Evidence != "unknown" && (known(i.Correctness) || known(i.AdditionalValue) || known(i.UnnecessaryIntervention) || known(i.Rework) || member(i.ExpectedAction, "block", "allow"))
}

// ValidateReview checks a proposed append against the latest revisions. It does
// not infer a human judgement from a tool result or promote unknowns to zero.
func ValidateReview(s Snapshot, r Review) error {
	if r.Schema != Schema || r.ExperimentID != s.Experiment.ID {
		return fmt.Errorf("review schema or experiment does not match")
	}
	if r.Reviewer != "human" {
		return fmt.Errorf("reviewer must be human")
	}
	x := latestReviews(s)
	seen := map[string]bool{}
	for _, t := range r.Tasks {
		if _, ok := s.Tasks[t.TaskID]; !ok {
			return fmt.Errorf("task review references an unknown task")
		}
		if seen[t.TaskID] || t.Revision != x.tasks[t.TaskID].Revision+1 {
			return fmt.Errorf("task review has a duplicate or nonconsecutive revision")
		}
		seen[t.TaskID] = true
		if !member(t.Eligibility, "yes", "no", "unknown") || !member(t.Outcome, "unknown", "ongoing", "completed", "failed", "abandoned") || !member(t.TaskType, "coding", "review", "configuration", "other", "unknown") || !validVerdict(t.TerminalRecordObserved) {
			return fmt.Errorf("task review has an invalid judgement")
		}
		if t.ExclusionReason != "" && !simpleToken.MatchString(t.ExclusionReason) {
			return fmt.Errorf("exclusion reason must be a short category token")
		}
		if t.Eligibility == "no" && !member(t.ExclusionReason, "child", "eval_self", "pure_qa", "out_of_scope") {
			return fmt.Errorf("an excluded task requires a predefined population reason")
		}
		if t.Eligibility == "yes" && (s.Tasks[t.TaskID].ParentID != "" || s.Tasks[t.TaskID].ExclusionReason != "") {
			return fmt.Errorf("a source-excluded task cannot be marked eligible")
		}
		if t.Eligibility == "yes" && t.ExclusionReason != "" {
			return fmt.Errorf("an eligible task cannot have an exclusion reason")
		}
		if t.TerminalRecordObserved == Confirmed && !terminal(t.Outcome) {
			return fmt.Errorf("terminal observation requires a terminal outcome")
		}
		x.tasks[t.TaskID] = t
	}
	seen = map[string]bool{}
	for _, i := range r.Incidents {
		if !simpleToken.MatchString(i.ID) || seen[i.ID] || i.Revision != x.incidents[i.ID].Revision+1 {
			return fmt.Errorf("incident has an invalid ID or revision")
		}
		seen[i.ID] = true
		if !member(i.Module, "ward", "seal") || !member(i.ExpectedAction, "block", "allow", "unknown", "not_applicable") || !member(i.ActualAction, "blocked", "allowed", "unknown") || !member(i.Severity, "unknown", "minor", "major", "critical") || !member(i.Evidence, "unknown", "human_review", "reproduction", "independent_test") {
			return fmt.Errorf("incident has an invalid judgement")
		}
		if !validVerdict(i.Correctness) || !validVerdict(i.AdditionalValue) || !validVerdict(i.UnnecessaryIntervention) || !validVerdict(i.Rework) {
			return fmt.Errorf("incident has an invalid verdict")
		}
		if i.TaskID != "" {
			if _, ok := s.Tasks[i.TaskID]; !ok {
				return fmt.Errorf("incident references an unknown task")
			}
		}
		if len(i.EventIDs) == 0 {
			return fmt.Errorf("incident requires source events")
		}
		unique := map[string]bool{}
		for _, id := range i.EventIDs {
			e, ok := s.Events[id]
			if !ok || e.Module != i.Module {
				return fmt.Errorf("incident references an unknown event or mismatched module")
			}
			if unique[eventSource(e)] {
				return fmt.Errorf("incident repeats a source run")
			}
			unique[eventSource(e)] = true
			if i.TaskID != "" && e.RepositoryID != "" && s.Tasks[i.TaskID].RepositoryID != "" && e.RepositoryID != s.Tasks[i.TaskID].RepositoryID {
				return fmt.Errorf("incident task and event repositories differ")
			}
		}
		if incidentVersion(s, i) == "mixed" {
			return fmt.Errorf("incident cannot combine module versions")
		}
		a := incidentSourceAction(s, i)
		if a == "mixed" {
			return fmt.Errorf("incident cannot combine opposite recorded actions")
		}
		if a != "unknown" && i.ActualAction != "unknown" && a != i.ActualAction {
			return fmt.Errorf("incident action contradicts its source")
		}
		effectiveAction := i.ActualAction
		if effectiveAction == "unknown" {
			effectiveAction = a
		}
		if i.Module == "ward" {
			switch wardDecision(s, i) {
			case "blocked":
				effectiveAction = "blocked"
			case "not_blocked":
				effectiveAction = "allowed"
			}
		}
		if known(i.Correctness) && member(i.ExpectedAction, "block", "allow") && effectiveAction != "unknown" {
			correct := (i.ExpectedAction == "block" && effectiveAction == "blocked") || (i.ExpectedAction == "allow" && effectiveAction == "allowed")
			if (i.Correctness == Confirmed) != correct {
				return fmt.Errorf("correctness contradicts expected and actual actions")
			}
		}
		if incidentReviewed(i) && i.TaskID == "" {
			return fmt.Errorf("a reviewed incident requires explicit task membership")
		}
		if i.Active && incidentReviewed(i) && staleIncident(s, i) {
			return fmt.Errorf("a reviewed incident references a superseded source snapshot")
		}
		if i.Evidence == "unknown" && (member(i.ExpectedAction, "block", "allow") || known(i.Correctness) || known(i.AdditionalValue) || known(i.UnnecessaryIntervention) || known(i.Rework)) {
			return fmt.Errorf("a known incident judgement requires an evidence category")
		}
		if i.Module == "ward" && wardDecision(s, i) == "mixed" {
			return fmt.Errorf("incident cannot combine blocking and nonblocking Ward verdicts")
		}
		x.incidents[i.ID] = i
	}
	// Enforce exclusivity after folding the complete amendment, so a human can
	// deactivate one incident and regroup its events atomically in the same review.
	owners := map[string]string{}
	for _, i := range x.incidents {
		if !i.Active {
			continue
		}
		for _, id := range i.EventIDs {
			key := eventSource(s.Events[id])
			if owner, ok := owners[key]; ok && owner != i.ID {
				return fmt.Errorf("active incidents overlap the same source run")
			}
			owners[key] = i.ID
		}
	}
	seen = map[string]bool{}
	for _, c := range r.Comparisons {
		if !simpleToken.MatchString(c.ID) || seen[c.ID] || c.Revision != x.comparisons[c.ID].Revision+1 {
			return fmt.Errorf("comparison has an invalid ID or revision")
		}
		seen[c.ID] = true
		if c.BaselineTaskID == c.ToolTaskID {
			return fmt.Errorf("comparison requires two distinct tasks")
		}
		if _, ok := s.Tasks[c.BaselineTaskID]; !ok {
			return fmt.Errorf("comparison references an unknown baseline task")
		}
		if _, ok := s.Tasks[c.ToolTaskID]; !ok {
			return fmt.Errorf("comparison references an unknown tool task")
		}
		if !member(c.Module, "ward", "seal") || !validVerdict(c.BaselineSuccess) || !validVerdict(c.ToolSuccess) {
			return fmt.Errorf("comparison has an invalid judgement")
		}
		if (c.BaselineSeconds == nil) != (c.ToolSeconds == nil) {
			return fmt.Errorf("comparison timings must be paired")
		}
		for _, v := range []*float64{c.BaselineSeconds, c.ToolSeconds} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
				return fmt.Errorf("comparison timings must be finite and nonnegative")
			}
		}
		if c.Comparable {
			if _, err := comparisonCohort(s, x, c); err != nil {
				return err
			}
		}
		x.comparisons[c.ID] = c
	}
	pairedTasks := map[string]bool{}
	for _, c := range x.comparisons {
		if !c.Comparable {
			continue
		}
		for _, id := range []string{c.BaselineTaskID, c.ToolTaskID} {
			key := c.Module + "/" + id
			if pairedTasks[key] {
				return fmt.Errorf("a task cannot be counted in multiple comparable pairs for one module")
			}
			pairedTasks[key] = true
		}
	}
	return nil
}

func taskModuleVersion(s Snapshot, x reviewState, taskID, module string) string {
	version := ""
	for _, i := range x.incidents {
		if !i.Active || i.TaskID != taskID || i.Module != module {
			continue
		}
		v := incidentVersion(s, i)
		if v == "unknown" || v == "mixed" {
			return v
		}
		if version != "" && version != v {
			return "mixed"
		}
		version = v
	}
	return version
}

func comparisonCohort(s Snapshot, x reviewState, c Comparison) (CohortKey, error) {
	a, b := s.Tasks[c.BaselineTaskID], s.Tasks[c.ToolTaskID]
	ta, tb := x.tasks[c.BaselineTaskID], x.tasks[c.ToolTaskID]
	if ta.Eligibility != "yes" || tb.Eligibility != "yes" {
		return CohortKey{}, fmt.Errorf("comparable tasks must both be eligible")
	}
	if !terminal(ta.Outcome) || !terminal(tb.Outcome) {
		return CohortKey{}, fmt.Errorf("comparable tasks must both have reviewed terminal outcomes")
	}
	if a.Profile == "" || a.Profile == "unknown" || a.Model == "" || a.Model == "unknown" || ta.TaskType == "" || ta.TaskType == "unknown" || a.Profile != b.Profile || a.Model != b.Model || ta.TaskType != tb.TaskType {
		return CohortKey{}, fmt.Errorf("comparison requires the same known profile, model, and task type")
	}
	v := taskModuleVersion(s, x, c.ToolTaskID, c.Module)
	if v == "" || v == "unknown" || v == "mixed" {
		return CohortKey{}, fmt.Errorf("comparison requires a known tool version")
	}
	bv := taskModuleVersion(s, x, c.BaselineTaskID, c.Module)
	if bv != "" && bv != v {
		return CohortKey{}, fmt.Errorf("comparison module versions differ")
	}
	return CohortKey{Profile: safeLabel(a.Profile), Model: safeLabel(a.Model), TaskType: ta.TaskType, Module: c.Module, Version: safeLabel(v)}, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func markdownText(s string) string {
	return strings.NewReplacer("&", "&amp;", "|", "\\|", "\n", " ", "\r", " ", "<", "&lt;", ">", "&gt;", "`", "&#96;", "[", "\\[", "]", "\\]").Replace(s)
}

func allowedReviewReference(s Snapshot, reference string) bool {
	if !filepath.IsAbs(reference) || strings.ContainsAny(reference, "\x00\r\n") {
		return false
	}
	for _, roots := range [][]string{s.Experiment.Config.Repositories, s.Experiment.Config.SessionDirs, s.Experiment.Config.WardDirs} {
		for _, root := range roots {
			if !filepath.IsAbs(root) {
				continue
			}
			relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(reference))
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

func reviewLocalLink(s Snapshot, reference, label string) string {
	if !allowedReviewReference(s, reference) {
		return "reference unavailable"
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(reference)), "/")
	for n, part := range parts {
		parts[n] = url.PathEscape(part)
	}
	target := strings.NewReplacer("(", "%28", ")", "%29").Replace(strings.Join(parts, "/"))
	return "[" + markdownText(label) + "](<" + target + ">)"
}

func privateBindingDescription(s Snapshot, b Binding) string {
	if b.Kind == "seal_run" || b.Kind == "seal_task" {
		parts := strings.Split(b.Key, "\x00")
		want := 2
		if b.Kind == "seal_run" {
			want = 3
		}
		if len(parts) != want || filepath.Clean(parts[0]) != filepath.Clean(b.Reference) || !simpleToken.MatchString(parts[1]) {
			return "identity unavailable"
		}
		if b.Kind == "seal_task" {
			return "Task `" + markdownText(parts[1]) + "`: " + reviewLocalLink(s, filepath.Join(b.Reference, ".seal", "tasks", parts[1]+".json"), "saved Task")
		}
		if !simpleToken.MatchString(parts[2]) {
			return "identity unavailable"
		}
		return "Task `" + markdownText(parts[1]) + "`, Run `" + markdownText(parts[2]) + "`: " + reviewLocalLink(s, filepath.Join(b.Reference, ".seal", "evidence", parts[1], parts[2], "run-manifest.json"), "Run manifest")
	}
	return reviewLocalLink(s, b.Reference, "source")
}

// ReviewMarkdown is private and may contain local source references. Aggregate
// reports deliberately use a different renderer with no Snapshot access.
func ReviewMarkdown(s Snapshot, r Review) string {
	var b strings.Builder
	b.WriteString("# Private evaluation review\n\nHuman judgements are independent of tool results. Unknown is not a negative judgement. Link each reviewed incident to a task explicitly. Templates do not certify task completion.\n\n")
	b.WriteString("## Tasks\n\n| Task | Eligibility | Outcome | Type | Terminal record observed | Revision |\n|---|---|---|---|---|---:|\n")
	for _, t := range r.Tasks {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d |\n", markdownText(t.TaskID), t.Eligibility, t.Outcome, t.TaskType, t.TerminalRecordObserved, t.Revision)
	}
	for _, i := range r.Incidents {
		fmt.Fprintf(&b, "\n## Incident %s\n\nModule: %s; task: %s; active: %t; revision: %d.\n\n", markdownText(i.ID), i.Module, markdownText(i.TaskID), i.Active, i.Revision)
		fmt.Fprintf(&b, "Expected: %s; recorded action: %s; correctness: %s; additional value: %s; evidence: %s.\n\n", i.ExpectedAction, i.ActualAction, i.Correctness, i.AdditionalValue, i.Evidence)
		for _, id := range i.EventIDs {
			e := s.Events[id]
			fmt.Fprintf(&b, "- Event `%s`: %s %s; source `%s`; Evidence SHA-256 `%s`; source outcome `%s`; source task hint `%s`.\n", markdownText(id), markdownText(e.Module), markdownText(e.Version), markdownText(e.SourceID), markdownText(e.EvidenceSHA256), markdownText(e.Outcome), markdownText(e.HintTaskID))
		}
	}
	if len(s.Bindings) > 0 {
		b.WriteString("\n## Private source references\n\n")
		for _, binding := range s.Bindings {
			if binding.Reference != "" {
				fmt.Fprintf(&b, "- `%s` (%s): %s\n", markdownText(binding.ID), markdownText(binding.Kind), privateBindingDescription(s, binding))
			}
		}
	}
	b.WriteString("\nReview input is a human attestation. Paired comparisons require independent comparability judgement; these records alone do not establish causality.\n")
	return b.String()
}
