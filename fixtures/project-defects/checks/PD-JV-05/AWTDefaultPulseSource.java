package org.pushingpixels.radiance.animation.internal.swing;

import org.pushingpixels.radiance.animation.api.RadianceAnimationCortex;

/**
 * Stand-in for the screen-device query. The class of the same name in the analysed sources reads the refresh rate
 * of every local screen device, which fails on a machine without a display; this stand-in keeps the same type and
 * uses that class's own fallback of 60 pulses per second. It is placed on the source path ahead of the analysed
 * sources only for this check.
 */
public class AWTDefaultPulseSource extends RadianceAnimationCortex.FixedRatePulseSource {
    public AWTDefaultPulseSource() {
        super(1000 / 60);
    }
}
