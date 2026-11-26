import picocli.CommandLine.Model.CommandSpec;
import picocli.CommandLine.Model.OptionSpec;
import picocli.codegen.annotation.processing.AbstractCommandSpecProcessor;

import javax.annotation.processing.RoundEnvironment;
import javax.lang.model.element.Element;
import javax.lang.model.element.TypeElement;
import javax.tools.Diagnostic;
import javax.tools.DiagnosticCollector;
import javax.tools.JavaCompiler;
import javax.tools.JavaFileObject;
import javax.tools.SimpleJavaFileObject;
import javax.tools.ToolProvider;
import java.net.URI;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Runs the annotation processor model builder over a command class with one {@code @Mixin} field whose type
 * declares an {@code @Option}, and checks that the command built by the processor carries that option, as
 * picocli does at runtime when the mixin is added to the command.
 */
public class CheckMixinOptions {

    static final class Capture extends AbstractCommandSpecProcessor {
        final Map<Element, CommandSpec> seen = new LinkedHashMap<Element, CommandSpec>();

        @Override
        protected boolean handleCommands(Map<Element, CommandSpec> commands,
                                         Set<? extends TypeElement> annotations,
                                         RoundEnvironment roundEnv) {
            seen.putAll(commands);
            return false;
        }
    }

    static final class Source extends SimpleJavaFileObject {
        final String text;

        Source(String className, String text) {
            super(URI.create("string:///" + className.replace('.', '/') + Kind.SOURCE.extension), Kind.SOURCE);
            this.text = text;
        }

        @Override
        public CharSequence getCharContent(boolean ignoreEncodingErrors) {
            return text;
        }
    }

    public static void main(String[] args) {
        JavaCompiler javac = ToolProvider.getSystemJavaCompiler();
        if (javac == null) {
            System.out.println("SETUP: no system Java compiler");
            System.exit(2);
        }
        List<JavaFileObject> units = new ArrayList<JavaFileObject>();
        units.add(new Source("sample.Tool",
                "package sample;\n"
                + "import picocli.CommandLine.Command;\n"
                + "import picocli.CommandLine.Mixin;\n"
                + "@Command(name = \"tool\")\n"
                + "public class Tool {\n"
                + "    @Mixin Shared shared;\n"
                + "}\n"));
        units.add(new Source("sample.Shared",
                "package sample;\n"
                + "import picocli.CommandLine.Option;\n"
                + "public class Shared {\n"
                + "    @Option(names = \"--level\") int level;\n"
                + "}\n"));
        DiagnosticCollector<JavaFileObject> diagnostics = new DiagnosticCollector<JavaFileObject>();
        Capture processor = new Capture();
        List<String> options = Arrays.asList("-proc:only", "-classpath", System.getProperty("java.class.path"));
        JavaCompiler.CompilationTask task = javac.getTask(null, null, diagnostics, options, null, units);
        task.setProcessors(Collections.singletonList(processor));
        boolean ok = task.call();
        for (Diagnostic<? extends JavaFileObject> d : diagnostics.getDiagnostics()) {
            if (d.getKind() == Diagnostic.Kind.ERROR) {
                System.out.println("SETUP: compiler error: " + d.getMessage(null));
                ok = false;
            }
        }
        if (!ok) {
            System.exit(2);
        }

        CommandSpec tool = null;
        CommandSpec sharedInMap = null;
        for (Map.Entry<Element, CommandSpec> entry : processor.seen.entrySet()) {
            if ("sample.Tool".equals(entry.getKey().toString())) {
                tool = entry.getValue();
            } else if ("sample.Shared".equals(entry.getKey().toString())) {
                sharedInMap = entry.getValue();
            }
        }
        if (tool == null) {
            System.out.println("SETUP: the processor built no model for sample.Tool: " + processor.seen.keySet());
            System.exit(2);
        }
        List<String> names = optionNames(tool);
        System.out.println("mixins of sample.Tool: " + tool.mixins().keySet() + ", options: " + names);
        CommandSpec held = tool.mixins().get("shared");
        if (held != null && sharedInMap != null) {
            System.out.println("spec held by the mixin entry is the map's spec for sample.Shared: " + (held == sharedInMap)
                    + "; options of the held spec: " + optionNames(held)
                    + "; options of the map's spec: " + optionNames(sharedInMap));
        }
        if (!tool.mixins().containsKey("shared")) {
            System.out.println("CHECK-VIOLATION: the command model has no mixin named shared");
            System.exit(1);
        }
        if (!names.contains("--level")) {
            System.out.println("CHECK-VIOLATION: the command model lacks the mixin option --level");
            System.exit(1);
        }
        System.out.println("ok: the command model carries the mixin option");
    }

    static List<String> optionNames(CommandSpec spec) {
        List<String> names = new ArrayList<String>();
        for (OptionSpec option : spec.options()) {
            names.add(option.longestName());
        }
        return names;
    }
}
