package handlers

import (
	"net/http"
	"os/exec"
	"strconv"
)

func Ping(w http.ResponseWriter, r *http.Request) {
	// The host is one argument of a fixed program, not shell text.
	out, _ := exec.Command("ping", "-c", "1", "--", r.FormValue("host")).Output()
	w.Write(out)
}

func Count(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.FormValue("n"))
	if err != nil {
		return
	}
	out, _ := exec.Command("sh", "-c", "seq "+strconv.Itoa(n)).Output()
	w.Write(out)
}
