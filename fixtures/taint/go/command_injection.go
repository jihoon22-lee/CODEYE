package handlers

import (
	"net/http"
	"os/exec"

	"github.com/gin-gonic/gin"
)

func Ping(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	out, _ := exec.Command("sh", "-c", "ping -c 1 "+host).Output() // expect: codeye.go.command-injection
	w.Write(out)
}

func runTool(name string) error {
	return exec.Command(name, "--version").Run() // expect: codeye.go.command-injection
}

func Tool(c *gin.Context) {
	_ = runTool(c.Query("tool"))
}
