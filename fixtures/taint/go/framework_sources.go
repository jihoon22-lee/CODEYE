package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/mux"
	"github.com/labstack/echo/v4"
)

type exportRequest struct {
	Name string `json:"name"`
}

func Export(w http.ResponseWriter, r *http.Request) {
	var in exportRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	os.WriteFile(filepath.Join(root, in.Name), nil, 0o600) // expect: codeye.go.path-traversal
}

func DeleteAsset(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	os.Remove(filepath.Join(root, name)) // expect: codeye.go.path-traversal
}

func Probe(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	exec.Command("sh", "-c", "ping -c 1 "+host).Run() // expect: codeye.go.command-injection
}

func Archive(c *gin.Context) {
	var in exportRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		return
	}
	os.Remove(filepath.Join(root, "archive", in.Name)) // expect: codeye.go.path-traversal
}

func Restore(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParamFromCtx(r.Context(), "name")
	os.Remove(filepath.Join(root, "restore", name)) // expect: codeye.go.path-traversal
}

func Import(c echo.Context) error {
	var in exportRequest
	if err := c.Bind(&in); err != nil {
		return err
	}
	return os.Remove(filepath.Join(root, "import", in.Name)) // expect: codeye.go.path-traversal
}
