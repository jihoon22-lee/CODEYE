package review

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const cargoToml = "[package]\nname = 'shop'\nversion = '0.1.0'\n"

// rustTree is a crate whose src/lib.rs is body.
func rustTree(body string) map[string]string {
	return map[string]string{"Cargo.toml": cargoToml, "src/lib.rs": body}
}

const rustPrice = "\n/// The unit price.\n#[inline]\nfn price(qty: u32) -> u32 {\n    qty * 7\n} // price end\n"

// A plain Rust call binds where Rust's name resolution puts it: the fn item
// of its module (attributes, derives and macros around it), which a glob
// use yields to and an inline module reaches through use super::*. A
// parameter, a let, closure, match or if-let pattern, or a nested fn of the
// name bind it to no project function; an explicit use, a const or a second
// conditional fn of the name leave it ambiguous; an item of another inline
// module is not in scope, nor is a call inside a macro_rules body bound.
func TestRustDefinitionLeadsFollowScoping(t *testing.T) {
	runBindCases(t, rustScopingCases())
}

// rustScopingCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func rustScopingCases() []bindCase {
	total := "#[derive(Debug)]\npub struct Order {\n    pub qty: u32,\n}\n\npub fn total(o: &Order) -> u32 {\n    println!(\"{:?}\", o);\n    price(o.qty)\n}\n"
	return []bindCase{
		{name: "same-file fn beside a derive and a macro", tree: rustTree(total + rustPrice),
			path: "src/lib.rs", line: "price(o.qty)", symbol: "price", target: "src/lib.rs", first: "#[inline]", last: "// price end"},
		{name: "a glob use yields to the item", tree: rustTree("use crate::rates::*;\n\n" + total + rustPrice),
			path: "src/lib.rs", line: "price(o.qty)", symbol: "price", target: "src/lib.rs", first: "#[inline]", last: "// price end"},
		{name: "a test module reaches the file's fn through use super::*", tree: rustTree(rustPrice + "\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn prices() {\n        assert_eq!(price(1), 7);\n    }\n}\n"),
			path: "src/lib.rs", line: "assert_eq!(price(1), 7)", symbol: "price", target: "src/lib.rs", first: "#[inline]", last: "// price end"},
		{name: "a function from a method of an impl that has a method of the name", tree: rustTree("pub struct Order;\n\nimpl Order {\n    fn price(&self, q: u32) -> u32 {\n        q\n    }\n\n    pub fn total(&self, q: u32) -> u32 {\n        price(q)\n    }\n}\n" + rustPrice),
			path: "src/lib.rs", line: "        price(q)", symbol: "price", target: "src/lib.rs", first: "#[inline]", last: "// price end"},

		{name: "parameter of the name", tree: rustTree("pub fn total(price: fn(u32) -> u32, q: u32) -> u32 {\n    price(q)\n}\n" + rustPrice),
			path: "src/lib.rs", line: "    price(q)", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "let of the name", tree: rustTree("pub fn total(q: u32) -> u32 {\n    let price = |n: u32| n * 2;\n    price(q)\n}\n" + rustPrice),
			path: "src/lib.rs", line: "    price(q)", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "closure parameter of the name", tree: rustTree("pub fn total(fs: &[fn(u32) -> u32]) -> Vec<u32> {\n    fs.iter().map(|price| price(1)).collect()\n}\n" + rustPrice),
			path: "src/lib.rs", line: "fs.iter().map(", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "match arm pattern of the name", tree: rustTree("pub fn total(op: Option<fn(u32) -> u32>, q: u32) -> u32 {\n    match op {\n        Some(price) => price(q),\n        None => 0,\n    }\n}\n" + rustPrice),
			path: "src/lib.rs", line: "Some(price) => price(q)", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "if-let pattern of the name", tree: rustTree("pub fn total(op: Option<fn(u32) -> u32>, q: u32) -> u32 {\n    if let Some(price) = op {\n        return price(q);\n    }\n    0\n}\n" + rustPrice),
			path: "src/lib.rs", line: "return price(q)", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "nested fn of the name", tree: rustTree("pub fn total(q: u32) -> u32 {\n    fn price(n: u32) -> u32 {\n        n\n    }\n    price(q)\n}\n" + rustPrice),
			path: "src/lib.rs", line: "    price(q)", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "an item of another inline module", tree: rustTree("mod rates {\n    pub fn price(qty: u32) -> u32 {\n        qty * 7\n    }\n}\n\npub fn total(q: u32) -> u32 {\n    price(q)\n}\n"),
			path: "src/lib.rs", line: "    price(q)", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "a call inside a macro_rules body", tree: rustTree("macro_rules! priced {\n    ($q:expr) => {\n        price($q)\n    };\n}\n" + rustPrice),
			path: "src/lib.rs", line: "price($q)", symbol: "price", reason: record.LeadDefinitionMissing},

		{name: "an explicit use and an item of the name", tree: rustTree("use crate::rates::price;\n\n" + total + rustPrice),
			path: "src/lib.rs", line: "price(o.qty)", symbol: "price", reason: record.LeadAmbiguous},
		{name: "a const and a fn of the name", tree: rustTree("#[allow(non_upper_case_globals)]\nconst price: u32 = 1;\n\n" + total + rustPrice),
			path: "src/lib.rs", line: "price(o.qty)", symbol: "price", reason: record.LeadAmbiguous},
		{name: "fns under two cfg attributes", tree: rustTree(total + "\n#[cfg(unix)]\nfn price(qty: u32) -> u32 {\n    qty * 7\n}\n\n#[cfg(not(unix))]\nfn price(qty: u32) -> u32 {\n    qty * 8\n}\n"),
			path: "src/lib.rs", line: "price(o.qty)", symbol: "price", reason: record.LeadAmbiguous},
		{name: "a fn under one cfg attribute", tree: rustTree(total + "\n#[cfg(feature = \"fast\")]\nfn price(qty: u32) -> u32 {\n    qty * 7\n}\n"),
			path: "src/lib.rs", line: "price(o.qty)", symbol: "price", reason: record.LeadAmbiguous},
	}
}

// Self::m, Type::m and self.m bind to the one fn m of the plain inherent
// impl blocks of the type in the file; self.m only when m takes self as the
// calling method does. A generic impl, a method only a trait impl declares,
// a type name the enclosing function rebinds (a type parameter) and two
// conditional methods are not bound.
func TestRustMethodLeadsBindInherentImpls(t *testing.T) {
	runBindCases(t, rustMethodCases())
}

// rustMethodCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func rustMethodCases() []bindCase {
	impl := "#[derive(Debug, Default)]\npub struct Order {\n    qty: u32,\n}\n\nimpl Order {\n    pub fn new(qty: u32) -> Self {\n        Self { qty }\n    } // new end\n\n" +
		"    fn price(&self) -> u32 {\n        self.qty * 7\n    } // price end\n\n    pub fn total(&self) -> u32 {\n        self.price() + Self::new(1).qty\n    }\n\n" +
		"    pub fn bump(&mut self) -> u32 {\n        self.qty += 1;\n        self.price() // after the bump\n    }\n}\n"
	return []bindCase{
		{name: "Self::new", tree: rustTree(impl), path: "src/lib.rs", line: "self.price() + Self::new(1).qty", symbol: "new",
			target: "src/lib.rs", first: "pub fn new(qty: u32)", last: "// new end"},
		{name: "Type::new from a free fn", tree: rustTree(impl + "\npub fn make() -> Order {\n    Order::new(2)\n}\n"), path: "src/lib.rs", line: "Order::new(2)", symbol: "new",
			target: "src/lib.rs", first: "pub fn new(qty: u32)", last: "// new end"},
		{name: "self.price from a &self method", tree: rustTree(impl), path: "src/lib.rs", line: "self.price() + Self::new(1).qty", symbol: "price",
			target: "src/lib.rs", first: "fn price(&self)", last: "// price end"},

		{name: "self.price from a &mut self method", tree: rustTree(impl), path: "src/lib.rs", line: "self.price() // after the bump", symbol: "price", reason: record.LeadDefinitionMissing},
		{name: "a method of a generic impl", tree: rustTree("pub struct Wrap<T>(T);\n\nimpl<T: Copy> Wrap<T> {\n    fn get(&self) -> T {\n        self.0\n    }\n\n    pub fn twice(&self) -> (T, T) {\n        (self.get(), self.get())\n    }\n}\n"),
			path: "src/lib.rs", line: "(self.get(), self.get())", symbol: "get", reason: record.LeadDefinitionMissing},
		{name: "a method only a trait impl declares", tree: rustTree("pub struct Order;\n\nimpl Clone for Order {\n    fn clone(&self) -> Self {\n        Order\n    }\n}\n\nimpl Order {\n    pub fn copy(&self) -> Self {\n        Self::clone(self)\n    }\n}\n"),
			path: "src/lib.rs", line: "Self::clone(self)", symbol: "clone", reason: record.LeadDefinitionMissing},
		{name: "a type parameter of the type's name", tree: rustTree(impl + "\npub fn make<Order: Default>() -> Order {\n    Order::new(2)\n}\n"), path: "src/lib.rs", line: "Order::new(2)", symbol: "new", reason: record.LeadDefinitionMissing},
		{name: "methods under two cfg attributes", tree: rustTree("pub struct Order;\n\n#[cfg(unix)]\nimpl Order {\n    fn price(&self) -> u32 {\n        7\n    }\n}\n\n#[cfg(not(unix))]\nimpl Order {\n    fn price(&self) -> u32 {\n        8\n    }\n}\n\nimpl Order {\n    pub fn total(&self) -> u32 {\n        self.price()\n    }\n}\n"),
			path: "src/lib.rs", line: "        self.price()", symbol: "price", reason: record.LeadAmbiguous},
	}
}

// rustWholeFile holds a raw string, a lifetime, nested block comments, a
// char literal and a division before the call it resolves.
const rustWholeFile = "/* outer /* nested */ still a comment */\npub fn total<'a>(name: &'a str, q: u32) -> u32 {\n    let _raw = r#\"quote \" inside\"#;\n    let _c = '}';\n    price(q / 2)\n}\n" + rustPrice

// The Rust reader keeps the file it reads whole: a raw string, a lifetime,
// nested block comments, a char literal and a division do not stop it.
func TestRustLeadTokensReadTheWholeFile(t *testing.T) {
	body := rustWholeFile
	s := leadStage(t, treeJob(t, rustTree(body)))
	window, anchor := leadWindow(t, s, "src/lib.rs", "price(q / 2)", 0)
	l := resolveOne(t, s, record.LeadNeedDefinition, "price", window, anchor)
	if !l.Resolved() || l.Targets[0].Path != "src/lib.rs" || l.Targets[0].Start != evidenceLine(t, body, "#[inline]") || l.Targets[0].End != evidenceLine(t, body, "// price end") {
		t.Fatalf("lead %+v", l)
	}
}
