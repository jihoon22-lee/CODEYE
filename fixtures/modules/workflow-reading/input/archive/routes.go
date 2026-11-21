package archive

import "net/http"

type shelf interface {
	Lookup(string) (string, error)
	Remove(string) error
}
type EmptyShelf struct{}

func (EmptyShelf) Error() string { return "empty shelf" }

type Server struct {
	shelf  shelf
	router *http.ServeMux
}

func New(dependency shelf) Server {
	s := Server{shelf: dependency, router: http.NewServeMux()}
	s.router.HandleFunc("GET /browse/{label}", s.view)
	s.router.HandleFunc("GET /legacy/{label}", s.view)
	s.router.HandleFunc("DELETE /catalog/{label}", s.remove)
	return s
}
func (s Server) Router() *http.ServeMux { return s.router }
func (s Server) view(w http.ResponseWriter, r *http.Request) {
	label := r.PathValue("label")
	value, err := s.shelf.Lookup(label)
	if _, ok := err.(EmptyShelf); ok {
		http.Error(w, "empty shelf", http.StatusNotFound)
		return
	}
	w.Write([]byte(value))
}
func (s Server) remove(w http.ResponseWriter, r *http.Request) {
	err := s.shelf.Remove(r.PathValue("label"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
