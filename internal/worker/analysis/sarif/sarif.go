// Package sarif is the SARIF 2.1.0 input adapter of the analyzer packs
// (docs/ANALYZERS.md#sarif-입력-어댑터). A pack whose tool writes a SARIF log
// decodes the recorded stream here and turns each result into the common
// observation model: the primary location, the tool's related locations
// and code flows (kept in their order and marked as produced by the tool),
// the fingerprints and the rule metadata (id, short description, CWE tags).
//
// The log is the raw stream an observation points into: every result keeps
// its JSON pointer and byte range, every code flow step its byte range.
// Decoding is strict: input that is too large, malformed, holds a field this
// adapter does not model (member names are matched exactly), or a value out
// of range is a shape error, never "no results". Text the adapter returns is
// checked (length, control characters); text it does not use (snippets,
// notification and location messages, help) is bounded only by the size of
// the log, so one long or odd source line does not reject the log. It is an
// adapter for the output of a tool a pack runs, not a way to import results
// produced elsewhere.
package sarif

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Version is the only SARIF version accepted.
const Version = "2.1.0"

// Schemas are the $schema values of SARIF 2.1.0 logs.
var Schemas = []string{
	"https://docs.oasis-open.org/sarif/sarif/v2.1.0/os/schemas/sarif-schema-2.1.0.json",
	"https://json.schemastore.org/sarif-2.1.0.json",
	"https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
}

// Default bounds of one log.
const (
	DefaultMaxBytes   = 32 << 20
	DefaultMaxResults = 100000
	// MaxSteps bounds the locations of one result (locations, related
	// locations and code flow steps together).
	MaxSteps     = 4096
	maxText      = 64 << 10
	maxShortText = 1024
	maxRules     = 4096
	maxNotes     = 10000
	maxDepth     = 64
)

// URIForm is how a tool writes artifactLocation.uri.
type URIForm int

const (
	// URIEncoded: an RFC 3986 relative reference with percent-encoding, as
	// SARIF 2.1.0 requires.
	URIEncoded URIForm = iota
	// URIRawPath: the file path verbatim, not percent-encoded (Opengrep 2.0
	// writes "sp ace/한글 %20.py" as is; decoding it would name another file).
	URIRawPath
)

// Options bound and interpret one log.
type Options struct {
	MaxBytes   int64
	MaxResults int
	URIForm    URIForm
	// BaseIDs are the uriBaseId values that mean "the directory the tool ran
	// in" (the work copy root). A location with another base, an absolute
	// URI or a path that climbs out of that directory is outside. A log that
	// gives one of them a URI in originalUriBaseIds is rejected: the adapter
	// does not resolve bases, it only knows they name the work copy.
	BaseIDs []string
	// ColumnKind is the unit the tool counts columns in, as the pack measured
	// it (ColumnBytes, or a SARIF columnKind). A log that declares another
	// columnKind is rejected; a log that declares none is read with this unit
	// (empty: the SARIF default, utf16CodeUnits).
	ColumnKind string
}

// Column units of Run.ColumnKind.
const (
	ColumnBytes      = "bytes"
	ColumnUTF16      = "utf16CodeUnits"
	ColumnCodePoints = "unicodeCodePoints"
)

// ErrShape marks a log that is not a well-formed SARIF 2.1.0 log of the
// modelled subset. A pack records it as a parse failure.
var ErrShape = errors.New("sarif shape error")

func shape(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrShape, fmt.Sprintf(format, a...))
}

// Log is a decoded SARIF log.
type Log struct {
	Runs []Run
}

// Run is one run: the driver, its rules, the notifications of its
// invocations and its results.
type Run struct {
	Driver        Driver
	Rules         []Rule
	Notifications []Notification
	// Invocations is the number of invocations; ExecutionSuccessful is
	// false when any invocation says so (a pack treats that as a failed run).
	Invocations         int
	ExecutionSuccessful bool
	// ColumnKind is the unit of the columns of every location of the run.
	ColumnKind string
	Results    []Result
}

// Driver identifies the tool.
type Driver struct {
	Name, Version, SemanticVersion string
}

// Rule is a reportingDescriptor: id, short description, the CWE tags
// ("CWE-78", from tags such as "CWE-78: OS Command Injection") and the
// other tags.
type Rule struct {
	ID               string
	Name             string
	ShortDescription string
	FullDescription  string
	DefaultLevel     string
	CWE              []string
	Tags             []string
}

// Notification is a toolExecutionNotification or
// toolConfigurationNotification: its descriptor id and level. Its message
// is not returned.
type Notification struct {
	ID, Level string
}

// Result is one result with the place of its raw bytes.
type Result struct {
	// Run and Index locate the result; Pointer is its JSON pointer and
	// ByteStart/ByteEnd its byte range in the log.
	Run, Index         int
	Pointer            string
	ByteStart, ByteEnd int64
	RuleID             string
	Level              string
	Message            string
	// Primary is the first of locations (nil when the result has none).
	Primary *Location
	// Steps are the other locations of the result in log order: further
	// locations, related locations and every code flow step.
	Steps []Step
	// Fingerprints holds fingerprints and partialFingerprints, the latter
	// prefixed "partial:".
	Fingerprints map[string]string
	// Suppressed: an accepted (or status-less) inSource or external
	// suppression is attached.
	Suppressed       bool
	SuppressionKinds []string
}

// Step kinds.
const (
	StepLocation = "location"  // locations[1:]
	StepRelated  = "related"   // relatedLocations
	StepFlow     = "code_flow" // codeFlows[].threadFlows[].locations
)

// Step is a secondary location of a result.
type Step struct {
	Kind string
	// Flow, Thread and Position number a code flow step (1-based) within
	// its code flow, thread flow and thread flow; Of is the thread flow's
	// length. Zero for other steps.
	Flow, Thread, Position, Of int
	// Kinds are the threadFlowLocation kinds ("call", "enter", "exit"...).
	Kinds        []string
	NestingLevel int
	Location     *Location
	// ByteStart/ByteEnd: the step's object in the log.
	ByteStart, ByteEnd int64
}

// Location is a physical location. Its messages are not returned.
type Location struct {
	// URI is the artifact URI as written; Path the work-copy relative path
	// it names ("" when it is outside, see Outside).
	URI     string
	Path    string
	Outside bool
	// Lines and columns as written (1-based, end column exclusive, in the
	// run's ColumnKind); 0 when absent.
	StartLine, StartColumn, EndLine, EndColumn int
}

// Decode strictly decodes data as a SARIF 2.1.0 log.
func Decode(data []byte, opt Options) (*Log, error) {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = DefaultMaxBytes
	}
	if opt.MaxResults <= 0 {
		opt.MaxResults = DefaultMaxResults
	}
	if int64(len(data)) > opt.MaxBytes {
		return nil, shape("log of %d bytes exceeds %d", len(data), opt.MaxBytes)
	}
	if !utf8.Valid(data) {
		return nil, shape("log is not UTF-8")
	}
	spans, err := index(data)
	if err != nil {
		return nil, err
	}
	var raw rawLog
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, shape("%v", err)
	}
	if raw.Version == nil || *raw.Version != Version {
		return nil, shape("version is not %s", Version)
	}
	if raw.Schema != nil && !slices.Contains(Schemas, *raw.Schema) {
		return nil, shape("unknown $schema")
	}
	if raw.Runs == nil || len(*raw.Runs) == 0 {
		return nil, shape("no runs")
	}
	log := &Log{}
	results := 0
	for ri, rr := range *raw.Runs {
		run, err := decodeRun(ri, rr, spans, opt)
		if err != nil {
			return nil, err
		}
		results += len(run.Results)
		if results > opt.MaxResults {
			return nil, shape("more than %d results", opt.MaxResults)
		}
		log.Runs = append(log.Runs, run)
	}
	return log, nil
}

func decodeRun(ri int, rr rawRun, spans map[string]span, opt Options) (Run, error) {
	var run Run
	if rr.Tool == nil || rr.Tool.Driver == nil || rr.Tool.Driver.Name == nil || !text(*rr.Tool.Driver.Name, maxShortText) || *rr.Tool.Driver.Name == "" {
		return run, shape("run %d: tool.driver.name", ri)
	}
	d := rr.Tool.Driver
	run.Driver = Driver{Name: *d.Name, Version: str(d.Version), SemanticVersion: str(d.SemanticVersion)}
	if !text(run.Driver.Version, 128) || !text(run.Driver.SemanticVersion, 128) || !text(str(d.InformationURI), maxShortText) {
		return run, shape("run %d: driver version", ri)
	}
	run.ColumnKind = opt.ColumnKind
	if run.ColumnKind == "" {
		run.ColumnKind = ColumnUTF16
	}
	if rr.ColumnKind != nil {
		if *rr.ColumnKind != ColumnUTF16 && *rr.ColumnKind != ColumnCodePoints {
			return run, shape("run %d: columnKind", ri)
		}
		if opt.ColumnKind != "" && *rr.ColumnKind != opt.ColumnKind {
			return run, shape("run %d: columnKind %s, the tool was measured to write %s", ri, *rr.ColumnKind, opt.ColumnKind)
		}
		run.ColumnKind = *rr.ColumnKind
	}
	for k, v := range rr.OriginalURIBaseIDs {
		if !text(k, 256) || v.URI == nil || !text(*v.URI, maxShortText) {
			return run, shape("run %d: originalUriBaseIds", ri)
		}
		if slices.Contains(opt.BaseIDs, k) {
			return run, shape("run %d: originalUriBaseIds gives %s a URI; only an unresolved work copy base is read", ri, k)
		}
	}
	if len(d.Rules) > maxRules {
		return run, shape("run %d: too many rules", ri)
	}
	ids := map[string]bool{}
	for i, r := range d.Rules {
		rule, err := decodeRule(r)
		if err != nil {
			return run, shape("run %d rule %d: %v", ri, i, err)
		}
		if ids[rule.ID] {
			return run, shape("run %d: duplicate rule %s", ri, rule.ID)
		}
		ids[rule.ID] = true
		run.Rules = append(run.Rules, rule)
	}
	run.ExecutionSuccessful = true
	for _, inv := range rr.Invocations {
		run.Invocations++
		if inv.ExecutionSuccessful == nil {
			return run, shape("run %d: invocation without executionSuccessful", ri)
		}
		run.ExecutionSuccessful = run.ExecutionSuccessful && *inv.ExecutionSuccessful
		if inv.ExitCode != nil && *inv.ExitCode < 0 {
			return run, shape("run %d: invocation exit code", ri)
		}
		for _, n := range append(append([]rawNotification{}, inv.ToolExecutionNotifications...), inv.ToolConfigurationNotifications...) {
			if len(run.Notifications) >= maxNotes {
				return run, shape("run %d: too many notifications", ri)
			}
			note := Notification{Level: "warning"}
			if n.Level != nil {
				note.Level = *n.Level
			}
			if n.Descriptor != nil && n.Descriptor.ID != nil {
				note.ID = *n.Descriptor.ID
			}
			if n.Message == nil || n.Message.Text == nil || !level(note.Level) || !text(note.ID, 256) {
				return run, shape("run %d: notification", ri)
			}
			run.Notifications = append(run.Notifications, note)
		}
	}
	if rr.Results == nil {
		return run, shape("run %d: results is missing (an empty list is required when there are none)", ri)
	}
	for i, r := range *rr.Results {
		res, err := decodeResult(ri, i, r, run.Rules, spans, opt)
		if err != nil {
			return run, err
		}
		run.Results = append(run.Results, res)
	}
	return run, nil
}

var cweTag = regexp.MustCompile(`^CWE-([1-9][0-9]{0,5})(?::.*)?$`)

func decodeRule(r rawRule) (Rule, error) {
	var rule Rule
	if r.ID == nil || *r.ID == "" || !text(*r.ID, 256) || strings.ContainsAny(*r.ID, " \t\n") {
		return rule, errors.New("id")
	}
	rule.ID, rule.Name = *r.ID, str(r.Name)
	rule.ShortDescription, rule.FullDescription = msgText(r.ShortDescription), msgText(r.FullDescription)
	if !text(rule.Name, maxShortText) || !text(rule.ShortDescription, maxShortText) || !text(rule.FullDescription, maxText) {
		return rule, errors.New("text")
	}
	if r.DefaultConfiguration != nil && r.DefaultConfiguration.Level != nil {
		if !level(*r.DefaultConfiguration.Level) {
			return rule, errors.New("default level")
		}
		rule.DefaultLevel = *r.DefaultConfiguration.Level
	}
	if r.Properties != nil {
		tags, err := propertyTags(*r.Properties)
		if err != nil {
			return rule, err
		}
		rule.Tags = tags
		for _, t := range tags {
			if m := cweTag.FindStringSubmatch(t); m != nil {
				rule.CWE = append(rule.CWE, "CWE-"+m[1])
			}
		}
		sort.Strings(rule.CWE)
		rule.CWE = slices.Compact(rule.CWE)
	}
	return rule, nil
}

// propertyTags reads the "tags" of a property bag. A bag is open by the
// SARIF specification; only its size and the shape of tags are checked.
func propertyTags(bag json.RawMessage) ([]string, error) {
	if len(bag) > maxText {
		return nil, errors.New("property bag too large")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(bag, &m); err != nil {
		return nil, errors.New("property bag is not an object")
	}
	raw, ok := m["tags"]
	if !ok {
		return nil, nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil || len(tags) > 64 {
		return nil, errors.New("tags")
	}
	for _, t := range tags {
		if !text(t, 256) {
			return nil, errors.New("tag")
		}
	}
	return tags, nil
}

func decodeResult(ri, i int, r rawResult, rules []Rule, spans map[string]span, opt Options) (Result, error) {
	ptr := "/runs/" + strconv.Itoa(ri) + "/results/" + strconv.Itoa(i)
	sp, ok := spans[ptr]
	if !ok {
		return Result{}, shape("%s: no byte range", ptr)
	}
	res := Result{Run: ri, Index: i, Pointer: ptr, ByteStart: sp.start, ByteEnd: sp.end, Level: "warning", Fingerprints: map[string]string{}}
	switch {
	case r.RuleID != nil:
		res.RuleID = *r.RuleID
		if r.RuleIndex != nil && (*r.RuleIndex < 0 || *r.RuleIndex >= len(rules) || rules[*r.RuleIndex].ID != res.RuleID) {
			return res, shape("%s: ruleIndex does not name ruleId", ptr)
		}
	case r.RuleIndex != nil && *r.RuleIndex >= 0 && *r.RuleIndex < len(rules):
		res.RuleID = rules[*r.RuleIndex].ID
	case r.Rule != nil && r.Rule.ID != nil:
		res.RuleID = *r.Rule.ID
	}
	if res.RuleID == "" || !text(res.RuleID, 256) || strings.ContainsAny(res.RuleID, " \t\n") {
		return res, shape("%s: rule id", ptr)
	}
	if r.Rule != nil && r.Rule.ID != nil && *r.Rule.ID != res.RuleID {
		return res, shape("%s: rule reference", ptr)
	}
	if r.Level != nil {
		if !level(*r.Level) {
			return res, shape("%s: level", ptr)
		}
		res.Level = *r.Level
	} else if j := slices.IndexFunc(rules, func(x Rule) bool { return x.ID == res.RuleID }); j >= 0 && rules[j].DefaultLevel != "" {
		res.Level = rules[j].DefaultLevel
	}
	if r.Kind != nil && *r.Kind != "fail" {
		// pass, open, review, notApplicable, informational: not a diagnostic
		// this adapter models.
		return res, shape("%s: kind %q", ptr, *r.Kind)
	}
	if r.Message == nil || r.Message.Text == nil || *r.Message.Text == "" || !text(*r.Message.Text, maxText) {
		return res, shape("%s: message.text", ptr)
	}
	res.Message = *r.Message.Text
	for name, fps := range map[string]map[string]string{"": r.Fingerprints, "partial:": r.PartialFingerprints} {
		for k, v := range fps {
			if !text(k, 128) || k == "" || !text(v, 1024) || strings.ContainsAny(k+v, "\n\t") {
				return res, shape("%s: fingerprint", ptr)
			}
			res.Fingerprints[name+k] = v
		}
	}
	for _, s := range r.Suppressions {
		if s.Kind == nil || (*s.Kind != "inSource" && *s.Kind != "external") {
			return res, shape("%s: suppression kind", ptr)
		}
		if s.Status != nil && *s.Status != "accepted" && *s.Status != "underReview" && *s.Status != "rejected" {
			return res, shape("%s: suppression status", ptr)
		}
		res.SuppressionKinds = append(res.SuppressionKinds, *s.Kind)
		if s.Status == nil || *s.Status == "accepted" {
			res.Suppressed = true
		}
	}
	if r.Properties != nil && len(*r.Properties) > maxText {
		return res, shape("%s: property bag too large", ptr)
	}
	steps := len(r.Locations) + len(r.RelatedLocations)
	for li, l := range r.Locations {
		loc, err := decodeLocation(l, opt)
		if err != nil {
			return res, shape("%s/locations/%d: %v", ptr, li, err)
		}
		if li == 0 {
			res.Primary = loc
			continue
		}
		sp, ok := spans[ptr+"/locations/"+strconv.Itoa(li)]
		if !ok {
			return res, shape("%s/locations/%d: no byte range", ptr, li)
		}
		res.Steps = append(res.Steps, Step{Kind: StepLocation, Location: loc, ByteStart: sp.start, ByteEnd: sp.end})
	}
	for li, l := range r.RelatedLocations {
		loc, err := decodeLocation(l, opt)
		if err != nil {
			return res, shape("%s/relatedLocations/%d: %v", ptr, li, err)
		}
		sp, ok := spans[ptr+"/relatedLocations/"+strconv.Itoa(li)]
		if !ok {
			return res, shape("%s/relatedLocations/%d: no byte range", ptr, li)
		}
		res.Steps = append(res.Steps, Step{Kind: StepRelated, Location: loc, ByteStart: sp.start, ByteEnd: sp.end})
	}
	for fi, f := range r.CodeFlows {
		if f.Message != nil && f.Message.Text == nil {
			return res, shape("%s/codeFlows/%d: message", ptr, fi)
		}
		if len(f.ThreadFlows) == 0 {
			return res, shape("%s/codeFlows/%d: no threadFlows", ptr, fi)
		}
		for ti, tf := range f.ThreadFlows {
			if len(tf.Locations) == 0 {
				return res, shape("%s/codeFlows/%d/threadFlows/%d: no locations", ptr, fi, ti)
			}
			for si, tl := range tf.Locations {
				steps++
				if steps > MaxSteps {
					return res, shape("%s: more than %d locations", ptr, MaxSteps)
				}
				p := fmt.Sprintf("%s/codeFlows/%d/threadFlows/%d/locations/%d", ptr, fi, ti, si)
				sp, ok := spans[p]
				if !ok {
					return res, shape("%s: no byte range", p)
				}
				step := Step{Kind: StepFlow, Flow: fi + 1, Thread: ti + 1, Position: si + 1, Of: len(tf.Locations), ByteStart: sp.start, ByteEnd: sp.end}
				if tl.NestingLevel != nil {
					if *tl.NestingLevel < 0 || *tl.NestingLevel > 1024 {
						return res, shape("%s: nestingLevel", p)
					}
					step.NestingLevel = *tl.NestingLevel
				}
				if tl.ExecutionOrder != nil && *tl.ExecutionOrder < -1 {
					return res, shape("%s: executionOrder", p)
				}
				if len(tl.Kinds) > 16 {
					return res, shape("%s: kinds", p)
				}
				for _, k := range tl.Kinds {
					if k == "" || !text(k, 64) || strings.ContainsAny(k, " \n\t") {
						return res, shape("%s: kind", p)
					}
				}
				step.Kinds = append([]string{}, tl.Kinds...)
				if tl.Location != nil {
					loc, err := decodeLocation(*tl.Location, opt)
					if err != nil {
						return res, shape("%s: %v", p, err)
					}
					step.Location = loc
				}
				res.Steps = append(res.Steps, step)
			}
		}
	}
	if steps > MaxSteps {
		return res, shape("%s: more than %d locations", ptr, MaxSteps)
	}
	// Log order: the steps are reported in the order their bytes appear.
	sort.SliceStable(res.Steps, func(a, b int) bool { return res.Steps[a].ByteStart < res.Steps[b].ByteStart })
	return res, nil
}

func decodeLocation(l rawLocation, opt Options) (*Location, error) {
	loc := &Location{}
	if l.Message != nil && l.Message.Text == nil {
		return nil, errors.New("location message")
	}
	if l.ID != nil && *l.ID < -1 {
		return nil, errors.New("location id")
	}
	p := l.PhysicalLocation
	if p == nil {
		if l.Message == nil || *l.Message.Text == "" {
			return nil, errors.New("location without physicalLocation or message")
		}
		return loc, nil
	}
	if p.ArtifactLocation == nil || p.ArtifactLocation.URI == nil || *p.ArtifactLocation.URI == "" || !text(*p.ArtifactLocation.URI, 4096) {
		return nil, errors.New("artifactLocation.uri")
	}
	loc.URI = *p.ArtifactLocation.URI
	if p.ArtifactLocation.Index != nil && *p.ArtifactLocation.Index < -1 {
		return nil, errors.New("artifactLocation.index")
	}
	base := ""
	if p.ArtifactLocation.URIBaseID != nil {
		base = *p.ArtifactLocation.URIBaseID
	}
	loc.Path, loc.Outside = mapURI(loc.URI, base, opt)
	if r := p.Region; r != nil {
		if r.Message != nil && r.Message.Text == nil {
			return nil, errors.New("region message")
		}
		if r.Snippet != nil && r.Snippet.Text == nil {
			return nil, errors.New("region snippet")
		}
		if r.StartLine == nil || *r.StartLine < 1 {
			return nil, errors.New("region.startLine")
		}
		loc.StartLine, loc.EndLine = *r.StartLine, *r.StartLine
		if r.EndLine != nil {
			if *r.EndLine < *r.StartLine {
				return nil, errors.New("region.endLine")
			}
			loc.EndLine = *r.EndLine
		}
		if r.StartColumn != nil {
			if *r.StartColumn < 1 {
				return nil, errors.New("region.startColumn")
			}
			loc.StartColumn = *r.StartColumn
		}
		if r.EndColumn != nil {
			if *r.EndColumn < 1 || (loc.EndLine == loc.StartLine && loc.StartColumn > 0 && *r.EndColumn < loc.StartColumn) {
				return nil, errors.New("region.endColumn")
			}
			loc.EndColumn = *r.EndColumn
		}
		for _, o := range []*int{r.CharOffset, r.CharLength, r.ByteOffset, r.ByteLength} {
			if o != nil && *o < 0 {
				return nil, errors.New("region offset")
			}
		}
	}
	if p.ContextRegion != nil && p.ContextRegion.StartLine != nil && *p.ContextRegion.StartLine < 1 {
		return nil, errors.New("contextRegion")
	}
	return loc, nil
}

// mapURI maps an artifact URI to a work-copy relative path. Absolute URIs,
// unknown bases and paths that leave the base directory are outside.
func mapURI(uri, base string, opt Options) (string, bool) {
	if base != "" && !slices.Contains(opt.BaseIDs, base) {
		return "", true
	}
	p := uri
	if opt.URIForm == URIEncoded {
		u, err := url.Parse(uri)
		if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", true
		}
		p = u.Path
	}
	if strings.ContainsRune(p, 0) || strings.Contains(p, `\`) || strings.HasPrefix(p, "/") || driveLetter(p) {
		return "", true
	}
	if opt.URIForm == URIRawPath && strings.Contains(p, "://") {
		return "", true
	}
	// "./" names the base; what follows must still be relative
	// (".//etc/passwd" is the absolute path /etc/passwd).
	rest := strings.TrimPrefix(p, "./")
	if strings.HasPrefix(rest, "/") {
		return "", true
	}
	clean := path.Clean(rest)
	if path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || driveLetter(clean) {
		return "", true
	}
	return clean, false
}

// Maps reports whether a work-copy relative path written by a tool as its
// artifact URI is read back as that same path. A pack does not give a tool
// a file it could not name back.
func Maps(p string, opt Options) bool {
	uri := p
	if opt.URIForm == URIEncoded {
		uri = (&url.URL{Path: p}).EscapedPath()
	}
	got, outside := mapURI(uri, "", opt)
	return !outside && got == p
}

// driveLetter:"C:", "C:/x" or "C:\\x" (a Windows drive), not a name such as
// "a:b.py", which is an ordinary relative file name on POSIX.
func driveLetter(p string) bool {
	if len(p) < 2 || p[1] != ':' || !('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z') {
		return false
	}
	return len(p) == 2 || p[2] == '/' || p[2] == '\\'
}

func level(s string) bool { return s == "none" || s == "note" || s == "warning" || s == "error" }

func text(s string, n int) bool {
	if len(s) > n || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f })
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func msgText(m *rawMessage) string {
	if m == nil || m.Text == nil {
		return ""
	}
	return *m.Text
}
