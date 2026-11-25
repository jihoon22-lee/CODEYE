package fixtures;

import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.List;
import java.util.Map;
import javax.servlet.http.HttpServletRequest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class SqlInjectionSafe {
    private Connection conn;
    private JdbcTemplate jdbc;

    public ResultSet user(HttpServletRequest req) throws SQLException {
        PreparedStatement ps = conn.prepareStatement("SELECT * FROM users WHERE id = ?");
        ps.setString(1, req.getParameter("id"));
        return ps.executeQuery();
    }

    public ResultSet byNumber(HttpServletRequest req) throws SQLException {
        java.sql.Statement st = conn.createStatement();
        return st.executeQuery("SELECT * FROM users WHERE id = " + Integer.parseInt(req.getParameter("id")));
    }

    public boolean audit(HttpServletRequest req) throws SQLException {
        java.util.Enumeration<String> names = req.getHeaderNames();
        PreparedStatement ps = conn.prepareStatement("INSERT INTO audit (header) VALUES (?)");
        ps.setString(1, names.hasMoreElements() ? names.nextElement() : "");
        return ps.execute();
    }

    @GetMapping("/users")
    public List<Map<String, Object>> page(@RequestParam("limit") String limit) {
        return jdbc.queryForList("SELECT * FROM users LIMIT " + Integer.parseInt(limit));
    }

    @GetMapping("/named")
    public List<Map<String, Object>> named(@RequestParam("name") String name) {
        return jdbc.queryForList("SELECT * FROM users WHERE name = ?", name);
    }
}
