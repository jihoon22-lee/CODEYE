import dev.failsafe.CircuitBreaker;
import dev.failsafe.Failsafe;

import java.lang.management.ManagementFactory;
import java.lang.management.ThreadInfo;
import java.lang.management.ThreadMXBean;
import java.time.Duration;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Fixed interleaving on one circuit breaker. Thread A fails an execution; the breaker opens and calls the
 * delay function, which parks until released. While A is parked, thread B calls open() and the program
 * waits until B is blocked on the breaker's monitor. Then A is released and both threads finish. The open
 * listener must run exactly once: B has to see that A already opened the breaker.
 */
public class CheckOpenTransition {
  private static final long WAIT_SECONDS = 20;

  public static void main(String[] args) throws Exception {
    AtomicInteger openEvents = new AtomicInteger();
    CountDownLatch delayEntered = new CountDownLatch(1);
    CountDownLatch release = new CountDownLatch(1);

    CircuitBreaker<Object> breaker = CircuitBreaker.builder()
      .withFailureThreshold(1)
      .withDelayFn(ctx -> {
        delayEntered.countDown();
        if (!release.await(WAIT_SECONDS, TimeUnit.SECONDS))
          throw new IllegalStateException("delay function was never released");
        return Duration.ofMinutes(5);
      })
      .onOpen(e -> openEvents.incrementAndGet())
      .build();

    Thread failing = new Thread(() -> {
      try {
        Failsafe.with(breaker).run(() -> {
          throw new IllegalStateException("execution failure");
        });
      } catch (Throwable expected) {
        // the failure itself is the point of this execution
      }
    }, "failing-execution");
    failing.start();

    if (!delayEntered.await(WAIT_SECONDS, TimeUnit.SECONDS)) {
      System.out.println("SETUP-ERROR: the delay function was not reached");
      release.countDown();
      System.exit(2);
    }

    Thread opener = new Thread(breaker::open, "direct-open");
    opener.start();

    ThreadMXBean threads = ManagementFactory.getThreadMXBean();
    int monitor = System.identityHashCode(breaker);
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(WAIT_SECONDS);
    boolean blockedOnBreaker = false;
    while (System.nanoTime() < deadline && opener.isAlive()) {
      ThreadInfo info = threads.getThreadInfo(opener.threadId());
      if (info != null && info.getThreadState() == Thread.State.BLOCKED && info.getLockInfo() != null
        && info.getLockInfo().getIdentityHashCode() == monitor) {
        blockedOnBreaker = true;
        break;
      }
      Thread.sleep(5);
    }
    String openerOutcome = blockedOnBreaker ? "blocked on the breaker monitor"
      : opener.isAlive() ? "still running" : "finished while the first transition was in progress";

    release.countDown();
    failing.join(TimeUnit.SECONDS.toMillis(WAIT_SECONDS));
    opener.join(TimeUnit.SECONDS.toMillis(WAIT_SECONDS));
    if (failing.isAlive() || opener.isAlive()) {
      System.out.println("SETUP-ERROR: threads did not finish");
      System.exit(2);
    }

    int events = openEvents.get();
    System.out.println("direct open() call: " + openerOutcome + "; open listener calls: " + events
      + "; final state: " + breaker.getState());
    if (events != 1) {
      System.out.println("CHECK-VIOLATION: one CLOSED->OPEN change produced " + events + " open transitions");
      System.exit(1);
    }
    if (!blockedOnBreaker) {
      System.out.println("SETUP-ERROR: the direct open() call never waited for the breaker monitor");
      System.exit(2);
    }
    System.out.println("check passed");
  }
}
