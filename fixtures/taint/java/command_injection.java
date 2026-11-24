package fixtures;

import java.io.IOException;
import javax.servlet.http.HttpServletRequest;
import javax.servlet.http.HttpServletResponse;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class CommandInjection {
    public void ping(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        String host = req.getParameter("host");
        Runtime.getRuntime().exec("ping -c 1 " + host); // expect: codeye.java.command-injection
    }

    @GetMapping("/archive")
    public String archive(@RequestParam("dir") String dir) throws IOException {
        new ProcessBuilder("sh", "-c", "tar czf backup.tgz " + dir).start(); // expect: codeye.java.command-injection
        return "ok";
    }

    private Process launch(String program) throws IOException {
        return new ProcessBuilder(program, "--version").start(); // expect: codeye.java.command-injection
    }

    @GetMapping("/tool")
    public String tool(@RequestParam String name) throws IOException {
        launch(name);
        return "ok";
    }
}
