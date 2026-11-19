# Billing gateway

This repository holds a small billing gateway and two services under `svc/`.

## Guidance for every module

When a downstream call fails, retry failed calls up to 3 times before
returning the last error to the caller. A call therefore runs at most four
times in total.
