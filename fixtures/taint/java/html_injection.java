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

    private static void greet(HttpServletResponse resp, String name) throws IOException {
        resp.getWriter().println("<p>Welcome " + name + "</p>"); // expect: codeye.java.html-injection
    }

    public void welcome(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        greet(resp, req.getParameter("name"));
    }

    public void status(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        String state = req.getParameter("state");
        resp.getWriter().printf(java.util.Locale.ROOT, "<p>State: %s</p>", state); // expect: codeye.java.html-injection
    }

    public void raw(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        char[] text = req.getHeader("X-Title").toCharArray();
        resp.getWriter().write(text, 0, text.length); // expect: codeye.java.html-injection
    }

    public void fields(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        java.util.Enumeration<String> names = req.getParameterNames();
        while (names.hasMoreElements()) {
            resp.getWriter().println("<li>" + names.nextElement() + "</li>"); // expect: codeye.java.html-injection
        }
    }

    public void badge(HttpServletRequest req, HttpServletResponse resp) throws IOException {
        resp.getWriter().format("<span class=\"%s\"></span>", req.getHeader("X-Class")); // expect: codeye.java.html-injection
    }
}
