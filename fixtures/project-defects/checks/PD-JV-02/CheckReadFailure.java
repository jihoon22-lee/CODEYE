import java.io.IOException;
import java.io.Reader;
import java.io.UncheckedIOException;
import java.util.ArrayList;
import java.util.List;

import de.siegmar.fastcsv.reader.CsvReader;
import de.siegmar.fastcsv.reader.CsvRecord;

/**
 * Reads CSV from a Reader that delivers two complete lines and part of a third, then fails. Iterating the
 * CsvReader must surface the read failure to the caller; quietly ending after the complete lines is a
 * violation, because the caller cannot tell the truncated result from a complete file.
 */
public class CheckReadFailure {

  /** Hands out the given text once, then throws on every further read. */
  static final class FailingReader extends Reader {
    private final char[] text;
    private boolean delivered;

    FailingReader(String text) {
      this.text = text.toCharArray();
    }

    @Override
    public int read(char[] buf, int off, int len) throws IOException {
      if (!delivered) {
        delivered = true;
        int n = Math.min(len, text.length);
        System.arraycopy(text, 0, buf, off, n);
        return n;
      }
      throw new IOException("simulated device failure");
    }

    @Override
    public void close() {
    }
  }

  public static void main(String[] args) throws Exception {
    List<String> seen = new ArrayList<>();
    Throwable failure = null;
    try (CsvReader<CsvRecord> csv = CsvReader.builder().ofCsvRecord(new FailingReader("id,name\n1,alpha\n2,be"))) {
      for (CsvRecord record : csv) {
        seen.add(String.join("|", record.getFields()));
      }
    } catch (UncheckedIOException e) {
      failure = e;
    }
    System.out.println("records before the failure: " + seen);
    if (failure == null) {
      System.out.println("CHECK-VIOLATION: iteration ended normally although the source failed after "
        + seen.size() + " records");
      System.exit(1);
    }
    Throwable cause = failure.getCause();
    if (cause == null || !"simulated device failure".equals(cause.getMessage())) {
      System.out.println("SETUP-ERROR: unexpected failure " + failure);
      System.exit(2);
    }
    System.out.println("read failure reported: " + failure.getMessage());
    System.out.println("check passed");
  }
}
