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

const root = path.join(test262Root, "test", "built-ins", "RegExp");
if (!fs.existsSync(root)) {
  console.error(`test262 root not found: ${root}`);
  process.exit(1);
}

const harnessDir = path.join(test262Root, "harness");
const harnessFiles = fs
  .readdirSync(harnessDir)
  .filter((f) => f.endsWith(".js"))
  .map((f) => path.join(harnessDir, f));

const files = listJSFiles(root);
const cases = [];
let filesScanned = 0;
let filesFailed = 0;
let assertCount = 0;
let captured = 0;

for (const file of files) {
  filesScanned++;
  const content = fs.readFileSync(file, "utf8");
  const rel = path.relative(test262Root, file).replace(/\\/g, "/");
  const { ok, results, asserts } = runFile(rel, content, harnessFiles);
  if (!ok) {
    filesFailed++;
    continue;
  }
  assertCount += asserts;
  captured += results.length;
  cases.push(...results);
}

fs.mkdirSync(path.dirname(outPath), { recursive: true });
fs.writeFileSync(outPath, JSON.stringify({
  meta: {
    root: "test/built-ins/RegExp",
    filesScanned,
    filesFailed,
    asserts: assertCount,
    captured,
  },
  cases,
}, null, 2) + "\n");

console.log(
  `captured ${captured} cases from ${assertCount} assert.sameValue calls (files scanned: ${filesScanned}, failed: ${filesFailed}) -> ${outPath}`
);

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
  let assertIndex = 0;

  const context = {
    console,
    $DONE: () => {},
    $ERROR: () => {},
  };
  context.globalThis = context;

  const ctx = vm.createContext(context);

  for (const hf of harnessFiles) {
    const h = fs.readFileSync(hf, "utf8");
    try {
      vm.runInContext(h, ctx, { filename: hf });
    } catch {
      // Ignore harness errors; we only need common globals.
    }
  }

  function recordCall(method, regex, input, result, extra = {}) {
    calls.push({
      method,
      pattern: regex.source,
      flags: regex.flags,
      input: safeToString(input),
      result,
      extra,
    });
  }

  function recordAssert(actual, expected) {
    assertIndex++;
    const match = findMatchingCall(calls, actual);
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

  ctx.__recordCall = recordCall;
  ctx.__recordAssert = recordAssert;

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
    if (globalThis.assert && typeof globalThis.assert.sameValue === "function") {
      const origSameValue = globalThis.assert.sameValue;
      globalThis.assert.sameValue = function(actual, expected) {
        globalThis.__recordAssert(actual, expected);
        try { return origSameValue(actual, expected); } catch (e) { return undefined; }
      };
    }
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

  return { ok: true, results, asserts: assertIndex };
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
