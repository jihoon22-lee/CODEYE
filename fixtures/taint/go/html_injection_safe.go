package handlers

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
)

var page = template.Must(template.New("p").Parse(`<div>{{.}}</div>`))

func Hello(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "<h1>Hello %s</h1>", html.EscapeString(r.FormValue("name")))
}

func Note(w http.ResponseWriter, r *http.Request) {
	// html/template escapes the value.
	page.Execute(w, r.FormValue("note"))
}

func Plain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "hello %s\n", r.FormValue("name"))
}
