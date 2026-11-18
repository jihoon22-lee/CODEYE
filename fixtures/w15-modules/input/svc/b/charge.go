// Package b charges a customer through the payment endpoint.
package b

// Charge calls pay exactly once and returns its error unchanged. It never
// retries, because a repeated call may charge the customer twice.
func Charge(pay func() error) error {
	return pay()
}
