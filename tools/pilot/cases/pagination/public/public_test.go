package task

import (
	"reflect"
	"testing"
)

func TestPublicFirstPage(t *testing.T) {
	got, err := Page([]int{1, 2, 3, 4, 5}, 0, 2)
	if err != nil || !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("got %v, %v", got, err)
	}
}
