package api

import (
	"encoding/json"
	"net/http"
)

func Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /cups/{cup}", lower)
	mux.HandleFunc("GET /cups", list)
	return mux
}

func lower(w http.ResponseWriter, r *http.Request) {
	removeFromShelf(r.PathValue("cup"))
	w.WriteHeader(http.StatusNoContent)
}

func list(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(cupsOnShelf())
}
