package domain

import (
	"net/url"
	"strings"
)

// GoSettingCarriesCredentials reports whether a Go download setting
// (GOPROXY's comma- or pipe-separated list, or GOSUMDB's "name+key URL")
// names a URL with userinfo, such as https://user:token@proxy.example.
// Such a value is never carried into configuration, flags, the worker
// hello or a report: it would put a credential where the platform keeps
// none (docs/ANALYZERS.md#go-의존성-받기).
func GoSettingCarriesCredentials(value string) bool {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '|' || r == ' ' || r == '\t' }) {
		if !strings.Contains(part, "@") {
			continue
		}
		u, err := url.Parse(part)
		if err != nil || u.User != nil || !strings.Contains(part, "://") {
			// An unparsable entry with "@", or one without a scheme
			// (user:token@host), is treated as a credential too.
			return true
		}
	}
	return false
}
