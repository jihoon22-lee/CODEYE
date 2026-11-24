package handlers

import (
	"fmt"
	"html/template"
	"net/http"
)

var page = template.Must(template.New("p").Parse(`<div>{{.}}</div>`))

func Hello(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "<h1>Hello %s</h1>", r.FormValue("name")) // expect: codeye.go.html-injection
}

func Note(w http.ResponseWriter, r *http.Request) {
	page.Execute(w, template.HTML(r.FormValue("note"))) // expect: codeye.go.html-injection
}
