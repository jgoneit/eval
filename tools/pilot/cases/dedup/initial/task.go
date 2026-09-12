package task

import "sort"

func Unique(items []string) []string {
	sort.Strings(items)
	result := items[:0]
	for _, item := range items {
		if len(result) == 0 || result[len(result)-1] != item {
			result = append(result, item)
		}
	}
	return result
}
