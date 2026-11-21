package eslint

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	ManagedJSRulePackID      = "codeye-js-eslint-rejection-types"
	managedJSRulePackVersion = "1"
	typedRejectRule          = "@typescript-eslint/" + promiseRejectRule
	managedJSBudget          = "javascript_type_input_budget_exceeded"
	managedJSInvalidInput    = "javascript_type_input_unsupported"
	managedJSNoSources       = "javascript_type_no_eligible_files"
	managedJSExcluded        = "javascript_type_files_excluded"
	managedJSMaxFiles        = 48
	managedJSMaxFileBytes    = 128 << 10
	managedJSMaxSourceBytes  = 1 << 20
	managedJSTimeout         = 25 * time.Second
	managedJSNodeHeap        = "--max-old-space-size=192"
)

//go:embed javascript_types.cjs
var managedJSProgram string

func managedJavaScriptFiles(mod jsts.Module) []string {
	if mod.TSConfig {
		return nil
	}
	var files []string
	for _, p := range mod.Files {
		ext := path.Ext(p)
		if !jsts.IsJS(p) || ext != strings.ToLower(ext) || strings.HasSuffix(strings.TrimSuffix(p, ext), ".min") {
			continue
		}
		switch strings.ToLower(strings.TrimSuffix(path.Base(p), ext)) {
		case "eslint.config", ".eslintrc":
			continue
		}
		dirs := strings.Split(path.Dir(p), "/")
		if slices.ContainsFunc(dirs, func(dir string) bool {
			switch strings.ToLower(dir) {
			case "dist", "build", "gen", "generated", "third-party", "third_party", "fixtures", "test-data", "examples", "samples":
				return true
			default:
				return false
			}
		}) {
			continue
		}
		files = append(files, p)
	}
	return files
}

func managedJavaScriptOverBudget(expected []string, files map[string]source.Entry) bool {
	if len(expected) > managedJSMaxFiles {
		return true
	}
	var total int64
	for _, p := range expected {
		size := files[p].Size
		if size == nil || *size < 0 || *size > managedJSMaxFileBytes {
			return true
		}
		total += *size
	}
	return total > managedJSMaxSourceBytes
}

var errManagedJSSnapshot = errors.New("invalid managed JavaScript snapshot input")
var errManagedJSText = errors.New("unsupported managed JavaScript text encoding")

// The parser and linter get a private tree with exact inputs, even if the
// normal work copy contains an installed dependency or unrelated file.
func stageManagedJavaScript(ctx context.Context, snapshot, stagedRoot string, expected []string, files map[string]source.Entry) error {
	root, err := os.OpenRoot(snapshot)
	if err != nil {
		return errManagedJSSnapshot
	}
	defer root.Close()
	for _, p := range expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		segments := strings.Split(p, "/")
		for i := range segments {
			info, err := root.Lstat(filepath.FromSlash(strings.Join(segments[:i+1], "/")))
			last := i == len(segments)-1
			if err != nil || (!last && !info.IsDir()) || (last && !info.Mode().IsRegular()) {
				return errManagedJSSnapshot
			}
		}
		f, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			return errManagedJSSnapshot
		}
		text, err := io.ReadAll(io.LimitReader(f, managedJSMaxFileBytes+1))
		_ = f.Close()
		if err != nil || int64(len(text)) != *files[p].Size || obsdoc.SHA256Hex(text) != files[p].SHA256 {
			return errManagedJSSnapshot
		}
		if !utf8.Valid(text) {
			return errManagedJSText
		}
		dest := filepath.Join(stagedRoot, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := analysis.WriteFileExclusive(dest, text); err != nil {
			return err
		}
	}
	return nil
}

func managedJavaScriptConfig(p plugins, moduleAbs string, relative []string) string {
	return managedJSProgram + "\nmodule.exports = module.exports(" + lit(map[string]any{
		"typescript": p.typescript.Entry, "typescriptESLint": p.tseslint.Entry,
		"cwd": moduleAbs, "files": relative, "ruleName": promiseRejectRule,
	}) + ");\n"
}
