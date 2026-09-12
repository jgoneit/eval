package assessment

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeStrictRejectsCaseAliasesAndAbsentOrNullCounters(t *testing.T) {
	s, a := advFixture()
	r, err := Assess(s, a)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"case-alias", []byte(strings.Replace(string(data), `"case_id":"boundary"`, `"case_id":"boundary","CASE_ID":"other"`, 1))},
		{"absent-counter", []byte(strings.Replace(string(data), `"planned":1,`, "", 1))},
		{"null-counter", []byte(strings.Replace(string(data), `"planned":1`, `"planned":null`, 1))},
		{"invalid-utf8", append([]byte(`{"schema":"`), append([]byte{0xff}, []byte(`"}`)...)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var target Assessment
			if DecodeStrict(test.data, &target) == nil {
				t.Fatal("accepted ambiguous or missing data")
			}
		})
	}
	var roundTrip Assessment
	if err := DecodeStrict(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAssessment(roundTrip); err != nil {
		t.Fatal(err)
	}
}
