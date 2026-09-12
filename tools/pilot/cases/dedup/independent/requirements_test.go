package task

import (
	"reflect"
	"testing"
)

func TestRequirements(t *testing.T) {
	items := []string{"b", "a", "b", "", "a", "c", ""}
	before := append([]string(nil), items...)
	got := Unique(items)
	if !reflect.DeepEqual(got, []string{"b", "a", "", "c"}) {
		t.Errorf("got %v", got)
	}
	if !reflect.DeepEqual(items, before) {
		t.Error("input mutated")
	}
	if len(got) > 0 {
		got[0] = "changed"
	}
	if !reflect.DeepEqual(items, before) {
		t.Error("result aliases input")
	}
	if len(Unique(nil)) != 0 || len(Unique([]string{})) != 0 {
		t.Error("empty input")
	}
}
