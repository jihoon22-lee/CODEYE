package archive

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type displayCase map[string]string

func (s displayCase) Lookup(label string) (string, error) {
	if value, ok := s[label]; ok {
		return value, nil
	}
	return "", EmptyShelf{}
}
func (s displayCase) Remove(label string) error { delete(s, label); return nil }
func TestRemoveFromDisplay(t *testing.T) {
	contents := displayCase{"novels": "M"}
	app := New(contents)
	req, err := http.NewRequest(http.MethodDelete, "/catalog/novels", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	app.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected response %d", rec.Code)
	}
	_, err = contents.Lookup("novels")
	if _, ok := err.(EmptyShelf); !ok {
		t.Fatalf("unexpected lookup error %v", err)
	}
}
