package com.llama4j;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Set;
import java.util.TreeSet;
import java.util.random.RandomGenerator;

/**
 * ToppSampler on fixed probability vectors without ties: every token it can return must belong to the
 * nucleus, i.e. the smallest prefix of the tokens sorted by descending probability whose probability sum
 * exceeds topp. The random draw is swept over [0, 1) so the check does not depend on a seed.
 */
public class CheckToppNucleus {

  /** A generator whose nextFloat(bound) returns a chosen fraction of the bound. */
  static final class FixedDraw implements RandomGenerator {
    float fraction;

    @Override
    public long nextLong() {
      return (long) (fraction * (1L << 53)) << 11;
    }

    @Override
    public float nextFloat(float bound) {
      return fraction * bound;
    }
  }

  static Set<Integer> nucleus(float[] p, float topp) {
    Integer[] order = new Integer[p.length];
    for (int i = 0; i < p.length; i++) {
      order[i] = i;
    }
    Arrays.sort(order, (a, b) -> Float.compare(p[b], p[a]));
    Set<Integer> out = new TreeSet<>();
    float sum = 0f;
    for (Integer i : order) {
      out.add(i);
      sum += p[i];
      if (sum > topp) {
        break;
      }
    }
    return out;
  }

  public static void main(String[] args) {
    float[][] vectors = {
        {0.50f, 0.30f, 0.15f, 0.05f},
        {0.035f, 0.131f, 0.130f, 0.068f, 0.014f, 0.107f, 0.209f, 0.306f},
        {0.236f, 0.018f, 0.059f, 0.273f, 0.137f, 0.224f, 0.053f},
        {0.90f, 0.06f, 0.04f},
    };
    float[] topps = {0.75f, 0.82f, 0.90f, 0.85f};
    int failing = 0;
    for (int v = 0; v < vectors.length; v++) {
      float[] p = vectors[v];
      Set<Integer> expected = nucleus(p, topps[v]);
      Set<Integer> seen = new TreeSet<>();
      FixedDraw draw = new FixedDraw();
      ToppSampler sampler = new ToppSampler(p.length, topps[v], draw);
      for (int k = 0; k < 200; k++) {
        draw.fraction = (k + 0.5f) / 200f;
        seen.add(sampler.sampleToken(new ArrayFloatTensor(p.clone())));
      }
      List<Integer> outside = new ArrayList<>(seen);
      outside.removeAll(expected);
      System.out.println("p=" + Arrays.toString(p) + " topp=" + topps[v] + " nucleus=" + expected + " returned="
          + seen + (outside.isEmpty() ? "" : "  outside nucleus: " + outside));
      if (!outside.isEmpty()) {
        failing++;
      }
    }
    if (failing > 0) {
      System.out.println("CHECK-VIOLATION: top-p sampling returned tokens outside the nucleus for " + failing
          + " probability vector(s)");
      System.exit(1);
    }
    System.out.println("check passed");
  }
}
