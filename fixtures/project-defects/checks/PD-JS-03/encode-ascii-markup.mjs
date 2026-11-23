// encodeNonAsciiHTML must escape the XML special characters wherever they appear.
// Copied to <work>/check/encode-ascii-markup.mjs and run straight from the TypeScript
// sources: node strips the types, and the hook maps the sources' relative `.js`
// imports to the `.ts` files next to them.
import { registerHooks } from "node:module";

registerHooks({
    resolve(specifier, context, nextResolve) {
        if (specifier.startsWith(".") && specifier.endsWith(".js")) {
            try {
                return nextResolve(specifier, context);
            } catch {
                return nextResolve(`${specifier.slice(0, -3)}.ts`, context);
            }
        }
        return nextResolve(specifier, context);
    },
});

const { encodeNonAsciiHTML } = await import("../src/encode.ts");

const cases = [
    ["<b>", "&lt;b&gt;"],
    ["a & b", "a &amp; b"],
    ['"x"', "&quot;x&quot;"],
    ["it's", "it&apos;s"],
    ["<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"],
    ['<img src=x onerror="go()">', "&lt;img src=x onerror=&quot;go()&quot;&gt;"],
    ["café <i>", "caf&eacute; &lt;i&gt;"],
    ["plain text", "plain text"],
];

let failed = 0;
for (const [input, expected] of cases) {
    const got = encodeNonAsciiHTML(input);
    if (got !== expected) {
        failed++;
        console.log(
            `MISMATCH encodeNonAsciiHTML(${JSON.stringify(input)}) = ${JSON.stringify(got)}, expected ${JSON.stringify(expected)}`,
        );
    }
}
if (failed) {
    process.exitCode = 1;
} else {
    console.log(`all ${cases.length} cases agree`);
}
