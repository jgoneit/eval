package task

import (
	"reflect"
	"testing"
)

func TestPublicDuplicates(t *testing.T) {
	got := Unique([]string{"a", "a", "b"})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
}
