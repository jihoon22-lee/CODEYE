package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/mux"
	"github.com/labstack/echo/v4"
)

type deleteRequest struct {
	IDs []int `json:"ids"`
}

func DeleteThumbnails(w http.ResponseWriter, r *http.Request) {
	var in deleteRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, id := range in.IDs {
		os.Remove(filepath.Join(root, "thumb", strconv.Itoa(id)))
	}
}

type archiveRequest struct {
	ID uint64 `json:"id"`
}

func DeleteAsset(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join(root, mux.Vars(r)["name"])
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	os.Remove(p)
}

func Probe(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(chi.URLParam(r, "port"))
	exec.Command("sh", "-c", "nc -z localhost "+strconv.Itoa(port)).Run()
	n, _ := strconv.ParseInt(chi.URLParamFromCtx(r.Context(), "n"), 10, 64)
	os.Remove(filepath.Join(root, "restore", strconv.FormatInt(n, 10)))
}

func Archive(c *gin.Context) {
	var in archiveRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		return
	}
	os.Remove(filepath.Join(root, "archive", strconv.FormatUint(in.ID, 10)))
}

func Import(c echo.Context) error {
	var in archiveRequest
	if err := c.Bind(&in); err != nil {
		return err
	}
	return os.Remove(filepath.Join(root, "import", strconv.FormatUint(in.ID, 10)))
}

type tuneRequest struct {
	Delay float64 `json:"delay"`
	Fast  bool    `json:"fast"`
}

func Tune(w http.ResponseWriter, r *http.Request) {
	var in tuneRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return
	}
	exec.Command("sh", "-c", "sleep "+strconv.FormatFloat(in.Delay, 'f', -1, 64)+" && tune --fast="+strconv.FormatBool(in.Fast)).Run()
}

func Settings(w http.ResponseWriter, r *http.Request) {
	var in deleteRequest
	in.IDs = []int{1}
	os.Remove(filepath.Join(root, "settings.json"))
}
