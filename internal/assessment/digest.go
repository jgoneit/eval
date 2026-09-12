package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// DigestInputs binds the typed, compact JSON input to the derived assessment.
// This checksum establishes consistency; it does not authenticate a producer.
func DigestInputs(inputs AssessmentInputs) string {
	data, _ := json.Marshal(inputs)
	return hash(InputsSchema + "\n" + string(data))
}

// DigestFiles hashes sorted path/digest pairs using a versioned newline format.
// Callers must validate manifests before treating this consistency digest as evidence.
func DigestFiles(files []File) string {
	files = append([]File(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var b strings.Builder
	b.WriteString("eval-files/v1\n")
	for _, f := range files {
		b.WriteString(f.Path + "\t" + f.Digest + "\n")
	}
	return hash(b.String())
}

func DigestCriteria(c Case) string {
	var b strings.Builder
	b.WriteString("eval-criteria/v1\n")
	for _, group := range []struct {
		prefix string
		paths  []string
	}{{"allow", c.AllowedFiles}, {"protect", c.ProtectedFiles}} {
		paths := append([]string(nil), group.paths...)
		sort.Strings(paths)
		for _, p := range paths {
			b.WriteString(group.prefix + "\t" + p + "\n")
		}
	}
	checks := append([]CheckCriterion(nil), c.RequiredChecks...)
	sort.Slice(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	for _, c := range checks {
		b.WriteString("check\t" + c.ID + "\t" + c.Kind + "\t" + c.CheckerDigest + "\n")
	}
	return hash(b.String())
}

func DigestSuite(s Suite) string {
	cases := append([]Case(nil), s.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	var b strings.Builder
	b.WriteString(SuiteSchema + "\n" + s.ID + "\n" + s.Version + "\n")
	for _, c := range cases {
		b.WriteString(c.ID + "\t" + c.InputDigest + "\t" + c.CriteriaDigest + "\n")
	}
	return hash(b.String())
}
