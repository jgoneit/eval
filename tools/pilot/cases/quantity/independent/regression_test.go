package task

import "testing"

func TestRegression(t *testing.T) {
	for _, tc := range []struct {
		input, name string
		n           int
	}{
		{"vitamin 12", "vitamin", 12}, {"pain relief 1", "pain relief", 1},
		{"  product 9999  ", "product", 9999}, {"비타민정 30", "비타민정", 30},
	} {
		name, n, err := ParseQuantity(tc.input)
		if err != nil || name != tc.name || n != tc.n {
			t.Errorf("%q: got %q, %d, %v", tc.input, name, n, err)
		}
	}
}
