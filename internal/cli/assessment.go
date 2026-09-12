package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jgoneit/eval/internal/assessment"
	"github.com/jgoneit/eval/internal/store"
)

// Assessment is stateless: it never loads or appends an experiment journal.
// Exit status describes validation/publication, not whether an agent passed.
func runAssessment(ctx context.Context, args []string, rt Runtime) int {
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var suite, attempts, out, baseline, candidate, format string
	if command == "assess" {
		fs.StringVar(&suite, "suite", "", "fixed suite JSON")
		fs.StringVar(&attempts, "attempts", "", "normalized attempts JSON")
		fs.StringVar(&out, "out", "", "new private artifact directory outside Git")
	} else {
		fs.StringVar(&baseline, "baseline", "", "baseline assessment JSON")
		fs.StringVar(&candidate, "candidate", "", "candidate assessment JSON")
		fs.StringVar(&format, "format", "json", "json or markdown")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(rt.Stderr)
			fmt.Fprintf(rt.Stderr, "Usage of %s:\n", command)
			fs.PrintDefaults()
			return ExitSuccess
		}
		return ledgerError(rt, "invalid-argument", ExitUsage)
	}
	if fs.NArg() != 0 || (command == "assess" && (suite == "" || attempts == "" || out == "")) || (command == "compare" && (baseline == "" || candidate == "")) {
		return ledgerError(rt, "missing-or-invalid-argument", ExitUsage)
	}
	if command == "compare" && format != "json" && format != "markdown" {
		return ledgerError(rt, "invalid-format", ExitUsage)
	}
	if ctx.Err() != nil {
		return ledgerError(rt, "assessment-canceled", 1)
	}
	if command == "compare" {
		b, err := readAssessmentRecord(baseline)
		if err != nil {
			return assessmentFailure(rt, err)
		}
		c, err := readAssessmentRecord(candidate)
		if err != nil {
			return assessmentFailure(rt, err)
		}
		comparison, err := assessment.Compare(b, c)
		if err != nil {
			return assessmentFailure(rt, err)
		}
		if format == "json" {
			return ledgerJSON(rt, comparison)
		}
		if _, err := fmt.Fprint(rt.Stdout, assessment.ComparisonMarkdown(comparison)); err != nil {
			return 1
		}
		return ExitSuccess
	}
	var s assessment.Suite
	var a assessment.AttemptSet
	if err := readAssessmentJSON(suite, &s); err != nil {
		return assessmentFailure(rt, err)
	}
	if err := readAssessmentJSON(attempts, &a); err != nil {
		return assessmentFailure(rt, err)
	}
	result, err := assessment.Assess(s, a)
	if err != nil {
		return assessmentFailure(rt, err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return assessmentFailure(rt, err)
	}
	inputs, err := json.MarshalIndent(assessment.AssessmentInputs{Schema: assessment.InputsSchema, Suite: s, Attempts: a}, "", "  ")
	if err != nil {
		return assessmentFailure(rt, err)
	}
	if len(data)+1 > assessment.MaxBytes || len(inputs)+1 > assessment.MaxBytes {
		return assessmentFailure(rt, assessment.ErrInvalid)
	}
	if ctx.Err() != nil {
		return ledgerError(rt, "assessment-canceled", 1)
	}
	commit, err := store.CreateArtifactBundle(out, map[string][]byte{
		"assessment.json": append(data, '\n'),
		"inputs.json":     append(inputs, '\n'),
		"report.md":       []byte(assessment.Markdown(result)),
	})
	if !commit.Committed {
		return assessmentFailure(rt, err)
	}
	durability := "unconfirmed"
	if commit.DurabilityConfirmed {
		durability = "confirmed"
	}
	code := ledgerJSON(rt, map[string]any{"status": "assessed", "committed": true, "durability": durability})
	if code != 0 {
		return code
	}
	if !commit.DurabilityConfirmed {
		return 2
	}
	return ExitSuccess
}

// Assessment files and their fixed sibling inputs are one private bundle.
// Store.Read retains the artifact store's bounded, no-symlink and identity
// checks without creating state, directories, or lock files.
func readAssessmentRecord(path string) (assessment.AssessmentRecord, error) {
	var record assessment.AssessmentRecord
	abs, err := filepath.Abs(path)
	if err != nil {
		return record, err
	}
	for _, file := range []struct {
		name   string
		target any
	}{{filepath.Base(abs), &record.Assessment}, {"inputs.json", &record.Inputs}} {
		reader, err := store.New(filepath.Dir(abs), file.name, store.Options{MaxBytes: assessment.MaxBytes})
		if err != nil {
			return record, err
		}
		data, err := reader.Read()
		if err != nil {
			return record, err
		}
		if err := assessment.DecodeStrict(data, file.target); err != nil {
			return record, err
		}
	}
	return record, nil
}

func readAssessmentJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, assessment.MaxBytes+1))
	if err != nil {
		return err
	}
	return assessment.DecodeStrict(data, target)
}

func assessmentFailure(rt Runtime, err error) int {
	if errors.Is(err, assessment.ErrInvalid) {
		return ledgerError(rt, "invalid-assessment-data", 1)
	}
	// Do not expose filesystem paths, JSON values, or parser diagnostics.
	return ledgerError(rt, store.SafeReason(err), 1)
}
