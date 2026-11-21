package report

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"html/template"
	"strings"
	"time"
)

//go:embed reportui/icons/*.svg
var reportIcons embed.FS

//go:embed reportui/font.css
var reportFontCSS string

//go:embed reportui/noto-sans-kr.woff2
var reportFont []byte

//go:embed reportui/tabler.css
var reportTablerCSS string

//go:embed reportui/report.css
var reportThemeCSS string

//go:embed reportui/export.css
var reportExportCSS string

//go:embed reportui/workbench.css
var reportWorkbenchCSS string

//go:embed reportui/export-navigation.js
var reportExportNavigation string

func exportNavigationHash() string {
	hash := sha256.Sum256([]byte(reportExportNavigation))
	return base64.StdEncoding.EncodeToString(hash[:])
}

//go:embed reportui/LICENSE.tabler-core
var tablerCoreNotice string

//go:embed reportui/LICENSE.tabler-icons
var tablerIconsNotice string

//go:embed reportui/LICENSE.bootstrap
var bootstrapNotice string

//go:embed reportui/LICENSE.noto-sans-kr
var notoSansKRNotice string

// The same font and components are bundled by the web app. The saved HTML has
// no companion files, so use a data URL for the font in this renderer.
var reportFontURL = "data:font/woff2;base64," + base64.StdEncoding.EncodeToString(reportFont)
var sectionedExportCSS = strings.Replace(reportFontCSS, "./noto-sans-kr.woff2", reportFontURL, 1) +
	reportTablerCSS + reportThemeCSS + reportExportCSS + reportWorkbenchCSS

type reportTimestamp struct {
	Label, Original, Display string
	at                       time.Time
}
type reportTiming struct {
	Times    []reportTimestamp
	Duration string
}
type reportAssetNotice struct{ Name, Text string }

func (v secView) HeaderSource() string {
	if p := v.R.Project; p != nil {
		return p.Source
	}
	return ""
}

func (secView) AssetNotices() []reportAssetNotice {
	return []reportAssetNotice{
		{"Tabler Core", tablerCoreNotice},
		{"Tabler Icons", tablerIconsNotice},
		{"Bootstrap", bootstrapNotice},
		{"Noto Sans KR", notoSansKRNotice},
	}
}

func recordedTimestamp(label, raw string) reportTimestamp {
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return reportTimestamp{}
	}
	return reportTimestamp{label, raw, at.UTC().Format("2006-01-02 15:04:05 MST"), at}
}

func (v secView) HeaderTiming() reportTiming {
	l, r := v.L, v.R
	var out reportTiming
	var end reportTimestamp
	if p := r.Project; p != nil {
		start := recordedTimestamp(l.Label("identity.start"), p.StartedAt)
		end = recordedTimestamp(l.Label("identity.finish"), p.FinishedAt)
		if start.Display != "" {
			out.Times = append(out.Times, start)
		}
		if start.Display != "" && end.Display != "" && !end.at.Before(start.at) {
			seconds := int64(end.at.Sub(start.at).Round(time.Second) / time.Second)
			var units []string
			if hours := seconds / 3600; hours > 0 {
				units = append(units, tf(l, "identity.duration_hours", hours))
			}
			if minutes := seconds / 60 % 60; minutes > 0 {
				units = append(units, tf(l, "identity.duration_minutes", minutes))
			}
			out.Duration = strings.Join(append(units, tf(l, "identity.duration_seconds", seconds%60)), " ")
		}
	}
	if end.Display == "" {
		end = recordedTimestamp(l.Label("identity.published"), r.CreatedAt)
	}
	if end.Display != "" {
		out.Times = append(out.Times, end)
	}
	return out
}

func findingCitation(l Locale, evidence Evidence) sourceCitation {
	if evidence.Location == nil {
		return sourceCitation{}
	}
	loc := evidence.Location
	return newSourceCitation(l, evidence.Kind, loc.Path, loc.StartLine, loc.EndLine, evidence.Excerpt)
}

func reportIcon(name string) template.HTML {
	data, err := reportIcons.ReadFile("reportui/icons/" + name + ".svg")
	if err != nil {
		return ""
	}
	// These SVGs are local, versioned template assets. The caller supplies an
	// icon name from the renderer, never markup from a report.
	return template.HTML(`<span aria-hidden="true" class="report-icon">` + string(data) + `</span>`)
}

func sectionIcon(key string) string {
	switch key {
	case "overview":
		return "dashboard"
	case SectionEnvironment:
		return "server"
	case SectionDocuments:
		return "file-description"
	case SectionStaticAnalysis:
		return "terminal-2"
	case SectionMetrics:
		return "chart-bar"
	case SectionSecrets:
		return "shield-lock"
	case SectionCodeReview:
		return "code"
	case SectionGaps:
		return "alert-triangle"
	default:
		return "list-details"
	}
}

func groupIcon(tone string) string {
	switch tone {
	case "action":
		return "alert-triangle"
	case "strength":
		return "check"
	case "pending":
		return "clock"
	default:
		return "clipboard-check"
	}
}
