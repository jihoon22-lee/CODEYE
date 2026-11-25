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

    public byte[] avatar(HttpServletRequest req) throws IOException {
        String name = org.apache.commons.io.FilenameUtils.getName(req.getParameter("avatar"));
        return java.nio.file.Files.readAllBytes(java.nio.file.Paths.get("/srv/avatars/" + name));
    }

    public void upload(HttpServletRequest req) throws IOException {
        String name = org.apache.commons.io.FilenameUtils.getName(req.getHeader("X-File-Name"));
        new java.io.FileOutputStream("/srv/uploads/" + name, false).close();
    }

    public boolean purge(HttpServletRequest req) {
        String name = new java.io.File(req.getParameter("purge")).getName();
        java.io.File f = new java.io.File("/srv/files", name);
        return f.delete();
    }

    public byte[] index() throws IOException {
        return Files.readAllBytes(ROOT.resolve("index.txt"));
    }
}
