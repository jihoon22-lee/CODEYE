package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type deleteRequest struct {
	IDs []int `json:"ids"`
}

func DeleteThumbnails(w http.ResponseWriter, r *http.Request) {
	var in deleteRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, id := range in.IDs {
		os.Remove(filepath.Join(root, "thumb", strconv.Itoa(id)))
	}
}

func Settings(w http.ResponseWriter, r *http.Request) {
	var in deleteRequest
	in.IDs = []int{1}
	os.Remove(filepath.Join(root, "settings.json"))
}
