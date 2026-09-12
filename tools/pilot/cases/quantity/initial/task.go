package task

import (
	"errors"
	"strconv"
	"strings"
)

func ParseQuantity(s string) (string, int, error) {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return "", 0, errors.New("missing quantity")
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "", 0, err
	}
	return strings.Join(parts[:len(parts)-1], " "), n, nil
}
