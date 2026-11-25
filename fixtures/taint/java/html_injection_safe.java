package fixtures;

import java.io.IOException;
import java.io.PrintWriter;
import javax.servlet.http.HttpServletRequest;
import javax.servlet.http.HttpServletResponse;
import org.springframework.web.util.HtmlUtils;

public class HtmlInjectionSafe {
    public void hello(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        resp.setContentType("text/html");
        PrintWriter out = resp.getWriter();
        out.println("<h1>Hello " + HtmlUtils.htmlEscape(req.getParameter("name")) + "</h1>");
    }

    public void echo(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        resp.setContentType("application/json");
        resp.getWriter().write("{\"q\":" + quote(req.getParameter("q")) + "}");
    }

    public void card(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        String title = org.springframework.web.util.HtmlUtils.htmlEscape(req.getParameter("title"));
        resp.getWriter().printf("<h2>%s</h2>", title);
        String note = org.owasp.esapi.ESAPI.encoder().encodeForHTML(req.getParameter("note"));
        resp.getWriter().println("<p>" + note + "</p>");
        String tag = org.apache.commons.lang.StringEscapeUtils.escapeHtml(req.getHeader("X-Tag"));
        resp.getWriter().write("<span>" + tag + "</span>");
    }

    private static String quote(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
