package discovery

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestParseManifests(t *testing.T) {
	cases := []struct {
		path, body string
		want       []declared
		malformed  bool
		noParser   bool
	}{
		{path: "go.mod", body: "module x // go 1.1\r\n\r\ngo 1.22\r\n\r\ntoolchain go1.31.0 // preferred\r\n",
			want: []declared{{Kind: "go_directive", Constraint: "1.22", Start: 3, End: 3}, {Kind: "go_toolchain", Constraint: "go1.31.0", Start: 5, End: 5}}},
		{path: "go.mod", body: "module x\n\nrequire (\n\tgo.uber.org/zap v1.0.0\n\tgo 1.1\n)\n\ngo 1.21\n",
			want: []declared{{Kind: "go_directive", Constraint: "1.21", Start: 8, End: 8}}},
		{path: "go.mod", body: "module x\ngo 1.22\ngo 1.23\n", malformed: true},
		{path: "go.mod", body: "module x\ngo\n", malformed: true},
		{path: "go.mod", body: "module x\n", want: nil},
		{path: "a/go.work", body: "go 1.23.1\n\nuse ./a\n",
			want: []declared{{Kind: "go_work_directive", Constraint: "1.23.1", Start: 1, End: 1}}},
		{path: "package.json", body: "{\n  \"name\": \"x\",\n  \"scripts\": {\"node\": \"evil\"},\n  \"engines\": {\n    \"npm\": \">=9\",\n    \"node\": \">=18.0.0\"\n  }\n}\n",
			want: []declared{{Kind: "engines_node", Constraint: ">=18.0.0", Start: 6, End: 6}}},
		{path: "package.json", body: `{"engines":{"node":18}}`, malformed: true},
		{path: "package.json", body: `{"engines":["node"],"config":{"engines":{"node":"1"}}}`, want: nil},
		{path: "package.json", body: `{"engines":{"node":">=18"},"engines":{"node":">=20"}}`, malformed: true},
		{path: "package.json", body: `{"engines":{"node":">=18"}`, malformed: true},
		{path: "package.json", body: `[1]`, malformed: true},
		{path: "pyproject.toml", body: "[tool.x]\nrequires-python = \">=2\"\n\n[project]\nname = \"x\"\nrequires-python = \">=3.10\" # comment\n",
			want: []declared{{Kind: "requires_python", Constraint: ">=3.10", Start: 6, End: 6}}},
		{path: "pyproject.toml", body: "[project]\nrequires-python = '>=3.9'\n", want: []declared{{Kind: "requires_python", Constraint: ">=3.9", Start: 2, End: 2}}},
		{path: "pyproject.toml", body: "[project]\nrequires-python = \"\"\"\n>=3.9\n\"\"\"\n", malformed: true},
		{path: "pyproject.toml", body: "[project]\nrequires-python = \">=3.9\n", malformed: true},
		{path: "pyproject.toml", body: "[project\nrequires-python = \">=3.9\"\n", malformed: true},
		{path: "setup.cfg", body: "[metadata]\nname = x\n\n[options]\npython_requires = >=3.8\n",
			want: []declared{{Kind: "python_requires", Constraint: ">=3.8", Start: 5, End: 5}}},
		{path: "setup.cfg", body: "[options]\npython_requires: >=3.9\n", want: []declared{{Kind: "python_requires", Constraint: ">=3.9", Start: 2, End: 2}}},
		{path: "pyproject.toml", body: "[project]\nrequires-python = \"api_key = 'abcdefghijklmnop'\"\n", malformed: true},
		{path: "x/.python-version", body: "# pinned\n3.11.4\n3.10.13\n",
			want: []declared{{Kind: "python_version_file", Constraint: "3.11.4", Start: 2, End: 2, Limitations: []string{LimExtraPythonVersions}}}},
		{path: "Cargo.toml", body: "[workspace.package]\nrust-version = \"1.60\"\n\n[package]\nname = \"x\"\nrust-version = \"1.70\"\n",
			want: []declared{{Kind: "rust_version", Constraint: "1.70", Start: 6, End: 6}}},
		{path: "Cargo.toml", body: "[package]\nname = \"x\"\nmatrix = [\n  [1, 2],\n  [3, 4]\n]\nrust-version = \"1.74\" # msrv\n\n[target.'cfg(unix)'.dependencies]\nlibc = \"0.2\"\n\n[profile.dev.package.\"*\"]\nopt-level = 1\n\n[[bin]]\nname = \"x\"\nrust-version = \"9\"\n",
			want: []declared{{Kind: "rust_version", Constraint: "1.74", Start: 7, End: 7}}},
		{path: "Cargo.toml", body: "[package\nrust-version = \"1.70\"\n", malformed: true},
		{path: "Cargo.toml", body: "[package]\nrust-version.workspace = true\n",
			want: []declared{{Kind: "rust_version", Constraint: "workspace", Start: 2, End: 2, Limitations: []string{LimWorkspaceInheritance}}}},
		{path: "rust-toolchain", body: "\n1.75.0\n", want: []declared{{Kind: "rust_toolchain_channel", Constraint: "1.75.0", Start: 2, End: 2}}},
		{path: "rust-toolchain.toml", body: "[toolchain]\nchannel = \"stable\"\ncomponents = [\"clippy\"]\n",
			want: []declared{{Kind: "rust_toolchain_channel", Constraint: "stable", Start: 2, End: 2}}},
		{path: "CMakeLists.txt", body: "# cmake_minimum_required(VERSION 2.8)\nCMAKE_MINIMUM_REQUIRED(\n  VERSION 3.10...3.28 # range\n  FATAL_ERROR)\nproject(x)\n",
			want: []declared{{Kind: "cmake_minimum_required", Constraint: "3.10...3.28", Start: 2, End: 4}}},
		{path: "CMakeLists.txt", body: "cmake_minimum_required(3.20)\n", malformed: true},
		{path: "CMakeLists.txt", body: "cmake_minimum_required(VERSION 3.20\n", malformed: true},
		{path: "Makefile", body: "all:\n\tcc x.c\n", noParser: true},
		{path: "requirements-dev.txt", body: "pytest\n", noParser: true},
	}
	for _, c := range cases {
		got, ok, err := parseManifest(c.path, []byte(c.body))
		if ok == c.noParser || (err != nil) != c.malformed {
			t.Fatalf("%s %q: ok=%v err=%v", c.path, c.body, ok, err)
		}
		if !c.malformed && !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s %q:\n got %+v\nwant %+v", c.path, c.body, got, c.want)
		}
	}
}

// Arbitrary bytes never crash a parser: malformed input is a limitation.
func TestParseManifestsNeverPanic(t *testing.T) {
	r := rand.New(rand.NewSource(15))
	alphabet := []byte("go toolchain[]{}\"'=:#()\n\r\\.1239 engines node project requires-python VERSION cmake_minimum_required package rust-version")
	for _, p := range []string{"go.mod", "go.work", "package.json", "pyproject.toml", "setup.cfg", ".python-version", "Cargo.toml", "rust-toolchain", "rust-toolchain.toml", "CMakeLists.txt"} {
		for i := 0; i < 2000; i++ {
			b := make([]byte, r.Intn(120))
			for j := range b {
				b[j] = alphabet[r.Intn(len(alphabet))]
			}
			reqs, _, err := parseManifest(p, b)
			for _, d := range reqs {
				if err == nil && (d.Start < 1 || d.End < d.Start) {
					t.Fatalf("%s %q: bad range %+v", p, b, d)
				}
			}
		}
	}
}
