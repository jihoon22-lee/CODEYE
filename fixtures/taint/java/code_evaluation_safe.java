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
public class CodeEvaluationSafe {
    private final ExpressionParser parser = new SpelExpressionParser();

    @GetMapping("/double")
    public Object twice(@RequestParam("n") String n) throws ScriptException {
        ScriptEngine engine = new ScriptEngineManager().getEngineByName("javascript");
        return engine.eval("2 * " + Integer.parseInt(n));
    }

    @GetMapping("/now")
    public Object now() {
        return parser.parseExpression("T(java.time.Instant).now()").getValue();
    }
}
