import com.mojang.brigadier.CommandDispatcher;
import com.mojang.brigadier.exceptions.CommandSyntaxException;

import java.util.ArrayList;
import java.util.List;

import com.mojang.brigadier.builder.LiteralArgumentBuilder;

/**
 * Registers commands whose nodes require an operator source and runs them through the dispatcher with an operator
 * and with a guest. A guest must get a syntax error and must never reach a command that requires an operator.
 */
public class CheckRequirementGate {

    static final class Source {
        final String name;
        final boolean operator;

        Source(String name, boolean operator) {
            this.name = name;
            this.operator = operator;
        }
    }

    public static void main(String[] args) {
        final List<String> ran = new ArrayList<String>();
        CommandDispatcher<Source> dispatcher = new CommandDispatcher<Source>();
        dispatcher.register(LiteralArgumentBuilder.<Source>literal("stop")
                .requires(s -> s.operator)
                .executes(c -> { ran.add(c.getSource().name + ":stop"); return 1; }));
        dispatcher.register(LiteralArgumentBuilder.<Source>literal("tools")
                .then(LiteralArgumentBuilder.<Source>literal("wipe")
                        .requires(s -> s.operator)
                        .executes(c -> { ran.add(c.getSource().name + ":tools wipe"); return 1; })));
        dispatcher.register(LiteralArgumentBuilder.<Source>literal("op")
                .requires(s -> s.operator)
                .then(LiteralArgumentBuilder.<Source>literal("all")
                        .executes(c -> { ran.add(c.getSource().name + ":op all"); return 1; })));

        Source operator = new Source("operator", true);
        Source guest = new Source("guest", false);
        String[] inputs = {"stop", "tools wipe"};

        for (String input : new String[] {"stop", "tools wipe", "op all"}) {
            try {
                dispatcher.execute(input, operator);
            } catch (CommandSyntaxException e) {
                System.out.println("SETUP: the operator could not run '" + input + "': " + e.getMessage());
                System.exit(2);
            }
        }
        // Informational only: input that continues below a node the guest may not use.
        try {
            dispatcher.execute("op all", guest);
            System.out.println("guest 'op all' ran");
        } catch (CommandSyntaxException e) {
            System.out.println("guest 'op all' refused: " + e.getMessage());
        }
        List<String> admitted = new ArrayList<String>();
        for (String input : inputs) {
            try {
                dispatcher.execute(input, guest);
                admitted.add(input);
            } catch (CommandSyntaxException e) {
                System.out.println("guest '" + input + "' refused: " + e.getMessage());
            }
        }
        System.out.println("commands run: " + ran);
        if (!admitted.isEmpty()) {
            System.out.println("CHECK-VIOLATION: a source without the required permission ran " + admitted.size()
                    + " operator command(s): " + admitted);
            System.exit(1);
        }
        System.out.println("ok: only the operator reached the operator commands");
    }
}
