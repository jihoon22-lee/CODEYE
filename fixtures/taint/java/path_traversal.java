package fixtures;

import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import javax.servlet.http.HttpServletRequest;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class PathTraversal {
    private static final String ROOT = "/srv/files";

    public byte[] read(HttpServletRequest req) throws IOException {
        Path p = Paths.get(ROOT, req.getParameter("name"));
        return Files.readAllBytes(p); // expect: codeye.java.path-traversal
    }

    public int first(HttpServletRequest req) throws IOException {
        try (FileInputStream in = new FileInputStream(ROOT + "/" + req.getParameter("file"))) { // expect: codeye.java.path-traversal
            return in.read();
        }
    }

    @DeleteMapping("/files/{name}")
    public boolean delete(@PathVariable String name) {
        File f = new File(ROOT, name);
        return f.delete(); // expect: codeye.java.path-traversal
    }
}
