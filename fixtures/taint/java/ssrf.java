package fixtures;

import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.client.RestTemplate;

@RestController
public class Ssrf {
    private final RestTemplate rest = new RestTemplate();

    @GetMapping("/proxy")
    public String proxy(@RequestParam("url") String target) {
        return rest.getForObject(target, String.class); // expect: codeye.java.ssrf
    }

    @GetMapping("/fetch")
    public int fetch(@RequestParam("host") String host) throws IOException {
        URL url = new URL("https://" + host + "/status");
        try (InputStream in = url.openStream()) { // expect: codeye.java.ssrf
            return in.read();
        }
    }
}
