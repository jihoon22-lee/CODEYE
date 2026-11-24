package fixtures;

import java.io.IOException;
import java.io.PrintWriter;
import javax.servlet.http.HttpServletRequest;
import javax.servlet.http.HttpServletResponse;

public class HtmlInjection {
    public void hello(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        resp.setContentType("text/html");
        PrintWriter out = resp.getWriter();
        out.println("<h1>Hello " + req.getParameter("name") + "</h1>"); // expect: codeye.java.html-injection
    }

    public void search(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        resp.getWriter().write("<p>Results for " + req.getParameter("q") + "</p>"); // expect: codeye.java.html-injection
    }
}
