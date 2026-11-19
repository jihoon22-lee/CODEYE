package deps

import (
	"context"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// A file:, link: or portal: dependency outside the snapshot stops the
// install before anything runs and names the dependency; local
// dependencies inside the snapshot are fine.
func TestJSLocalDependencyOutsideTheProject(t *testing.T) {
	inside := `{"name":"app","dependencies":{"lib":"file:./libs/lib","b":"link:packages/b"},"devDependencies":{"typescript":"6.0.3"}}`
	for name, tc := range map[string]struct {
		files map[string]string
		want  []string
	}{
		"inside":           {map[string]string{"package.json": inside, "libs/lib/package.json": `{"name":"lib"}`}, nil},
		"package.json":     {map[string]string{"package.json": `{"name":"app","dependencies":{"host":"file:../../etc"},"optionalDependencies":{"abs":"link:/opt/x"}}`}, []string{"abs", "host"}},
		"member":           {map[string]string{"package.json": inside, "packages/b/package.json": `{"name":"b","peerDependencies":{"p":"portal:../../../p"}}`}, []string{"p"}},
		"npm lockfile":     {map[string]string{"package.json": inside, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{},"node_modules/x":{"resolved":"../x","link":true},"../x":{"version":"1.0.0"}}}`}, []string{"../x", "x"}},
		"pnpm importer":    {map[string]string{"package.json": inside, "pnpm-lock.yaml": "lockfileVersion: '9.0'\n\nimporters:\n\n  packages/b:\n    dependencies:\n      c:\n        specifier: link:../c\n        version: link:../c\n      d:\n        specifier: link:../../../d\n        version: link:../../../d\n"}, []string{"link:../../../d"}},
		"yarn file entry":  {map[string]string{"package.json": inside, "yarn.lock": "# yarn lockfile v1\n\n\"x@file:../x\":\n  version \"1.0.0\"\n"}, []string{"file:../x"}},
		"windows absolute": {map[string]string{"package.json": `{"name":"app","dependencies":{"w":"file:C:\\src\\w"}}`}, []string{"w"}},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := jsJob(t, tc.files)
			files := govet.IncludedFiles(job.Snapshot.Manifest)
			read := func(rel string) ([]byte, bool) {
				b, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, files[rel].Path)
				return b, err == nil
			}
			manager, lock := ManagerNpm, []byte(nil)
			for lf, m := range map[string]string{"package-lock.json": ManagerNpm, "pnpm-lock.yaml": ManagerPnpm, "yarn.lock": ManagerYarn} {
				if b, ok := tc.files[lf]; ok {
					manager, lock = m, []byte(b)
				}
			}
			if got := localDepsOutside(files, read, ".", manager, lock); !slices.Equal(got, tc.want) {
				t.Fatalf("outside %v, want %v", got, tc.want)
			}
		})
	}
	// Through the task: nothing runs and the record names the dependency.
	job, _ := jsJob(t, map[string]string{"package.json": `{"name":"app","dependencies":{"host":"file:../outside"}}`, "tsconfig.json": "{}", "a.ts": ""})
	r := &fakeRunner{fn: func(context.Context, executor.Spec) executor.Result {
		t.Fatal("nothing may run")
		return executor.Result{}
	}}
	res := handler(t, jsTools(t)).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonLocalDependencyOutside {
		t.Fatalf("result %s/%s", res.Status, res.Reason)
	}
	rec := readRecord(t, job, "deps.javascript")
	if len(rec.FailedItems) != 1 || rec.FailedItems[0].Name != "host" || rec.TreeUsable() {
		t.Fatalf("record %+v", rec)
	}
}
