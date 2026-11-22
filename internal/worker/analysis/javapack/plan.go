package javapack

import (
	"context"
	"fmt"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	PMD      = "pmd"
	SpotBugs = "spotbugs"
)

type target struct {
	task   pack.Task
	module javaproject.Module
	files  []source.Entry
}

func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return plan(ctx, job, a.name)
}

func PlanPMD(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return plan(ctx, job, PMD)
}

func PlanSpotBugs(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return plan(ctx, job, SpotBugs)
}

func plan(ctx context.Context, job analysis.Job, name string) ([]pack.Task, error) {
	selected, err := targets(ctx, job, name)
	var out []pack.Task
	for _, t := range selected {
		out = append(out, t.task)
	}
	return out, err
}

func targets(ctx context.Context, job analysis.Job, name string) ([]target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !slices.Contains(pack.InScope(job.Snapshot.Manifest, job.Scope), "java") {
		return nil, nil
	}
	modules, err := javaproject.Scan(job.Snapshot.SnapshotDir, job.Snapshot.Manifest)
	if err != nil {
		return nil, err
	}
	var out []target
	for _, module := range modules {
		var files []source.Entry
		for _, f := range module.Files {
			if anypack.InScope(f.Path, job.Scope) {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			continue
		}
		if len(out) == 64 {
			return nil, fmt.Errorf("Java analysis supports up to 64 selected modules")
		}
		out = append(out, target{
			task: pack.Task{Spec: analysis.TaskSpec{Key: name + module.Suffix, Title: name + " Java", Required: name == PMD, Stage: domain.StageStaticAnalysis},
				Module: module.Dir, Patterns: anypack.Roots(job.Scope), FilesInScope: anypack.Ptr(len(files))},
			module: module, files: files,
		})
	}
	if len(out) == 0 {
		out = append(out, target{task: pack.Task{Spec: analysis.TaskSpec{Key: name, Title: name + " Java", Required: name == PMD, Stage: domain.StageStaticAnalysis}, Module: ".", Patterns: anypack.Roots(job.Scope), FilesInScope: anypack.Ptr(0)}})
	}
	return out, nil
}
