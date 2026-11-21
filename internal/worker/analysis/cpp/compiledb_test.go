package cpp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func snapshotFiles(paths ...string) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, p := range paths {
		out[p] = source.Entry{Path: p, Kind: "file", SHA256: strings.Repeat("a", 64)}
	}
	return out
}

func all(string) bool { return true }

// absWS is an absolute workspace path on the test OS ("/ws/..." on Unix,
// `C:\ws\...` on Windows, where a rooted path without a volume is not
// absolute).
func absWS(elem ...string) string {
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = `C:\`
	}
	return filepath.Join(append([]string{root, "ws"}, elem...)...)
}

func mapOne(t *testing.T, db string, files map[string]source.Entry, scope func(string) bool) Mapping {
	t.Helper()
	m, err := MapDBs([]string{CompileDBName}, map[string][]byte{CompileDBName: []byte(db)}, files, scope)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	return m
}

func rendered(t *testing.T, m Mapping, ws string) [][]string {
	t.Helper()
	var out [][]string
	for _, u := range m.Units {
		out = append(out, u.Render(ws))
	}
	return out
}

// TC-105: the compile DB of another machine is data. Its directory, file and
// include paths are mapped onto the workspace through the inferred original
// root; compiler wrappers, code-loading flags, response files and every flag
// outside the allowlist never reach the CODEYE database; nothing of the
// entry is executed (the driver is a basename that only names the language
// mode).
func TestCompileDBMapsPathsAndDropsCodeLoading(t *testing.T) {
	files := snapshotFiles("src/a.c", "src/b.cpp", "include/a.h", "flags.rsp", "plugin.so")
	db := `[
{"directory": "/build/p/_build", "command": "/usr/bin/ccache /usr/bin/cc -DX=1 -D Y -UZ -I/build/p/include -I ../include -isystem /usr/include/foo -iquote /build/p/missing -include /build/p/include/a.h -O2 -g -Wall -Werror -std=gnu11 -fplugin=/build/p/plugin.so -fsanitize=address -MD -MF a.d -o a.o -c /build/p/src/a.c", "file": "/build/p/src/a.c"},
{"directory": "/build/p/_build", "arguments": ["sccache", "clang++", "-Xclang", "-load", "-Xclang", "/build/p/plugin.so", "@/build/p/flags.rsp", "-fpass-plugin=x.so", "--config=/build/p/x.cfg", "--sysroot=/opt/sdk", "-B/opt/sdk/bin", "-mllvm", "-inline-threshold=1", "-Wl,-rpath,/x", "-x", "c++", "--std=c++20", "-fno-exceptions", "-march=native", "-c", "../src/b.cpp"], "file": "../src/b.cpp"}
]`
	m := mapOne(t, db, files, all)
	if len(m.Units) != 2 || m.Units[0].Rel != "src/a.c" || m.Units[1].Rel != "src/b.cpp" || m.Units[0].Driver != "cc" || m.Units[1].Driver != "clang++" {
		t.Fatalf("units %+v", m.Units)
	}
	ws := absWS("attempt")
	got := rendered(t, m, ws)
	want := [][]string{
		{"clang", "-DX=1", "-DY", "-UZ", "-I" + filepath.Join(ws, "include"), "-I" + filepath.Join(ws, "include"), "-iquote" + filepath.Join(ws, "missing"),
			"-include", filepath.Join(ws, "include", "a.h"), "-O2", "-std=gnu11", filepath.Join(ws, "src", "a.c")},
		{"clang++", "-x", "c++", "-std=c++20", "-fno-exceptions", "-march=native", filepath.Join(ws, "src", "b.cpp")},
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("unit %d argv\n got %q\nwant %q", i, got[i], want[i])
		}
	}
	joined := strings.Join(append(got[0], got[1]...), " ")
	for _, bad := range []string{"plugin", "-load", "@", "ccache", "sccache", "sanitize", "--config", "sysroot", "-B", "mllvm", "-Wl", "/usr/", "/build/", "-Werror", "-MF", "-o"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("%q reached the CODEYE database: %s", bad, joined)
		}
	}
	wantDropped := map[string]int{DropCodeLoading: 4, DropResponseFile: 1, DropDriverConfig: 1, DropToolchainPath: 2, DropBackendOption: 1, DropPassthrough: 1,
		DropIncludeOutside: 1, DropInstrumentation: 1}
	for k, v := range wantDropped {
		if m.Dropped[k] != v {
			t.Fatalf("dropped %s = %d, want %d (%v)", k, m.Dropped[k], v, m.Dropped)
		}
	}
	if m.Wrappers != 2 || m.DirsMissing != 1 || m.FilesMissing != 0 || m.RootUnknown || m.Total != 2 || !slices.Equal(m.Drivers, []string{"cc", "clang++"}) {
		t.Fatalf("mapping %+v", m)
	}
	lims := m.Limitations()
	for _, l := range []string{LimCodeLoadingDropped, LimResponseFiles, LimIncludeOutside, LimFlagsDropped, LimIncludeDirsMissing, LimWrappersStripped} {
		if !slices.Contains(lims, l) {
			t.Fatalf("limitation %s missing: %v", l, lims)
		}
	}
	// Structural, warning and linker flags are counted but not reported.
	if txt := m.DroppedText(); strings.Contains(txt, "structural") || strings.Contains(txt, "warning") || !strings.Contains(txt, "code_loading:4") {
		t.Fatalf("dropped text %q", txt)
	}
	if Std(m.Units) != "c++20,gnu11" {
		t.Fatalf("std %q", Std(m.Units))
	}
	b, err := RenderDB(ws, m.Units)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(b, &entries); err != nil || len(entries) != 2 || entries[0]["directory"] != ws || entries[0]["file"] != filepath.Join(ws, "src", "a.c") {
		t.Fatalf("rendered DB %s (%v)", b, err)
	}
	if _, has := entries[0]["command"]; has {
		t.Fatalf("rendered DB has a command string: %s", b)
	}
}

// TC-105: entries are excluded with a reason and counted; nothing is
// guessed for them. A source whose every entry is excluded is recorded with
// the first reason; a duplicate entry keeps the first one.
func TestCompileDBExclusions(t *testing.T) {
	files := snapshotFiles("src/a.c", "src/a.h", "src/gen.py", "vendor/v.c", "src/only_excluded.c", "src/out.c")
	entry := func(dir, file, cmd string) string {
		b, _ := json.Marshal(map[string]string{"directory": dir, "file": file, "command": cmd})
		return string(b)
	}
	db := "[" + strings.Join([]string{
		entry("/r", "src/a.c", "cc -c src/a.c"),
		entry("/r", "src/a.c", "cc -DSECOND -c src/a.c"),
		entry("/r", "/elsewhere/x.c", "cc -c /elsewhere/x.c"),
		entry("/r", "src/missing.c", "cc -c src/missing.c"),
		entry("/r", "src/a.h", "cc -c src/a.h"),
		entry("/r", "src/gen.py", "cc -c src/gen.py"),
		entry("/r", "vendor/v.c", "cc -c vendor/v.c"),
		entry("relative", "src/a.c", "cc -c src/a.c"),
		entry("/r", "src/only_excluded.c", "cl.exe /c src/only_excluded.c"),
		entry("/r", "src/only_excluded.c", "nvcc -c src/only_excluded.c"),
		entry("/r", "src/out.c", "cc -c src/out.c"),
		`{"directory": "/r", "file": "src/a.c"}`,
		`{"directory": 7, "file": "src/a.c", "command": "cc"}`,
		entry("/r", "src/a.c", "cc 'unterminated"),
	}, ",") + "]"
	m := mapOne(t, db, files, func(p string) bool { return p != "src/out.c" })
	want := map[string]int{ExclDuplicate: 1, ExclOutsideRoot: 1, ExclNotInSnapshot: 1, ExclHeader: 1, ExclNotCpp: 1, ExclDataDir: 1, ExclDirectory: 1,
		ExclMSVCDriver: 1, ExclUnsupportedDrive: 1, ExclOutOfScope: 1, ExclMalformed: 3}
	for k, v := range want {
		if m.Excluded[k] != v {
			t.Fatalf("excluded %s = %d, want %d: %v", k, m.Excluded[k], v, m.Excluded)
		}
	}
	if len(m.Units) != 1 || m.Units[0].Rel != "src/a.c" || slices.ContainsFunc(m.Units[0].Args, func(a Arg) bool { return a.Flag == "-DSECOND" }) {
		t.Fatalf("units %+v", m.Units)
	}
	if m.ExcludedSources["src/only_excluded.c"] != ExclMSVCDriver || len(m.ExcludedSources) != 1 {
		t.Fatalf("excluded sources %v", m.ExcludedSources)
	}
	lims := m.Limitations()
	if !slices.Contains(lims, LimEntriesExcluded) || !slices.Contains(lims, LimDuplicateEntries) {
		t.Fatalf("limitations %v", lims)
	}
}

func TestCompileDBRootIncludeDirectories(t *testing.T) {
	files := snapshotFiles("src/service.c", "src/worker.c", "src/rejected.c", "include/config.h", "build/local.h")
	db := `[
{"directory":"/build/project", "file":"src/service.c", "arguments":["cc","-I.","-iquote",".","-isystem","/build/project","-idirafter","./","-I","include","-I../outside","-I","/usr/include","-I","","-I=","-Ibad\u0000/..","-Ibad\t/..","-Ibad\n/..","-c","src/service.c"]},
{"directory":"/build/project/build", "file":"../src/worker.c", "arguments":["cc","-I",".","-iquote","..","-c","../src/worker.c"]},
{"directory":"/build/project", "file":"src/rejected.c", "arguments":["cc","-include",".","-c","src/rejected.c"]},
{"directory":"/build/project", "file":".", "arguments":["cc","-I.","-c","."]}
]`
	m := mapOne(t, db, files, all)
	if len(m.Units) != 2 || m.Excluded[ExclUnsafeArgument] != 1 || m.Excluded[ExclOutsideRoot] != 1 ||
		m.Dropped[DropIncludeOutside] != 2 || m.Dropped[DropPositional] != 5 || m.DirsMissing != 0 {
		t.Fatalf("unexpected mapping %+v", m)
	}
	ws := absWS("scratch", "project")
	renderedDB, err := RenderDB(ws, m.Units)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Arguments []string `json:"arguments"`
	}
	if err := json.Unmarshal(renderedDB, &entries); err != nil {
		t.Fatal(err)
	}
	wants := [][]string{
		{"clang", "-I" + ws, "-iquote" + ws, "-isystem" + ws, "-idirafter" + ws,
			"-I" + filepath.Join(ws, "include"), filepath.Join(ws, "src", "service.c")},
		{"clang", "-I" + filepath.Join(ws, "build"), "-iquote" + ws, filepath.Join(ws, "src", "worker.c")},
	}
	for i, ent := range entries {
		if ent.Directory != ws || ent.File != wants[i][len(wants[i])-1] || !slices.Equal(ent.Arguments, wants[i]) {
			t.Fatalf("unit %d rendered outside the working copy or with changed arguments: %+v", i, ent)
		}
	}
}

// The original root is the prefix that maps the most entry files onto the
// snapshot; a DB that matches nothing has no root and excludes every entry.
// Windows build paths are slash-normalized.
func TestCompileDBRootInference(t *testing.T) {
	files := snapshotFiles("lib/x.c", "lib/y.c", "app/main.c")
	db := `[
{"directory": "C:\\src\\proj\\build", "command": "cc -IC:\\src\\proj\\lib -c ..\\lib\\x.c", "file": "C:\\src\\proj\\lib\\x.c"},
{"directory": "C:\\src\\proj\\build", "arguments": ["cc", "-c", "C:/src/proj/lib/y.c"], "file": "C:/src/proj/lib/y.c"},
{"directory": "C:\\src\\proj\\build", "arguments": ["cc", "-c", "C:/src/proj/app/main.c"], "file": "C:/src/proj/app/main.c"}
]`
	m := mapOne(t, db, files, all)
	if len(m.Units) != 3 || m.RootUnknown || m.Roots != 1 {
		t.Fatalf("windows DB %+v", m)
	}
	if got := m.Units[1].Render("/w"); !slices.Contains(got, "-I"+filepath.Join("/w", "lib")) {
		t.Fatalf("windows include not mapped: %q", got)
	}
	none := mapOne(t, `[{"directory": "/x", "file": "/x/other.c", "arguments": ["cc", "-c", "/x/other.c"]}]`, files, all)
	if len(none.Units) != 0 || !none.RootUnknown || !slices.Contains(none.Limitations(), LimNoRootInferred) {
		t.Fatalf("no root %+v", none)
	}
	if r := inferRoot([]string{"/a/b/lib/x.c", "/a/b/lib/y.c"}, files); r != "/a/b" {
		t.Fatalf("root %q", r)
	}
}

// A DB that is not a JSON array of objects fails the mapping; nothing is
// guessed from it.
func TestCompileDBUnreadable(t *testing.T) {
	for _, db := range []string{`{"directory": "/x"}`, `[{"directory": "/x"}`, `[] []`, "", `[1, 2]`} {
		m, err := MapDBs([]string{CompileDBName}, map[string][]byte{CompileDBName: []byte(db)}, snapshotFiles("a.c"), all)
		if db == `[1, 2]` {
			if err != nil || m.Excluded[ExclMalformed] != 2 {
				t.Fatalf("%q: %v %+v", db, err, m)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%q mapped: %+v", db, m)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	for in, want := range map[string][]string{
		`cc -DA="x y" -c 'a b.c'`:    {"cc", "-DA=x y", "-c", "a b.c"},
		`cc -DQ=\"q\" -I/x\ y`:       {"cc", `-DQ="q"`, "-I/x y"},
		"cc\t-c  a.c\n":              {"cc", "-c", "a.c"},
		`cc "-DP=\"/opt/a\"" -c a.c`: {"cc", `-DP="/opt/a"`, "-c", "a.c"},
	} {
		if got, ok := splitCommand(in); !ok || !slices.Equal(got, want) {
			t.Fatalf("%q: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{`cc "open`, `cc 'open`, `cc \`} {
		if _, ok := splitCommand(bad); ok {
			t.Fatalf("%q split", bad)
		}
	}
	for in, want := range map[string][]string{
		`C:\LLVM\bin\clang.exe -IC:\src\inc "-DP=a b" -c ..\src\a.c`: {`C:\LLVM\bin\clang.exe`, `-IC:\src\inc`, "-DP=a b", "-c", `..\src\a.c`},
		`cc -DQ=\"q\" "C:\dir with space\\"`:                         {"cc", `-DQ="q"`, `C:\dir with space\`},
	} {
		if got, ok := splitWindowsCommand(in); !ok || !slices.Equal(got, want) {
			t.Fatalf("windows %q: %q %v", in, got, ok)
		}
	}
	if _, ok := splitWindowsCommand(`cc "open`); ok {
		t.Fatal("unterminated windows quote split")
	}
}

// The committed fixture DB maps as docs/ANALYZERS.md#compile-db-매핑 states (also the expectation
// of fixtures/cpp/expected.json).
func TestFixtureCompileDB(t *testing.T) {
	files := fixtureFiles(t)
	snap := map[string]source.Entry{}
	for p := range files {
		snap[p] = source.Entry{Path: p, Kind: "file", SHA256: strings.Repeat("b", 64)}
	}
	b, err := os.ReadFile(filepath.Join(fixtureInput, CompileDBName))
	if err != nil {
		t.Fatal(err)
	}
	m := mapOne(t, string(b), snap, all)
	exp := loadExpected(t).CompileDB
	var units []string
	for _, u := range m.Units {
		units = append(units, u.Rel)
	}
	if !slices.Equal(units, exp.Units) || m.Total != exp.Entries || countText(m.Excluded) != exp.EntriesExcluded || m.DroppedText() != exp.FlagsDropped ||
		!slices.Equal(slices.Sorted(slices.Values(m.Limitations())), exp.Limitations) {
		t.Fatalf("fixture mapping units %v total %d excluded %q dropped %q limitations %v", units, m.Total, countText(m.Excluded), m.DroppedText(), m.Limitations())
	}
}

// Security (docs/ANALYZERS.md#compile-db-매핑): clang expands any argv element that starts with "@"
// as a response file. A compile DB value in any position that the allowlist
// keeps is validated, so no "@" element and no code-loading or toolchain
// option reaches the CODEYE database; the entry's driver name is recorded
// but the rendered driver is always clang or clang++.
func TestCompileDBValuesCannotInjectArguments(t *testing.T) {
	files := snapshotFiles("src/a.c", "src/b.cpp", "src/c.c")
	args := []string{"cc", "-x", "@/tmp/evil.rsp", "-target", "@/tmp/evil.rsp", "--target=@evil", "-target", "x86_64-linux-gnu", "-D", "@/tmp/evil.rsp",
		"-D@evil", "-U@evil", "-U", "@evil", "-std=@evil", "--std=@evil", "-stdlib=@evil", "-O@evil", "-O3", "-x", "-fplugin=x.so", "-I", "@inc", "-isystem@sys",
		"-include", "@hdr.h", "-imacros@m.h", "@/tmp/evil.rsp", "-Xclang", "@/tmp/evil.rsp", "-mllvm", "@/tmp/evil.rsp", "--config=@x", "-B@x",
		"-x", "c", "-DOK=\"@fine\"", "-c", "/r/src/a.c"}
	b, _ := json.Marshal([]map[string]any{
		{"directory": "/r", "file": "/r/src/a.c", "arguments": args},
		{"directory": "/r", "file": "src/b.cpp", "arguments": []string{"@cc", "-c", "src/b.cpp"}},
		{"directory": "/r", "file": "src/c.c", "arguments": []string{"x86_64-w64-mingw32-g++-13", "-c", "src/c.c"}},
	})
	m := mapOne(t, string(b), files, all)
	if len(m.Units) != 2 || m.Excluded[ExclUnsupportedDrive] != 1 {
		t.Fatalf("units %+v excluded %v", m.Units, m.Excluded)
	}
	ws := absWS()
	for _, argv := range rendered(t, m, ws) {
		for i, a := range argv {
			if strings.HasPrefix(a, "@") || (i > 0 && unsafeArg(a)) {
				t.Fatalf("unsafe element %q in %q", a, argv)
			}
			if i > 0 && !strings.HasPrefix(a, "-") && !filepath.IsAbs(a) && !keepLanguages[a] {
				t.Fatalf("positional %q is not absolute in %q", a, argv)
			}
		}
	}
	got := m.Units[0].Render(ws)
	want := []string{"clang", "--target=x86_64-linux-gnu", "-O3", "-I" + filepath.Join(ws, "@inc"), "-isystem" + filepath.Join(ws, "@sys"), "-include",
		filepath.Join(ws, "@hdr.h"), "-imacros", filepath.Join(ws, "@m.h"), "-x", "c", `-DOK="@fine"`, filepath.Join(ws, "src", "a.c")}
	if !slices.Equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	// A C++ driver name selects clang++ even for a .c file (g++ compiles it
	// as C++); its target prefix is not used.
	if d := m.Units[1].RenderedDriver(); d != "clang++" || m.Units[1].Driver != "x86_64-w64-mingw32-g++-13" {
		t.Fatalf("driver %q (%q)", d, m.Units[1].Driver)
	}
	// Defense in depth: a unit that still holds an unsafe argument is
	// refused by RenderDB and by the mapping.
	for _, bad := range []Unit{
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-x", Value: "@/tmp/evil.rsp"}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-fplugin=/x.so"}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-Xclang"}, {Flag: "-load"}}},
		{Rel: "src/a.c", Driver: "@cc"},
		{Rel: "src/../../etc/x.c", Driver: "cc"},
		{Rel: ".", Driver: "cc", Args: []Arg{{Flag: "-I", Path: ".", Joined: true}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-include", Path: "../outside.h"}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-imacros", Path: ".", Joined: true}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-I.", Path: ".", Joined: true}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-I", Path: "."}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-I", Path: ".", Value: "c", Joined: true}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-I", Path: "./", Joined: true}}},
		{Rel: "src/a.c", Driver: "cc", Args: []Arg{{Flag: "-I", Path: "src/..", Joined: true}}},
	} {
		if bad.safe() {
			t.Fatalf("unit %+v is safe", bad)
		}
		if _, err := RenderDB(ws, []Unit{bad}); err == nil {
			t.Fatalf("RenderDB accepted %+v", bad)
		}
	}
}

// Security: repository file names that look like options never become argv
// elements as they are. Every positional the tools receive is an absolute
// path (main files, include paths, -p, --project=, --file-list= and the
// lines of the file list).
func TestOptionLikeFileNamesStayPaths(t *testing.T) {
	files := snapshotFiles("src/--help.cpp", "src/-x.c", "src/-Iinc.c")
	b, _ := json.Marshal([]map[string]any{
		{"directory": "/r/src", "file": "--help.cpp", "arguments": []string{"c++", "-c", "--help.cpp"}},
		{"directory": "/r/src", "file": "-x.c", "arguments": []string{"cc", "-c", "-x.c"}},
		{"directory": "/r", "file": "/r/src/-Iinc.c", "command": "cc -c /r/src/-Iinc.c"},
	})
	m := mapOne(t, string(b), files, all)
	if len(m.Units) != 3 {
		t.Fatalf("units %+v %v", m.Units, m.Excluded)
	}
	ws := absWS()
	db, err := RenderDB(ws, m.Units)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		File      string   `json:"file"`
		Arguments []string `json:"arguments"`
	}
	if err := json.Unmarshal(db, &entries); err != nil {
		t.Fatal(err)
	}
	var mains []string
	for _, e := range entries {
		last := e.Arguments[len(e.Arguments)-1]
		if last != e.File || !filepath.IsAbs(last) {
			t.Fatalf("main file %q of %q", last, e.Arguments)
		}
		for _, a := range e.Arguments[1 : len(e.Arguments)-1] {
			if a == "--help.cpp" || a == "-x.c" || a == "-x" || strings.HasPrefix(a, "-Iinc") {
				t.Fatalf("option-like name became an argument: %q", e.Arguments)
			}
		}
		mains = append(mains, last)
	}
	args := ClangTidyArgs(filepath.Join(ws, "scratch"), mains)
	for _, a := range args[len(args)-len(mains):] {
		if !filepath.IsAbs(a) {
			t.Fatalf("clang-tidy positional %q", a)
		}
	}
	if p := args[1]; !filepath.IsAbs(p) {
		t.Fatalf("-p %q", p)
	}
	for _, a := range CppcheckArgs("--file-list=" + filepath.Join(ws, "files.txt")) {
		if !strings.HasPrefix(a, "--") {
			t.Fatalf("cppcheck argument %q is not an option", a)
		}
	}
}
