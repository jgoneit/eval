package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/jgoneit/eval/internal/analyze"
	"github.com/jgoneit/eval/internal/contract"
	"github.com/jgoneit/eval/internal/state"
	"github.com/jgoneit/eval/internal/store"
)

type Dataset struct {
	Rows       []contract.ParsedRow
	Validation contract.LogValidation
}

func ReadState(ctx context.Context, root string) (Dataset, error) {
	v1, err := readOptional(ctx, root, state.V1RelativePath)
	if err != nil {
		return Dataset{}, err
	}
	v2, err := readOptional(ctx, root, state.V2RelativePath)
	if err != nil {
		return Dataset{}, err
	}
	return ParseStores(v1, v2), nil
}

func ReadFile(path string) (Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, err
	}
	rows := contract.ParseJSONL("file", data)
	return Dataset{Rows: rows, Validation: contract.ValidateLog(rows)}, nil
}

func ParseStores(v1, v2 []byte) Dataset {
	v1Rows := enforceStoreVersion(contract.ParseJSONL("v1", v1), contract.ObservationV1)
	v2Rows := enforceStoreVersion(contract.ParseJSONL("v2", v2), contract.ObservationV2)
	rows := append(v1Rows, v2Rows...)
	return Dataset{Rows: rows, Validation: contract.ValidateLog(rows)}
}

func enforceStoreVersion(rows []contract.ParsedRow, expected string) []contract.ParsedRow {
	for index := range rows {
		row := &rows[index]
		if row.Observation == nil || row.Observation.SchemaVersion == expected {
			continue
		}
		row.Issues = append(row.Issues, contract.Issue{
			Code: "misplaced_schema", Message: "observation schema does not match its versioned state store",
			Source: row.Source, Line: row.Line,
		})
	}
	return rows
}

func (d Dataset) AsOf(asOf time.Time) Dataset {
	rows := make([]contract.ParsedRow, 0, len(d.Rows))
	for _, row := range d.Rows {
		if row.Observation == nil {
			rows = append(rows, row)
			continue
		}
		recorded, err := time.Parse(time.DateOnly, row.Observation.RecordedOn)
		if err != nil || !recorded.After(asOf) {
			rows = append(rows, row)
		}
	}
	return Dataset{Rows: rows, Validation: contract.ValidateLog(rows)}
}

func (d Dataset) Analysis(from *time.Time, through time.Time) ([]analyze.Record, analyze.Exclusions) {
	validation := d.Validation
	latestAll := validation.Latest("")
	latestReal := validation.Latest("real")
	exclusions := analyze.Exclusions{
		InvalidRows:        validation.InvalidRowCount,
		InvalidChains:      validation.InvalidChainCount,
		InvalidOrChainRows: validation.ExcludedRowCount,
		SyntheticRows:      0,
		SupersededRows:     len(validation.ValidObservations) - len(latestAll),
	}
	for _, observation := range latestAll {
		if observation.Population == "synthetic" {
			exclusions.SyntheticRows++
		}
	}
	records := make([]analyze.Record, 0, len(latestReal))
	for _, observation := range latestReal {
		terminal, err := time.Parse(time.DateOnly, observation.TerminalOn)
		if err != nil || terminal.After(through) || (from != nil && terminal.Before(*from)) {
			exclusions.OutsideDateWindow++
			continue
		}
		records = append(records, analysisRecord(observation))
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].TaskID == records[j].TaskID {
			return records[i].ObservationID < records[j].ObservationID
		}
		return records[i].TaskID < records[j].TaskID
	})
	return records, exclusions
}

func analysisRecord(observation *contract.Observation) analyze.Record {
	modules := make(map[string]analyze.Module, len(observation.Modules))
	for id, module := range observation.Modules {
		var metrics map[string]any
		if module.Metrics != nil {
			metrics = module.Metrics.Values
		}
		modules[id] = analyze.Module{
			Used: module.Used, VersionStatus: module.Version.Status,
			Version: module.Version.Value, Metrics: metrics,
		}
	}
	effects := make(map[string]any, len(observation.TaskEffects))
	for id, effect := range observation.TaskEffects {
		effects[id] = effect.Values
	}
	return analyze.Record{
		TaskID: observation.TaskID, ObservationID: observation.ObservationID,
		SchemaVersion: observation.SchemaVersion, TerminalOn: observation.TerminalOn,
		Population: observation.Population, TaskType: observation.TaskType,
		Modules: modules, Outcome: observation.Outcome, TaskEffects: effects,
	}
}

func readOptional(ctx context.Context, root, relative string) ([]byte, error) {
	stateStore, err := store.New(root, relative, store.Options{})
	if err != nil {
		return nil, err
	}
	data, err := stateStore.Read(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
