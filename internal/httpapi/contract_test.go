package httpapi

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// TestOpenAPIImplementedOperationsMatchRoutes keeps api/openapi.yaml honest:
// exactly the operations marked x-codeye-status: implemented-w03 or
// implemented-w06 are routed.
func TestOpenAPIImplementedOperationsMatchRoutes(t *testing.T) {
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pathLine := regexp.MustCompile(`^  (/api/v1/\S*):\s*$`)
	methodLine := regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	param := regexp.MustCompile(`\{[^}]+\}`)
	var path, method string
	documented := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := pathLine.FindStringSubmatch(line); m != nil {
			path, method = param.ReplaceAllString(m[1], "{id}"), ""
		} else if m := methodLine.FindStringSubmatch(line); m != nil {
			method = strings.ToUpper(m[1])
		} else if st := strings.TrimSpace(line); (st == "x-codeye-status: implemented-w03" || st == "x-codeye-status: implemented-w06") && path != "" && method != "" {
			documented[method+" "+path] = true
		}
	}
	srv, err := New(Config{Listen: mustLoopback(t)}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	routed := map[string]bool{}
	for _, rt := range srv.routes {
		routed[rt.method+" "+apiPrefix+"/"+strings.Join(rt.parts, "/")] = true
	}
	var missing, extra []string
	for k := range routed {
		if !documented[k] {
			missing = append(missing, k)
		}
	}
	for k := range documented {
		if !routed[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 {
		t.Fatalf("routes not marked implemented in OpenAPI: %v; marked but not routed: %v", missing, extra)
	}
}
