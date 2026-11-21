package main

import (
	"log"
	"net/http"

	"example.test/shelf/api"
)

func main() { log.Fatal(http.ListenAndServe("127.0.0.1:8080", api.Routes())) }
