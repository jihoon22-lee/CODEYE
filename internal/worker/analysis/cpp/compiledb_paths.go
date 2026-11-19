package cpp

import (
	"path"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var drivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// norm makes a build-machine path slash separated (Windows drive and UNC
// paths use backslashes).
func norm(p string) string {
	if drivePath.MatchString(p) || strings.HasPrefix(p, `\\`) {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}

func isAbs(p string) bool { return strings.HasPrefix(p, "/") || drivePath.MatchString(p) }

// winDir reports a compile DB entry written on Windows (drive or UNC
// directory): its paths use backslashes, also relative ones.
func winDir(dir string) bool {
	return drivePath.MatchString(dir) || strings.HasPrefix(dir, `\\`) || strings.HasPrefix(dir, "//")
}

// absOn resolves a build-machine path against the entry directory.
func absOn(dir, p string) (string, bool) {
	if winDir(dir) {
		p = strings.ReplaceAll(p, `\`, "/")
	} else {
		p = norm(p)
	}
	if !isAbs(p) {
		if !isAbs(dir) {
			return "", false
		}
		p = dir + "/" + p
	}
	return path.Clean(p), true
}

// mapUnder maps an absolute build-machine path under root to a snapshot
// path ("." for the root itself).
func mapUnder(root, p string) (string, bool) {
	if root == "" {
		return "", false
	}
	var rel string
	switch {
	case p == root:
		return ".", true
	case root == "/":
		rel = strings.TrimPrefix(p, "/")
	case strings.HasPrefix(p, root+"/"):
		rel = p[len(root)+1:]
	default:
		return "", false
	}
	if domain.ValidateEvidencePath(rel) != nil {
		return "", false
	}
	return rel, true
}

// inferRoot finds the build-machine directory the snapshot root was: the
// prefix that maps the most entry files onto included snapshot files (ties:
// the longest matching suffix). It is never recorded (an absolute path of
// another machine).
func inferRoot(files []string, snapshot map[string]source.Entry) string {
	score := map[string]int{}
	for _, f := range files {
		seen := map[string]bool{}
		for i := 0; i < len(f); i++ {
			if f[i] != '/' {
				continue
			}
			suffix := f[i+1:]
			if _, ok := snapshot[suffix]; !ok || suffix == "" {
				continue
			}
			root := f[:i]
			if root == "" {
				root = "/"
			}
			if !seen[root] {
				seen[root] = true
				score[root]++
			}
		}
	}
	best, bestScore := "", 0
	for root, n := range score {
		if n > bestScore || n == bestScore && (len(root) < len(best) || len(root) == len(best) && root < best) {
			best, bestScore = root, n
		}
	}
	return best
}
