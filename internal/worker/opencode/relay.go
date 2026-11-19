package opencode

import (
	"encoding/json"
	"errors"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// readRelay reads the QA relay's ready file: a private regular file of at
// most 4096 bytes.
func readRelay(path string) (*executor.LocalModelRelay, error) {
	bad := errors.New("invalid relay descriptor")
	b, err := readPrivate(path, 4096)
	if err != nil {
		return nil, bad
	}
	var r executor.LocalModelRelay
	if json.Unmarshal(b, &r) != nil {
		return nil, bad
	}
	return &r, nil
}
