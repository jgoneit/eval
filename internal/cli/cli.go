package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jgoneit/eval/internal/experiment"
	"github.com/jgoneit/eval/internal/state"
	"github.com/jgoneit/eval/internal/store"
	"github.com/jgoneit/eval/internal/version"
)

const (
	ExitSuccess = 0
	ExitUsage   = 64
)

var (
	errInvalidJournal = errors.New("invalid experiment journal")
	errJournalFull    = errors.New("experiment journal has twenty rows")
)

type Runtime struct {
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Getenv       func(string) string
	UserHomeDir  func() (string, error)
	Now          func() time.Time
	StoreOptions store.Options
}

func Run(ctx context.Context, args []string, runtime Runtime) int {
	runtime = defaults(runtime)
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(runtime.Stdout, "evalctl %s\n", version.Current)
		return ExitSuccess
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		usage(runtime.Stdout)
		return ExitSuccess
	}
	if len(args) > 0 {
		switch args[0] {
		case "assess", "compare":
			return runAssessment(ctx, args, runtime)
		case "experiment", "collect", "review", "report":
			return runEvaluation(ctx, args, runtime)
		}
	}
	if len(args) == 0 || args[0] != "observe" {
		usage(runtime.Stderr)
		return ExitUsage
	}
	return runObserve(ctx, args[1:], runtime)
}

func runObserve(ctx context.Context, args []string, runtime Runtime) int {
	flags := flag.NewFlagSet("observe", flag.ContinueOnError)
	flags.SetOutput(runtime.Stderr)
	stateRoot := flags.String("state-root", "", "absolute state root")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitUsage
	}
	if flags.NArg() != 0 {
		usage(runtime.Stderr)
		return ExitUsage
	}

	draft, err := experiment.DecodeDraft(runtime.Stdin)
	if err != nil {
		writeResult(runtime.Stdout, observeResult{Status: "skipped", Reason: "invalid-observation"})
		return ExitSuccess
	}
	root, err := state.Root(*stateRoot, runtime.Getenv, runtime.UserHomeDir)
	if err != nil {
		writeResult(runtime.Stdout, observeResult{Status: "skipped", Reason: "unsafe-state-path"})
		return ExitSuccess
	}
	journal, err := store.New(root, state.JournalRelativePath, runtime.StoreOptions)
	if err != nil {
		writeResult(runtime.Stdout, observeResult{Status: "skipped", Reason: store.SafeReason(err)})
		return ExitSuccess
	}

	var slot int64
	commit, err := journal.Update(ctx, func(existing []byte) ([]byte, error) {
		rows, parseErr := experiment.ParseJournal(bytes.NewReader(existing))
		if parseErr != nil {
			return nil, fmt.Errorf("%w: %v", errInvalidJournal, parseErr)
		}
		if int64(len(rows)) >= experiment.MaxRows {
			return nil, errJournalFull
		}
		slot = int64(len(rows) + 1)
		row, rowErr := experiment.NewRow(draft, slot, runtime.Now())
		if rowErr != nil {
			return nil, rowErr
		}
		encoded, rowErr := experiment.MarshalCanonicalRow(row)
		if rowErr != nil {
			return nil, rowErr
		}
		prospective := make([]byte, 0, len(existing)+len(encoded)+1)
		prospective = append(prospective, existing...)
		prospective = append(prospective, encoded...)
		prospective = append(prospective, '\n')
		return prospective, nil
	})
	if commit.Committed {
		durability := "confirmed"
		if !commit.DurabilityConfirmed {
			durability = "unconfirmed"
		}
		writeResult(runtime.Stdout, observeResult{Status: "recorded", Slot: slot, Durability: durability})
		return ExitSuccess
	}
	if err != nil {
		reason := classifyObserveFailure(err)
		writeResult(runtime.Stdout, observeResult{Status: "skipped", Reason: reason})
		return ExitSuccess
	}
	writeResult(runtime.Stdout, observeResult{Status: "skipped", Reason: "state-io-error"})
	return ExitSuccess
}

type observeResult struct {
	Status     string `json:"status"`
	Slot       int64  `json:"slot,omitempty"`
	Durability string `json:"durability,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func classifyObserveFailure(err error) string {
	switch {
	case errors.Is(err, errInvalidJournal):
		return "invalid-state-data"
	case errors.Is(err, errJournalFull):
		return "journal-full"
	default:
		var storeErr *store.Error
		if errors.As(err, &storeErr) {
			return store.SafeReason(err)
		}
		return "state-io-error"
	}
}

func writeResult(writer io.Writer, result observeResult) {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(result)
}

func usage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "usage: evalctl --version")
	_, _ = fmt.Fprintln(writer, "       evalctl observe [--state-root ABS]")
	_, _ = fmt.Fprintln(writer, "       evalctl experiment init --config FILE [--state-root ABS]")
	_, _ = fmt.Fprintln(writer, "       evalctl collect --experiment ID [--state-root ABS]")
	_, _ = fmt.Fprintln(writer, "       evalctl review export --experiment ID --out DIR [--state-root ABS]")
	_, _ = fmt.Fprintln(writer, "       evalctl review apply --experiment ID --file FILE [--state-root ABS]")
	_, _ = fmt.Fprintln(writer, "       evalctl report --experiment ID --format json|markdown [--state-root ABS]")
	_, _ = fmt.Fprintln(writer, "       evalctl assess --suite FILE --attempts FILE --out DIR")
	_, _ = fmt.Fprintln(writer, "       evalctl compare --baseline FILE --candidate FILE --format json|markdown")
}

func defaults(runtime Runtime) Runtime {
	if runtime.Stdin == nil {
		runtime.Stdin = os.Stdin
	}
	if runtime.Stdout == nil {
		runtime.Stdout = os.Stdout
	}
	if runtime.Stderr == nil {
		runtime.Stderr = os.Stderr
	}
	if runtime.Getenv == nil {
		runtime.Getenv = os.Getenv
	}
	if runtime.UserHomeDir == nil {
		runtime.UserHomeDir = os.UserHomeDir
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return runtime
}
