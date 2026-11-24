package fixtures;

import java.io.IOException;
import javax.servlet.http.HttpServletRequest;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class CommandInjectionSafe {
    public void ping(HttpServletRequest req) throws IOException {
        // The host is one argument of a fixed program, not shell text.
        new ProcessBuilder("ping", "-c", "1", "--", req.getParameter("host")).start();
    }

    @GetMapping("/count")
    public String count(@RequestParam("n") String n) throws IOException {
        int count = Integer.parseInt(n);
        Runtime.getRuntime().exec("seq " + count);
        return "ok";
    }

    @GetMapping("/uptime")
    public String uptime() throws IOException {
        Runtime.getRuntime().exec(new String[] {"uptime"});
        return "ok";
    }
}
