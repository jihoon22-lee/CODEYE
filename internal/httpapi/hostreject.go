package httpapi

import (
	"html"
	"net/http"
	"slices"
	"strings"
)

// hostRejection is what a request with a Host this service does not answer
// gets told: the address used (only when it reads as host[:port]), the
// addresses that work, and the command that adds the one used.
type hostRejection struct {
	Host     string
	Accepted []string
	Command  string
}

func (s *Server) hostRejectionOf(r *http.Request) hostRejection {
	var out hostRejection
	f := s.faceOf(r)
	if f == s.local {
		for h := range f.hosts {
			out.Accepted = append(out.Accepted, "http://"+h)
		}
		slices.Sort(out.Accepted)
	} else {
		for _, a := range s.Addresses() {
			out.Accepted = append(out.Accepted, a.Origin)
		}
	}
	if _, _, ok := splitHost(r.Host); ok {
		out.Host = strings.ToLower(r.Host)
		// A public origin needs TLS or plain HTTP; the default loopback
		// service has no command that adds a name.
		if f == s.pub && (f.tls != nil || f.plain) {
			args := []string{"codeye", "up"}
			for _, o := range s.origins {
				args = append(args, "--public-origin", o)
			}
			out.Command = strings.Join(append(args, "--public-origin", s.scheme()+"://"+out.Host), " ")
		}
	}
	return out
}

// message is the rejection in plain words (Korean, like the web app's
// default language).
func (h hostRejection) message() string {
	var b strings.Builder
	if h.Host != "" {
		b.WriteString("이 주소(" + h.Host + ")로는 이 CODEYE 서비스에 들어올 수 없습니다. ")
	} else {
		b.WriteString("요청의 주소(Host)를 알아볼 수 없어 이 CODEYE 서비스에 들어올 수 없습니다. ")
	}
	if len(h.Accepted) > 0 {
		b.WriteString("받는 주소: " + strings.Join(h.Accepted, ", ") + ". ")
	}
	if h.Command != "" {
		b.WriteString("이 주소도 받게 하려면 서비스를 띄운 머신에서 다음을 실행하세요: " + h.Command)
	}
	return strings.TrimSpace(b.String())
}

// rejectHost answers a request whose Host is not accepted: 403
// HOST_REJECTED, as JSON for the API and as a page for a browser that
// opened the address.
func (s *Server) rejectHost(w http.ResponseWriter, r *http.Request) {
	rej := s.hostRejectionOf(r)
	api := r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")
	if api || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		details := map[string]any{"accepted_origins": rej.Accepted}
		if rej.Host != "" {
			details["host"] = rej.Host
		}
		if rej.Command != "" {
			details["add_command"] = rej.Command
		}
		writeErrorDetails(w, r, http.StatusForbidden, "HOST_REJECTED", rej.message(), false, details)
		return
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"ko\"><head><meta charset=\"utf-8\"><title>CODEYE: 받지 않는 주소</title></head><body>\n")
	if rej.Host != "" {
		b.WriteString("<h1>이 주소로는 CODEYE에 들어올 수 없습니다</h1>\n<p>쓴 주소: <code>" + html.EscapeString(rej.Host) + "</code></p>\n")
	} else {
		b.WriteString("<h1>요청의 주소를 알아볼 수 없습니다</h1>\n")
	}
	if len(rej.Accepted) > 0 {
		b.WriteString("<p>이 서비스가 받는 주소:</p>\n<ul>\n")
		for _, o := range rej.Accepted {
			e := html.EscapeString(o)
			b.WriteString("<li><a href=\"" + e + "/\">" + e + "</a></li>\n")
		}
		b.WriteString("</ul>\n")
	}
	if rej.Command != "" {
		b.WriteString("<p>이 주소도 받게 하려면 서비스를 띄운 머신에서 다음을 실행하세요.</p>\n<pre>" + html.EscapeString(rej.Command) + "</pre>\n")
	}
	b.WriteString("</body></html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(b.String()))
}
