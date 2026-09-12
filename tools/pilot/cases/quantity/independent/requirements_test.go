package task

import "testing"

func TestRequirements(t *testing.T) {
	for _, tc := range []struct {
		input, name string
		quantity    int
	}{
		{"pain relief x12", "pain relief", 12}, {"vitamin X 3", "vitamin", 3},
		{"  비타민 × 2  ", "비타민", 2}, {"milk ×9", "milk", 9},
		{"multi  word 12", "multi  word", 12}, {"product x0002", "product", 2},
	} {
		name, n, err := ParseQuantity(tc.input)
		if err != nil || name != tc.name || n != tc.quantity {
			t.Errorf("%q: got %q, %d, %v", tc.input, name, n, err)
		}
	}
	for _, input := range []string{"", "12", "product", "product x", "product -1", "product 0", "product 10000", "product 1.5", "product +2", "product x-2", "product 999999999999999999999999"} {
		if _, _, err := ParseQuantity(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}
