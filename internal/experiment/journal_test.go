package experiment

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMarshalCanonicalRowExactBytes(t *testing.T) {
	t.Parallel()

	version := "0.1.0"
	rework := false
	zero := int64(0)
	two := int64(2)
	three := int64(3)
	blocked := false
	row := Row{
		SchemaVersion:  SchemaVersion,
		Slot:           1,
		RecordedAt:     "2026-08-26T03:34:56.12Z",
		Outcome:        OutcomeCompleted,
		ReworkRequired: &rework,
		Ward: Module{
			Used:                        true,
			Version:                     &version,
			DefectsCaughtBeforeTerminal: &zero,
			AddedUserInterventions:      &two,
			InteractionSeconds:          &three,
			NormalWorkBlocked:           &blocked,
		},
		Seal: Module{Used: false, Version: nil},
	}
	want := `{"schema_version":"eval-experiment/v1","slot":1,"recorded_at":"2026-08-26T03:34:56.12Z",` +
		`"outcome":"completed","rework_required":false,"ward":{"used":true,"version":"0.1.0",` +
		`"defects_caught_before_terminal":0,"added_user_interventions":2,"interaction_seconds":3,` +
		`"normal_work_blocked":false},"seal":{"used":false,"version":null}}`

	got, err := MarshalCanonicalRow(row)
	if err != nil {
		t.Fatalf("MarshalCanonicalRow() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("MarshalCanonicalRow() =\n%s\nwant:\n%s", got, want)
	}
}

func TestParseJournalRejectsFractionalAndExponentSlots(t *testing.T) {
	t.Parallel()

	base := `{"schema_version":"eval-experiment/v1","slot":%s,"recorded_at":"2026-08-26T03:34:56Z",` +
		`"outcome":"completed","ward":{"used":true,"version":null},"seal":{"used":false,"version":null}}`
	for _, slot := range []string{"1.0", "1e0", "1E+0"} {
		input := strings.Replace(base, "%s", slot, 1) + "\n"
		if _, err := ParseJournal(strings.NewReader(input)); err == nil {
			t.Errorf("ParseJournal(slot=%s) succeeded, want int64 lexical rejection", slot)
		}
	}
}

func TestValidateJournalRejectsInvalidSlots(t *testing.T) {
	t.Parallel()

	row := canonicalTestRow(t, 1)
	tests := []struct {
		name string
		rows []Row
	}{
		{name: "slot zero", rows: []Row{withSlot(row, 0)}},
		{name: "slot over maximum", rows: []Row{withSlot(row, MaxRows+1)}},
		{name: "starts at two", rows: []Row{withSlot(row, 2)}},
		{name: "gap", rows: []Row{row, withSlot(row, 3)}},
		{name: "duplicate", rows: []Row{row, row}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateJournal(test.rows); err == nil {
				t.Fatal("ValidateJournal() succeeded, want slot rejection")
			}
		})
	}
}

func TestParseJournalRejectsGappedAndOverTwentyRows(t *testing.T) {
	t.Parallel()

	first := canonicalTestRow(t, 1)
	third := canonicalTestRow(t, 3)
	gapped := canonicalLine(t, first) + "\n" + canonicalLine(t, third) + "\n"
	if _, err := ParseJournal(strings.NewReader(gapped)); err == nil {
		t.Fatal("ParseJournal(gapped) succeeded")
	}

	row := canonicalLine(t, first) + "\n"
	over := strings.Repeat(row, int(MaxRows)+1)
	if _, err := ParseJournal(strings.NewReader(over)); err == nil {
		t.Fatal("ParseJournal(21 rows) succeeded")
	}
}

func TestValidateJournalRejectsMoreThanTwentyRows(t *testing.T) {
	t.Parallel()

	rows := make([]Row, MaxRows+1)
	row := canonicalTestRow(t, 1)
	for index := range rows {
		rows[index] = row
	}
	if err := ValidateJournal(rows); err == nil {
		t.Fatal("ValidateJournal(21 rows) succeeded")
	}
}

func TestParseJournalAcceptsTwentyContiguousRows(t *testing.T) {
	t.Parallel()

	var journal strings.Builder
	for slot := int64(1); slot <= MaxRows; slot++ {
		journal.WriteString(canonicalLine(t, canonicalTestRow(t, slot)))
		journal.WriteByte('\n')
	}
	rows, err := ParseJournal(strings.NewReader(journal.String()))
	if err != nil {
		t.Fatalf("ParseJournal() error = %v", err)
	}
	if int64(len(rows)) != MaxRows || rows[MaxRows-1].Slot != MaxRows {
		t.Fatalf("rows = %d last slot = %d, want twenty contiguous rows", len(rows), rows[len(rows)-1].Slot)
	}
}

func TestParseJournalInputLimit(t *testing.T) {
	t.Parallel()

	_, err := ParseJournal(strings.NewReader(strings.Repeat(" ", int(MaxInputBytes)+1)))
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("ParseJournal() over limit error = %v, want ErrInputTooLarge", err)
	}
}

func canonicalTestRow(t *testing.T, slot int64) Row {
	t.Helper()
	row, err := NewRow(validDraft(t), slot, time.Date(2026, 8, 26, 3, 34, 56, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewRow(slot=%d) error = %v", slot, err)
	}
	return row
}

func canonicalLine(t *testing.T, row Row) string {
	t.Helper()
	encoded, err := MarshalCanonicalRow(row)
	if err != nil {
		t.Fatalf("MarshalCanonicalRow() error = %v", err)
	}
	return string(encoded)
}

func withSlot(row Row, slot int64) Row {
	row.Slot = slot
	return row
}
