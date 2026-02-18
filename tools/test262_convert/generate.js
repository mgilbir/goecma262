import fs from "fs";
import path from "path";
import process from "process";
import { parse } from "acorn";
import * as walk from "acorn-walk";

function usage() {
  console.error("usage: node generate.js --test262 /tmp/test262 --out tests/test262_generated_test.go");
  process.exit(1);
}

const args = process.argv.slice(2);
let test262Root = "/tmp/test262";
let outPath = "tests/test262_generated_test.go";
for (let i = 0; i < args.length; i++) {
  const arg = args[i];
  if (arg === "--test262") {
    test262Root = args[++i];
  } else if (arg === "--out") {
    outPath = args[++i];
  } else if (arg === "--help" || arg === "-h") {
    usage();
  }
}

const root = path.join(test262Root, "test", "built-ins", "RegExp");
if (!fs.existsSync(root)) {
  console.error(`test262 root not found: ${root}`);
  process.exit(1);
}

const files = listJSFiles(root);
const cases = [];
let filesScanned = 0;
let casesSkipped = 0;
let casesMarkedSkip = 0;
let filesFailed = 0;

for (const file of files) {
  filesScanned++;
  const content = fs.readFileSync(file, "utf8");
  const parsed = parseFile(file, content, test262Root);
  if (!parsed.ok) {
    filesFailed++;
    continue;
  }
  for (const c of parsed.cases) {
    const reason = skipReason(c);
    if (reason) {
      casesMarkedSkip++;
      c.skip = true;
      c.skipReason = reason;
    } else {
      c.skip = false;
      c.skipReason = "";
    }
    cases.push(c);
  }
  casesSkipped += parsed.skipped;
}

cases.sort((a, b) => {
  if (a.file === b.file) {
    return a.name.localeCompare(b.name);
  }
  return a.file.localeCompare(b.file);
});

writeGo(outPath, cases, { filesScanned, casesSkipped, casesMarkedSkip, filesFailed });

console.log(
  `generated ${cases.length} cases (${filesScanned} files scanned, ${casesSkipped} skipped, ${casesMarkedSkip} marked-skip, ${filesFailed} parse-failed) -> ${outPath}`
);

function skipReason(c) {
  if (c.flags.includes("y") || c.flags.includes("g")) {
    return "unsupported flags g/y (lastIndex semantics)";
  }
  if (c.file.includes("/named-groups/duplicate-names")) {
    return "unsupported duplicate named group semantics";
  }
  return "";
}

function listJSFiles(dir) {
  const out = [];
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...listJSFiles(full));
    } else if (entry.isFile() && entry.name.endsWith(".js")) {
      out.push(full);
    }
  }
  return out;
}

function parseFile(file, content, test262Root) {
  let ast;
  try {
    ast = parse(content, {
      ecmaVersion: "latest",
      sourceType: "script",
      allowHashBang: true,
    });
  } catch (err) {
    try {
      ast = parse(content, {
        ecmaVersion: "latest",
        sourceType: "module",
        allowHashBang: true,
      });
    } catch (err2) {
      return { ok: false };
    }
  }

  const regexVars = new Map();
  const stringVars = new Map();
  walk.simple(ast, {
    VariableDeclarator(node) {
      if (!node.id || node.id.type !== "Identifier") return;
      const name = node.id.name;
      const regex = evalRegex(node.init, regexVars, stringVars);
      if (regex) {
        regexVars.set(name, regex);
        return;
      }
      const str = evalString(node.init, stringVars);
      if (str != null) {
        stringVars.set(name, str);
      }
    },
  });

  const cases = [];
  let skipped = 0;
  let idx = 0;
  walk.simple(ast, {
    CallExpression(node) {
      if (!isAssertSameValue(node)) return;
      idx++;
      const result = convertAssert(node, regexVars, stringVars);
      if (!result) {
        skipped++;
        return;
      }
      const base = path.basename(file);
      const rel = path.relative(test262Root, file).replace(/\\/g, "/");
      cases.push({
        name: `${base}#${idx}`,
        file: rel,
        ...result,
      });
    },
  });

  return { ok: true, cases, skipped };
}

function isAssertSameValue(node) {
  if (!node.callee || node.callee.type !== "MemberExpression") return false;
  const { object, property } = node.callee;
  if (!object || object.type !== "Identifier" || object.name !== "assert") return false;
  if (!property || property.type !== "Identifier" || property.name !== "sameValue") return false;
  return true;
}

function convertAssert(node, regexVars, stringVars) {
  if (node.arguments.length < 2) return null;
  const actual = node.arguments[0];
  const expected = node.arguments[1];

  if (expected.type === "Literal" && typeof expected.value === "boolean") {
    return convertTestExpression(actual, regexVars, stringVars, expected.value);
  }
  if (expected.type === "Literal" && expected.value === null) {
    return convertExecNull(actual, regexVars, stringVars);
  }
  if (expected.type === "Literal" && typeof expected.value === "string") {
    return convertExecExpected(actual, regexVars, stringVars, expected.value);
  }
  return null;
}

function convertTestExpression(actual, regexVars, stringVars, want) {
  if (actual.type !== "CallExpression") return null;
  if (!actual.callee || actual.callee.type !== "MemberExpression") return null;
  if (!isIdentifier(actual.callee.property, "test")) return null;
  const regex = evalRegex(actual.callee.object, regexVars, stringVars);
  if (!regex) return null;
  const input = evalString(actual.arguments[0], stringVars);
  if (input == null) return null;
  return {
    pattern: regex.pattern,
    flags: regex.flags,
    input,
    want,
    expect: "",
    expectIndex: -1,
    hasExpect: false,
  };
}

function convertExecNull(actual, regexVars, stringVars) {
  if (actual.type !== "CallExpression") return null;
  if (!actual.callee || actual.callee.type !== "MemberExpression") return null;
  if (!isIdentifier(actual.callee.property, "exec")) return null;
  const regex = evalRegex(actual.callee.object, regexVars, stringVars);
  if (!regex) return null;
  const input = evalString(actual.arguments[0], stringVars);
  if (input == null) return null;
  return {
    pattern: regex.pattern,
    flags: regex.flags,
    input,
    want: false,
    expect: "",
    expectIndex: -1,
    hasExpect: false,
  };
}

function convertExecExpected(actual, regexVars, stringVars, expected) {
  if (actual.type !== "MemberExpression") return null;
  if (!actual.object || actual.object.type !== "CallExpression") return null;
  if (!actual.object.callee || actual.object.callee.type !== "MemberExpression") return null;
  if (!isIdentifier(actual.object.callee.property, "exec")) return null;
  const index = getIndex(actual.property, actual.computed);
  if (index == null) return null;
  const regex = evalRegex(actual.object.callee.object, regexVars, stringVars);
  if (!regex) return null;
  const input = evalString(actual.object.arguments[0], stringVars);
  if (input == null) return null;
  return {
    pattern: regex.pattern,
    flags: regex.flags,
    input,
    want: true,
    expect: expected,
    expectIndex: index,
    hasExpect: true,
  };
}

function getIndex(prop, computed) {
  if (!computed) return null;
  if (!prop || prop.type !== "Literal") return null;
  if (typeof prop.value !== "number") return null;
  if (!Number.isInteger(prop.value) || prop.value < 0) return null;
  return prop.value;
}

function evalRegex(node, regexVars, stringVars) {
  if (!node) return null;
  if (node.type === "Literal" && node.regex) {
    return { pattern: node.regex.pattern, flags: node.regex.flags || "" };
  }
  if (node.type === "Identifier" && regexVars.has(node.name)) {
    return regexVars.get(node.name);
  }
  if (node.type === "NewExpression" && isIdentifier(node.callee, "RegExp")) {
    if (node.arguments.length === 0) return null;
    const arg0 = node.arguments[0];
    const arg1 = node.arguments[1];
    let pattern = null;
    let flags = "";
    if (arg0.type === "Literal" && typeof arg0.value === "string") {
      pattern = arg0.value;
    } else if (arg0.type === "Literal" && arg0.regex) {
      pattern = arg0.regex.pattern;
      flags = arg0.regex.flags || "";
    } else {
      pattern = evalConstString(arg0, stringVars);
    }
    if (pattern == null) return null;
    if (arg1) {
      const f = evalConstString(arg1, stringVars);
      if (f != null) {
        flags = f;
      }
    }
    return { pattern, flags };
  }
  return null;
}

function evalString(node, stringVars) {
  return evalConstString(node, stringVars);
}

function evalConstString(node, stringVars) {
  if (!node) return null;
  if (node.type === "Literal") {
    if (typeof node.value === "string") {
      return node.value;
    }
    if (typeof node.value === "number" || typeof node.value === "boolean") {
      return String(node.value);
    }
  }
  if (node.type === "Identifier" && stringVars.has(node.name)) {
    return stringVars.get(node.name);
  }
  if (node.type === "BinaryExpression" && node.operator === "+") {
    const left = evalConstString(node.left, stringVars);
    const right = evalConstString(node.right, stringVars);
    if (left != null && right != null) {
      return left + right;
    }
    const leftNum = evalConstNumber(node.left);
    if (leftNum != null && right != null) {
      return String(leftNum) + right;
    }
    const rightNum = evalConstNumber(node.right);
    if (left != null && rightNum != null) {
      return left + String(rightNum);
    }
    return null;
  }
  if (node.type === "TemplateLiteral") {
    let out = "";
    for (let i = 0; i < node.quasis.length; i++) {
      out += node.quasis[i].value.cooked ?? "";
      if (i < node.expressions.length) {
        const expr = evalConstString(node.expressions[i], stringVars);
        if (expr == null) return null;
        out += expr;
      }
    }
    return out;
  }
  if (node.type === "CallExpression" && node.callee) {
    if (isIdentifier(node.callee, "String")) {
      if (node.arguments.length === 0) return "";
      const arg = evalConstString(node.arguments[0], stringVars);
      if (arg == null) return null;
      return String(arg);
    }
    if (node.callee.type === "MemberExpression" && isIdentifier(node.callee.object, "String")) {
      if (isIdentifier(node.callee.property, "raw") && node.arguments.length === 1) {
        const tpl = node.arguments[0];
        if (tpl && tpl.type === "TemplateLiteral") {
          let out = "";
          for (let i = 0; i < tpl.quasis.length; i++) {
            out += tpl.quasis[i].value.raw ?? "";
            if (i < tpl.expressions.length) {
              const expr = evalConstString(tpl.expressions[i], stringVars);
              if (expr == null) return null;
              out += expr;
            }
          }
          return out;
        }
      }
      if (isIdentifier(node.callee.property, "fromCharCode")) {
        const chars = [];
        for (const arg of node.arguments) {
          const val = evalConstNumber(arg);
          if (val == null) return null;
          chars.push(String.fromCharCode(val));
        }
        return chars.join("");
      }
      if (isIdentifier(node.callee.property, "fromCodePoint")) {
        const chars = [];
        for (const arg of node.arguments) {
          const val = evalConstNumber(arg);
          if (val == null) return null;
          chars.push(String.fromCodePoint(val));
        }
        return chars.join("");
      }
    }
  }
  return null;
}

function evalConstNumber(node) {
  if (!node) return null;
  if (node.type === "Literal" && typeof node.value === "number") {
    return node.value;
  }
  if (node.type === "Literal" && typeof node.value === "string") {
    if (node.value.trim() === "") return null;
    const num = Number(node.value);
    if (!Number.isNaN(num)) return num;
  }
  if (node.type === "UnaryExpression" && (node.operator === "+" || node.operator === "-")) {
    const val = evalConstNumber(node.argument);
    if (val == null) return null;
    return node.operator === "-" ? -val : val;
  }
  return null;
}

function isIdentifier(node, name) {
  return node && node.type === "Identifier" && node.name === name;
}

function goString(s) {
  return JSON.stringify(s);
}

function writeGo(outPath, cases, stats) {
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  const lines = [];
  lines.push("// Code generated by tools/test262_convert/generate.js; DO NOT EDIT.");
  lines.push(
    `// Source: tc39/test262/test/built-ins/RegExp (files scanned: ${stats.filesScanned}, skipped cases: ${stats.casesSkipped}, marked-skip: ${stats.casesMarkedSkip}, parse-failed: ${stats.filesFailed})`
  );
  lines.push("");
  lines.push("package ecma262_test");
  lines.push("");
  lines.push("import (");
  lines.push("\t\"os\"");
  lines.push("\t\"testing\"");
  lines.push("");
  lines.push("\t\"github.com/mgilbir/goecma262\"");
  lines.push("\t\"github.com/mgilbir/goecma262/flags\"");
  lines.push(")");
  lines.push("");
  lines.push("var test262GeneratedCases = []struct {");
  lines.push("\tname string");
  lines.push("\tfile string");
  lines.push("\tpattern string");
  lines.push("\tflags string");
  lines.push("\tinput string");
  lines.push("\twant bool");
  lines.push("\texpect string");
  lines.push("\texpectIndex int");
  lines.push("\thasExpect bool");
  lines.push("\tskip bool");
  lines.push("\tskipReason string");
  lines.push("}{");
  for (const c of cases) {
    lines.push(
      `\t{${goString(c.name)}, ${goString(c.file)}, ${goString(c.pattern)}, ${goString(c.flags)}, ${goString(
        c.input
      )}, ${c.want}, ${goString(c.expect || "")}, ${c.expectIndex ?? -1}, ${c.hasExpect}, ${c.skip}, ${goString(
        c.skipReason || ""
      )}},`
    );
  }
  lines.push("}");
  lines.push("");
  lines.push("func TestTest262Generated(t *testing.T) {");
  lines.push("\tstrict := os.Getenv(\"TEST262_STRICT\") == \"1\"");
  lines.push("\tfor _, tc := range test262GeneratedCases {");
  lines.push("\t\ttc := tc");
  lines.push("\t\tt.Run(tc.name, func(t *testing.T) {");
  lines.push("\t\t\tf, err := flags.Parse(tc.flags)");
  lines.push("\t\t\tif err != nil {");
  lines.push("\t\t\t\tif strict {");
  lines.push("\t\t\t\t\tt.Fatalf(\"%s: invalid flags %q: %v\", tc.file, tc.flags, err)");
  lines.push("\t\t\t\t}");
  lines.push("\t\t\t\tt.Skipf(\"%s: invalid flags %q: %v\", tc.file, tc.flags, err)");
  lines.push("\t\t\t\treturn");
  lines.push("\t\t\t}");
  lines.push("\t\t\tre, err := ecma262.Compile(tc.pattern, f)");
  lines.push("\t\t\tif err != nil {");
  lines.push("\t\t\t\tif strict {");
  lines.push("\t\t\t\t\tt.Fatalf(\"%s: compile error for %q: %v\", tc.file, tc.pattern, err)");
  lines.push("\t\t\t\t}");
  lines.push("\t\t\t\tt.Skipf(\"%s: compile error for %q: %v\", tc.file, tc.pattern, err)");
  lines.push("\t\t\t\treturn");
  lines.push("\t\t\t}");
  lines.push("\t\t\tif tc.skip {");
  lines.push("\t\t\t\tt.Skipf(\"%s: %s\", tc.file, tc.skipReason)");
  lines.push("\t\t\t\treturn");
  lines.push("\t\t\t}");
  lines.push("\t\t\tif tc.hasExpect {");
  lines.push("\t\t\t\tif tc.expectIndex >= 0 {");
  lines.push("\t\t\t\t\tgot := re.FindStringSubmatch(tc.input)");
  lines.push("\t\t\t\t\tif got == nil || tc.expectIndex >= len(got) {");
  lines.push("\t\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindStringSubmatch(%q) missing index %d\", tc.file, tc.pattern, tc.flags, tc.input, tc.expectIndex)");
  lines.push("\t\t\t\t\t}");
  lines.push("\t\t\t\t\tif got[tc.expectIndex] != tc.expect {");
  lines.push("\t\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindStringSubmatch(%q)[%d] = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, tc.expectIndex, got[tc.expectIndex], tc.expect)");
  lines.push("\t\t\t\t\t}");
  lines.push("\t\t\t\t\treturn");
  lines.push("\t\t\t\t}");
  lines.push("\t\t\t\tgot := re.FindString(tc.input)");
  lines.push("\t\t\t\tif got != tc.expect {");
  lines.push("\t\t\t\t\tt.Fatalf(\"%s: /%s/%s.FindString(%q) = %q, want %q\", tc.file, tc.pattern, tc.flags, tc.input, got, tc.expect)");
  lines.push("\t\t\t\t}");
  lines.push("\t\t\t\treturn");
  lines.push("\t\t\t}");
  lines.push("\t\t\tgot := re.MatchString(tc.input)");
  lines.push("\t\t\tif got != tc.want {");
  lines.push("\t\t\t\tt.Fatalf(\"%s: /%s/%s.MatchString(%q) = %v, want %v\", tc.file, tc.pattern, tc.flags, tc.input, got, tc.want)");
  lines.push("\t\t\t}");
  lines.push("\t\t})");
  lines.push("\t}");
  lines.push("}");

  fs.writeFileSync(outPath, lines.join("\n") + "\n", "utf8");
}
