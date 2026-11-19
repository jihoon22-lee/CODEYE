package main

import (
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/items", listItems)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
