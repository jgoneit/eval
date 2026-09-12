package task

import "testing"

func TestPublicLegacy(t *testing.T) {
	name, n, err := ParseQuantity("vitamin 12")
	if err != nil || name != "vitamin" || n != 12 {
		t.Fatalf("got %q, %d, %v", name, n, err)
	}
}
