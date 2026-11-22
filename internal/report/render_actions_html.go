package report

import "html/template"

// The HTML of the action-first opening. These templates join the sectioned
// page template so they can reuse its citation and source preview blocks.
var _ = template.Must(htmlSectionsTemplate.Parse(`
{{define "action_coverage"}}{{with .Actions}}{{if .CoverageLine}}<p class="report-coverage-note" role="note"><strong>{{.CoverageLead}}</strong> {{.CoverageLine}} <a href="#coverage">{{.CoverageLabel}}</a></p>{{end}}{{end}}{{end}}
{{define "action_row"}}<div><dt>{{.Label}}</dt><dd>{{if .Text}}{{if .Inline}}{{range inlineCodeParts .Text}}{{if .Code}}<code>{{.Text}}</code>{{else}}{{.Text}}{{end}}{{end}}{{else}}{{.Text}}{{end}}{{end}}{{range .Codes}} <code>{{.}}</code>{{end}}{{range .Sources}}{{template "citation" .}}{{end}}</dd></div>{{end}}
{{define "action_block"}}{{with .Actions}}{{if not .Empty}}
<section class="report-actions" id="actions" aria-labelledby="actions-heading">
<div class="report-group" data-tone="action">
<div class="report-group-title"><span class="report-group-mark">{{reportIcon "alert-triangle"}}</span><h3 id="actions-heading">{{.Heading}}</h3></div>
<p class="report-actions-lead">{{.Lead}}</p>
{{range .Paras}}<p class="report-actions-note">{{.}}</p>
{{end}}{{range .Cards}}<article class="card report-action-card" id="{{.Anchor}}" data-finding-card="{{.FindingID}}" data-severity="{{.SeverityCode}}" data-validation="{{.ValidationCode}}">
<div class="card-body">
<header class="report-action-head">
<h4>{{.Title}}</h4>
<p class="report-action-badges"><span class="report-status-badge" data-tone="{{.Tone}}">{{.State}}</span> <span class="report-action-severity">{{.Severity}}</span></p>
</header>
{{if .Before}}<dl class="report-action-rows">{{range .Before}}{{template "action_row" .}}{{end}}</dl>{{end}}
{{if .Preview}}{{template "source_preview" .Preview}}{{if .Source.URL}}<a href="{{.Source.URL}}" class="btn btn-sm btn-ghost-primary report-source-action" target="_blank" rel="noopener noreferrer">{{reportIcon "external-link"}}{{.Source.LinkLabel}}</a>{{end}}
{{else if .Source.Location}}{{template "citation" .Source}}
{{else if .Location}}<p class="report-action-location"><code class="report-path-text">{{.Location}}</code>{{with .MoreLocations}} <small>{{.}}</small>{{end}}</p>{{end}}
{{if .After}}<dl class="report-action-rows">{{range .After}}{{template "action_row" .}}{{end}}</dl>{{end}}
{{if .EvidenceLink}}<a href="#{{.EvidenceLink}}" class="btn btn-sm btn-ghost-primary report-card-action">{{.EvidenceLabel}}{{reportIcon "chevron-right"}}</a>{{end}}
</div></article>
{{end}}</div>
{{if .MismatchHeading}}<div class="report-group" data-tone="pending" id="document-mismatches">
<div class="report-group-title"><span class="report-group-mark">{{reportIcon "file-description"}}</span><h3>{{.MismatchHeading}}</h3></div>
<p class="report-actions-lead">{{.MismatchLead}}</p>
{{range .Mismatches}}{{template "mismatch_card" .}}{{end}}
{{if .MismatchMore}}<details class="card report-disclosure"><summary>{{reportIcon "chevron-down"}} {{.MismatchMoreLabel}}</summary>
{{range .MismatchMore}}{{template "mismatch_card" .}}{{end}}</details>{{end}}
</div>{{end}}
{{if .Notices}}<div class="report-group" data-tone="neutral">
<div class="report-group-title"><span class="report-group-mark">{{reportIcon "clipboard-check"}}</span><h3>{{.NoticesHeading}}</h3></div>
<ul class="report-actions-notices">{{range .Notices}}<li>{{template "item" .}}</li>{{end}}</ul>
</div>{{end}}
{{template "candidate_fold" .Candidates}}{{template "candidate_fold" .Secondary}}
{{with .Diagnostics}}{{if .Groups}}<details class="card report-disclosure" id="{{.ID}}"><summary>{{reportIcon "chevron-down"}} {{.Label}}</summary>
<p>{{.Note}}</p>{{with .Unplaced}}<p>{{.}}</p>{{end}}
<div class="report-diag-groups">{{range .Groups}}<details class="report-diag-group"><summary><span class="report-diag-name"><code>{{.Analyzer}}</code> <code>{{.Rule}}</code></span> <span class="report-diag-count">{{.Count}}</span>{{with .Severity}} <span class="report-diag-severity">{{.}}</span>{{end}}{{with .Description}}<span class="report-diag-description">{{.}}</span>{{end}}</summary>
<ul>{{range .Places}}<li><code class="report-path-text">{{.}}</code></li>{{end}}</ul>{{with .More}}<p><small>{{.}}</small></p>{{end}}</details>
{{end}}</div></details>{{end}}{{end}}
</section>{{end}}{{end}}{{end}}
{{define "mismatch_card"}}<article class="card report-action-card report-mismatch-card" id="{{.Anchor}}" data-comparison="{{.Status}}">
<div class="card-body">
<header class="report-action-head">
<h4>{{.Title}}</h4>
<p class="report-action-badges"><span class="report-status-badge" data-tone="{{.Tone}}">{{.State}}</span> <small class="report-group-note" role="note">{{.Interpretation}}</small></p>
</header>
<dl class="report-action-rows">{{range .Rows}}{{template "action_row" .}}{{end}}</dl>
{{if .Link}}<a href="#{{.Link}}" class="btn btn-sm btn-ghost-primary report-card-action">{{.LinkLabel}}{{reportIcon "chevron-right"}}</a>{{end}}
</div></article>{{end}}
{{define "candidate_fold"}}{{if .Rows}}<details class="card report-disclosure" id="{{.ID}}"><summary>{{reportIcon "chevron-down"}} {{.Label}}</summary>
<p>{{.Note}}</p>
<ul class="report-action-list">{{range .Rows}}<li>
<div class="report-action-list-head"><strong>{{.Title}}</strong><span class="report-status-badge" data-tone="{{.Tone}}">{{.State}}</span><span class="report-action-severity">{{.Severity}}</span></div>
{{with .Location}}<code class="report-path-text">{{.}}</code>{{end}}
{{with .Next}}<p><strong>{{.Label}}</strong> {{.Text}}</p>{{end}}
{{if .Link}}<a href="#{{.Link}}" class="report-action-link">{{.LinkLabel}}</a>{{end}}
</li>{{end}}</ul></details>{{end}}{{end}}
`))
