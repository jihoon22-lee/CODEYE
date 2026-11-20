package source

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

// ReadHostToken reads the token of host from a Git credential file (the
// service writes <state>/secrets/github-users/<user_id>.json: {"host": ...,
// "token": ...}). It
// returns "" when path is empty, the file is missing or unreadable, or it
// holds a token of another host: the fetch then runs without credentials, as
// for any public repository.
func ReadHostToken(path, host string) string {
	if path == "" || host == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 8<<10))
	if err != nil {
		return ""
	}
	var c struct {
		Host  string `json:"host"`
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &c) != nil || !strings.EqualFold(c.Host, host) || c.Token == "" || len(c.Token) > 512 || strings.ContainsAny(c.Token, "\x00\r\n ") {
		return ""
	}
	return c.Token
}
