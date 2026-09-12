package evaluation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestObservedFloatingTotalsAreReproducible(t *testing.T) {
	s := reviewFixture()
	for n, value := range []float64{1e20, 10000, 10000, 10000, 10000, 10000, 10000, 10000} {
		e := addTestEvent(&s, fmt.Sprintf("event-%02d", n), "ward", "defer")
		e.DurationMS = &value
		s.Events[e.ID] = e
	}
	expected, err := json.Marshal(BuildReport(s))
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		actual, err := json.Marshal(BuildReport(s))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatal("report changes with Go map traversal order")
		}
	}
}

func TestKnownSourceExclusionsDoNotRequireHumanReview(t *testing.T) {
	s := reviewFixture()
	s.Reviews = nil
	for id, task := range s.Tasks {
		task.ExclusionReason = "child"
		s.Tasks[id] = task
	}
	r := BuildReport(s)
	if r.Counts.ExcludedTasks != len(s.Tasks) || r.Counts.EligibilityUnknown != 0 || r.Counts.EligibleTasks != 0 {
		t.Fatalf("source exclusion lost: %+v", r.Counts)
	}
}
