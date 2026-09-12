package task

import (
	"reflect"
	"testing"
)

func TestRegression(t *testing.T) {
	items := []int{7, 8, 9, 10, 11}
	before := append([]int(nil), items...)
	got, err := Page(items, 1, 2)
	if err != nil || !reflect.DeepEqual(got, []int{9, 10}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if !reflect.DeepEqual(items, before) {
		t.Fatal("input mutated")
	}
}
