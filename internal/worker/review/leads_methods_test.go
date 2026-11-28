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
// source of the language writes a member of the name, patches classes
// otherwise or declares a member of the name in another class: sources
// outside the scope, in a directory the review does not read (.github) or
// on a secondary path are read for this and do not stop it. this must be
// the instance of a method of the class, through arrow functions only (not
// a nested function, a static method, an object's method or a field's
// initializer); self the first parameter of a plain method, bound nowhere
// else (a nested function's closure keeps it). A subclass's override, an
// instance or class assignment, an annotation (a dataclass field), setattr
// with a computed name, a constructor parameter property, a getter beside
// the method, Object.assign onto a prototype or a same-named method in
// another class is ambiguous; a getter or property alone, a field, a
// decorated method, an inherited method and a receiver that is not the
// instance resolve nothing; a withheld source leaves the lead unanswered.
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

		{name: "a TypeScript private method beside a JavaScript subclass's override", tree: map[string]string{"app/job.ts": tsPrivateJob,
			"app/fast.js": "import { Job } from './job.js';\n\nexport class FastJob extends Job {\n  helper(x) {\n    return x;\n  }\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a TypeScript private method written in another module", tree: map[string]string{"app/job.ts": tsPrivateJob,
			"app/patch.ts": "import { Job } from './job.js';\n\nexport function patch(job: Job) {\n  (job as any).helper = (x: number) => x;\n}\n"},
			path: "app/job.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a static method assigned in another module", tree: map[string]string{"app/job.ts": tsStaticJob, "app/patch.ts": "import { Job } from './job.js';\n\nJob.helper = (x: number) => x;\n"},
			path: "app/job.ts", line: "return Job.helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
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
		{name: "an inherited Python method", tree: map[string]string{"app/job.py": "class Base:\n    def helper(self, x):\n        return x\n\n\nclass Job(Base):\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/job.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
	}
}
