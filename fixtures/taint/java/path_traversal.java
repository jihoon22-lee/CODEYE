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

    public void upload(HttpServletRequest req) throws IOException {
        String target = ROOT + "/" + req.getHeader("X-File-Name");
        new java.io.FileOutputStream(target, false).close(); // expect: codeye.java.path-traversal
    }

    public byte[] report(HttpServletRequest req) throws IOException {
        Path p = Paths.get(req.getParameter("report"));
        return Files.readAllBytes(p); // expect: codeye.java.path-traversal
    }

    public byte[] export(HttpServletRequest req) throws IOException {
        return java.nio.file.Files.readAllBytes(java.nio.file.Paths.get(req.getParameter("export"))); // expect: codeye.java.path-traversal
    }

    @DeleteMapping("/files/{name}")
    public boolean delete(@PathVariable String name) {
        File f = new File(ROOT, name);
        return f.delete(); // expect: codeye.java.path-traversal
    }
}
