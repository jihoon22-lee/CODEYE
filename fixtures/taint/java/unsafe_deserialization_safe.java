package fixtures;

import java.io.IOException;
import java.io.ObjectInputFilter;
import java.io.ObjectInputStream;
import javax.servlet.http.HttpServletRequest;

public class UnsafeDeserializationSafe {
    public Object load(HttpServletRequest req) throws IOException, ClassNotFoundException {
        ObjectInputStream in = new ObjectInputStream(req.getInputStream());
        in.setObjectInputFilter(ObjectInputFilter.Config.createFilter("fixtures.Profile;!*"));
        return in.readObject();
    }

    public String name(HttpServletRequest req) throws IOException {
        return new String(req.getInputStream().readAllBytes());
    }
}
