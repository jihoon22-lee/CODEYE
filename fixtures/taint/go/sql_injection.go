package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
)

type Store struct {
	db *sql.DB
}

func (s *Store) User(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	row := s.db.QueryRow("SELECT name FROM users WHERE id = '" + id + "'") // expect: codeye.go.sql-injection
	var name string
	row.Scan(&name)
}

func (s *Store) Sorted(w http.ResponseWriter, r *http.Request) {
	q := fmt.Sprintf("SELECT name FROM users ORDER BY %s", r.URL.Query().Get("sort"))
	rows, err := s.db.QueryContext(r.Context(), q) // expect: codeye.go.sql-injection
	if err == nil {
		rows.Close()
	}
}
