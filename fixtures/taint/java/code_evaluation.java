package fixtures;

import javax.script.ScriptEngine;
import javax.script.ScriptEngineManager;
import javax.script.ScriptException;
import org.springframework.expression.ExpressionParser;
import org.springframework.expression.spel.standard.SpelExpressionParser;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class CodeEvaluation {
    private final ExpressionParser parser = new SpelExpressionParser();

    @GetMapping("/calc")
    public Object calc(@RequestParam("expr") String expr) throws ScriptException {
        ScriptEngine engine = new ScriptEngineManager().getEngineByName("javascript");
        return engine.eval(expr); // expect: codeye.java.code-evaluation
    }

    @GetMapping("/value")
    public Object value(@RequestParam("expr") String expr) {
        return parser.parseExpression(expr).getValue(); // expect: codeye.java.code-evaluation
    }
}
