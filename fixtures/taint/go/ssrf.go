package handlers

import (
	"io"
	"net/http"
)

var client = &http.Client{}

func Proxy(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get(r.URL.Query().Get("url")) // expect: codeye.go.ssrf
	if err != nil {
		return
	}
	defer resp.Body.Close()
	io.Copy(w, resp.Body)
}

func Hook(w http.ResponseWriter, r *http.Request) {
	target := "https://" + r.FormValue("host") + "/hook"
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, target, nil) // expect: codeye.go.ssrf
	client.Do(req)
}
