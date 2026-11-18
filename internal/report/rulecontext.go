package report

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
)

// Rule context of a tool coverage row (W19, docs/39 §5.1, docs/40 §3.3):
// what decides which diagnostics a tool reports besides the code and the
// tool version. A comparison may call a vanished tool finding
// resolved_observed only when the rule context is known and equal on both
// sides.
const (
	// Config states. not_read: the rule pack overrides repository
	// configuration or the tool has none. recorded: the tool honors it and
	// the files it read are recorded (ConfigPaths, ConfigDigest); with no
	// file read ConfigPaths is absent and ConfigDigest is the digest of the
	// empty list (W21: gitleaks without a .gitleaksignore).
	// unrecorded: the tool honors it but the pack could not enumerate the
	// files; the context is unknown.
	ConfigNotRead    = "not_read"
	ConfigRecorded   = "recorded"
	ConfigUnrecorded = "unrecorded"

	// GoVetRulePack is the synthesized rule pack of go vet: the toolchain's
	// default analyzer set, fixed by the go version (already compared as the
	// tool version). The go vet v0.1 document has no rule pack and stays
	// byte-identical, so the decoder (and a comparison reading a revision
	// before builder 4) synthesizes it.
	GoVetRulePack        = "codeye-go-vet-default"
	GoVetRulePackVersion = "1"
)

// rulePackPattern is the envelope's rule pack id (obsdoc component id).
var rulePackPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// RuleContext is the rule pack, its selection and the repository
// configuration and in-source suppression handling of one tool row.
// ConfigPaths and ConfigDigest are removed from the code-free export: a
// digest over configuration bytes would let a reader confirm guessed
// content (as fingerprint_basis).
type RuleContext struct {
	RulePack           string   `json:"rule_pack"`
	RulePackVersion    string   `json:"rule_pack_version"`
	Selection          []string `json:"selection"`
	RepositoryConfig   string   `json:"repository_config"`
	SourceSuppressions string   `json:"source_suppressions"`
	Config             string   `json:"config"`
	ConfigPaths        []string `json:"config_paths,omitempty"`
	ConfigDigest       string   `json:"config_digest,omitempty"`
	// UnreportedSuppressionRules are the rules whose suppressions stay
	// hidden although SourceSuppressions is reported (obsdoc.RulePack).
	UnreportedSuppressionRules []string `json:"unreported_suppression_rules,omitempty"`
}

// ConfigDigest is the SHA-256 over the configuration files a tool read:
// each snapshot path with its manifest SHA-256, in path order.
func ConfigDigest(files []obsdoc.ConfigFile) string {
	h := sha256.New()
	h.Write([]byte("codeye-rule-config-v1\x00"))
	for _, f := range files {
		h.Write([]byte(f.Path + "\x00" + f.SHA256 + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ruleContextOf is the row context of an envelope rule pack.
func ruleContextOf(rp obsdoc.RulePack) *RuleContext {
	c := &RuleContext{RulePack: rp.ID, RulePackVersion: rp.Version, Selection: append([]string{}, rp.Selection...), RepositoryConfig: rp.RepositoryConfig,
		SourceSuppressions: rp.SourceSuppressions, Config: ConfigNotRead}
	if len(rp.UnreportedSuppressionRules) > 0 {
		c.UnreportedSuppressionRules = append([]string{}, rp.UnreportedSuppressionRules...)
	}
	if rp.RepositoryConfig == obsdoc.RepoConfigHonored {
		c.Config = ConfigUnrecorded
		if rp.ConfigFiles != nil {
			c.Config = ConfigRecorded
			for _, f := range rp.ConfigFiles {
				c.ConfigPaths = append(c.ConfigPaths, f.Path)
			}
			c.ConfigDigest = ConfigDigest(rp.ConfigFiles)
		}
	}
	return c
}

// GoVetRuleContext is the synthesized context of every go vet row.
func GoVetRuleContext() RuleContext {
	return RuleContext{RulePack: GoVetRulePack, RulePackVersion: GoVetRulePackVersion, Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigNone,
		SourceSuppressions: obsdoc.SuppressNone, Config: ConfigNotRead}
}

// RuleContextOf is the rule context of a row: the recorded one, or for a go
// vet row without one (a revision before builder 4) the synthesized one.
// nil means unknown.
func RuleContextOf(tc ToolCoverage) *RuleContext {
	if tc.RuleContext != nil {
		return tc.RuleContext
	}
	if tc.Analyzer == govet.AnalyzerID {
		c := GoVetRuleContext()
		return &c
	}
	return nil
}

// Known reports a context that fully identifies the rules applied.
func (c RuleContext) Known() bool {
	return c.Config == ConfigNotRead || (c.Config == ConfigRecorded && len(c.ConfigDigest) == 64)
}

// Key is the equality key of a context (the full configuration digest).
func (c RuleContext) Key() string {
	return strings.Join([]string{c.RulePack, c.RulePackVersion, strings.Join(c.Selection, "\x01"), c.RepositoryConfig, c.SourceSuppressions, c.Config, c.ConfigDigest,
		strings.Join(c.UnreportedSuppressionRules, "\x01")}, "\x00")
}

// Label is a short display form of the context.
func (c RuleContext) Label() string {
	s := c.RulePack + "@" + c.RulePackVersion
	if len(c.Selection) > 0 {
		s += " [" + strings.Join(c.Selection, " ") + "]"
	}
	s += " config=" + c.RepositoryConfig
	switch {
	case c.Config == ConfigRecorded && len(c.ConfigDigest) >= 12:
		s += ":" + c.ConfigDigest[:12]
	case c.Config == ConfigUnrecorded:
		s += ":unrecorded"
	}
	s += " suppressions=" + c.SourceSuppressions
	if len(c.UnreportedSuppressionRules) > 0 {
		s += " unreported=" + strings.Join(c.UnreportedSuppressionRules, ",")
	}
	return s
}

// HidesSuppressionsOf reports that a vanished finding of rule may have been
// suppressed in source without the tool reporting it: the tool honors
// suppressions, or reports them except for this rule.
func (c RuleContext) HidesSuppressionsOf(rule string) bool {
	return c.SourceSuppressions == obsdoc.SuppressHonored || slices.Contains(c.UnreportedSuppressionRules, rule)
}

// SuppressedFingerprints are the fp2 identities of the observations of
// analyzer that the tool reported as suppressed in source, computed with the
// one fingerprint definition (ToolFingerprint) so a vanished finding can be
// matched against them. They are derived, never stored.
func SuppressedFingerprints(r Report, analyzer string) map[string]bool {
	out := map[string]bool{}
	for _, o := range r.Observations {
		if !o.Suppressed || o.Tool != analyzer || o.Location == nil || o.RawMessage == nil {
			continue
		}
		fp, _ := ToolFingerprint(analyzer, o.RuleID, o.Location.Path, "", *o.RawMessage)
		out[fp] = true
	}
	return out
}
