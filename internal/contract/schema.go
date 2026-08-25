package contract

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/jgoneit/eval/schemas"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	observationV1SchemaURL = "urn:jgoneit:eval:observation:v1"
	observationV2SchemaURL = "urn:jgoneit:eval:observation:v2"
	draftV2SchemaURL       = "urn:jgoneit:eval:observe-draft:v2"
)

var (
	schemaOnce sync.Once
	schemaSet  *compiledSchemas
	schemaErr  error
)

type compiledSchemas struct {
	observation map[string]*jsonschema.Schema
	draft       *jsonschema.Schema
	extension   map[string]*jsonschema.Schema
}

var extensionSchemaURLs = map[string]string{
	"completion-effect/v1":   "urn:jgoneit:eval:extension:completion-effect:v1",
	"requirements-effect/v1": "urn:jgoneit:eval:extension:requirements-effect:v1",
	"security-effect/v1":     "urn:jgoneit:eval:extension:security-effect:v1",
	"seal-metrics/v1":        "urn:jgoneit:eval:extension:seal-metrics:v1",
	"spec-metrics/v1":        "urn:jgoneit:eval:extension:spec-metrics:v1",
	"ward-metrics/v1":        "urn:jgoneit:eval:extension:ward-metrics:v1",
}

func getSchemas() (*compiledSchemas, error) {
	schemaOnce.Do(func() {
		schemaSet, schemaErr = compileSchemas()
	})
	return schemaSet, schemaErr
}

func compileSchemas() (*compiledSchemas, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()

	err := fs.WalkDir(schemas.Files, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := schemas.Files.ReadFile(path)
		if err != nil {
			return err
		}
		var document any
		if err := json.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("embedded schema %s: %w", path, err)
		}
		object, ok := document.(map[string]any)
		if !ok {
			return fmt.Errorf("embedded schema %s is not an object", path)
		}
		identifier, ok := object["$id"].(string)
		if !ok || identifier == "" {
			return fmt.Errorf("embedded schema %s has no $id", path)
		}
		if err := compiler.AddResource(identifier, document); err != nil {
			return fmt.Errorf("add embedded schema %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := &compiledSchemas{
		observation: make(map[string]*jsonschema.Schema, 2),
		extension:   make(map[string]*jsonschema.Schema, len(extensionSchemaURLs)),
	}
	for version, location := range map[string]string{
		ObservationV1: observationV1SchemaURL,
		ObservationV2: observationV2SchemaURL,
	} {
		compiled, err := compiler.Compile(location)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", version, err)
		}
		result.observation[version] = compiled
	}
	result.draft, err = compiler.Compile(draftV2SchemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile draft: %w", err)
	}
	for version, location := range extensionSchemaURLs {
		compiled, err := compiler.Compile(location)
		if err != nil {
			return nil, fmt.Errorf("compile extension %s: %w", version, err)
		}
		result.extension[version] = compiled
	}
	return result, nil
}

func validateWith(schema *jsonschema.Schema, value any) error {
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}
	return nil
}
