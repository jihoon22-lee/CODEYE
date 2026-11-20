package source

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/github"
)

// ReadHostToken returns the credential bound to this fetch's exact host.
// Manually configured agents may also use a single-host token file.
func ReadHostToken(path, host string) string {
	if path == "" || host == "" {
		return ""
	}
	if creds, err := github.ReadPlatformCredentials(path); err == nil {
		return creds[host].Token
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
