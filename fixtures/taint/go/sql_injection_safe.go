package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
)

type Store struct {
	db *sql.DB
}

func (s *Store) User(w http.ResponseWriter, r *http.Request) {
	row := s.db.QueryRow("SELECT name FROM users WHERE id = $1", r.FormValue("id"))
	var name string
	row.Scan(&name)
}

func (s *Store) Page(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.FormValue("limit"))
	if err != nil {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT name FROM users LIMIT "+strconv.Itoa(limit))
	if err == nil {
		rows.Close()
	}
}
