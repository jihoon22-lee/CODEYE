package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// listItems returns at most limit items; limit defaults to 10.
func listItems(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		limit = 10
	}
	items := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		items = append(items, "item-"+strconv.Itoa(i))
	}
	_ = json.NewEncoder(w).Encode(items)
}
