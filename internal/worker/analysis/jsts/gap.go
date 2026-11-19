package jsts

import "slices"

// GapCodes are the TypeScript diagnostics that say a module, type package or
// project output the program needs is not available: dependency gaps, never
// defects (docs/ANALYZERS.md#의존성-공백). A snapshot never holds node_modules, so they are the
// normal case for any project with dependencies. Measured with TypeScript
// 6.0.3 (fixtures and the CODEYE web sources without node_modules): 2307,
// 2591, 2593, 2688, 2875, 2882, 6305, 7026. The others are the same messages
// for other module kinds and runtimes (2792 moduleResolution hint, 7016
// untyped JavaScript module, 2580 node, 2582 test runner, 2867/2868 Bun).
var GapCodes = []int{2307, 2580, 2582, 2591, 2593, 2688, 2792, 2867, 2868, 2875, 2882, 6305, 7016, 7026}

// IsGap reports a dependency-gap diagnostic code.
func IsGap(code int) bool { return slices.Contains(GapCodes, code) }

// DeprecationCodes are TypeScript 6.0's "option is deprecated and will stop
// functioning in TypeScript 7.0" errors. The options still work in 6.0; tsc
// runs with --ignoreDeprecations 6.0, and the type-aware ESLint program,
// which cannot take that flag, records them as a limitation instead.
var DeprecationCodes = []int{5101, 5107}

// IsDeprecation reports a TypeScript 6 deprecation diagnostic.
func IsDeprecation(code int) bool { return slices.Contains(DeprecationCodes, code) }
