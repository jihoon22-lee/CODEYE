package fixtures;

import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.client.RestTemplate;

@RestController
public class SsrfSafe {
    private final RestTemplate rest = new RestTemplate();

    @GetMapping("/user")
    public String user(@RequestParam("id") String id) {
        return rest.getForObject("https://api.example.com/users/" + id, String.class);
    }

    @GetMapping("/search")
    public int search(@RequestParam("q") String q) throws IOException {
        URL url = new URL("https://search.example.com/?q=" + URLEncoder.encode(q, StandardCharsets.UTF_8));
        try (InputStream in = url.openStream()) {
            return in.read();
        }
    }

    @GetMapping("/health")
    public String health() {
        return rest.getForObject("https://status.example.com/health", String.class);
    }
}
