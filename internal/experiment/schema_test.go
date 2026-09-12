package experiment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoredRowSchemaRemainsSeparateFromAssessmentSchemas(t *testing.T) {
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
	want := map[string]bool{
		"experiment-v1.schema.json": true,
		"suite-v1.schema.json":      true,
		"attempts-v1.schema.json":   true,
		"assessment-v1.schema.json": true,
		"comparison-v1.schema.json": true,
	}
	if len(entries) != len(want) {
		t.Fatalf("schema entries = %v", entries)
	}
	for _, entry := range entries {
		if entry.IsDir() || !want[entry.Name()] {
			t.Fatalf("unexpected public schema %s", entry.Name())
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "experiment-v1.schema.json"))
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
