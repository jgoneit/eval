package assessment

import (
	"fmt"
	"strconv"
	"strings"
)

func measure(v *int64) string {
	if v == nil {
		return "unmeasured"
	}
	return strconv.FormatInt(*v, 10)
}

// Markdown renders only sanitized assessment fields, with no wall-clock data.
func Markdown(a Assessment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Coding assessment\n\nSuite: %s (%s). Condition: %s. Evaluator: %s.\n\n", a.SuiteID, a.SuiteVersion, a.Condition.ID, a.EvaluatorVersion)
	fmt.Fprintf(&b, "Planned: %d; attempted: %d; missing: %d.\n\n", a.Summary.Planned, a.Summary.Attempted, a.Summary.Missing)
	b.WriteString("Evidence provenance is declared, not authenticated. IDs and digests establish input consistency, not trusted Host identity. Results and process rules are reported separately; there is no overall score.\n\n")
	b.WriteString("| Case | Termination | Requirements | Regressions | Outcome | Process | Agent verification attempts | Independent verification attempts |\n| --- | --- | --- | --- | --- | --- | ---: | ---: |\n")
	for _, c := range a.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %d | %d |\n", c.CaseID, c.Termination, c.Requirements, c.Regressions, c.Outcome, c.Process, c.Verification.Agent, c.Verification.Independent)
	}
	b.WriteString("\n| Case | Observed ms | Input tokens | Output tokens | Cached input tokens |\n| --- | ---: | ---: | ---: | ---: |\n")
	for _, c := range a.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", c.CaseID, measure(c.Measurements.DurationMS), measure(c.Measurements.InputTokens), measure(c.Measurements.OutputTokens), measure(c.Measurements.CachedInputTokens))
	}
	for _, c := range a.Cases {
		fmt.Fprintf(&b, "\n## %s\n\nTool coverage: %s. Manifest coverage: %s. Permission coverage: %s. Configuration observation: %s.\n\n", c.CaseID, c.Coverage.Tools, c.Coverage.Manifest, c.Coverage.Permissions, c.ConfigurationObservation.Status)
		b.WriteString("| Kind | Criterion | Status | Evidence IDs |\n| --- | --- | --- | --- |\n")
		for _, f := range c.Checks {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", f.Kind, f.ID, f.Status, strings.Join(f.EvidenceIDs, ", "))
		}
		for _, f := range c.Rules {
			fmt.Fprintf(&b, "| process | %s | %s | %s |\n", f.ID, f.Status, strings.Join(f.EvidenceIDs, ", "))
		}
		if len(c.Tools) > 0 {
			b.WriteString("\n| Tool | Calls | Failed calls | Repeated fingerprints |\n| --- | ---: | ---: | ---: |\n")
			for _, t := range c.Tools {
				fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", t.Tool, t.Calls, t.Failures, t.RepeatedCalls)
			}
		}
		if len(c.Evidence) > 0 {
			b.WriteString("\n| Evidence ID | Kind | Declared provenance | Status | Executor |\n| --- | --- | --- | --- | --- |\n")
			for _, e := range c.Evidence {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", e.ID, e.Kind, e.Provenance, e.Status, e.Executor)
			}
		}
	}
	b.WriteString("\nRepeated fingerprints are counts, not a judgment of unnecessary work. Missing or partial permission records cannot establish permission compliance. Observed duration is not a claim of time saved.\n")
	return b.String()
}

func ComparisonMarkdown(c Comparison) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Coding assessment comparison\n\nSuite: %s (%s). Baseline: %s. Candidate: %s.\n\n", c.SuiteID, c.SuiteVersion, c.Baseline.ID, c.Candidate.ID)
	fmt.Fprintf(&b, "Comparison kind: %s.\n\n", c.Kind)
	if c.Kind == "same_instruction" {
		b.WriteString("The instruction digests match. Differences describe variation between samples, not an effect of changed instructions.\n\n")
	}
	fmt.Fprintf(&b, "Planned: %d; comparable outcomes: %d; improved: %d; regressed: %d; unchanged: %d; unevaluated: %d. Missing baseline: %d; missing candidate: %d.\n\n", c.Summary.Planned, c.Summary.Comparable, c.Summary.Improved, c.Summary.Regressed, c.Summary.Unchanged, c.Summary.Unevaluated, c.Summary.BaselineMissing, c.Summary.CandidateMissing)
	b.WriteString("Only pairs with completed attempts, pass/fail outcomes, and unchanged configurations matching across the pair count as comparable. Noncompleted attempts or changed/unavailable configurations have unevaluated outcome and process changes; their findings and observed measurement deltas remain visible. No overall score or automatic winner is selected.\n\n")
	b.WriteString("| Case | Baseline | Candidate | Outcome change | Baseline process | Candidate process | Process change | Baseline termination | Candidate termination |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range c.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.CaseID, r.Baseline, r.Candidate, r.OutcomeChange, r.BaselineProcess, r.CandidateProcess, r.ProcessChange, r.BaselineTermination, r.CandidateTermination)
	}

	b.WriteString("\n| Case | Baseline configuration | Candidate configuration | Matching unchanged configuration |\n| --- | --- | --- | --- |\n")
	for _, r := range c.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %t |\n", r.CaseID, r.BaselineConfiguration, r.CandidateConfiguration, r.ConfigurationComparable)
	}
	b.WriteString("\nMeasurement deltas are candidate minus baseline; missing values are unmeasured.\n\n| Case | Observed ms delta | Input token delta | Output token delta | Cached input token delta |\n| --- | ---: | ---: | ---: | ---: |\n")
	for _, r := range c.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.CaseID, measure(r.Delta.DurationMS), measure(r.Delta.InputTokens), measure(r.Delta.OutputTokens), measure(r.Delta.CachedInputTokens))
	}
	fmt.Fprintf(&b, "\nMeasured pairs: duration %d; input tokens %d; output tokens %d; cached input tokens %d.\n", c.Summary.DurationMeasuredPairs, c.Summary.InputTokensMeasuredPairs, c.Summary.OutputTokensMeasuredPairs, c.Summary.CachedInputTokensMeasuredPairs)
	b.WriteString("\n| Case | Process rule | Baseline | Candidate | Change |\n| --- | --- | --- | --- | --- |\n")
	for _, r := range c.Cases {
		for _, f := range r.Rules {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.CaseID, f.ID, f.Baseline, f.Candidate, f.Change)
		}
	}
	b.WriteString("\nThis comparison describes the supplied sample. Observed duration differences do not establish time savings or general superiority. Declared provenance and matching digests do not authenticate Host identity.\n")
	return b.String()
}
