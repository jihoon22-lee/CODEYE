# Gateway design notes

These notes record decisions for the billing gateway. They are long on purpose; reviewers usually read only the sections that apply to the module under review.

## Decision 1: request identity

Status: accepted in revision 1.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 1: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 2: downstream timeouts

Status: accepted in revision 2.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 2: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 3: retry budget

Status: accepted in revision 3.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 3: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 4: error reporting

Status: accepted in revision 4.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 4: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 5: identifier normalization

Status: accepted in revision 5.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 5: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 6: module boundaries

Status: accepted in revision 6.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 6: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 7: request identity

Status: accepted in revision 7.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 7: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 8: downstream timeouts

Status: accepted in revision 8.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 8: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 9: retry budget

Status: accepted in revision 9.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 9: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 10: error reporting

Status: accepted in revision 10.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 10: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 11: identifier normalization

Status: accepted in revision 11.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 11: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 12: module boundaries

Status: accepted in revision 12.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 12: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 13: request identity

Status: accepted in revision 13.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 13: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 14: downstream timeouts

Status: accepted in revision 14.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 14: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 15: retry budget

Status: accepted in revision 15.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 15: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 16: error reporting

Status: accepted in revision 16.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 16: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 17: identifier normalization

Status: accepted in revision 17.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 17: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 18: module boundaries

Status: accepted in revision 18.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 18: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 19: request identity

Status: accepted in revision 19.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 19: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 20: downstream timeouts

Status: accepted in revision 20.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 20: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 21: retry budget

Status: accepted in revision 21.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 21: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 22: error reporting

Status: accepted in revision 22.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 22: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 23: identifier normalization

Status: accepted in revision 23.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 23: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 24: module boundaries

Status: accepted in revision 24.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 24: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 25: request identity

Status: accepted in revision 25.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 25: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 26: downstream timeouts

Status: accepted in revision 26.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 26: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 27: retry budget

Status: accepted in revision 27.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 27: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 28: error reporting

Status: accepted in revision 28.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 28: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 29: identifier normalization

Status: accepted in revision 29.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 29: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 30: module boundaries

Status: accepted in revision 30.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 30: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 31: request identity

Status: accepted in revision 31.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 31: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 32: downstream timeouts

Status: accepted in revision 32.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 32: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 33: retry budget

Status: accepted in revision 33.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 33: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 34: error reporting

Status: accepted in revision 34.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 34: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 35: identifier normalization

Status: accepted in revision 35.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 35: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 36: module boundaries

Status: accepted in revision 36.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 36: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 37: request identity

Status: accepted in revision 37.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 37: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 38: downstream timeouts

Status: accepted in revision 38.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 38: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 39: retry budget

Status: accepted in revision 39.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 39: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 40: error reporting

Status: accepted in revision 40.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 40: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 41: identifier normalization

Status: accepted in revision 41.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 41: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 42: module boundaries

Status: accepted in revision 42.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 42: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 43: request identity

Status: accepted in revision 43.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 43: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 44: downstream timeouts

Status: accepted in revision 44.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 44: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 45: retry budget

Status: accepted in revision 45.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 45: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 46: error reporting

Status: accepted in revision 46.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 46: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 47: identifier normalization

Status: accepted in revision 47.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 47: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 48: module boundaries

Status: accepted in revision 48.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 48: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 49: request identity

Status: accepted in revision 49.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 49: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 50: downstream timeouts

Status: accepted in revision 50.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 50: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 51: retry budget

Status: accepted in revision 51.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 51: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 52: error reporting

Status: accepted in revision 52.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 52: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 53: identifier normalization

Status: accepted in revision 53.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 53: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 54: module boundaries

Status: accepted in revision 54.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 54: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 55: request identity

Status: accepted in revision 55.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 55: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 56: downstream timeouts

Status: accepted in revision 56.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 56: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 57: retry budget

Status: accepted in revision 57.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 57: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 58: error reporting

Status: accepted in revision 58.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 58: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 59: identifier normalization

Status: accepted in revision 59.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 59: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 60: module boundaries

Status: accepted in revision 60.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 60: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 61: request identity

Status: accepted in revision 61.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 61: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 62: downstream timeouts

Status: accepted in revision 62.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 62: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 63: retry budget

Status: accepted in revision 63.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 63: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 64: error reporting

Status: accepted in revision 64.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 64: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 65: identifier normalization

Status: accepted in revision 65.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 65: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 66: module boundaries

Status: accepted in revision 66.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 66: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 67: request identity

Status: accepted in revision 67.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 67: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 68: downstream timeouts

Status: accepted in revision 68.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 68: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 69: retry budget

Status: accepted in revision 69.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 69: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 70: error reporting

Status: accepted in revision 70.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 70: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 71: identifier normalization

Status: accepted in revision 71.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 71: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 72: module boundaries

Status: accepted in revision 72.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 72: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 73: request identity

Status: accepted in revision 73.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 73: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 74: downstream timeouts

Status: accepted in revision 74.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 74: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 75: retry budget

Status: accepted in revision 75.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 75: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 76: error reporting

Status: accepted in revision 76.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 76: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 77: identifier normalization

Status: accepted in revision 77.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 77: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 78: module boundaries

Status: accepted in revision 78.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 78: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 79: request identity

Status: accepted in revision 79.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 79: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 80: downstream timeouts

Status: accepted in revision 80.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 80: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 81: retry budget

Status: accepted in revision 81.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 81: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 82: error reporting

Status: accepted in revision 82.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 82: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 83: identifier normalization

Status: accepted in revision 83.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 83: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 84: module boundaries

Status: accepted in revision 84.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 84: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 85: request identity

Status: accepted in revision 85.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 85: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 86: downstream timeouts

Status: accepted in revision 86.

Each downstream call has its own deadline derived from the caller context. A call that exceeds the deadline is reported as failed, and the caller decides whether the overall operation may continue.

Consequence 86: code that touches downstream timeouts keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 87: retry budget

Status: accepted in revision 87.

The gateway retries a failed call up to three times. The payment client in svc/b is the exception: a payment may not be repeated because the endpoint has no idempotency support.

Consequence 87: code that touches retry budget keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 88: error reporting

Status: accepted in revision 88.

Errors are returned to the caller unchanged so that the original cause stays visible. The gateway never converts an error into a success value and never hides the last failure.

Consequence 88: code that touches error reporting keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 89: identifier normalization

Status: accepted in revision 89.

Account identifiers are compared after trimming surrounding spaces and folding to lower case. An identifier that becomes empty after normalization is invalid and must be rejected by the caller.

Consequence 89: code that touches identifier normalization keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 90: module boundaries

Status: accepted in revision 90.

The root module owns forwarding, svc/a owns identifier handling and svc/b owns payment calls. Each module has its own go.mod and may require a different Go version.

Consequence 90: code that touches module boundaries keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.

## Decision 91: request identity

Status: accepted in revision 91.

Every billing request carries a request identifier chosen by the caller. The gateway uses it only to correlate log lines; it is not an idempotency key, and the payment service does not deduplicate on it.

Consequence 91: code that touches request identity keeps this rule, and a change to the rule needs a new decision that names the modules it affects and the callers that must be updated together.
