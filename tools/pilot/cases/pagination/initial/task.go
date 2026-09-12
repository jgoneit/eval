package task

import "errors"

func Page(items []int, page, size int) ([]int, error) {
	if page < 0 || size < 0 {
		return nil, errors.New("invalid page")
	}
	start := page * size
	end := start + size
	if start >= len(items) || end >= len(items) {
		return nil, errors.New("page outside input")
	}
	return items[start:end], nil
}
