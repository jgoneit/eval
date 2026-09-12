package task

import "testing"

func TestRequirements(t *testing.T) {
	for _, tc := range []struct {
		items      []int
		page, size int
		want       []int
		invalid    bool
	}{
		{nil, 0, 3, nil, false},
		{[]int{1, 2, 3}, 1, 2, []int{3}, false},
		{[]int{1, 2, 3, 4}, 1, 2, []int{3, 4}, false},
		{[]int{1}, 5, 2, nil, false},
		{[]int{1}, int(^uint(0) >> 1), 2, nil, false},
		{[]int{1}, -1, 2, nil, true},
		{[]int{1}, 0, 0, nil, true},
		{[]int{1}, 0, -1, nil, true},
	} {
		got, err := Page(tc.items, tc.page, tc.size)
		if (err != nil) != tc.invalid {
			t.Errorf("page=%d size=%d: error=%v", tc.page, tc.size, err)
			continue
		}
		if tc.invalid {
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("got %v want %v", got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("got %v want %v", got, tc.want)
				break
			}
		}
	}
}
