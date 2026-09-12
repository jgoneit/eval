Repair `Page(items []int, page, size int) ([]int, error)` in `task.go`.

Pages are numbered from zero. A negative page or nonpositive size is invalid and must return an error. Valid requests return the corresponding consecutive items, including a shorter last page. An empty input or a page past the end returns an empty result without error. Very large page values must not overflow or panic. Preserve input contents. Keep the public function signature and package unchanged.
