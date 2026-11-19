package main

// w21NotConfigured are the W21 analyzers (lizard, jscpd, gitleaks; docs/ANALYZERS.md)
// a node without their tool flags reports, in catalog order.
const w21NotConfigured = "lizard:not_configured,jscpd:not_configured,gitleaks:not_configured"
