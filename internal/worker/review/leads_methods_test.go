package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// tsJob and pyJob are a class whose run calls its own method helper; the
// others the same with a private, a static or a name-mangled helper.
const (
	tsJob        = "export class Job {\n  helper(x: number) {\n    return x + 1;\n  } // helper end\n\n  run(x: number) {\n    return this.helper(x);\n  }\n}\n"
	tsPrivateJob = "export class Job {\n  private helper(x: number) {\n    return x + 1;\n  } // helper end\n\n  run(x: number) {\n    return this.helper(x);\n  }\n}\n"
	tsStaticJob  = "export class Job {\n  static helper(x: number) {\n    return x + 1;\n  } // helper end\n}\n\nexport function run(x: number) {\n  return Job.helper(x);\n}\n"
	pyJob        = "class Job:\n    def helper(self, x):\n        return x + 1  # helper end\n\n    def run(self, x):\n        return self.helper(x)\n"
	pyMangledJob = "class Job:\n    def __helper(self, x):\n        return x + 1  # helper end\n\n    def run(self, x):\n        return self.__helper(x)\n"
	pyStaticJob  = "class Job:\n    @staticmethod\n    def helper(x):\n        return x + 1  # helper end\n\n\ndef run(x):\n    return Job.helper(x)\n"
)

// A call through this, self or a class's name binds to the method its class
// body declares only when nothing else can answer it at run time. A
// private method (this.#m, TypeScript private), a name-mangled one
// (self.__m) and a static one through the name of a class the file declares
// or imports (JavaScript static, Python staticmethod or classmethod) bind to
// their class's declaration, whatever other classes declare. An ordinary
// method binds when its class declares the name once, as a method, and no
// source of the language writes a member of the name through any receiver,
// patches a class or declares a member of the name in another class:
// sources outside the scope, in a directory the review does not read
// (.github) or on a secondary path are read for this and do not stop it. An
// instance's own members of computed names (this[k] = ..., Object.assign(this,
// ...), setattr(self, k, v), an update of self.__dict__) do not stop it: the
// resolver follows the declaration, as an editor does. this must be the
// instance of a method of the class, through arrow functions only (not a
// nested function, a static method, an object's method or a field's
// initializer); self the first parameter of a plain method, bound nowhere
// else (a nested function's closure keeps it). A subclass's override, an
// instance or class assignment, an annotation (a dataclass field), a write
// of the name through an attribute dictionary, a constructor parameter
// property, a getter beside the method, a prototype's patch (Object.assign
// onto one, a computed member of one, its replacement, setPrototypeOf),
// setattr with a computed name on anything but the instance (a class, cls,
// type(self), a receiver it cannot tell) or a same-named method in another
// class is ambiguous; a getter or property alone, a field, a decorated
// method, an inherited method and a receiver that is not the instance
// resolve nothing; a withheld source holding the name or a word of a
// class's patch leaves the lead unanswered.
func TestMethodLeadsBindInAClosedWorld(t *testing.T) {
	runBindCases(t, methodCases())
}

// methodCases lists the test's cases; the lead reader fuzz tests seed with
// their trees.
func methodCases() []bindCase {
	first, last := "helper(x: number) {", "// helper end"
	return []bindCase{
		{name: "an ordinary method", tree: map[string]string{"app/job.ts": tsJob},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},
		{name: "an ordinary method from an arrow function", tree: map[string]string{"app/job.ts": strings.Replace(tsJob, "run(x: number) {\n    return this.helper(x);", "run(xs: number[]) {\n    return xs.map((x) => this.helper(x));", 1)},
			path: "app/job.ts", line: "this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},
		{name: "a private method beside another class's", tree: map[string]string{"app/job.js": "export class Job {\n  #helper(x) {\n    return x + 1;\n  } // helper end\n\n  run(x) {\n    return this.#helper(x);\n  }\n}\n",
			"app/other.js": "export class Other {\n  #helper() {\n    return 0;\n  }\n\n  helper() {\n    return 1;\n  }\n}\n"},
			path: "app/job.js", line: "return this.#helper(x)", symbol: "helper", target: "app/job.js", first: "#helper(x) {", last: "// helper end"},
		{name: "a TypeScript private method beside another class's method of the name", tree: map[string]string{"app/job.ts": tsPrivateJob,
			"app/report.ts": "export class Report {\n  helper(x: number) {\n    return x * 2;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: "private helper(x: number) {", last: "// helper end"},
		{name: "a static method through its class's name", tree: map[string]string{"app/job.ts": tsStaticJob,
			"app/report.ts": "export class Report {\n  static helper(x: number) {\n    return x * 2;\n  }\n}\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", target: "app/job.ts", first: "static helper(x: number) {", last: "// helper end"},
		{name: "a static method of an imported class", tree: map[string]string{"app/run.ts": "import { Job } from './job.js';\n\nexport function run(x: number) {\n  return Job.helper(x);\n}\n",
			"app/job.ts": "export class Job {\n  static helper(x: number) {\n    return x + 1;\n  } // helper end\n}\n"},
			path: "app/run.ts", line: "return Job.helper(x)", symbol: "helper", target: "app/job.ts", first: "static helper(x: number) {", last: "// helper end"},
		{name: "an ordinary method beside sources the review does not read or on a secondary path", tree: map[string]string{"app/job.ts": tsJob,
			".github/scripts/release.js": "console.log('release');\n", "examples/demo.ts": "export class Demo {\n  show() {\n    return 1;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},

		{name: "a static method beside merges into fresh objects", tree: map[string]string{"app/job.ts": tsStaticJob +
			"\nexport function merged(a: object, b: object) {\n  return [Object.assign({}, a, b), Object.assign([], a), Object.assign(new Set<number>(), a), Object.defineProperty(new Map(), 'size', {})];\n}\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", target: "app/job.ts", first: "static helper(x: number) {", last: "// helper end"},

		{name: "a TypeScript private method beside a JavaScript subclass's override", tree: map[string]string{"app/job.ts": tsPrivateJob,
			"app/fast.js": "import { Job } from './job.js';\n\nexport class FastJob extends Job {\n  helper(x) {\n    return x;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a TypeScript private method written in another module", tree: map[string]string{"app/job.ts": tsPrivateJob,
			"app/patch.ts": "import { Job } from './job.js';\n\nexport function patch(job: Job) {\n  (job as any).helper = (x: number) => x;\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method assigned in another module", tree: map[string]string{"app/job.ts": tsStaticJob, "app/patch.ts": "import { Job } from './job.js';\n\nJob.helper = (x: number) => x;\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method beside a merge into its class", tree: map[string]string{"app/job.ts": tsStaticJob + "\nObject.assign(Job, overrides);\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method beside a merge into a member of a new object", tree: map[string]string{"app/job.ts": tsStaticJob + "\nObject.assign(new Registry().target, overrides);\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method beside a merge into an element of a new array", tree: map[string]string{"app/job.ts": tsStaticJob + "\nObject.assign([Job][0], overrides);\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method beside a computed member of this written in its class", tree: map[string]string{"app/job.js": "export class Job {\n  static configure(options) {\n    for (const k in options) {\n      this[k] = options[k];\n    }\n  }\n\n  static helper(x) {\n    return x + 1;\n  }\n}\n\nexport function run(x) {\n  return Job.helper(x);\n}\n"},
			path: "app/job.js", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method beside a computed member of this written in another module", tree: map[string]string{"app/job.ts": tsStaticJob,
			"app/options.js": "export class Options {\n  constructor(o) {\n    for (const k in o) {\n      this[k] = o[k];\n    }\n  }\n}\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", target: "app/job.ts", first: "static helper(x: number) {", last: "// helper end"},
		{name: "a static method beside a merge into this in a static method", tree: map[string]string{"app/job.js": "export class Job {\n  static configure(options) {\n    Object.assign(this, options);\n  }\n\n  static helper(x) {\n    return x + 1;\n  }\n}\n\nexport function run(x) {\n  return Job.helper(x);\n}\n"},
			path: "app/job.js", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method of an imported class beside a property defined on another object in its file", tree: map[string]string{"app/run.ts": "import { Job } from './job.js';\n\nexport function run(x: number) {\n  return Job.helper(x);\n}\n",
			"app/job.ts": "export class Job {\n  static helper(x: number) {\n    return x + 1;\n  }\n}\n\nObject.defineProperty(registry, 'job', { value: Job });\n"},
			path: "app/run.ts", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a subclass's override", tree: map[string]string{"app/job.ts": tsJob,
			"app/fast.ts": "import { Job } from './job.js';\n\nexport class FastJob extends Job {\n  helper(x: number) {\n    return x;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a subclass's override on a secondary path", tree: map[string]string{"app/job.ts": tsJob,
			"examples/custom.ts": "import { Job } from '../app/job.js';\n\nclass Custom extends Job {\n  helper(x: number) {\n    return x;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a same-named method in another class", tree: map[string]string{"app/job.ts": tsJob,
			"app/report.ts": "export class Report {\n  helper(x: number) {\n    return x * 2;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "an instance assignment", tree: map[string]string{"app/job.js": "export class Job {\n  constructor(fast) {\n    if (fast) {\n      this.helper = (x) => x;\n    }\n  }\n\n  helper(x) {\n    return x + 1;\n  }\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a getter beside the method", tree: map[string]string{"app/job.js": "export class Job {\n  helper(x) {\n    return x + 1;\n  }\n\n  get helper() {\n    return (x) => x;\n  }\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a constructor parameter property of the name in another class", tree: map[string]string{"app/job.ts": tsJob,
			"app/wrap.ts": "export class Wrap {\n  constructor(private helper: (x: number) => number) {}\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "Object.assign onto a prototype", tree: map[string]string{"app/job.ts": tsJob, "app/mixin.ts": "import { Job } from './job.js';\n\nObject.assign(Job.prototype, extras);\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a withheld source without the name", tree: map[string]string{"app/job.ts": tsJob, "app/keys.ts": "export const region = 'eu';\n"}, withheld: []string{"app/keys.ts"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},
		{name: "a withheld source holding the name", tree: map[string]string{"app/job.ts": tsJob,
			"app/keyed.ts": "import { Job } from './job.js';\n\nexport class KeyedJob extends Job {\n  helper(x: number) {\n    return x;\n  }\n}\n"}, withheld: []string{"app/keyed.ts"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadWithheld},
		{name: "a nested function with its own this", tree: map[string]string{"app/job.js": "export class Job {\n  helper(x) {\n    return x + 1;\n  }\n\n  run(xs) {\n    return xs.map(function (x) {\n      return this.helper(x);\n    });\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "this in a static method beside an instance method of the name", tree: map[string]string{"app/job.js": "export class Job {\n  helper(x) {\n    return x + 1;\n  }\n\n  static run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "this in an object's method", tree: map[string]string{"app/job.js": "export class Job {\n  helper(x) {\n    return x + 1;\n  }\n}\n\nexport const runner = {\n  run(x) {\n    return this.helper(x);\n  },\n};\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "this in a field's initializer", tree: map[string]string{"app/job.js": "export class Job {\n  helper(x) {\n    return x + 1;\n  }\n\n  run = (x) => this.helper(x);\n}\n"},
			path: "app/job.js", line: "run = (x) => this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a getter alone", tree: map[string]string{"app/job.js": "export class Job {\n  get helper() {\n    return (x) => x + 1;\n  }\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a field of the name", tree: map[string]string{"app/job.js": "export class Job {\n  helper = (x) => x + 1;\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a decorated method", tree: map[string]string{"app/job.ts": strings.Replace(tsJob, "  helper(x: number) {", "  @memo\n  helper(x: number) {", 1)},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "an assignment through another receiver", tree: map[string]string{"app/job.ts": tsJob,
			"app/wire.ts": "import { Job } from './job.js';\n\nexport function wire(job: Job) {\n  job.helper = (x: number) => x;\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a string-keyed assignment through another receiver", tree: map[string]string{"app/job.ts": tsJob,
			"app/wire.js": "export function wire(job) {\n  job['helper'] = (x) => x;\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "Object.assign onto this in the class", tree: map[string]string{"app/job.js": "export class Job {\n  constructor(options) {\n    Object.assign(this, options);\n  }\n\n  helper(x) {\n    return x + 1;\n  } // helper end\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", target: "app/job.js", first: "helper(x) {", last: "// helper end"},
		{name: "a computed member of this assigned in the class", tree: map[string]string{"app/job.js": "export class Job {\n  constructor(options) {\n    for (const key of Object.keys(options)) {\n      this[key] = options[key];\n    }\n  }\n\n  helper(x) {\n    return x + 1;\n  } // helper end\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", target: "app/job.js", first: "helper(x) {", last: "// helper end"},
		{name: "Object.defineProperty with a computed name onto this in a base class", tree: map[string]string{"app/job.ts": tsJob,
			"app/base.js": "export class Base {\n  constructor(name, f) {\n    Object.defineProperty(this, name, { value: f });\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},
		{name: "a computed member of this assigned in a base class", tree: map[string]string{"app/job.ts": tsJob,
			"app/base.js": "export class Base {\n  constructor(options) {\n    for (const k of Object.keys(options)) {\n      this[k] = options[k];\n    }\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},
		{name: "a withheld source that merges into an instance", tree: map[string]string{"app/job.ts": tsJob, "app/options.js": "export class Options {\n  constructor(o) {\n    Object.assign(this, o);\n  }\n}\n"},
			withheld: []string{"app/options.js"}, path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: first, last: last},
		{name: "Object.assign onto a base class's prototype", tree: map[string]string{"app/job.ts": tsJob, "app/mixin.js": "import { Base } from './base.js';\n\nObject.assign(Base.prototype, mixin);\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a computed member of a prototype assigned", tree: map[string]string{"app/job.ts": tsJob,
			"app/mixin.js": "export function mix(Base, methods) {\n  for (const k of Object.keys(methods)) {\n    Base.prototype[k] = methods[k];\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a prototype replaced", tree: map[string]string{"app/job.ts": tsJob, "app/legacy.js": "export function Legacy() {}\n\nLegacy.prototype = { run() { return 1; } };\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a prototype set", tree: map[string]string{"app/job.ts": tsJob, "app/wire.js": "export function wire(job, other) {\n  Object.setPrototypeOf(job, other);\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "Object.defineProperty of the name onto this", tree: map[string]string{"app/job.ts": tsJob,
			"app/base.js": "export class Base {\n  constructor(f) {\n    Object.defineProperty(this, 'helper', { value: f });\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "Object.assign of the name onto this", tree: map[string]string{"app/job.ts": tsJob, "app/base.js": "export class Base {\n  constructor(helper) {\n    Object.assign(this, { helper });\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a withheld source that may patch a prototype", tree: map[string]string{"app/job.ts": tsJob, "app/mixin.js": "Object.assign(Base.prototype, mixin);\n"},
			withheld: []string{"app/mixin.js"}, path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadWithheld},
		{name: "a delete of the method from the prototype", tree: map[string]string{"app/job.ts": tsJob, "app/patch.js": "import { Job } from './job.js';\n\ndelete Job.prototype.helper;\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a field of the name beside the method", tree: map[string]string{"app/job.js": "export class Job {\n  helper = (x) => x;\n\n  helper(x) {\n    return x + 1;\n  }\n\n  run(x) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.js", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a field of the name in another class", tree: map[string]string{"app/job.ts": tsJob, "app/other.ts": "export class Other {\n  helper = (x: number) => x;\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a declared field of the name in another class", tree: map[string]string{"app/job.ts": tsJob, "app/other.ts": "export class Other {\n  helper?: (x: number) => number;\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a class expression's method of the name", tree: map[string]string{"app/job.ts": tsJob, "app/other.js": "export const Other = class {\n  helper(x) {\n    return x;\n  }\n};\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a default-exported class's method of the name", tree: map[string]string{"app/job.ts": tsJob, "app/other.js": "export default class {\n  helper(x) {\n    return x;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a mixin's class expression of the name", tree: map[string]string{"app/job.ts": tsJob,
			"app/mixin.js": "export const Fast = (Base) => class extends Base {\n  helper(x) {\n    return x;\n  }\n};\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "overload signatures before the implementation", tree: map[string]string{"app/job.ts": "export class Job {\n  helper(x: number): number;\n  helper(x: string): string;\n  helper(x: any) {\n    return x;\n  } // helper end\n\n  run(x: number) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", target: "app/job.ts", first: "helper(x: any) {", last: "// helper end"},
		{name: "an inherited method", tree: map[string]string{"app/job.ts": "export class Base {\n  helper(x: number) {\n    return x;\n  }\n}\n\nexport class Job extends Base {\n  run(x: number) {\n    return this.helper(x);\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},

		{name: "an ordinary Python method", tree: map[string]string{"app/job.py": pyJob},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "an ordinary Python method from a nested function", tree: map[string]string{"app/job.py": "class Job:\n    def helper(self, x):\n        return x + 1  # helper end\n\n    def run(self, xs):\n        def one(x):\n            return self.helper(x)\n        return [one(x) for x in xs]\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "a name-mangled method beside another class's", tree: map[string]string{"app/job.py": pyMangledJob,
			"app/other.py": "class Other:\n    def __helper(self):\n        return 0\n"},
			path: "app/job.py", line: "return self.__helper(x)", symbol: "__helper", target: "app/job.py", first: "def __helper(self, x):", last: "# helper end"},
		{name: "a staticmethod through its class's name", tree: map[string]string{"app/job.py": pyStaticJob,
			"app/report.py": "class Report:\n    def helper(self, x):\n        return x * 2\n"},
			path: "app/job.py", line: "return Job.helper(x)", symbol: "helper", target: "app/job.py", first: "@staticmethod", last: "# helper end"},
		{name: "a classmethod of an imported class", tree: map[string]string{"app/run.py": "from .job import Job\n\n\ndef run(x):\n    return Job.make(x)\n",
			"app/job.py": "class Job:\n    @classmethod\n    def make(cls, x):\n        return cls()  # make end\n"},
			path: "app/run.py", line: "return Job.make(x)", symbol: "make", target: "app/job.py", first: "@classmethod", last: "# make end"},

		{name: "a name-mangled method written by its mangled name in another module", tree: map[string]string{"app/job.py": pyMangledJob,
			"app/patch.py": "from .job import Job\n\n\ndef patch(job):\n    job._Job__helper = abs\n"},
			path: "app/job.py", line: "return self.__helper(x)", symbol: "__helper", reason: record.LeadAmbiguous},
		{name: "a staticmethod assigned in another module", tree: map[string]string{"app/job.py": pyStaticJob, "app/patch.py": "from .job import Job\n\nJob.helper = staticmethod(abs)\n"},
			path: "app/job.py", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a Python subclass's override", tree: map[string]string{"app/job.py": pyJob,
			"app/fast.py": "from .job import Job\n\n\nclass FastJob(Job):\n    def helper(self, x):\n        return x\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a dataclass field of the name in a subclass", tree: map[string]string{"app/job.py": pyJob,
			"app/fast.py": "from dataclasses import dataclass\n\nfrom .job import Job\n\n\n@dataclass\nclass FastJob(Job):\n    helper: object\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a same-named Python method in another class", tree: map[string]string{"app/job.py": pyJob,
			"app/report.py": "class Report:\n    def helper(self, x):\n        return x * 2\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a Python instance assignment", tree: map[string]string{"app/job.py": "class Job:\n    def __init__(self, fast):\n        if fast:\n            self.helper = abs\n\n    def helper(self, x):\n        return x + 1\n\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a class attribute assigned in another module", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "from .job import Job\n\nJob.helper = print\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "from .job import Job\n\n\ndef patch(name, f):\n    setattr(Job, name, f)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a withheld Python source without the name", tree: map[string]string{"app/job.py": pyJob, "app/keys.py": "REGION = 'eu'\n"}, withheld: []string{"app/keys.py"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "a withheld Python source holding the name", tree: map[string]string{"app/job.py": pyJob,
			"app/keyed.py": "from .job import Job\n\n\nclass KeyedJob(Job):\n    def helper(self, x):\n        return x\n"}, withheld: []string{"app/keyed.py"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadWithheld},
		{name: "self in a staticmethod", tree: map[string]string{"app/job.py": "class Job:\n    def helper(self, x):\n        return x + 1\n\n    @staticmethod\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a reassigned self", tree: map[string]string{"app/job.py": "class Job:\n    def helper(self, x):\n        return x + 1\n\n    def run(self, x, other):\n        if other:\n            self = other\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a property of the name", tree: map[string]string{"app/job.py": "class Job:\n    @property\n    def helper(self):\n        return abs\n\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "setattr of the name through another receiver", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    setattr(job, 'helper', abs)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "an attribute assignment through another receiver", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    job.helper = abs\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a delete of the method from the class", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "from .job import Job\n\ndel Job.helper\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name on self in the class", tree: map[string]string{"app/job.py": "class Job:\n    def __init__(self, **options):\n        for k, v in options.items():\n            setattr(self, k, v)\n\n    def helper(self, x):\n        return x + 1  # helper end\n\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "an update of the instance's __dict__ in a base class", tree: map[string]string{"app/job.py": pyJob, "app/base.py": "class Base:\n    def __init__(self, **options):\n        self.__dict__.update(options)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "object.__setattr__ with a computed name on self", tree: map[string]string{"app/job.py": pyJob, "app/base.py": "class Base:\n    def __init__(self, name, f):\n        object.__setattr__(self, name, f)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "super().__setattr__ with a computed name in a base class", tree: map[string]string{"app/job.py": pyJob,
			"app/base.py": "class Frozen:\n    def __setattr__(self, name, value):\n        if getattr(self, '_frozen', False):\n            raise AttributeError(name)\n        super().__setattr__(name, value)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "a withheld Python source that updates an instance's __dict__", tree: map[string]string{"app/job.py": pyJob, "app/options.py": "class Options:\n    def __init__(self, **o):\n        self.__dict__.update(o)\n"},
			withheld: []string{"app/options.py"}, path: "app/job.py", line: "return self.helper(x)", symbol: "helper", target: "app/job.py", first: "def helper(self, x):", last: "# helper end"},
		{name: "setattr with a computed name on cls", tree: map[string]string{"app/job.py": pyJob, "app/register.py": "def register(cls):\n    for name, f in HOOKS.items():\n        setattr(cls, name, f)\n    return cls\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name on type(self)", tree: map[string]string{"app/job.py": pyJob, "app/base.py": "class Base:\n    def install(self, name, f):\n        setattr(type(self), name, f)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name on a receiver it cannot tell", tree: map[string]string{"app/job.py": pyJob, "app/util.py": "def assign(target, name, value):\n    setattr(target, name, value)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name on a classmethod's first parameter named self", tree: map[string]string{"app/job.py": pyJob,
			"app/base.py": "class Base:\n    @classmethod\n    def configure(self, **options):\n        for k, v in options.items():\n            setattr(self, k, v)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name on a function's parameter named self", tree: map[string]string{"app/job.py": pyJob, "app/init.py": "def init(self, **options):\n    for k, v in options.items():\n        setattr(self, k, v)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "setattr with a computed name on an attribute of self", tree: map[string]string{"app/job.py": pyJob, "app/base.py": "class Base:\n    def __init__(self, **options):\n        for k, v in options.items():\n            setattr(self.settings, k, v)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "super().__setattr__ with a computed name in a classmethod", tree: map[string]string{"app/job.py": pyJob, "app/base.py": "class Base:\n    @classmethod\n    def configure(cls, name, value):\n        super().__setattr__(name, value)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "object.__setattr__ with a computed name on a receiver it cannot tell", tree: map[string]string{"app/job.py": pyJob, "app/util.py": "def force(target, name, value):\n    object.__setattr__(target, name, value)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "type.__setattr__ with a computed name", tree: map[string]string{"app/job.py": pyJob, "app/meta.py": "def install(cls, name, f):\n    type.__setattr__(cls, name, f)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "an item of the name in an instance's __dict__", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    job.__dict__['helper'] = abs\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "the name as a keyword of an update of vars", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    vars(job).update(helper=abs)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "the name as a key of an update of __dict__", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    job.__dict__.update({'helper': abs})\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a setdefault of the name in __dict__", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    job.__dict__.setdefault('helper', abs)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a withheld Python source that may set class attributes", tree: map[string]string{"app/job.py": pyJob, "app/register.py": "def register(cls):\n    setattr(cls, NAME, impl)\n    return cls\n"},
			withheld: []string{"app/register.py"}, path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadWithheld},
		{name: "object.__setattr__ of the name", tree: map[string]string{"app/job.py": pyJob, "app/patch.py": "def patch(job):\n    object.__setattr__(job, 'helper', abs)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "an inherited Python method", tree: map[string]string{"app/job.py": "class Base:\n    def helper(self, x):\n        return x\n\n\nclass Job(Base):\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
	}
}
