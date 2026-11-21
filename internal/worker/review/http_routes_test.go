package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const wateringRoutes = `package garden
import (
	"net/http"
	"strings"
)
type request struct{}
type routeRule struct { verbName string; method string; parts []string; handle func(*Garden, *request) }
func (g *Garden) rules() []routeRule {
	newRule := func(method, pattern, label string, next func(*Garden, *request)) routeRule {
		return routeRule{method: method, parts: strings.Split(strings.TrimPrefix(pattern, "/"), "/"), verbName: label, handle: next}
	}
	return []routeRule{
		newRule(http.MethodPost, "gardens/{id}/watering", "water", (*Garden).water),
	}
}
`

const wateringHandler = `package garden
type Watering interface { Run(zone string) bool }
type Garden struct { actuator Watering }
func New(a Watering) *Garden {
	g := &Garden{actuator: a}
	return g
}
func (g *Garden) water(q *request) {
	if ok := g.actuator.Run("north"); !ok { return }
}
`

func wateringTree() map[string]string {
	return map[string]string{
		"go.mod":           "module example.test/irrigation\n",
		"garden/routes.go": wateringRoutes,
		"garden/water.go":  wateringHandler,
		"valves/valve.go": `package valves
type Valve struct{ opened bool }
func Open() Valve { return Valve{} }
func (v *Valve) Run(zone string) bool {
	if zone == "" { return false }
	v.opened = true
	return true
}
`,
		"cmd/main.go": `package main
import (
	"example.test/irrigation/garden"
	"example.test/irrigation/valves"
)
func serve() {
	valve := valves.Open()
	_ = garden.New(&valve)
}
`,
	}
}

func TestGoHTTPWorkflowLinksRouteHandlerAndInjectedDecision(t *testing.T) {
	j := treeJob(t, wateringTree())
	s := roleStage{ctx: t.Context(), j: j}
	routes, err := s.workflowRoutes()
	if err != nil || len(routes) != 1 {
		t.Fatalf("routes %v, %v", routes, err)
	}
	r := routes[0]
	if r.Method != "POST" || r.Path != "gardens/{id}/watering" || r.Symbol != "water" ||
		r.Registration.Path != "garden/routes.go" || !strings.Contains(r.Registration.Text, "(*Garden).water") ||
		r.Handler.Path != "garden/water.go" || !strings.Contains(r.Handler.Text, `g.actuator.Run("north")`) ||
		r.DecisionCall.Path != r.Handler.Path || r.Decision.Path != "valves/valve.go" || !strings.Contains(r.Decision.Text, "v.opened = true") {
		t.Fatalf("route omitted a source layer: %+v", r)
	}
	windows := []record.Excerpt{r.Registration, r.Handler, r.DecisionCall, r.Decision}
	verified, err := s.withText(windows)
	if err != nil || !slices.Equal(windows, verified) {
		t.Fatalf("source windows did not round-trip: %v, %v", verified, err)
	}
}

func TestGoHTTPWorkflowTracksTypedMuxesAndGuardedCalls(t *testing.T) {
	j := treeJob(t, map[string]string{"server.go": `package server
import (
	"net/http"
	routing "github.com/gorilla/mux"
)
type Server struct { router *routing.Router }
type FakeMux struct{}
func (FakeMux) HandleFunc(string, http.HandlerFunc) {}
func (s *Server) routes() {
	http.HandleFunc("GET /watering-level", read)
	serve := http.NewServeMux()
	serve.Handle("/events", http.HandlerFunc(read))
	sub := s.router.PathPrefix("/greenhouse").Subrouter()
	sub.HandleFunc("/valves", s.list()).Methods(http.MethodGet)
	sub.PathPrefix("/area/{name}").HandlerFunc(s.list()).Methods(http.MethodHead)
	sub.HandleFunc("/conflicting-methods", s.list()).Methods(http.MethodGet).Methods(http.MethodPut)
	sub.HandleFunc("/chained-conditions", s.list()).Methods(http.MethodHead).Host("garden.example")
	other := s.router.PathPrefix("/dynamic").Subrouter()
	other = s.router
	other.HandleFunc("/ambiguous", read).Methods(http.MethodPost)
	local := FakeMux{}
	local.HandleFunc("/not-a-router", read)
}
func (s *Server) list() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" {
			return
		}
		s.send(w)
	}
}
func (s *Server) send(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) }
func read(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }
`})
	s := roleStage{ctx: t.Context(), j: j}
	routes, err := s.workflowRoutes()
	if err != nil || len(routes) != 4 {
		t.Fatalf("routes %v, %v", routes, err)
	}
	if routes[0].Method != "GET" || routes[0].Path != "/watering-level" || routes[1].Method != "" || routes[1].Path != "/events" ||
		routes[2].Path != "/greenhouse/valves" || routes[2].Method != "GET" || routes[2].PatternKind != "" ||
		routes[3].Path != "/greenhouse/area/{name}" || routes[3].Method != "HEAD" || routes[3].PatternKind != "prefix" {
		t.Fatalf("wrong static patterns: %+v", routes)
	}
	for _, r := range routes[2:] {
		if r.Symbol != "list" || !strings.Contains(r.Handler.Text, `return func(`) ||
			!strings.Contains(r.DecisionCall.Text, `if r.URL.Path == ""`) || !strings.Contains(r.DecisionCall.Text, `s.send(w)`) ||
			!strings.Contains(r.Decision.Text, `w.WriteHeader(http.StatusOK)`) {
			t.Fatalf("guard and its following call missing: %+v", r)
		}
	}
}

func TestGoHTTPWorkflowOmitsAmbiguousAndIneligibleSource(t *testing.T) {
	for _, tc := range []struct {
		name, path, add string
		edit            func(map[string]string)
		configure       func(*roleStage)
		want            int
		decision        bool
	}{
		{name: "repeated receiver method", path: "garden/duplicate.go", add: "package garden\nfunc (g *Garden) water(q *request) {}\n"},
		{name: "generated route", edit: func(files map[string]string) {
			files["garden/routes.go"] = "// Code generated by generator. DO NOT EDIT.\n" + files["garden/routes.go"]
		}},
		{name: "generated handler", edit: func(files map[string]string) {
			files["garden/water.go"] = "// Code generated by generator. DO NOT EDIT.\n" + files["garden/water.go"]
		}},
		{name: "test route", edit: func(files map[string]string) {
			files["garden/routes_test.go"] = files["garden/routes.go"]
			delete(files, "garden/routes.go")
		}},
		{name: "platform-specific route", edit: func(files map[string]string) {
			files["garden/routes_linux.go"] = files["garden/routes.go"]
			delete(files, "garden/routes.go")
		}},
		{name: "withheld route", configure: func(s *roleStage) {
			s.screen.withheld = map[string]bool{"garden/routes.go": true}
		}},
		{name: "withheld handler", configure: func(s *roleStage) {
			s.screen.withheld = map[string]bool{"garden/water.go": true}
		}},
		{name: "withheld implementation", want: 1, configure: func(s *roleStage) {
			s.screen.withheld = map[string]bool{"valves/valve.go": true}
		}},
		{name: "scope outside handler", configure: func(s *roleStage) {
			s.j.Scope = domain.Scope{Kind: "paths", Paths: []string{"garden/routes.go"}}
		}},
		{name: "scope keeps handler, drops wiring", want: 1, configure: func(s *roleStage) {
			s.j.Scope = domain.Scope{Kind: "paths", Paths: []string{"garden"}}
		}},
		{name: "PR omits unchanged handler", configure: func(s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"garden/routes.go"}}
		}},
		{name: "PR contains route and handler", want: 1, configure: func(s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"garden/routes.go", "garden/water.go"}}
		}},
		{name: "unresolved alternative injection", want: 1, path: "cmd/extra.go", add: `package main
import "example.test/irrigation/garden"
func alternate(a garden.Watering) { _ = garden.New(a) }
`},
		{name: "direct field reassignment", want: 1, path: "garden/extra.go", add: `package garden
func switchActuator(g *Garden, a Watering) { g.actuator = a }
`},
		{name: "independent composite initialization", want: 1, path: "garden/extra.go", add: `package garden
type Backup struct{}
func (*Backup) Run(string) bool { return false }
func alternate() *Garden { return &Garden{actuator: &Backup{}} }
`},
		{name: "malformed receiver", want: 1, decision: true, path: "other/malformed.go", add: "package other\nfunc () Empty() {}\n"},
		{name: "conflicting concrete injection", want: 1, path: "cmd/extra.go", add: `package main
import "example.test/irrigation/garden"
type Another struct{}
func (*Another) Run(string) bool { return true }
func alternate() { _ = garden.New(&Another{}) }
`},
		{name: "matching additional injection", want: 1, decision: true, path: "cmd/extra.go", add: `package main
import (
	"example.test/irrigation/garden"
	"example.test/irrigation/valves"
)
func also(v *valves.Valve) { _ = garden.New(v) }
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := wateringTree()
			if tc.path != "" {
				tree[tc.path] = tc.add
			}
			if tc.edit != nil {
				tc.edit(tree)
			}
			s := roleStage{ctx: t.Context(), j: treeJob(t, tree)}
			if tc.configure != nil {
				tc.configure(&s)
			}
			routes, err := s.workflowRoutes()
			if err != nil || len(routes) != tc.want || (len(routes) > 0 && (routes[0].Decision.ID != "") != tc.decision) {
				t.Fatalf("routes %v, %v", routes, err)
			}
		})
	}
}

func TestGoHTTPWorkflowBoundsRoutesAndSource(t *testing.T) {
	var entries strings.Builder
	entries.WriteString(`package greenhouse
import "net/http"
func register() {
`)
	for i := range httpRouteMax + 8 {
		fmt.Fprintf(&entries, "http.HandleFunc(\"GET /valve/%d\", show)\n", i)
	}
	entries.WriteString(`}
func show(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
`)
	j := treeJob(t, map[string]string{
		"many/routes.go":      entries.String(),
		"oversized/routes.go": "//" + strings.Repeat("x", httpRouteFileBytes) + "\n" + strings.Replace(entries.String(), "/valve/", "/other/", 1),
	})
	s := roleStage{ctx: t.Context(), j: j}
	routes, err := s.workflowRoutes()
	if err != nil || len(routes) != httpRouteMax || routes[0].Path != "/valve/0" || routes[len(routes)-1].Path != fmt.Sprintf("/valve/%d", httpRouteMax-1) {
		t.Fatalf("route bounds were not applied: %d, %v", len(routes), err)
	}
	idx, err := s.httpRouteIndex()
	if err != nil || len(idx.files) != 1 || idx.files[0].entry.Path != "many/routes.go" {
		t.Fatalf("source bounds were not applied: %v, %v", idx.files, err)
	}
}
