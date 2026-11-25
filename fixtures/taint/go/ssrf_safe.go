package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
)

func User(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get("https://api.example.com/users/" + url.PathEscape(r.FormValue("id")))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	io.Copy(w, resp.Body)
}

func Repo(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get(fmt.Sprintf("https://api.example.com/repos/%s", r.FormValue("name")))
	if err != nil {
		return
	}
	resp.Body.Close()
}

func Health(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get("https://status.example.com/health")
	if err == nil {
		resp.Body.Close()
	}
}
