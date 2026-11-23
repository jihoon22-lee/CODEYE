package pond

import (
	"runtime"
	"testing"
	"time"
)

// Producers keep a bounded queue busy while the test samples WaitingTasks.
// A sample can never be larger than the queue size.
func TestWaitingTasksStaysWithinQueueSize(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 4 {
		runtime.GOMAXPROCS(4)
	}
	const queueSize = 64
	deadline := time.Now().Add(8 * time.Second)
	for round := 0; time.Now().Before(deadline); round++ {
		pool := NewPool(4, WithQueueSize(queueSize))
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < 100000; i++ {
				pool.Go(func() {})
			}
		}()
		var worst uint64
	sample:
		for {
			if waiting := pool.WaitingTasks(); waiting > worst {
				worst = waiting
			}
			select {
			case <-done:
				break sample
			default:
			}
		}
		pool.StopAndWait()
		if worst > queueSize {
			t.Fatalf("round %d: WaitingTasks reported %d with a queue of %d", round, worst, queueSize)
		}
	}
}
