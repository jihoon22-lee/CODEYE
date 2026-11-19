package pack

import "strings"

// SeriesTested reports a version of the tested release series ("1.26" for
// 1.26.x). The bare series ("1.26") is not a version of it.
func SeriesTested(version, series string) bool { return strings.HasPrefix(version, series+".") }
