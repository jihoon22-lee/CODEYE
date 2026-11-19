package toolfind

import "testing"

// Only a registry that differs from npm's default and carries no
// credential is kept.
func TestRegistryOf(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "",
		"https://registry.npmjs.org/\n":  "",
		"https://registry.npmjs.org":     "",
		"https://npm.example/repo/\n":    "https://npm.example/repo/",
		"http://127.0.0.1:4873/":         "http://127.0.0.1:4873/",
		"https://user:tok@npm.example/":  "",
		"https://npm.example/?token=a@b": "",
		"file:///home/u/registry":        "",
		"undefined":                      "",
	} {
		if got := registryOf(in); got != want {
			t.Errorf("registryOf(%q) = %q, want %q", in, got, want)
		}
	}
}
