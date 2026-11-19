package main

import (
	"net/http/httptest"
	"testing"
)

func TestListItemsDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	listItems(rec, httptest.NewRequest("GET", "/items", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}
