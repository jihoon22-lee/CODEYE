package docpath

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Selection ranks eligible paths for a bounded read. Links can promote a
// document only after its primary README has passed the caller's normal read
// and content checks. Selection neither opens files nor changes scope.
type Selection struct {
	primary string
	paths   map[string]bool
	linked  map[string]bool
	design  bool
	guides  bool
}

func NewSelection(paths []string) *Selection {
	s := &Selection{paths: make(map[string]bool, len(paths))}
	var primaryDepth int
	var ambiguous bool
	for _, p := range paths {
		s.paths[p] = true
		class := Classify(p)
		if administrative(p, class) {
			continue
		}
		if class == Design {
			s.design = true
			s.guides = s.guides || guide(p) != notGuide
		}
		if class != Readme || stem(p) != "readme" || (path.Dir(p) != "." && guide(p) != notGuide) {
			continue
		}
		depth := strings.Count(p, "/")
		switch {
		case s.primary == "" || depth < primaryDepth:
			s.primary, primaryDepth, ambiguous = p, depth, false
		case depth == primaryDepth && path.Dir(p) != path.Dir(s.primary):
			ambiguous = true
		case depth == primaryDepth && p < s.primary:
			s.primary = p
		}
	}
	if ambiguous {
		s.primary = ""
	}
	return s
}

// HasDesign reports whether the inventory includes guidance beyond README
// files and contributor instructions that could compete for the read budget.
func (s *Selection) HasDesign() bool { return s.design }

func stem(p string) string {
	base := strings.ToLower(path.Base(p))
	return strings.TrimSuffix(base, path.Ext(base))
}

const (
	notGuide = iota
	guideTopic
	guideSection
	guideRequirements
)

func topic(name string) int {
	name = strings.ReplaceAll(name, "_", "-")
	switch name {
	case "requirements", "product-requirements", "functional-requirements", "software-requirements", "product-spec", "product-specification", "prd":
		return guideRequirements
	case "overview", "getting-started", "get-started", "quickstart", "quick-start", "usage", "configuration", "how-it-works", "user-guide", "가이드", "사용법", "안내":
		return guideTopic
	case "guide", "guides", "manual", "tutorial", "tutorials", "reference", "api", "installation":
		return guideSection
	}
	if strings.HasSuffix(name, "-guide") || strings.HasSuffix(name, "-reference") {
		return guideSection
	}
	return notGuide
}

func namedGuide(p string) int {
	name := stem(p)
	if name == "index" || name == "+page" || name == "_index" {
		return topic(strings.ToLower(path.Base(path.Dir(p))))
	}
	return topic(name)
}

func guide(p string) int {
	if kind := namedGuide(p); kind != notGuide {
		return kind
	}
	for _, segment := range strings.Split(strings.ToLower(path.Dir(p)), "/") {
		if topic(segment) != notGuide {
			return guideSection
		}
	}
	return notGuide
}

func administrative(p, class string) bool {
	if class == Contributing {
		return true
	}
	for _, name := range append(strings.Split(strings.ToLower(path.Dir(p)), "/"), stem(p)) {
		switch strings.ReplaceAll(name, "_", "-") {
		case ".github", ".gitlab", "template", "templates", "issue-template", "pull-request-template", "changelog", "license", "code-of-conduct", "contributing", "governance", "funding", "roadmap", "business-policy", "privacy-policy", "terms-of-service":
			return true
		}
	}
	return false
}

var locale = regexp.MustCompile(`^[a-z]{2}([-_][a-z0-9]{2,8})*$`)

// A language tag on its own is ambiguous. Demote a suspected duplicate
// only when the equivalent untagged file is also in the candidate set.
func (s *Selection) translation(p string) bool {
	base := path.Base(p)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	for i, r := range name {
		if strings.ContainsRune("-_.", r) && locale.MatchString(strings.ToLower(name[i+1:])) && s.paths[path.Join(path.Dir(p), name[:i]+ext)] {
			return true
		}
	}
	parts := strings.Split(p, "/")
	for i, part := range parts[:len(parts)-1] {
		if locale.MatchString(strings.ToLower(part)) && s.paths[path.Join(strings.Join(parts[:i], "/"), strings.Join(parts[i+1:], "/"))] {
			return true
		}
	}
	return false
}

// Primary reports whether p is the repository's main README or the README
// of the sole shallowest documented region when the root has no README.
func (s *Selection) Primary(p string) bool { return p == s.primary }

// Linked reports whether the verified main README points to p.
func (s *Selection) Linked(p string) bool { return s.linked[p] }

// Product reports whether a document can supply additional usage excerpts.
func (s *Selection) Product(p, class string) bool {
	return !administrative(p, class) && !s.translation(p) && (class == Readme || s.linked[p] || (class == Design && guide(p) != notGuide))
}

func (s *Selection) rank(p, class string) int {
	switch {
	case s.Primary(p):
		return 0
	case !s.paths[p]:
		return 10
	case (s.guides || len(s.linked) > 0) && strings.EqualFold(p, "AGENTS.md"):
		return 1
	case class == Other:
		return 9
	case administrative(p, class) || s.translation(p):
		return 8
	case class == Design && namedGuide(p) == guideRequirements:
		return 2
	case s.linked[p]:
		return 3
	case class == Design && guide(p) == guideTopic:
		return 4
	case class == Design && guide(p) == guideSection:
		return 5
	case class == Readme:
		return 6
	default:
		return 7
	}
}

var classOrder = map[string]int{Agent: 0, Readme: 1, Contributing: 2, Design: 3, Other: 4}

// Sort orders documents before applying a caller's budget. With no
// selection pressure, pass nil to retain the class, depth, path order.
func Sort[T any](docs []T, selection *Selection, file func(T) (string, string)) {
	ranks := map[string]int{}
	if selection != nil {
		for _, doc := range docs {
			p, class := file(doc)
			ranks[p] = selection.rank(p, class)
		}
	}
	sort.SliceStable(docs, func(i, j int) bool {
		x, xc := file(docs[i])
		y, yc := file(docs[j])
		if ranks[x] != ranks[y] {
			return ranks[x] < ranks[y]
		}
		if classOrder[xc] != classOrder[yc] {
			return classOrder[xc] < classOrder[yc]
		}
		if xd, yd := strings.Count(x, "/"), strings.Count(y, "/"); xd != yd {
			return xd < yd
		}
		return x < y
	})
}

var inlineLink = regexp.MustCompile(`\[[^\]\r\n]*\]\([\t ]*(?:<([^>\r\n]+)>|([^()\t \r\n]+))(?:[\t ]+[^\r\n]*)?\)`)

// WithReadmeLinks recognizes simple inline links to local guidance, including
// unambiguous site index paths. No path outside the candidate set is used.
func (s *Selection) WithReadmeLinks(p, text string) bool {
	if !s.Primary(p) {
		return false
	}
	switch strings.ToLower(path.Ext(p)) {
	case "", ".md", ".mdx", ".markdown":
	default:
		return false
	}
	s.linked = map[string]bool{}
	var fence string
	for _, line := range strings.Split(text[:min(len(text), 32<<10)], "\n") {
		line = strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fence = line[:3]
			continue
		}
		for _, match := range inlineLink.FindAllStringSubmatchIndex(line, -1) {
			// Skip images, inline code, and escaped opening brackets.
			start := match[0]
			if (start > 0 && strings.ContainsRune("!\\", rune(line[start-1]))) || strings.Count(line[:start], "`")%2 != 0 {
				continue
			}
			a, b := match[2], match[3]
			if a < 0 {
				a, b = match[4], match[5]
			}
			if target := s.localTarget(line[a:b]); target != "" {
				s.linked[target] = true
			}
		}
	}
	return len(s.linked) > 0
}

func (s *Selection) localTarget(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || path.IsAbs(u.Path) || strings.Contains(u.Path, "\\") {
		return ""
	}
	p := path.Join(path.Dir(s.primary), u.Path)
	if !s.paths[p] && path.Ext(p) == "" {
		var found string
		for _, candidate := range []string{p + ".md", p + ".mdx", path.Join(p, "index.md"), path.Join(p, "_index.md"), path.Join(p, "+page.md")} {
			if s.paths[candidate] {
				if found != "" {
					return ""
				}
				found = candidate
			}
		}
		p = found
	}
	if class := Classify(p); s.paths[p] && !s.Primary(p) && (class == Design || class == Readme) && !administrative(p, class) && !s.translation(p) {
		return p
	}
	return ""
}
