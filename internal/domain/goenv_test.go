package domain

import "testing"

func TestGoSettingCarriesCredentials(t *testing.T) {
	for value, want := range map[string]bool{
		"":                                        false,
		"https://proxy.golang.org,direct":         false,
		"off":                                     false,
		"https://goproxy.example/@v/list":         false,
		"https://user:tok@goproxy.example":        true,
		"https://a.example|https://u@b.example":   true,
		"user:tok@goproxy.example":                true,
		"sum.golang.org":                          false,
		"sum.example+abc https://u:p@sum.example": true,
	} {
		if got := GoSettingCarriesCredentials(value); got != want {
			t.Errorf("%q = %v, want %v", value, got, want)
		}
	}
}
