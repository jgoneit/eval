package experiment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoredRowSchemaIsTheOnlySchema(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	directory := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "schemas"))
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "experiment-v1.schema.json" {
		t.Fatalf("schema entries = %v", entries)
	}
	data, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema JSON: %v", err)
	}
	if schema["$id"] != "urn:jgoneit:eval:experiment:v1" || schema["additionalProperties"] != false {
		t.Fatalf("unexpected stored-row schema identity: %#v", schema)
	}
}
