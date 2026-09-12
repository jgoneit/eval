package task

import "errors"

func Page(items []int, page, size int) ([]int, error) {
	if page < 0 || size <= 0 {
		return nil, errors.New("invalid page")
	}
	if len(items) == 0 || page > (len(items)-1)/size {
		return []int{}, nil
	}
	start := page * size
	count := len(items) - start
	if count > size {
		count = size
	}
	return items[start : start+count], nil
}
