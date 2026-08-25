package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeJSONStrict decodes one JSON value and rejects duplicate object keys,
// malformed/non-finite numbers, and trailing values.
func DecodeJSONStrict(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if _, err := strictValue(decoder); err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("trailing JSON: %w", err)
		}
		return nil, fmt.Errorf("trailing JSON token %v", token)
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func strictValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}

	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, err := strictValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim('}') {
			return nil, fmt.Errorf("object ended with %v", end)
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := strictValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim(']') {
			return nil, fmt.Errorf("array ended with %v", end)
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delim)
	}
}

// CanonicalJSON returns compact JSON. encoding/json sorts string map keys,
// making output stable for the contract's JSON-compatible value types. The
// caller owns newline framing.
func CanonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
