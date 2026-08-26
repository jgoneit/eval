package experiment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseJournal strictly parses and validates a complete existing JSONL
// journal. An empty journal is valid. Blank rows and more than 20 rows are not.
func ParseJournal(r io.Reader) ([]Row, error) {
	data, err := readLimited(r)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if int64(len(lines)) > MaxRows {
		return nil, fmt.Errorf("journal contains more than %d rows", MaxRows)
	}

	rows := make([]Row, 0, len(lines))
	for index, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, fmt.Errorf("journal row %d is blank", index+1)
		}
		row, err := decodeRow(line)
		if err != nil {
			return nil, fmt.Errorf("journal row %d: %w", index+1, err)
		}
		rows = append(rows, row)
	}
	if err := ValidateJournal(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ValidateJournal requires physical slot order 1..n with no gaps and caps the
// bounded experiment at 20 rows.
func ValidateJournal(rows []Row) error {
	if int64(len(rows)) > MaxRows {
		return fmt.Errorf("journal contains more than %d rows", MaxRows)
	}
	for index, row := range rows {
		if err := ValidateRow(row); err != nil {
			return fmt.Errorf("journal row %d: %w", index+1, err)
		}
		expected := int64(index + 1)
		if row.Slot != expected {
			return fmt.Errorf("journal slot must be contiguous: got %d, want %d", row.Slot, expected)
		}
	}
	return nil
}

// MarshalCanonicalRow validates row and returns compact typed JSON. Struct
// field order defines the stable stored representation.
func MarshalCanonicalRow(row Row) ([]byte, error) {
	if err := ValidateRow(row); err != nil {
		return nil, err
	}
	data, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode journal row: %w", err)
	}
	return data, nil
}
