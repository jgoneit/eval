package assessment

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid assessment data")

// DecodeStrict rejects ambiguous JSON, unknown fields, and trailing values.
// Errors deliberately omit input content, which may contain private canaries.
func DecodeStrict(data []byte, target any) error {
	if len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueValue(d, 0); err != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return ErrInvalid
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return ErrInvalid
	}
	if err := requiredFields(data, t.Elem()); err != nil {
		return ErrInvalid
	}
	return nil
}

func requiredFields(data []byte, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil
		}
		return requiredFields(data, t.Elem())
	}
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Map && bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalid
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			return ErrInvalid
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			parts := strings.Split(field.Tag.Get("json"), ",")
			if parts[0] == "-" {
				continue
			}
			name := parts[0]
			if name == "" {
				name = field.Name
			}
			allowed[name] = true
			value, ok := fields[name]
			optional := len(parts) > 1 && parts[1] == "omitempty"
			if !ok {
				if !optional {
					return ErrInvalid
				}
				continue
			}
			if requiredFields(value, field.Type) != nil {
				return ErrInvalid
			}
		}
		for name := range fields {
			if !allowed[name] {
				return ErrInvalid
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return ErrInvalid
		}
		for _, v := range values {
			if requiredFields(v, t.Elem()) != nil {
				return ErrInvalid
			}
		}
	}
	return nil
}

func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	v, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch v {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrInvalid
			}
			seen[s] = true
			if e = uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return ErrInvalid
	}
	end, err := d.Token()
	if err != nil {
		return err
	}
	if (v == '{' && end != json.Delim('}')) || (v == '[' && end != json.Delim(']')) {
		return ErrInvalid
	}
	return nil
}
