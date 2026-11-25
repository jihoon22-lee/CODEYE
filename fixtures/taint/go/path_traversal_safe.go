package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const root = "/srv/files"

func Read(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(root, filepath.Base(r.FormValue("name"))))
	if err != nil {
		return
	}
	w.Write(data)
}

func Download(w http.ResponseWriter, r *http.Request) {
	full := filepath.Join(root, r.FormValue("file"))
	if !strings.HasPrefix(full, root+string(filepath.Separator)) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	data, _ := os.ReadFile(full)
	w.Write(data)
}

func Index(w http.ResponseWriter, r *http.Request) {
	data, _ := os.ReadFile(filepath.Join(root, "index.txt"))
	w.Write(data)
}
