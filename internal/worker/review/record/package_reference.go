package record

import (
	"path"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// PackageReference links a document example to a package manifest and a
// runtime entry verified by the worker. The excerpt still cites the guide.
type PackageReference struct {
	ManifestPath   string `json:"manifest_path"`
	ManifestSHA256 string `json:"manifest_sha256"`
	EntryPath      string `json:"entry_path"`
	EntrySHA256    string `json:"entry_sha256"`
	Name           string `json:"name,omitempty"`
	Callable       string `json:"callable,omitempty"`
	Export         string `json:"export,omitempty"`
}

const (
	LimDocumentScopeItemsOmitted = "document_scope_items_omitted"
	LimDocumentPackageNarrowed   = "document_package_scope_narrowed"
)

const packageNamePart = `[a-z0-9][a-z0-9._-]*`
const packageIdentifier = `[A-Za-z_$][A-Za-z0-9_$]{0,63}`

var packageName = regexp.MustCompile(`^(?:@` + packageNamePart + `/)?` + packageNamePart + `$`)
var packageCallable = regexp.MustCompile(`^` + packageIdentifier + `(?:\.` + packageIdentifier + `)?$`)
var packageExport = regexp.MustCompile(`^` + packageIdentifier + `$`)

func ValidPackageName(name string) bool {
	return len(name) <= 128 && packageName.MatchString(name) && !Sensitive([]byte(name))
}

func (p PackageReference) Scope() string { return path.Dir(p.ManifestPath) }

func (p PackageReference) IsZero() bool { return p == (PackageReference{}) }

func (e Excerpt) ValidPackageReference(redacted bool) bool {
	p := e.PackageReference
	if p.IsZero() {
		return true
	}
	if e.Kind != "document" || e.DirectivePath != "" || !PackageGuidePath(e.Path) ||
		domain.ValidateEvidencePath(p.ManifestPath) != nil || path.Base(p.ManifestPath) != "package.json" ||
		domain.ValidateEvidencePath(p.EntryPath) != nil || !PackageRuntimePath(p.EntryPath) ||
		!hex64.MatchString(p.ManifestSHA256) || !hex64.MatchString(p.EntrySHA256) ||
		!ScopeUnder(p.EntryPath, p.Scope()) || ScopeUnder(p.Scope(), e.DocumentScope()) || ScopeUnder(e.Path, p.Scope()) {
		return false
	}
	if redacted {
		return p.Name == "" && p.Callable == "" && p.Export == ""
	}
	return ValidPackageName(p.Name) && packageCallable.MatchString(p.Callable) && packageExport.MatchString(p.Export) &&
		!Sensitive([]byte(p.Callable)) && !Sensitive([]byte(p.Export))
}

func PackageGuidePath(p string) bool {
	return MarkdownDirectivePath(p) || (strings.EqualFold(path.Ext(p), ".mdx") &&
		MarkdownDirectivePath(strings.TrimSuffix(p, path.Ext(p))+".md"))
}

func PackageRuntimePath(p string) bool {
	lower := strings.ToLower(p)
	if SourceEcosystem(p) != "javascript" || SourceKind(p) != "code" ||
		strings.HasSuffix(lower, ".d.ts") || strings.HasSuffix(lower, ".d.mts") || strings.HasSuffix(lower, ".d.cts") ||
		strings.Contains(path.Base(lower), ".min.") {
		return false
	}
	for _, dir := range strings.Split(path.Dir(lower), "/") {
		switch dir {
		case "dist", "build", "out", "node_modules", "vendor", "third_party", "testdata":
			return false
		}
	}
	return true
}

// DocumentClaimCovers checks a contract or question against its cited docs.
// An empty statement is used only when report prose has been redacted.
func DocumentClaimCovers(ids []string, catalog map[string]Excerpt, scope, statement string) bool {
	return documentsSupportScope(ids, catalog, scope, statement, false)
}

func documentsSupportScope(ids []string, catalog map[string]Excerpt, scope, statement string, narrower bool) bool {
	var outside []Excerpt
	for _, id := range ids {
		e := catalog[id]
		if !ScopeUnder(scope, e.DocumentScope()) {
			outside = append(outside, e)
		}
	}
	return len(outside) == 0 || packageScopeCovers(outside, scope, statement, narrower)
}

// PackageScopeCovers checks citations whose physical scopes do not include
// the claim. A cited verified call may carry directly adjacent support from
// the same verified page. Other pages need their own applicable evidence.
func PackageScopeCovers(outside []Excerpt, scope, statement string) bool {
	return packageScopeCovers(outside, scope, statement, false)
}

func packageScopeCovers(outside []Excerpt, scope, statement string, narrower bool) bool {
	var anchors []Excerpt
	for _, e := range outside {
		p := e.PackageReference
		if p.IsZero() {
			continue
		}
		if (scope != p.Scope() && (!narrower || !ScopeUnder(scope, p.Scope()))) || (statement != "" && !p.Matches(statement)) {
			return false
		}
		anchors = append(anchors, e)
	}
	if len(anchors) == 0 {
		return false
	}
	for _, e := range outside {
		if !e.PackageReference.IsZero() {
			continue
		}
		adjacent := false
		for _, anchor := range anchors {
			if e.Kind == "document" && e.DirectivePath == "" && e.Path == anchor.Path && e.SHA256 == anchor.SHA256 &&
				(anchor.Start-e.End == 1 || e.Start-anchor.End == 1) {
				adjacent = true
				break
			}
		}
		if !adjacent {
			return false
		}
	}
	return true
}

func (p PackageReference) Matches(statement string) bool {
	return documentName(statement, p.Callable) || (p.Export != "default" && documentName(statement, p.Name) && documentName(statement, p.Export))
}

func documentName(statement, name string) bool {
	if name == "" {
		return false
	}
	return regexp.MustCompile(`(^|[^a-zA-Z0-9_.$@/\-])` + regexp.QuoteMeta(name) + `($|[^a-zA-Z0-9_.$@/\-])`).MatchString(statement)
}
