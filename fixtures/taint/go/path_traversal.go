package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const root = "/srv/files"

func Read(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(root, r.URL.Query().Get("name"))) // expect: codeye.go.path-traversal
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write(data)
}

func Upload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	f, err := os.Create(filepath.Join(root, name)) // expect: codeye.go.path-traversal
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(f, r.Body)
}
