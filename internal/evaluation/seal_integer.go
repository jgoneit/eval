package evaluation

import "regexp"

// SealExitCode preserves a historical JSON integer without a machine-integer
// limit. It stays an unquoted number in the ledger and never passes through a
// float. A nil *SealExitCode represents an unavailable exit code.
type SealExitCode string

var sealInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

func (n *SealExitCode) UnmarshalJSON(data []byte) error {
	if !sealInteger.Match(data) {
		return ErrInvalid
	}
	*n = SealExitCode(data)
	return nil
}

func (n SealExitCode) MarshalJSON() ([]byte, error) {
	if !sealInteger.MatchString(string(n)) {
		return nil, ErrInvalid
	}
	return []byte(n), nil
}
