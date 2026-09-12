package task

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var quantityPattern = regexp.MustCompile(`^(.+?)\s+(?:[xX×]\s*)?([0-9]+)$`)

func ParseQuantity(s string) (string, int, error) {
	m := quantityPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", 0, errors.New("invalid quantity")
	}
	n, err := strconv.Atoi(m[2])
	name := strings.TrimSpace(m[1])
	if err != nil || n < 1 || n > 9999 || name == "" {
		return "", 0, errors.New("invalid quantity")
	}
	return name, n, nil
}
