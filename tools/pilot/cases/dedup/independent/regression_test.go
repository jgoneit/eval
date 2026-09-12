package task

import (
	"reflect"
	"testing"
)

func TestRegression(t *testing.T) {
	for _, tc := range []struct{ input, want []string }{
		{[]string{"a", "a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{[]string{"x", "x", "x"}, []string{"x"}},
	} {
		if got := Unique(tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("got %v want %v", got, tc.want)
		}
	}
}
