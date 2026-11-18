# Payment client

Call the payment endpoint exactly once per request. Never retry a failed
call: a repeated call may charge the customer twice. Return the error to the
caller instead.
