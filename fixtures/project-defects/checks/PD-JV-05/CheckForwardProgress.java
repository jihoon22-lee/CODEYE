package org.pushingpixels.radiance.animation.api;

import java.util.Arrays;
import java.util.concurrent.CountDownLatch;

/**
 * Plays a timeline of 1,000,000 ms and drives the engine by hand: the pulse thread is parked on its first pulse,
 * and before each of five updates the engine's last-iteration timestamp is moved 250,000 ms into the past. A
 * forward timeline must then advance by a quarter of its duration per update and reach the end. The margin is
 * large enough that the time spent between moving the timestamp and the update does not change the outcome.
 */
public class CheckForwardProgress {
    public static void main(String[] args) throws Exception {
        final CountDownLatch parked = new CountDownLatch(1);
        final CountDownLatch never = new CountDownLatch(1);
        RadianceAnimationCortex.setPulseSource(() -> {
            parked.countDown();
            try {
                never.await();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        });

        Timeline timeline = Timeline.builder().setDuration(1_000_000L).build();
        timeline.play();
        // the pulse thread has read its start time and waits for a pulse that never comes
        parked.await();

        TimelineEngine engine = TimelineEngine.getInstance();
        engine.updateTimelines(); // READY -> PLAYING_FORWARD, no progress on this update
        float[] seen = new float[5];
        for (int i = 0; i < seen.length; i++) {
            synchronized (TimelineEngine.LOCK) {
                engine.lastIterationTimeStamp = System.currentTimeMillis() - 250_000L;
            }
            engine.updateTimelines();
            seen[i] = timeline.durationFraction;
        }
        System.out.println("duration fraction after each update of 250000 ms: " + Arrays.toString(seen)
                + "; state " + timeline.getState());
        if (seen[0] < 0.2f || seen[0] >= 1.0f || seen[seen.length - 1] < 1.0f) {
            System.out.println("CHECK-VIOLATION: a forward timeline did not advance by a quarter per update: "
                    + Arrays.toString(seen));
            System.exit(1);
        }
        System.out.println("ok: the timeline advanced by a quarter per update and reached the end");
    }
}
