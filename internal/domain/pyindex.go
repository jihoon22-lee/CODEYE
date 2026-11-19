package domain

import (
	"net/url"
	"strings"
)

// ValidPackageIndexURL reports whether v is usable as the Python package
// index of the dependency fetch (--pip-index-url, agent.dependencies
// .pip_index_url): empty (PyPI), or one http(s) URL with a host and no
// userinfo. A credential in the URL would land in the agent's arguments
// and the worker hello, where the platform keeps none.
func ValidPackageIndexURL(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > 2048 || strings.ContainsAny(v, " \t\r\n\x00") || strings.Contains(v, "@") {
		return false
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}
