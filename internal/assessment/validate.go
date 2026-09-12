package assessment

import (
	"regexp"
	"strings"
)

var tokenRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var pathPartRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func token(s string) bool  { return tokenRE.MatchString(s) }
func digest(s string) bool { return digestRE.MatchString(s) }
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func trusted(s string) bool     { return oneOf(s, "host_record", "independent_check") }
func provenance(s string) bool  { return oneOf(s, "host_record", "independent_check", "agent_report") }
func validStatus(s Status) bool { return s == Pass || s == Fail || s == Unavailable || s == Error }
func validPath(s string) bool {
	if len(s) == 0 || len(s) > 512 {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == "." || p == ".." || !pathPartRE.MatchString(p) {
			return false
		}
	}
	return true
}
func validateFiles(files []File) error {
	if len(files) > 10000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range files {
		if !validPath(f.Path) || !digest(f.Digest) || seen[f.Path] {
			return ErrInvalid
		}
		seen[f.Path] = true
	}
	// A manifest represents regular files. A file cannot also be a parent directory.
	for p := range seen {
		parts := strings.Split(p, "/")
		for i := 1; i < len(parts); i++ {
			if seen[strings.Join(parts[:i], "/")] {
				return ErrInvalid
			}
		}
	}
	return nil
}
func validatePaths(paths []string) error {
	if len(paths) > 10000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if !validPath(p) || seen[p] {
			return ErrInvalid
		}
		seen[p] = true
	}
	return nil
}
func validateEnvironment(e Environment) error {
	if !token(e.Model) || !token(e.Reasoning) || !token(e.PermissionProfile) || !digest(e.EnvironmentDigest) || !digest(e.ToolDigest) {
		return ErrInvalid
	}
	return nil
}
func validateCondition(c Condition) error {
	if !token(c.ID) || !digest(c.InstructionDigest) {
		return ErrInvalid
	}
	return nil
}
func validateCoverage(c Coverage) error {
	for _, v := range []string{c.Manifest, c.Tools, c.Permissions} {
		if !oneOf(v, "complete", "partial", "unavailable") {
			return ErrInvalid
		}
	}
	return nil
}
func validateMeasurements(m Measurements) error {
	if !provenance(m.Provenance) && m.Provenance != "unavailable" {
		return ErrInvalid
	}
	for _, v := range []*int64{m.DurationMS, m.InputTokens, m.OutputTokens, m.CachedInputTokens} {
		if v != nil && (*v < 0 || m.Provenance == "unavailable") {
			return ErrInvalid
		}
	}
	if m.CachedInputTokens != nil && m.InputTokens != nil && *m.CachedInputTokens > *m.InputTokens {
		return ErrInvalid
	}
	return nil
}
func validateSuite(s Suite) error {
	if s.Schema != SuiteSchema || !token(s.ID) || !token(s.Version) || len(s.Cases) == 0 || len(s.Cases) > 1000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if !token(c.ID) || seen[c.ID] || len(c.InitialFiles) == 0 || validateFiles(c.InitialFiles) != nil || validatePaths(c.AllowedFiles) != nil || validatePaths(c.ProtectedFiles) != nil {
			return ErrInvalid
		}
		seen[c.ID] = true
		if len(c.RequiredChecks) == 0 || len(c.RequiredChecks) > 1000 {
			return ErrInvalid
		}
		checks := map[string]bool{}
		kinds := map[string]bool{}
		for _, check := range c.RequiredChecks {
			if !token(check.ID) || checks[check.ID] || !oneOf(check.Kind, "requirement", "regression") || !digest(check.CheckerDigest) {
				return ErrInvalid
			}
			checks[check.ID] = true
			kinds[check.Kind] = true
		}
		if !kinds["requirement"] || !kinds["regression"] {
			return ErrInvalid
		}
		allow := map[string]bool{}
		for _, p := range c.AllowedFiles {
			allow[p] = true
		}
		for _, p := range c.ProtectedFiles {
			if allow[p] {
				return ErrInvalid
			}
		}
		initial := map[string]bool{}
		for _, f := range c.InitialFiles {
			initial[f.Path] = true
		}
		for _, p := range c.ProtectedFiles {
			if !initial[p] {
				return ErrInvalid
			}
		}
		if c.InputDigest != DigestFiles(c.InitialFiles) || c.CriteriaDigest != DigestCriteria(c) {
			return ErrInvalid
		}
	}
	return nil
}
func validateAttempt(a Attempt, c Case) error {
	if !token(a.ID) || a.CaseID != c.ID || !oneOf(a.Termination, "completed", "timeout", "interrupted", "agent_error", "environment_error", "authentication_error") {
		return ErrInvalid
	}
	if validateFiles(a.Files) != nil || !digest(a.ArtifactDigest) || a.ArtifactDigest != DigestFiles(a.Files) || !provenance(a.ManifestProvenance) || !token(a.ManifestEvidenceID) || validateCoverage(a.Coverage) != nil || validateMeasurements(a.Measurements) != nil {
		return ErrInvalid
	}
	if len(a.Checks) > 2000 || len(a.Events) > 100000 {
		return ErrInvalid
	}
	criteria := map[string]CheckCriterion{}
	for _, v := range c.RequiredChecks {
		criteria[v.ID] = v
	}
	checks := map[string]bool{}
	unavailableEvidence := map[string]bool{}
	evidence := map[string]string{a.ManifestEvidenceID: a.ManifestProvenance}
	for _, check := range a.Checks {
		criterion, ok := criteria[check.ID]
		key := check.ID + "/" + check.Executor
		if !ok || checks[key] || !token(check.EvidenceID) || !provenance(check.Provenance) || !oneOf(check.Executor, "agent", "independent") || !validStatus(check.Status) || check.CheckerDigest != criterion.CheckerDigest || check.ArtifactDigest != a.ArtifactDigest || check.ArtifactAfterDigest != a.ArtifactDigest {
			return ErrInvalid
		}
		if _, ok := evidence[check.EvidenceID]; ok {
			return ErrInvalid
		}
		evidence[check.EvidenceID] = check.Provenance
		unavailableEvidence[check.EvidenceID] = check.Status == Unavailable
		checks[key] = true
	}
	previous := 0
	events := map[string]bool{}
	for _, e := range a.Events {
		if !token(e.ID) || events[e.ID] || e.ID == a.ManifestEvidenceID || e.Sequence <= previous || !provenance(e.Provenance) || !oneOf(e.Kind, "tool_call", "verification", "permission", "observation_gap") {
			return ErrInvalid
		}
		events[e.ID] = true
		previous = e.Sequence
		if p, ok := evidence[e.ID]; ok && (p != e.Provenance || e.Kind != "verification" || unavailableEvidence[e.ID]) {
			return ErrInvalid
		}
		evidence[e.ID] = e.Provenance
		if e.Fingerprint != "" && !digest(e.Fingerprint) {
			return ErrInvalid
		}
		if e.Kind == "permission" {
			if !oneOf(e.Status, "allowed", "denied", "violation", "unknown") || e.Tool != "" || e.Fingerprint != "" {
				return ErrInvalid
			}
		} else if !oneOf(e.Status, "succeeded", "failed", "unknown") {
			return ErrInvalid
		}
		if e.Kind == "tool_call" || e.Kind == "verification" {
			if !oneOf(e.Tool, "shell", "file_change", "mcp", "web", "plan", "other") {
				return ErrInvalid
			}
		} else if e.Tool != "" || e.Fingerprint != "" {
			return ErrInvalid
		}
		if e.Kind == "observation_gap" && (a.Coverage.Tools == "complete" || a.Coverage.Permissions == "complete") {
			return ErrInvalid
		}
	}
	return nil
}
func validateInputs(s Suite, a AttemptSet) error {
	if validateSuite(s) != nil || a.Schema != AttemptsSchema || a.SuiteID != s.ID || a.SuiteVersion != s.Version || a.SuiteDigest != DigestSuite(s) || validateCondition(a.Condition) != nil || validateEnvironment(a.Environment) != nil || len(a.Attempts) > len(s.Cases) {
		return ErrInvalid
	}
	cases := map[string]Case{}
	for _, c := range s.Cases {
		cases[c.ID] = c
	}
	ids := map[string]bool{}
	used := map[string]bool{}
	for _, attempt := range a.Attempts {
		c, ok := cases[attempt.CaseID]
		if !ok || ids[attempt.ID] || used[attempt.CaseID] || validateAttempt(attempt, c) != nil {
			return ErrInvalid
		}
		ids[attempt.ID] = true
		used[attempt.CaseID] = true
	}
	return nil
}
