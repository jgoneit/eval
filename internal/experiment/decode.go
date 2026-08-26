package experiment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var ErrInputTooLarge = errors.New("experiment input exceeds 64 KiB")

// DecodeDraft strictly decodes one host-provided draft. It rejects duplicate
// keys at every depth, trailing JSON values, unknown fields, missing required
// fields, and non-integer JSON representations for integer fields.
func DecodeDraft(r io.Reader) (Draft, error) {
	data, err := readLimited(r)
	if err != nil {
		return Draft{}, err
	}
	if err := checkStrictJSON(data); err != nil {
		return Draft{}, fmt.Errorf("invalid draft JSON: %w", err)
	}
	if err := checkObjectShape(data, false); err != nil {
		return Draft{}, err
	}

	var draft Draft
	if err := decodeTyped(data, &draft); err != nil {
		return Draft{}, fmt.Errorf("invalid draft: %w", err)
	}
	if err := ValidateDraft(draft); err != nil {
		return Draft{}, err
	}
	return draft, nil
}

func decodeRow(data []byte) (Row, error) {
	if err := checkStrictJSON(data); err != nil {
		return Row{}, fmt.Errorf("invalid journal row JSON: %w", err)
	}
	if err := checkObjectShape(data, true); err != nil {
		return Row{}, err
	}

	var row Row
	if err := decodeTyped(data, &row); err != nil {
		return Row{}, fmt.Errorf("invalid journal row: %w", err)
	}
	if err := ValidateRow(row); err != nil {
		return Row{}, err
	}
	return row, nil
}

func readLimited(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("experiment input is nil")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read experiment input: %w", err)
	}
	if int64(len(data)) > MaxInputBytes {
		return nil, ErrInputTooLarge
	}
	return data, nil
}

func decodeTyped(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func checkStrictJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := scanValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func scanValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for dec.More() {
			if err := scanValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func checkObjectShape(data []byte, row bool) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("JSON root must be an object: %w", err)
	}
	if top == nil {
		return errors.New("JSON root must be an object")
	}

	allowed := map[string]struct{}{
		"outcome": {}, "rework_required": {}, "ward": {}, "seal": {},
	}
	required := []string{"outcome", "ward", "seal"}
	if row {
		allowed["schema_version"] = struct{}{}
		allowed["slot"] = struct{}{}
		allowed["recorded_at"] = struct{}{}
		required = append([]string{"schema_version", "slot", "recorded_at"}, required...)
	}
	label := "draft"
	if row {
		label = "row"
	}
	if err := rejectUnknownAndRequire(top, allowed, required, label); err != nil {
		return err
	}
	if raw, ok := top["rework_required"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("rework_required must be boolean when present")
	}
	if err := checkModuleShape("ward", top["ward"]); err != nil {
		return err
	}
	if err := checkModuleShape("seal", top["seal"]); err != nil {
		return err
	}
	return nil
}

func checkModuleShape(name string, data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return fmt.Errorf("%s must be an object", name)
	}
	allowed := map[string]struct{}{
		"used": {}, "version": {}, "defects_caught_before_terminal": {},
		"added_user_interventions": {}, "interaction_seconds": {},
		"normal_work_blocked": {},
	}
	if err := rejectUnknownAndRequire(object, allowed, []string{"used", "version"}, name); err != nil {
		return err
	}
	for _, field := range []string{
		"defects_caught_before_terminal",
		"added_user_interventions",
		"interaction_seconds",
		"normal_work_blocked",
	} {
		if raw, ok := object[field]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s.%s must not be null", name, field)
		}
	}
	return nil
}

func rejectUnknownAndRequire(
	object map[string]json.RawMessage,
	allowed map[string]struct{},
	required []string,
	label string,
) error {
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown %s field %q", label, key)
		}
	}
	for _, key := range required {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("missing required %s field %q", label, key)
		}
	}
	return nil
}
