# Queue

A job queue with a concurrency limit. A failing job is retried at most three times; `drain` resolves after every job settled.
