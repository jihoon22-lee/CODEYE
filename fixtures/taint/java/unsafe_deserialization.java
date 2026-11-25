package fixtures;

import java.io.IOException;
import java.io.ObjectInputStream;
import javax.servlet.http.HttpServletRequest;

public class UnsafeDeserialization {
    public Object load(HttpServletRequest req) throws IOException, ClassNotFoundException {
        ObjectInputStream in = new ObjectInputStream(req.getInputStream());
        return in.readObject(); // expect: codeye.java.unsafe-deserialization
    }
}
