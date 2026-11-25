package sarif

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// The modelled subset of SARIF 2.1.0. Decoding rejects any other member, so
// a field a tool starts to emit is a shape error until it is modelled.
// Pointers tell an absent member from a zero value.

type rawLog struct {
	Schema  *string   `json:"$schema"`
	Version *string   `json:"version"`
	Runs    *[]rawRun `json:"runs"`
}

type rawRun struct {
	Tool               *rawTool                  `json:"tool"`
	Invocations        []rawInvocation           `json:"invocations"`
	Results            *[]rawResult              `json:"results"`
	OriginalURIBaseIDs map[string]rawArtifactLoc `json:"originalUriBaseIds"`
	ColumnKind         *string                   `json:"columnKind"`
}

type rawTool struct {
	Driver *rawDriver `json:"driver"`
}

type rawDriver struct {
	Name            *string   `json:"name"`
	Version         *string   `json:"version"`
	SemanticVersion *string   `json:"semanticVersion"`
	InformationURI  *string   `json:"informationUri"`
	Rules           []rawRule `json:"rules"`
}

type rawRule struct {
	ID                   *string          `json:"id"`
	Name                 *string          `json:"name"`
	ShortDescription     *rawMessage      `json:"shortDescription"`
	FullDescription      *rawMessage      `json:"fullDescription"`
	Help                 *rawMultiformat  `json:"help"`
	HelpURI              *string          `json:"helpUri"`
	DefaultConfiguration *rawRuleConfig   `json:"defaultConfiguration"`
	Properties           *json.RawMessage `json:"properties"`
}

type rawRuleConfig struct {
	Level *string `json:"level"`
}

type rawMultiformat struct {
	Text     *string `json:"text"`
	Markdown *string `json:"markdown"`
}

type rawMessage struct {
	Text *string `json:"text"`
}

type rawInvocation struct {
	ExecutionSuccessful            *bool             `json:"executionSuccessful"`
	ExitCode                       *int              `json:"exitCode"`
	ToolExecutionNotifications     []rawNotification `json:"toolExecutionNotifications"`
	ToolConfigurationNotifications []rawNotification `json:"toolConfigurationNotifications"`
}

type rawNotification struct {
	Descriptor *rawDescriptorRef `json:"descriptor"`
	Level      *string           `json:"level"`
	Message    *rawMessage       `json:"message"`
}

type rawDescriptorRef struct {
	ID    *string `json:"id"`
	Index *int    `json:"index"`
}

type rawResult struct {
	RuleID              *string           `json:"ruleId"`
	RuleIndex           *int              `json:"ruleIndex"`
	Rule                *rawDescriptorRef `json:"rule"`
	Kind                *string           `json:"kind"`
	Level               *string           `json:"level"`
	Message             *rawMessage       `json:"message"`
	Locations           []rawLocation     `json:"locations"`
	RelatedLocations    []rawLocation     `json:"relatedLocations"`
	CodeFlows           []rawCodeFlow     `json:"codeFlows"`
	Fingerprints        map[string]string `json:"fingerprints"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Suppressions        []rawSuppression  `json:"suppressions"`
	Properties          *json.RawMessage  `json:"properties"`
}

type rawSuppression struct {
	Kind          *string `json:"kind"`
	Status        *string `json:"status"`
	Justification *string `json:"justification"`
}

type rawLocation struct {
	ID               *int                 `json:"id"`
	PhysicalLocation *rawPhysicalLocation `json:"physicalLocation"`
	Message          *rawMessage          `json:"message"`
}

type rawPhysicalLocation struct {
	ArtifactLocation *rawArtifactLoc `json:"artifactLocation"`
	Region           *rawRegion      `json:"region"`
	ContextRegion    *rawRegion      `json:"contextRegion"`
}

type rawArtifactLoc struct {
	URI       *string `json:"uri"`
	URIBaseID *string `json:"uriBaseId"`
	Index     *int    `json:"index"`
}

type rawRegion struct {
	StartLine   *int        `json:"startLine"`
	StartColumn *int        `json:"startColumn"`
	EndLine     *int        `json:"endLine"`
	EndColumn   *int        `json:"endColumn"`
	CharOffset  *int        `json:"charOffset"`
	CharLength  *int        `json:"charLength"`
	ByteOffset  *int        `json:"byteOffset"`
	ByteLength  *int        `json:"byteLength"`
	Snippet     *rawMessage `json:"snippet"`
	Message     *rawMessage `json:"message"`
}

type rawCodeFlow struct {
	Message     *rawMessage     `json:"message"`
	ThreadFlows []rawThreadFlow `json:"threadFlows"`
}

type rawThreadFlow struct {
	Locations []rawThreadFlowLocation `json:"locations"`
}

type rawThreadFlowLocation struct {
	Location       *rawLocation `json:"location"`
	Kinds          []string     `json:"kinds"`
	NestingLevel   *int         `json:"nestingLevel"`
	ExecutionOrder *int         `json:"executionOrder"`
}

// span is the byte range of one JSON value.
type span struct{ start, end int64 }

// index walks the log once and returns the byte range of every value under
// a result (its pointer), rejecting duplicate object keys, trailing data
// and nesting deeper than maxDepth.
func index(data []byte) (map[string]span, error) {
	w := walker{data: data, dec: json.NewDecoder(bytes.NewReader(data)), out: map[string]span{}}
	w.dec.UseNumber()
	if err := w.value(nil); err != nil {
		return nil, err
	}
	if _, err := w.dec.Token(); err != io.EOF {
		return nil, shape("trailing data after the log")
	}
	return w.out, nil
}

type walker struct {
	data []byte
	dec  *json.Decoder
	out  map[string]span
}

// start is the offset of the next value: after whitespace and one ',' or
// ':' separator following the previous token.
func (w *walker) start() int64 {
	i := w.dec.InputOffset()
	for i < int64(len(w.data)) {
		switch w.data[i] {
		case ' ', '\t', '\r', '\n', ',', ':':
			i++
		default:
			return i
		}
	}
	return i
}

func (w *walker) value(path []string) error {
	if len(path) > maxDepth {
		return shape("nesting deeper than %d", maxDepth)
	}
	begin := w.start()
	tok, err := w.dec.Token()
	if err != nil {
		return shape("%v", err)
	}
	if d, ok := tok.(json.Delim); ok {
		switch d {
		case '{':
			seen := map[string]bool{}
			for w.dec.More() {
				k, err := w.dec.Token()
				if err != nil {
					return shape("%v", err)
				}
				key, ok := k.(string)
				if !ok {
					return shape("object key")
				}
				if seen[key] {
					return shape("duplicate key %q", key)
				}
				// encoding/json matches member names without regard to case;
				// only the exact name is a member of the modelled subset.
				if name, ok := memberNames[strings.ToLower(key)]; ok && name != key && !openKeys(path) {
					return shape("member %q is not %q", key, name)
				}
				seen[key] = true
				if err := w.value(append(path, key)); err != nil {
					return err
				}
			}
		case '[':
			for i := 0; w.dec.More(); i++ {
				if err := w.value(append(path, strconv.Itoa(i))); err != nil {
					return err
				}
			}
		default:
			return shape("unexpected delimiter")
		}
		if _, err := w.dec.Token(); err != nil {
			return shape("%v", err)
		}
	}
	if wanted(path) {
		w.out[pointer(path)] = span{start: begin, end: w.dec.InputOffset()}
	}
	return nil
}

// memberNames maps the lower-case form of every modelled member name to the
// name itself.
var memberNames = func() map[string]string {
	out := map[string]string{}
	var walk func(t reflect.Type)
	seen := map[reflect.Type]bool{}
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
				out[strings.ToLower(name)] = name
			}
			walk(f.Type)
		}
	}
	walk(reflect.TypeOf(rawLog{}))
	return out
}()

// openKeys: the members of a property bag, a fingerprint map and the
// originalUriBaseIds map are free-form names.
func openKeys(path []string) bool {
	for _, p := range path {
		if p == "properties" {
			return true
		}
	}
	if len(path) == 0 {
		return false
	}
	switch path[len(path)-1] {
	case "fingerprints", "partialFingerprints", "originalUriBaseIds":
		return true
	}
	return false
}

// wanted keeps results and the locations below them.
func wanted(p []string) bool {
	if len(p) < 4 || p[0] != "runs" || p[2] != "results" {
		return false
	}
	switch len(p) {
	case 4:
		return true
	case 6:
		return p[4] == "locations" || p[4] == "relatedLocations"
	case 10:
		return p[4] == "codeFlows" && p[6] == "threadFlows" && p[8] == "locations"
	}
	return false
}

func pointer(p []string) string {
	var b strings.Builder
	for _, s := range p {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(s))
	}
	return b.String()
}
