package fixtures;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import javax.servlet.http.HttpServletRequest;

public class PathTraversalSafe {
    private static final Path ROOT = Paths.get("/srv/files");

    public byte[] read(HttpServletRequest req) throws IOException {
        Path name = Paths.get(req.getParameter("name")).getFileName();
        return Files.readAllBytes(ROOT.resolve(name));
    }

    public byte[] download(HttpServletRequest req) throws IOException {
        Path p = ROOT.resolve(req.getParameter("file")).normalize();
        if (!p.startsWith(ROOT)) {
            throw new IOException("outside the root");
        }
        return Files.readAllBytes(p);
    }

    public byte[] index() throws IOException {
        return Files.readAllBytes(ROOT.resolve("index.txt"));
    }
}
