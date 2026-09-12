package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jgoneit/eval/internal/assessment"
)

func assessmentCLIInput() (assessment.Suite, assessment.AttemptSet) {
	digest := strings.Repeat("a", 64)
	c := assessment.Case{
		ID: "case-one", InitialFiles: []assessment.File{{Path: "path-canary/source.go", Digest: digest}, {Path: "public_test.go", Digest: digest}},
		AllowedFiles: []string{"path-canary/source.go"}, ProtectedFiles: []string{"public_test.go"},
		RequiredChecks: []assessment.CheckCriterion{{ID: "requirements", Kind: "requirement", CheckerDigest: digest}, {ID: "regressions", Kind: "regression", CheckerDigest: digest}},
	}
	c.InputDigest, c.CriteriaDigest = assessment.DigestFiles(c.InitialFiles), assessment.DigestCriteria(c)
	s := assessment.Suite{Schema: assessment.SuiteSchema, ID: "cli-suite", Version: "1", Cases: []assessment.Case{c}}
	a := assessment.AttemptSet{
		Schema: assessment.AttemptsSchema, SuiteID: s.ID, SuiteVersion: s.Version, SuiteDigest: assessment.DigestSuite(s),
		Condition:   assessment.Condition{ID: "baseline", InstructionDigest: digest},
		Environment: assessment.Environment{Model: "fixture-model", Reasoning: "high", PermissionProfile: "workspace-write", EnvironmentDigest: digest, ToolDigest: digest},
		Attempts:    []assessment.Attempt{},
	}
	return s, a
}

func writeAssessmentInput(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func invokeAssessmentCLI(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, Runtime{
		Stdout: &stdout, Stderr: &stderr,
		Getenv:      func(string) string { t.Fatal("assessment read experiment environment"); return "" },
		UserHomeDir: func() (string, error) { t.Fatal("assessment read experiment home"); return "", nil },
	})
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr %s", stderr.String())
	}
	return code, bytes.Clone(stdout.Bytes())
}

func TestAssessmentCLIPrivateReproducibleBundleAndCompare(t *testing.T) {
	base := t.TempDir()
	suite, attempts := assessmentCLIInput()
	suiteFile, attemptsFile := filepath.Join(base, "suite.json"), filepath.Join(base, "attempts.json")
	writeAssessmentInput(t, suiteFile, suite)
	writeAssessmentInput(t, attemptsFile, attempts)
	outputs := []string{filepath.Join(base, "first"), filepath.Join(base, "second"), filepath.Join(base, "candidate")}
	for _, out := range outputs[:2] {
		code, receipt := invokeAssessmentCLI(t, "assess", "--suite", suiteFile, "--attempts", attemptsFile, "--out", out)
		if code != 0 && code != 2 {
			t.Fatalf("assessment: %d %s", code, receipt)
		}
		if !bytes.Contains(receipt, []byte(`"committed":true`)) || !bytes.Contains(receipt, []byte(`"status":"assessed"`)) || bytes.Contains(receipt, []byte(base)) {
			t.Fatalf("bad publication receipt %s", receipt)
		}
	}
	for _, name := range []string{"assessment.json", "report.md"} {
		first, err := os.ReadFile(filepath.Join(outputs[0], name))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(filepath.Join(outputs[1], name))
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("non-reproducible %s: %v", name, err)
		}
		if bytes.Contains(first, []byte("path-canary")) || bytes.Contains(first, []byte(base)) {
			t.Fatalf("path disclosed in %s", name)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(outputs[0], name))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("artifact mode: %v %v", info, err)
			}
		}
	}
	code, data := invokeAssessmentCLI(t, "assess", "--suite", suiteFile, "--attempts", attemptsFile, "--out", outputs[0])
	if code != 1 || bytes.Contains(data, []byte(base)) {
		t.Fatalf("existing bundle overwritten or private path leaked: %d %s", code, data)
	}
	attempts.Condition.ID, attempts.Condition.InstructionDigest = "candidate", strings.Repeat("b", 64)
	writeAssessmentInput(t, attemptsFile, attempts)
	code, data = invokeAssessmentCLI(t, "assess", "--suite", suiteFile, "--attempts", attemptsFile, "--out", outputs[2])
	if code != 0 && code != 2 {
		t.Fatalf("candidate assessment: %d %s", code, data)
	}
	args := []string{"compare", "--baseline", filepath.Join(outputs[0], "assessment.json"), "--candidate", filepath.Join(outputs[2], "assessment.json")}
	code, data = invokeAssessmentCLI(t, args...)
	var comparison assessment.Comparison
	if code != 0 || json.Unmarshal(data, &comparison) != nil || comparison.Summary.Planned != 1 || comparison.Summary.Unevaluated != 1 || comparison.Summary.Comparable != 0 || comparison.Summary.BaselineMissing != 1 || comparison.Summary.CandidateMissing != 1 {
		t.Fatalf("missing cases lost: %d %s", code, data)
	}
	code, second := invokeAssessmentCLI(t, args...)
	if code != 0 || !bytes.Equal(data, second) {
		t.Fatal("comparison JSON not reproducible")
	}
	args = append(args, "--format", "markdown")
	code, data = invokeAssessmentCLI(t, args...)
	_, second = invokeAssessmentCLI(t, args...)
	if code != 0 || len(data) == 0 || !bytes.Equal(data, second) {
		t.Fatal("comparison Markdown not reproducible")
	}
}

func TestAssessmentCLIRejectsInvalidInputWithoutLeaksOrPublication(t *testing.T) {
	base := t.TempDir()
	file, out := filepath.Join(base, "private-path-canary.json"), filepath.Join(base, "output")
	if err := os.WriteFile(file, []byte(`{"schema":"eval-suite/v1","schema":"raw-conversation-canary"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"assess", "--suite", file, "--attempts", file, "--out", out},
		{"compare", "--baseline", file, "--candidate", file},
		{"assess", "--suite", file, "--attempts", file + ".missing", "--out", out},
	} {
		code, data := invokeAssessmentCLI(t, args...)
		if code != 1 || bytes.Contains(data, []byte("canary")) || bytes.Contains(data, []byte(base)) {
			t.Fatalf("validation leak: %d %s", code, data)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("invalid inputs published artifacts")
	}
	for _, args := range [][]string{
		{"assess", "--raw-conversation-canary"},
		{"compare", "--baseline", file, "--candidate", file, "--format", "path-canary"},
		{"compare"}, {"assess"},
	} {
		code, data := invokeAssessmentCLI(t, args...)
		if code != ExitUsage || bytes.Contains(data, []byte("canary")) {
			t.Fatalf("usage leak: %d %s", code, data)
		}
	}
}
