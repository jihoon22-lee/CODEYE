package opencode

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func readRelay(path string) (*executor.LocalModelRelay, error) {
	bad := errors.New("invalid relay descriptor")
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		return nil, bad
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, bad
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !os.SameFile(st, current) {
		return nil, bad
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return nil, bad
	}
	var r executor.LocalModelRelay
	if json.Unmarshal(b, &r) != nil {
		return nil, bad
	}
	return &r, nil
}
