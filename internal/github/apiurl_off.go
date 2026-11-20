//go:build !codeye_fixture

package github

// APIBase is the REST API the service calls: always github.com's. Only
// test builds (apiurl_on.go) may point it elsewhere, so no environment
// variable can send the token to another host.
func APIBase() string { return DefaultAPIURL }
