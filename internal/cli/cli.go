package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/jgoneit/eval/internal/analyze"
	"github.com/jgoneit/eval/internal/contract"
	"github.com/jgoneit/eval/internal/core"
	"github.com/jgoneit/eval/internal/state"
	"github.com/jgoneit/eval/internal/store"
	"github.com/jgoneit/eval/internal/version"
)

const (
	ExitSuccess     = 0
	ExitInvalidData = 1
	ExitOperational = 2
	ExitUsage       = 64
	maxDraftBytes   = 1 << 20
)

var moduleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type Runtime struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	Now     func() time.Time
	NewUUID func() (string, error)
}

func Run(ctx context.Context, args []string, runtime Runtime) int {
	runtime = defaults(runtime)
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintf(runtime.Stdout, "evalctl %s\n", version.Current)
		return ExitSuccess
	}
	if len(args) == 0 {
		usage(runtime.Stderr)
		return ExitUsage
	}
	switch args[0] {
	case "observe":
		return runObserve(ctx, args[1:], runtime)
	case "validate":
		return runValidate(ctx, args[1:], runtime)
	case "summarize":
		return runSummarize(ctx, args[1:], runtime)
	case "compare":
		return runCompare(ctx, args[1:], runtime)
	case "help", "--help", "-h":
		usage(runtime.Stdout)
		return ExitSuccess
	default:
		fmt.Fprintf(runtime.Stderr, "error: unknown command %q\n", args[0])
		usage(runtime.Stderr)
		return ExitUsage
	}
}

func runObserve(ctx context.Context, args []string, runtime Runtime) int {
	flags := newFlagSet("observe", runtime.Stderr)
	input := flags.String("input", "", "read the generated-field-free draft from -")
	bestEffort := flags.Bool("best-effort", false, "turn record failures into a skipped result")
	stateRoot := flags.String("state-root", "", "absolute private state root")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	if *input != "-" {
		fmt.Fprintln(runtime.Stderr, "error: observe requires --input -")
		return ExitUsage
	}

	draft, err := readBounded(runtime.Stdin, maxDraftBytes)
	if err != nil {
		return observeFailure(runtime, *bestEffort, "invalid-observation", err, ExitInvalidData)
	}
	root, err := state.Root(*stateRoot, runtime.Getenv)
	if err != nil {
		return observeFailure(runtime, *bestEffort, "unsafe-state-path", err, ExitOperational)
	}
	result, err := (core.Observer{Now: runtime.Now, NewUUID: runtime.NewUUID}).Observe(ctx, root, draft)
	if err != nil {
		exit, reason := classifyObserveError(err)
		return observeFailure(runtime, *bestEffort, reason, err, exit)
	}
	if err := writeJSON(runtime.Stdout, result); err != nil {
		fmt.Fprintln(runtime.Stderr, "error: write observe result")
		return ExitOperational
	}
	return ExitSuccess
}

func runValidate(ctx context.Context, args []string, runtime Runtime) int {
	flags := newFlagSet("validate", runtime.Stderr)
	file := flags.String("file", "", "validate one JSONL file")
	stateRoot := flags.String("state-root", "", "absolute private state root")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	if *file != "" && *stateRoot != "" {
		fmt.Fprintln(runtime.Stderr, "error: --file and --state-root are mutually exclusive")
		return ExitUsage
	}
	var dataset core.Dataset
	var err error
	if *file != "" {
		dataset, err = core.ReadFile(*file)
	} else {
		root, rootErr := state.Root(*stateRoot, runtime.Getenv)
		if rootErr != nil {
			fmt.Fprintln(runtime.Stderr, "error: invalid state root")
			return ExitOperational
		}
		dataset, err = core.ReadState(ctx, root)
	}
	if err != nil {
		fmt.Fprintln(runtime.Stderr, "error: cannot read observation data")
		return ExitOperational
	}
	report := validationReport(dataset.Validation)
	if err := writeJSON(runtime.Stdout, report); err != nil {
		fmt.Fprintln(runtime.Stderr, "error: write validation result")
		return ExitOperational
	}
	if !dataset.Validation.Valid() {
		return ExitInvalidData
	}
	return ExitSuccess
}

func runSummarize(ctx context.Context, args []string, runtime Runtime) int {
	flags := newFlagSet("summarize", runtime.Stderr)
	asOfValue := flags.String("as-of", "", "required report snapshot date")
	fromValue := flags.String("from", "", "optional terminal date lower bound")
	throughValue := flags.String("through", "", "optional terminal date upper bound")
	format := flags.String("format", "json", "json or markdown")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	window, from, through, asOf, err := parseWindow(*asOfValue, *fromValue, *throughValue)
	if err != nil {
		fmt.Fprintf(runtime.Stderr, "error: %v\n", err)
		return ExitUsage
	}
	if *format != "json" && *format != "markdown" {
		fmt.Fprintln(runtime.Stderr, "error: --format must be json or markdown")
		return ExitUsage
	}
	dataset, exit := readAnalysisDataset(ctx, runtime, asOf)
	if exit != ExitSuccess {
		return exit
	}
	records, exclusions := dataset.Analysis(from, through)
	summary := analyze.BuildSummary(records, window, exclusions)
	var output []byte
	if *format == "markdown" {
		output = analyze.RenderSummaryMarkdown(summary)
	} else {
		output, err = analyze.CanonicalJSON(summary)
		if err != nil {
			fmt.Fprintln(runtime.Stderr, "error: encode summary")
			return ExitOperational
		}
	}
	if _, err := runtime.Stdout.Write(output); err != nil {
		fmt.Fprintln(runtime.Stderr, "error: write summary")
		return ExitOperational
	}
	return ExitSuccess
}

func runCompare(ctx context.Context, args []string, runtime Runtime) int {
	flags := newFlagSet("compare", runtime.Stderr)
	moduleID := flags.String("module", "", "module identifier")
	by := flags.String("by", "", "usage or version")
	left := flags.String("left", "", "left public version")
	right := flags.String("right", "", "right public version")
	asOfValue := flags.String("as-of", "", "required report snapshot date")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	if !moduleIDPattern.MatchString(*moduleID) {
		fmt.Fprintln(runtime.Stderr, "error: --module must be a valid module identifier")
		return ExitUsage
	}
	if *by != "usage" && *by != "version" {
		fmt.Fprintln(runtime.Stderr, "error: --by must be usage or version")
		return ExitUsage
	}
	if *by == "usage" && (*left != "" || *right != "") {
		fmt.Fprintln(runtime.Stderr, "error: --left and --right apply only to version comparison")
		return ExitUsage
	}
	if *by == "version" {
		if (*left == "") != (*right == "") || (*left != "" && *left == *right) {
			fmt.Fprintln(runtime.Stderr, "error: provide two distinct versions or omit both")
			return ExitUsage
		}
	}
	window, from, through, asOf, err := parseWindow(*asOfValue, "", "")
	if err != nil {
		fmt.Fprintf(runtime.Stderr, "error: %v\n", err)
		return ExitUsage
	}
	dataset, exit := readAnalysisDataset(ctx, runtime, asOf)
	if exit != ExitSuccess {
		return exit
	}
	records, exclusions := dataset.Analysis(from, through)
	comparison, err := analyze.BuildComparison(records, window, exclusions, *moduleID, *by, *left, *right)
	if err != nil {
		fmt.Fprintf(runtime.Stderr, "error: %v\n", err)
		return ExitUsage
	}
	output, err := analyze.CanonicalJSON(comparison)
	if err != nil {
		fmt.Fprintln(runtime.Stderr, "error: encode comparison")
		return ExitOperational
	}
	if _, err := runtime.Stdout.Write(output); err != nil {
		fmt.Fprintln(runtime.Stderr, "error: write comparison")
		return ExitOperational
	}
	return ExitSuccess
}

func readAnalysisDataset(ctx context.Context, runtime Runtime, asOf time.Time) (core.Dataset, int) {
	root, err := state.Root("", runtime.Getenv)
	if err != nil {
		fmt.Fprintln(runtime.Stderr, "error: invalid state root")
		return core.Dataset{}, ExitOperational
	}
	dataset, err := core.ReadState(ctx, root)
	if err != nil {
		fmt.Fprintln(runtime.Stderr, "error: cannot read observation state")
		return core.Dataset{}, ExitOperational
	}
	return dataset.AsOf(asOf), ExitSuccess
}

type safeIssue struct {
	Code   string `json:"code"`
	Source string `json:"source,omitempty"`
	Line   int    `json:"line,omitempty"`
}

type validateOutput struct {
	SchemaVersion string      `json:"schema_version"`
	Status        string      `json:"status"`
	ValidRows     int         `json:"valid_rows"`
	InvalidRows   int         `json:"invalid_rows"`
	InvalidChains int         `json:"invalid_chains"`
	ExcludedRows  int         `json:"excluded_rows"`
	Issues        []safeIssue `json:"issues"`
}

func validationReport(validation contract.LogValidation) validateOutput {
	status := "valid"
	if !validation.Valid() {
		status = "invalid"
	}
	issues := make([]safeIssue, 0, len(validation.Issues))
	for _, issue := range validation.Issues {
		issues = append(issues, safeIssue{Code: issue.Code, Source: issue.Source, Line: issue.Line})
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Source != issues[j].Source {
			return issues[i].Source < issues[j].Source
		}
		if issues[i].Line != issues[j].Line {
			return issues[i].Line < issues[j].Line
		}
		return issues[i].Code < issues[j].Code
	})
	return validateOutput{
		SchemaVersion: "eval-validation/v1", Status: status,
		ValidRows: len(validation.ValidObservations), InvalidRows: validation.InvalidRowCount,
		InvalidChains: validation.InvalidChainCount, ExcludedRows: validation.ExcludedRowCount,
		Issues: issues,
	}
}

func parseWindow(asOfValue, fromValue, throughValue string) (analyze.Window, *time.Time, time.Time, time.Time, error) {
	if asOfValue == "" {
		return analyze.Window{}, nil, time.Time{}, time.Time{}, fmt.Errorf("--as-of is required")
	}
	asOf, err := parseDate(asOfValue)
	if err != nil {
		return analyze.Window{}, nil, time.Time{}, time.Time{}, fmt.Errorf("invalid --as-of date")
	}
	through := asOf
	if throughValue != "" {
		through, err = parseDate(throughValue)
		if err != nil {
			return analyze.Window{}, nil, time.Time{}, time.Time{}, fmt.Errorf("invalid --through date")
		}
	}
	if through.After(asOf) {
		return analyze.Window{}, nil, time.Time{}, time.Time{}, fmt.Errorf("--through must not be after --as-of")
	}
	var from *time.Time
	var fromString *string
	if fromValue != "" {
		parsed, parseErr := parseDate(fromValue)
		if parseErr != nil {
			return analyze.Window{}, nil, time.Time{}, time.Time{}, fmt.Errorf("invalid --from date")
		}
		if parsed.After(through) {
			return analyze.Window{}, nil, time.Time{}, time.Time{}, fmt.Errorf("--from must not be after --through")
		}
		from = &parsed
		value := parsed.Format(time.DateOnly)
		fromString = &value
	}
	return analyze.Window{
		From: fromString, Through: through.Format(time.DateOnly), AsOf: asOf.Format(time.DateOnly),
	}, from, through, asOf, nil
}

func parseDate(value string) (time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Format(time.DateOnly) != value {
		return time.Time{}, fmt.Errorf("invalid date")
	}
	return parsed, nil
}

func classifyObserveError(err error) (int, string) {
	var storeErr *store.Error
	if errors.As(err, &storeErr) {
		if storeErr.Category == store.CategoryValidation {
			return ExitInvalidData, "invalid-state-data"
		}
		return ExitOperational, store.SafeReason(err)
	}
	if errors.Is(err, core.ErrInvalidDraft) {
		return ExitInvalidData, "invalid-observation"
	}
	if errors.Is(err, core.ErrInvalidState) {
		return ExitInvalidData, "invalid-state-data"
	}
	if errors.Is(err, core.ErrOperational) {
		return ExitOperational, "observe-operational-error"
	}
	return ExitOperational, "state-io-error"
}

func observeFailure(runtime Runtime, bestEffort bool, reason string, err error, exit int) int {
	if bestEffort {
		if writeErr := writeJSON(runtime.Stdout, map[string]string{"status": "skipped", "reason": reason}); writeErr != nil {
			fmt.Fprintln(runtime.Stderr, "error: write skipped result")
			return ExitOperational
		}
		return ExitSuccess
	}
	if exit == ExitInvalidData {
		fmt.Fprintln(runtime.Stderr, "error: observation data is invalid")
	} else {
		fmt.Fprintln(runtime.Stderr, "error: observation storage failed")
	}
	_ = err
	return exit
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("input exceeds %d bytes", maximum)
	}
	return data, nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: evalctl --version")
	fmt.Fprintln(writer, "       evalctl observe --input - [--best-effort] [--state-root ABS]")
	fmt.Fprintln(writer, "       evalctl validate [--file PATH | --state-root ABS]")
	fmt.Fprintln(writer, "       evalctl summarize --as-of DATE [--from DATE] [--through DATE] [--format json|markdown]")
	fmt.Fprintln(writer, "       evalctl compare --module ID --by usage|version [--left VERSION --right VERSION] --as-of DATE")
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
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return runtime
}
