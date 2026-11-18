// Package gateway forwards billing requests to a downstream service.
package gateway

// MaxRetries is how many times Forward repeats a failed call, following the
// repository guidance to retry failed calls up to 3 times.
const MaxRetries = 3

// Forward calls send once and then retries up to MaxRetries times while it
// fails. It returns nil on the first success, otherwise the last error.
func Forward(send func() error) error {
	err := send()
	for i := 0; i < MaxRetries && err != nil; i++ {
		err = send()
	}
	return err
}
