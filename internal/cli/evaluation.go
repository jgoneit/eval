package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jgoneit/eval/internal/evaluation"
	"github.com/jgoneit/eval/internal/state"
	"github.com/jgoneit/eval/internal/store"
)

// Ledger commands fail visibly. Legacy observe keeps its silent best-effort
// success/skip contract in cli.go.
func runEvaluation(ctx context.Context, args []string, rt Runtime) int {
	command := args[0]
	args = args[1:]
	if command == "experiment" || command == "review" {
		if len(args) == 0 {
			return ledgerError(rt, "invalid-command", ExitUsage)
		}
		command += " " + args[0]
		args = args[1:]
	}
	if command != "experiment init" && command != "collect" && command != "review export" && command != "review apply" && command != "report" {
		return ledgerError(rt, "invalid-command", ExitUsage)
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("state-root", "", "absolute private state root")
	var id, config, file, out, format *string
	if command == "experiment init" {
		config = fs.String("config", "", "experiment configuration JSON")
	} else {
		id = fs.String("experiment", "", "experiment UUID")
	}
	if command == "review apply" {
		file = fs.String("file", "", "human review JSON")
	}
	if command == "review export" {
		out = fs.String("out", "", "new private export directory")
	}
	if command == "report" {
		format = fs.String("format", "json", "json or markdown")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(rt.Stderr)
			fmt.Fprintf(rt.Stderr, "Usage of %s:\n", command)
			fs.PrintDefaults()
			return 0
		}
		return ledgerError(rt, "invalid-argument", ExitUsage)
	}
	if fs.NArg() != 0 || (id != nil && *id == "") || (config != nil && *config == "") || (file != nil && *file == "") || (out != nil && *out == "") {
		return ledgerError(rt, "missing-or-invalid-argument", ExitUsage)
	}
	root, err := state.Root(*rootFlag, rt.Getenv, rt.UserHomeDir)
	if err != nil {
		return ledgerFailure(rt, err)
	}
	m := evaluation.Manager{Root: root, Options: rt.StoreOptions}
	switch command {
	case "experiment init":
		var c evaluation.Config
		if err = readLedgerJSON(*config, &c); err != nil {
			return ledgerFailure(rt, err)
		}
		e, commit, err := m.Init(ctx, c, rt.Now())
		if !commit.Committed {
			if e.ID != "" {
				_ = ledgerJSON(rt, map[string]any{"status": "initialization-incomplete", "experiment_id": e.ID, "reason": store.SafeReason(err)})
				return 1
			}
			return ledgerFailure(rt, err)
		}
		// Raw paths and configuration remain in private storage.
		durability := "unconfirmed"
		if commit.DurabilityConfirmed {
			durability = "confirmed"
		}
		code := ledgerJSON(rt, map[string]any{"status": "initialized", "schema": e.Schema, "experiment_id": e.ID, "started_at": e.StartedAt, "durability": durability})
		if code != 0 {
			return code
		}
		if !commit.DurabilityConfirmed {
			return 2
		}
		return 0
	case "collect":
		r, err := m.Collect(ctx, *id, rt.Now())
		if err != nil && !r.Committed {
			return ledgerFailure(rt, err)
		}
		code := ledgerJSON(rt, r)
		if code != 0 {
			return code
		}
		if !r.Complete || !r.Committed || r.Durability != "confirmed" {
			return 2
		}
		return 0
	case "review apply":
		var r evaluation.Review
		if err = readLedgerJSON(*file, &r); err != nil {
			return ledgerFailure(rt, err)
		}
		commit, err := m.Apply(ctx, *id, r, rt.Now())
		if !commit.Committed && err != nil {
			return ledgerFailure(rt, err)
		}
		durability := "unconfirmed"
		if commit.DurabilityConfirmed {
			durability = "confirmed"
		}
		code := ledgerJSON(rt, map[string]any{"status": "applied", "durability": durability})
		if code != 0 {
			return code
		}
		if !commit.DurabilityConfirmed {
			return 2
		}
		return 0
	case "review export":
		s, err := m.Load(*id)
		if err != nil {
			return ledgerFailure(rt, err)
		}
		r, err := evaluation.ReviewTemplate(s)
		if err != nil {
			return ledgerFailure(rt, err)
		}
		encoded, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return ledgerFailure(rt, err)
		}
		commit, err := store.CreateArtifactBundle(*out, map[string][]byte{
			"review.json": append(encoded, '\n'),
			"evidence.md": []byte(evaluation.ReviewMarkdown(s, r)),
		})
		if err != nil && !commit.Committed {
			return ledgerFailure(rt, err)
		}
		durability := "unconfirmed"
		if commit.DurabilityConfirmed {
			durability = "confirmed"
		}
		code := ledgerJSON(rt, map[string]any{"status": "exported", "experiment_id": *id, "durability": durability})
		if code != 0 {
			return code
		}
		if !commit.DurabilityConfirmed {
			return 2
		}
		return 0
	case "report":
		if *format != "json" && *format != "markdown" {
			return ledgerError(rt, "invalid-format", ExitUsage)
		}
		s, err := m.Load(*id)
		if err != nil {
			return ledgerFailure(rt, err)
		}
		r := evaluation.BuildReport(s)
		if *format == "json" {
			return ledgerJSON(rt, r)
		}
		if _, err := fmt.Fprint(rt.Stdout, evaluation.ReportMarkdown(r)); err != nil {
			return 1
		}
		return 0
	}
	return ExitUsage
}

func readLedgerJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, evaluation.MaxBytes+1))
	if err != nil {
		return err
	}
	return evaluation.DecodeStrict(data, target)
}

func ledgerJSON(rt Runtime, value any) int {
	encoder := json.NewEncoder(rt.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}
func ledgerError(rt Runtime, reason string, code int) int {
	_ = json.NewEncoder(rt.Stdout).Encode(map[string]any{"status": "error", "reason": reason})
	return code
}
func ledgerFailure(rt Runtime, err error) int {
	reason := store.SafeReason(err)
	if errors.Is(err, evaluation.ErrFull) {
		reason = "experiment-storage-full"
	}
	if errors.Is(err, evaluation.ErrInvalid) {
		reason = "invalid-evaluation-data"
	}
	return ledgerError(rt, reason, 1)
}
