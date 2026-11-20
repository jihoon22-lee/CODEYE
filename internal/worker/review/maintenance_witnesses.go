package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// maintenanceWitnesses identifies conventional maintenance tools and tests
// sharing those tools' stems. These paths get a lower contract match score
// if the contract's scope also has product source. They still participate
// in ordinary review ordering.
func (s *roleStage) maintenanceWitnesses(g codeGroups, queries []witnessQuery) (map[string]bool, error) {
	tools := map[string]bool{}
	maintenance := map[string]bool{}
	var tests []string
	for p := range g.rank {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if g.secondary[p] != "" || s.secretFiles()[p] || minifiedSource(p) {
			continue
		}
		dirs := strings.Split(path.Dir(p), "/")
		for i, d := range dirs {
			dirs[i] = strings.ToLower(d)
		}
		if windowKind(p) == "test" {
			tests = append(tests, p)
			continue
		}
		stem := maintenanceStem(p)
		if maintenanceTool(dirs, stem) {
			tools[path.Ext(p)+"\x00"+stem] = true
			maintenance[p] = true
			continue
		}
		if benchmarkPath(p) {
			continue
		}
		product := slices.ContainsFunc(dirs, func(dir string) bool {
			return slices.Contains([]string{"app", "apps", "src", "lib", "packages", "services", "crates", "cmd", "internal"}, dir)
		})
		runtimeJavaScript := record.SourceEcosystem(p) == "javascript" && !typeDeclarationPath(p)
		for i := range queries {
			if record.ScopeUnder(p, queries[i].scope) {
				queries[i].product = queries[i].product || product
				queries[i].hasRuntimeJavaScript = queries[i].hasRuntimeJavaScript || runtimeJavaScript
			}
		}
	}
	for _, p := range tests {
		if tools[path.Ext(p)+"\x00"+maintenanceStem(p)] {
			maintenance[p] = true
		}
	}
	return maintenance, nil
}

func benchmarkPath(p string) bool {
	bench := func(word string) bool {
		return slices.Contains([]string{"bench", "benches", "benchmark", "benchmarks", "benchmarker"}, word)
	}
	parts := strings.Split(strings.ToLower(p), "/")
	return slices.ContainsFunc(parts[:len(parts)-1], bench) ||
		slices.ContainsFunc(strings.FieldsFunc(maintenanceStem(p), func(r rune) bool { return r == '-' || r == '_' || r == '.' }), bench)
}

func performanceContract(statement string) bool {
	for _, word := range witnessTerms(statement) {
		switch word {
		case "benchmark", "benchmarks", "benchmarking", "performance", "latency", "throughput", "벤치마크", "성능", "처리량", "지연시간":
			return true
		}
	}
	return false
}

func typeDeclarationPath(p string) bool {
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".d.ts") || strings.HasSuffix(p, ".d.mts") || strings.HasSuffix(p, ".d.cts")
}

func mentionsJavaScriptTypes(statement string) bool {
	for _, word := range witnessTerms(statement) {
		switch word {
		case "types", "typing", "typescript", "declaration", "declarations", "typings", "타입", "타입정의", "타입선언":
			return true
		}
	}
	return false
}

func maintenanceStem(p string) string {
	stem := strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p)))
	stem = strings.TrimPrefix(stem, "test_")
	stem = strings.TrimPrefix(stem, "test-")
	for _, suffix := range []string{"_test", "-test", ".test", ".spec"} {
		stem = strings.TrimSuffix(stem, suffix)
	}
	return stem
}

func maintenanceTool(dirs []string, stem string) bool {
	switch dirs[0] {
	case "ci", "release", "releases", "deploy", "deployment", ".github":
		return true
	}
	if !slices.ContainsFunc(dirs, func(dir string) bool {
		return slices.Contains([]string{"scripts", "tools", "automation"}, dir)
	}) {
		return false
	}
	words := strings.FieldsFunc(stem, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for _, word := range words {
		switch word {
		case "release", "releases", "deploy", "deployment", "publish", "publishing", "build", "packaging", "ci", "artifact", "artifacts", "dogfood":
			return true
		}
	}
	return slices.Contains(words, "cloud") &&
		slices.ContainsFunc(words, func(word string) bool { return slices.Contains([]string{"results", "apk", "apks"}, word) })
}

func (q witnessQuery) namesMaintenance(p string) bool {
	return q.operation || q.names(p) || q.statement[witnessSeparators.Replace(maintenanceStem(p))] >= 4
}

func maintenanceOperation(statement string) bool {
	for _, word := range witnessTerms(statement) {
		switch word {
		case "ci", "cicd", "deploy", "deployment", "release", "releases", "packaging", "artifact", "artifacts", "dogfood":
			return true
		}
		for _, action := range []string{"배포", "릴리스", "패키징", "빌드", "아티팩트"} {
			if strings.HasPrefix(word, action) {
				return true
			}
		}
	}
	return false
}
