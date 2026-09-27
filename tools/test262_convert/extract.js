import { execFileSync } from "child_process";
import fs from "fs";
import path from "path";
import process from "process";
import vm from "vm";

function usage() {
  console.error("usage: node extract.js --test262 /tmp/test262 --out tests/test262_cases.json");
  process.exit(1);
}

const args = process.argv.slice(2);
let test262Root = "/tmp/test262";
let outPath = "tests/test262_cases.json";
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

// The directories whose tests exercise RegExp: the built-in, the literal
// syntax, and the Annex B (web-compatibility) grammar and semantics that the
// default syntax mode implements.
const roots = [
  "test/built-ins/RegExp",
  "test/language/literals/regexp",
  "test/annexB/built-ins/RegExp",
  "test/annexB/language/literals/regexp",
];
for (const r of roots) {
  if (!fs.existsSync(path.join(test262Root, r))) {
    console.error(`test262 directory not found: ${path.join(test262Root, r)}`);
    process.exit(1);
  }
}

let test262Commit = "";
try {
  test262Commit = execFileSync("git", ["-C", test262Root, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
} catch {
  // Not a git checkout; the generated files then name no commit.
}

const harnessDir = path.join(test262Root, "harness");
const harnessFiles = fs
  .readdirSync(harnessDir)
  .filter((f) => f.endsWith(".js"))
  .map((f) => path.join(harnessDir, f));

// regExpUtils.js aliases testExtendedCharacterClass with a top-level const,
// which the instrumentation below could not replace. Make it a var, and stop
// if the harness no longer has that line, rather than silently capturing
// nothing from the v-flag suites.
const harnessSources = new Map(harnessFiles.map((hf) => {
  let src = fs.readFileSync(hf, "utf8");
  if (path.basename(hf) === "regExpUtils.js") {
    const decl = "const testExtendedCharacterClass = testPropertyOfStrings;";
    if (!src.includes(decl)) {
      console.error(`${hf}: expected "${decl}"; update tools/test262_convert/extract.js`);
      process.exit(1);
    }
    src = src.replace(decl, decl.replace("const", "var"));
  }
  return [hf, src];
}));

// originalSource and originalFlags read a regex's [[OriginalSource]] and
// [[OriginalFlags]], the ones it matches with, through RegExp.prototype's own
// accessors. regex.flags would read the flag properties instead, which a test
// may shadow: builtin-infer-unicode.js gives /\udf06/u an own `unicode` of
// false to show that matching ignores it.
const protoGetter = (name) => Object.getOwnPropertyDescriptor(RegExp.prototype, name).get;
const sourceGetter = protoGetter("source");
const flagGetters = [
  ["d", "hasIndices"], ["g", "global"], ["i", "ignoreCase"], ["m", "multiline"],
  ["s", "dotAll"], ["u", "unicode"], ["v", "unicodeSets"], ["y", "sticky"],
].map(([c, name]) => [c, protoGetter(name)]);

function originalSource(regex) {
  return sourceGetter.call(regex);
}

function originalFlags(regex) {
  return flagGetters.filter(([, get]) => get.call(regex)).map(([c]) => c).join("");
}

const files = roots.flatMap((r) => listJSFiles(path.join(test262Root, r)));
const cases = [];
const syntax = [];
let filesScanned = 0;
let filesFailed = 0;
let assertCount = 0;
let captured = 0;
let notRegExp = 0;
const literalOnlyErrors = [];

for (const file of files) {
  filesScanned++;
  const content = fs.readFileSync(file, "utf8");
  const rel = path.relative(test262Root, file).replace(/\\/g, "/");
  if (isNegativeParseTest(content)) {
    // The file must not parse, so it cannot run: its pattern is the regular
    // expression literal after $DONOTEVALUATE().
    const lit = negativeTestLiteral(content);
    if (lit && syntaxErrorIn(lit.body, lit.flags)) {
      addSyntax(rel, lit.body, lit.flags, false);
    } else {
      // The error is in the literal's own syntax (a line terminator, an
      // escaped flag), which the RegExp constructor never sees.
      literalOnlyErrors.push(rel);
    }
    continue;
  }
  for (const lit of statementLiterals(content)) {
    if (!syntaxErrorIn(lit.body, lit.flags)) {
      addSyntax(rel, lit.body, lit.flags, true);
    }
  }
  const { ok, results, asserts, syntaxErrors } = runFile(rel, content, harnessFiles);
  if (!ok) {
    filesFailed++;
    continue;
  }
  assertCount += asserts;
  captured += results.length;
  cases.push(...results);
  for (const s of syntaxErrors) {
    addSyntax(rel, s.pattern, s.flags, false);
  }
}

fs.mkdirSync(path.dirname(outPath), { recursive: true });
fs.writeFileSync(outPath, JSON.stringify({
  meta: {
    roots,
    test262Commit,
    filesScanned,
    filesFailed,
    asserts: assertCount,
    captured,
    syntaxCaptured: syntax.length,
    literalOnlyErrors,
  },
  cases,
  syntax,
}, null, 2) + "\n");

console.log(
  `captured ${captured} cases from ${assertCount} assertions and ${syntax.length} syntax cases (files scanned: ${filesScanned}, failed: ${filesFailed}; ${literalOnlyErrors.length} literal-only syntax errors skipped; calls on non-RegExp receivers skipped: ${notRegExp}) -> ${outPath}`
);

// addSyntax records that pattern with flags must (valid) or must not compile,
// once per file.
function addSyntax(file, pattern, flags, valid) {
  const dup = syntax.some((s) => s.file === file && s.pattern === pattern && s.flags === flags);
  if (dup) return;
  const n = syntax.filter((s) => s.file === file).length + 1;
  syntax.push({ file, name: `${path.basename(file)}#s${n}`, pattern, flags, valid });
}

// syntaxErrorIn reports whether the RegExp constructor rejects pattern and
// flags with a SyntaxError, in the Node.js running this script.
function syntaxErrorIn(pattern, flags) {
  try {
    new RegExp(pattern, flags);
    return false;
  } catch (e) {
    return e instanceof SyntaxError;
  }
}

function isNegativeParseTest(content) {
  return /negative:\s*\n\s*phase:\s*parse\s*\n\s*type:\s*SyntaxError/.test(content);
}

// negativeTestLiteral returns the regular expression literal that follows
// $DONOTEVALUATE() in a negative syntax test, skipping whitespace and
// comments, or null if what follows is not one.
function negativeTestLiteral(content) {
  const at = content.indexOf("$DONOTEVALUATE();");
  if (at < 0) return null;
  let i = at + "$DONOTEVALUATE();".length;
  for (;;) {
    while (i < content.length && /\s/.test(content[i])) i++;
    if (content.startsWith("//", i)) {
      const nl = content.indexOf("\n", i);
      i = nl < 0 ? content.length : nl;
    } else if (content.startsWith("/*", i)) {
      const end = content.indexOf("*/", i + 2);
      if (end < 0) return null;
      i = end + 2;
    } else {
      break;
    }
  }
  return scanRegExpLiteral(content, i);
}

// statementLiterals returns the regular expression literals that make up a
// whole statement on their own line, such as "/(?i:a)/;". A line cannot
// start with a division, so a / there (not beginning a comment) starts a
// literal.
function statementLiterals(content) {
  const out = [];
  const lines = content.split("\n");
  let offset = 0;
  for (const line of lines) {
    const lead = line.length - line.trimStart().length;
    const i = offset + lead;
    offset += line.length + 1;
    const t = line.trimStart();
    if (!t.startsWith("/") || t.startsWith("//") || t.startsWith("/*")) continue;
    const lit = scanRegExpLiteral(content, i);
    if (!lit) continue;
    const rest = content.slice(lit.end, offset - 1).trim();
    if (rest === ";" || rest === "" || /^;?\s*\/\//.test(rest)) out.push(lit);
  }
  return out;
}

// scanRegExpLiteral reads a RegularExpressionLiteral starting at the / at i:
// the body up to the first / that is neither escaped nor in a class, then the
// flags. It returns null for a literal with a line terminator or an escaped
// flag, which are errors of the literal rather than of the pattern.
function scanRegExpLiteral(src, i) {
  if (src[i] !== "/") return null;
  let j = i + 1;
  let inClass = false;
  for (;;) {
    if (j >= src.length) return null;
    const c = src[j];
    if (c === "\n" || c === "\r" || c === "\u2028" || c === "\u2029") return null;
    if (c === "\\") {
      j += 2;
      continue;
    }
    if (c === "[") inClass = true;
    else if (c === "]") inClass = false;
    else if (c === "/" && !inClass) break;
    j++;
  }
  const body = src.slice(i + 1, j);
  let k = j + 1;
  while (k < src.length && /[\p{ID_Continue}$\u200c\u200d]/u.test(src[k])) k++;
  if (src[k] === "\\") return null;
  if (body === "" || body.startsWith("*")) return null; // a comment, not a literal
  return { body, flags: src.slice(j + 1, k), end: k };
}

// coerceLastIndexForJSON converts a JS lastIndex value to an integer suitable
// for JSON serialization, following ECMA-262 ToIntegerOrInfinity semantics.
// NaN and non-numeric values become 0; negative values become 0;
// Infinity becomes a large sentinel (2^31-1); finite values are truncated.
function coerceLastIndexForJSON(v) {
  if (v === undefined || v === null) return 0;
  const n = Number(v);
  if (Number.isNaN(n)) return 0;
  if (n <= 0) return 0;
  if (!Number.isFinite(n) || n > 2147483647) return 2147483647; // MaxInt32 sentinel
  return Math.trunc(n);
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

function runFile(relPath, content, harnessFiles) {
  const calls = [];
  const results = [];
  const syntaxErrors = [];
  let assertIndex = 0;
  let truthyIndex = 0;
  let arrayIndex = 0;
  let callsAtLastAssert = 0; // calls.length after the latest assertion

  const context = {
    console,
    $DONE: () => {},
    $ERROR: () => {},
  };
  context.globalThis = context;

  const ctx = vm.createContext(context);

  for (const hf of harnessFiles) {
    const h = harnessSources.get(hf);
    try {
      vm.runInContext(h, ctx, { filename: hf });
    } catch {
      // Ignore harness errors; we only need common globals.
    }
  }

  function recordCall(method, regex, input, result, extra = {}) {
    let pattern, flags;
    try {
      pattern = originalSource(regex);
      flags = originalFlags(regex);
    } catch {
      notRegExp++; // test and the String methods are generic
      return;
    }
    calls.push({
      method,
      pattern,
      flags,
      input: safeToString(input),
      result,
      extra,
    });
  }

  function recordAssert(actual, expected) {
    assertIndex++;
    const match = findMatchingCall(calls, actual);
    callsAtLastAssert = calls.length;
    if (!match) {
      return;
    }
    const { call, matchIndex } = match;
    results.push({
      file: relPath,
      name: `${path.basename(relPath)}#${assertIndex}`,
      method: call.method,
      pattern: call.pattern,
      flags: call.flags,
      input: call.input,
      expected,
      matchIndex,
      lastIndex: coerceLastIndexForJSON(call.extra.lastIndex),
      replaceWith: call.extra.replaceWith ?? null,
      splitLimit: call.extra.splitLimit ?? null,
    });
  }

  // recordTruthy handles assert(value): when value is a boolean the test
  // asserted to be true and the latest call since the previous assertion is
  // RegExp.prototype.test, the test expects that call's result (the
  // assertion is on it or on its negation, as in assert(!re.test(s))). The
  // Node.js running this passed the assertion, so its result is the one the
  // test requires. Names use #a so they cannot shift the numbering of the
  // assert.sameValue cases.
  function recordTruthy(value) {
    truthyIndex++;
    const fresh = calls.length > callsAtLastAssert;
    callsAtLastAssert = calls.length;
    if (value !== true || !fresh) return;
    const call = calls[calls.length - 1];
    if (call.method !== "test" || typeof call.result !== "boolean") return;
    calls.pop();
    callsAtLastAssert = calls.length;
    results.push({
      file: relPath,
      name: `${path.basename(relPath)}#a${truthyIndex}`,
      method: call.method,
      pattern: call.pattern,
      flags: call.flags,
      input: call.input,
      expected: call.result,
      matchIndex: null,
      lastIndex: coerceLastIndexForJSON(call.extra.lastIndex),
      replaceWith: null,
      splitLimit: null,
    });
  }

  // recordArray handles assert.compareArray(actual, expected) where actual is
  // the result array of the latest exec or non-global match: the test then
  // expects the whole match, with undefined for groups that did not
  // participate. Names use #c.
  function recordArray(actual, expected) {
    arrayIndex++;
    const fresh = calls.length > callsAtLastAssert;
    callsAtLastAssert = calls.length;
    if (!fresh || !Array.isArray(expected) || !Array.isArray(actual)) return;
    const call = calls[calls.length - 1];
    if (!Object.is(call.result, actual)) return;
    if (call.method !== "exec" && !(call.method === "string_match" && !call.flags.includes("g"))) return;
    if (!expected.every((e) => e === undefined || typeof e === "string")) return;
    calls.pop();
    callsAtLastAssert = calls.length;
    results.push({
      file: relPath,
      name: `${path.basename(relPath)}#c${arrayIndex}`,
      method: call.method,
      pattern: call.pattern,
      flags: call.flags,
      input: call.input,
      expected: null,
      expectedCaptures: expected.map((e) => (e === undefined ? null : e)),
      matchIndex: null,
      lastIndex: coerceLastIndexForJSON(call.extra.lastIndex),
      replaceWith: null,
      splitLimit: null,
    });
  }

  ctx.__recordCall = recordCall;
  ctx.__recordAssert = recordAssert;
  ctx.__recordTruthy = recordTruthy;
  ctx.__recordArray = recordArray;
  ctx.__recordSyntaxError = (pattern, flags) => syntaxErrors.push({ pattern, flags });
  ctx.__callCount = () => calls.length;
  ctx.__dropCallsFrom = (n) => {
    calls.length = n;
    callsAtLastAssert = Math.min(callsAtLastAssert, n);
  };

  const instrumentation = `(() => {
    const origTest = RegExp.prototype.test;
    const origExec = RegExp.prototype.exec;
    const origMatch = String.prototype.match;
    const origReplace = String.prototype.replace;
    const origSplit = String.prototype.split;
    RegExp.prototype.test = function(input) {
      const lastIndex = this.lastIndex;
      const res = origTest.call(this, input);
      globalThis.__recordCall("test", this, input, res, { lastIndex });
      return res;
    };
    RegExp.prototype.exec = function(input) {
      const lastIndex = this.lastIndex;
      const res = origExec.call(this, input);
      globalThis.__recordCall("exec", this, input, res, { lastIndex });
      return res;
    };
    String.prototype.match = function(re) {
      const lastIndex = (re instanceof RegExp) ? re.lastIndex : 0;
      const res = origMatch.call(this, re);
      if (re instanceof RegExp) {
        globalThis.__recordCall("string_match", re, String(this), res, { lastIndex });
      }
      return res;
    };
    String.prototype.replace = function(re, replacement) {
      const lastIndex = (re instanceof RegExp) ? re.lastIndex : 0;
      const res = origReplace.call(this, re, replacement);
      if (re instanceof RegExp) {
        globalThis.__recordCall("string_replace", re, String(this), res, { replaceWith: String(replacement), lastIndex });
      }
      return res;
    };
    String.prototype.split = function(sep, limit) {
      const lastIndex = (sep instanceof RegExp) ? sep.lastIndex : 0;
      const res = origSplit.call(this, sep, limit);
      if (sep instanceof RegExp) {
        const lim = typeof limit === "number" ? limit : null;
        globalThis.__recordCall("string_split", sep, String(this), res, { splitLimit: lim, lastIndex });
      }
      return res;
    };
    if (typeof globalThis.assert === "function") {
      const origAssert = globalThis.assert;
      const wrapped = function(value, message) {
        globalThis.__recordTruthy(value);
        try { return origAssert(value, message); } catch (e) { return undefined; }
      };
      Object.assign(wrapped, origAssert);
      globalThis.assert = wrapped;
      // assert.throws(SyntaxError, fn): run fn with a RegExp that records
      // the patterns it rejects with a SyntaxError. Those are the test's
      // expected syntax errors; calls fn makes to test or exec are dropped.
      // Other assert.throws calls keep the stub installed below.
      const stubThrows = wrapped.throws;
      const OrigRegExp = RegExp;
      const recordRejections = (args, construct) => {
        try {
          return construct();
        } catch (e) {
          const [p, f] = args;
          if (e instanceof SyntaxError && typeof p === "string" && (f === undefined || typeof f === "string")) {
            globalThis.__recordSyntaxError(p, f === undefined ? "" : f);
          }
          throw e;
        }
      };
      const Recording = new Proxy(OrigRegExp, {
        construct(target, args, newTarget) {
          return recordRejections(args, () => Reflect.construct(target, args, newTarget === Recording ? target : newTarget));
        },
        apply(target, thisArg, args) {
          return recordRejections(args, () => Reflect.apply(target, thisArg, args));
        },
      });
      wrapped.throws = function(expected, fn) {
        if (expected !== SyntaxError || typeof fn !== "function") {
          return stubThrows(expected, fn);
        }
        const mark = globalThis.__callCount();
        globalThis.RegExp = Recording;
        try { fn(); } catch (e) {} finally {
          globalThis.RegExp = OrigRegExp;
          globalThis.__dropCallsFrom(mark);
        }
      };
    }
    if (globalThis.assert && typeof globalThis.assert.compareArray === "function") {
      const origCompareArray = globalThis.assert.compareArray;
      globalThis.assert.compareArray = function(actual, expected, message) {
        globalThis.__recordArray(actual, expected);
        try { return origCompareArray(actual, expected, message); } catch (e) { return undefined; }
      };
    }
    if (globalThis.assert && typeof globalThis.assert.sameValue === "function") {
      const origSameValue = globalThis.assert.sameValue;
      globalThis.assert.sameValue = function(actual, expected) {
        globalThis.__recordAssert(actual, expected);
        try { return origSameValue(actual, expected); } catch (e) { return undefined; }
      };
    }
    // The v-flag suites (unicodeSets/generated, and the properties of strings)
    // check matches with assert(), which records nothing. These replace the
    // harness helpers with equivalents that make the same checks through
    // assert.sameValue: the joined strings where the harness accepts those,
    // and each string only where it falls back to them.
    // testPropertyEscapes is left alone: its inputs span whole Unicode
    // ranges, too large to embed in the generated test file.
    function checkStrings(args) {
      const re = args.regExp;
      const all = args.matchStrings.join("");
      const allMatch = re.test(all);
      if (allMatch) {
        globalThis.assert.sameValue(allMatch, true);
      } else {
        for (const s of args.matchStrings) globalThis.assert.sameValue(re.test(s), true);
      }
      if (!args.nonMatchStrings) return;
      const none = args.nonMatchStrings.join("");
      const noneMatch = re.test(none);
      if (!noneMatch) {
        globalThis.assert.sameValue(noneMatch, false);
      } else {
        for (const s of args.nonMatchStrings) globalThis.assert.sameValue(re.test(s), false);
      }
    }
    globalThis.testPropertyOfStrings = checkStrings;
    globalThis.testExtendedCharacterClass = checkStrings;
  })();`;

  const assert = ctx.assert || {};
  assert.throws = function (fn) {
    try {
      fn();
    } catch {
      return;
    }
  };
  ctx.assert = assert;
  ctx.globalThis.assert = assert;

  try {
    vm.runInContext(instrumentation, ctx, { filename: "instrumentation" });
    vm.runInContext(content, ctx, { filename: relPath });
  } catch {
    // Ignore test failures, we only care about captured calls.
  }

  return { ok: true, results, asserts: assertIndex + truthyIndex + arrayIndex, syntaxErrors };
}

  function findMatchingCall(calls, actual) {
    if (calls.length === 0) return null;
    const call = calls[calls.length - 1];
    if (Object.is(call.result, actual)) {
      calls.pop();
      return { call, matchIndex: null };
    }
    if (Array.isArray(call.result)) {
      for (let idx = 0; idx < call.result.length; idx++) {
        if (Object.is(call.result[idx], actual)) {
          calls.pop();
          return { call, matchIndex: idx };
        }
      }
    }
    return null;
  }

function safeToString(value) {
  try {
    return String(value);
  } catch {
    return "";
  }
}
