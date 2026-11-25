package main

// structureNotConfigured are the structure and secret analyzers (lizard, jscpd, gitleaks; docs/ANALYZERS.md)
// a node without their tool flags reports, in catalog order.
const structureNotConfigured = "lizard:not_configured,jscpd:not_configured,gitleaks:not_configured,opengrep:not_configured"
