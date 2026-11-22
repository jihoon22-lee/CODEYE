package record

import "testing"

// The prefilter must agree with the existing credential detector even on
// unusual whitespace, case folds, prefix boundaries and malformed UTF-8.
func FuzzSensitivePrefilter(f *testing.F) {
	for _, candidate := range []string{
		"", "package main\nfunc main() {}", "-----begin rsa private key-----", "-----BEGIN PRIVATE KEY-----",
		"sk-0123456789ab", "prefixSK-0123456789ab", "ſK-0123456789ab", "not_sk-0123456789ab", "x\xffsk-0123456789ab",
		"ghP_0123456789abcdef", "ghſ_0123456789abcdef", "xghp_0123456789abcdef",
		"authorization \r\n\t = 'BeArEr 01234567'", "AUTHORIZATION: Basic 01234567", "authorization\v: basic 01234567",
		"client_ſecret : 'abcdef0123456789'", "PaSsWoRd\t=\n 'abcdef0123456789'",
		"API-key='abcdef0123456789'", "api_Key='abcdef0123456789'", "APIKEY='abcdef0123456789'",
		"api__key='abcdef0123456789'", "publicKey=valid", "password=short",
	} {
		f.Add([]byte(candidate))
	}
	f.Fuzz(func(t *testing.T, content []byte) {
		if got, want := Sensitive(content), sensitive.Match(content); got != want {
			t.Fatalf("prefilter differs from regexp for %q: got %v, want %v", content, got, want)
		}
	})
}
