package fixtures;

import java.sql.Connection;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.List;
import java.util.Map;
import javax.servlet.http.HttpServletRequest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class SqlInjection {
    private Connection conn;
    private JdbcTemplate jdbc;

    public ResultSet user(HttpServletRequest req) throws SQLException {
        Statement st = conn.createStatement();
        return st.executeQuery("SELECT * FROM users WHERE id = '" + req.getParameter("id") + "'"); // expect: codeye.java.sql-injection
    }

    @GetMapping("/users")
    public List<Map<String, Object>> sorted(@RequestParam("sort") String sort) {
        String sql = String.format("SELECT * FROM users ORDER BY %s", sort);
        return jdbc.queryForList(sql); // expect: codeye.java.sql-injection
    }
}
