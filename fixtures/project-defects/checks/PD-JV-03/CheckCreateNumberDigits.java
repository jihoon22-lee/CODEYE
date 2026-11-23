import java.math.BigDecimal;

import org.apache.commons.lang3.math.NumberUtils;

/**
 * NumberUtils.createNumber on decimal strings without a type suffix: the decimal value of the returned
 * Number (read back through its toString) must equal the decimal value of the input text. Short inputs
 * that a Float represents exactly in its shortest form keep working as before.
 */
public class CheckCreateNumberDigits {

  public static void main(String[] args) {
    String[] inputs = {"1234.5", "0.1", "-1234.5", "193343.82", "0.100000001490116121"};
    int mismatches = 0;
    for (String input : inputs) {
      Number n = NumberUtils.createNumber(input);
      boolean same = new BigDecimal(n.toString()).compareTo(new BigDecimal(input)) == 0;
      System.out.println(input + " -> " + n.getClass().getSimpleName() + " " + n + (same ? "" : "  (value differs)"));
      if (!same) {
        mismatches++;
      }
    }
    if (mismatches > 0) {
      System.out.println("CHECK-VIOLATION: createNumber changed the value of " + mismatches + " decimal input(s)");
      System.exit(1);
    }
    System.out.println("check passed");
  }
}
