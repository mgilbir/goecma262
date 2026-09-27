#!/usr/bin/env node
// Oracle side of the live fuzzer.
//
// Reads one case per line from stdin and writes one result per line to stdout,
// preserving order. Strings travel as base64 of their UTF-16 code units, so any
// character — newlines, lone surrogates, control characters — survives the round
// trip byte for byte.
//
// Request:  <op> <b64 pattern> <b64 flags> <b64 input> [<b64 extra>]
//   op = "x"  single exec from index 0
//      = "a"  all matches, as String.prototype.matchAll (requires the g flag)
//      = "r"  String.prototype.replace with the extra field as the replacement
//      = "s"  String.prototype.split with the extra field as a decimal limit
//
// Response: "E"                            syntax error
//           "N"                            no match
//           "M <index> <n> <g0> ..."       a match; each group is b64, or "-" for null
//           "A <count> [<match> | ...]"    all matches, each rendered as above
//           "R <b64 result>"               replace result
//           "S <count> <b64 part> ..."     split result ("-" for an undefined part)
//           "U"                            a result with a position between the
//                                          halves of a surrogate pair (see
//                                          splitsResult)
//           "T"                            oracle-side failure (not a spec behaviour)
// A leading "!", "~", "%", "&", "@" or "*" marks a known V8 defect the comparison skips.

import readline from "node:readline";
import {
  hasSingleCharQuotedString,
  hasModifierWithWordEscape,
  hasEndAnchorAstralMiss,
  wrapClasses,
} from "./corpus.mjs";

/** base64 of UTF-16LE code units -> string, preserving lone surrogates. */
function decode(s) {
  const buf = Buffer.from(s, "base64");
  let out = "";
  for (let i = 0; i + 1 < buf.length; i += 2) out += String.fromCharCode(buf.readUInt16LE(i));
  return out;
}

function encode(s) {
  const buf = Buffer.alloc(s.length * 2);
  for (let i = 0; i < s.length; i++) buf.writeUInt16LE(s.charCodeAt(i), i * 2);
  return buf.toString("base64");
}

const renderMatch = (m) => {
  const groups = [...m].map((g) => (g === undefined ? "-" : encode(g)));
  return `${m.index} ${groups.length} ${groups.join(" ")}`;
};

const splitsPair = (s, i) =>
  i > 0 && i < s.length &&
  s.charCodeAt(i - 1) >= 0xd800 && s.charCodeAt(i - 1) <= 0xdbff &&
  s.charCodeAt(i) >= 0xdc00 && s.charCodeAt(i) <= 0xdfff;

/**
 * True when this engine placed a match at a position that splits a surrogate
 * pair under u/v.
 *
 * ECMA-262 matches such a pattern over code points, so no match can begin
 * inside a pair — but V8 does exactly that for zero-width assertions
 * (`/\B/u.exec("b😀").index` is 2 where the spec requires 3). Flagging the
 * case here lets the comparison skip it rather than report a false failure.
 */
function hasSurrogateSplitMatch(re, input, allMatches) {
  if (!/[uv]/.test(re.flags)) return false;
  if (!allMatches) {
    const probe = new RegExp(re.source, re.flags.replace("g", ""));
    const m = probe.exec(input);
    return m !== null && splitsPair(input, m.index);
  }
  const g = new RegExp(re.source, re.flags.includes("g") ? re.flags : re.flags + "g");
  for (const m of input.matchAll(g)) {
    if (splitsPair(input, m.index)) return true;
  }
  return false;
}

/**
 * True when, without u or v, the operation's result has no Go form, being
 * where goecma262 returns ErrSurrogateSplit. By op:
 *
 *   x, a  the start or end of the match or of any group, for any match, lies
 *         between the two halves of a surrogate pair, where a byte offset
 *         cannot (FindStringSubmatchIndexErr, FindAllStringSubmatchIndexErr)
 *   r     the result string holds half of a pair (ReplaceAllStringErr)
 *   s     some part holds half of a pair (SplitErr)
 */
function splitsResult(re, op, input, rendered) {
  if (/[uv]/.test(re.flags)) return false;
  const halfPair = (str) => /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/.test(str);
  // The d flag gives each match its indices; the case may already have it.
  const withIndices = () => new RegExp(re.source, re.flags.includes("d") ? re.flags : re.flags + "d");
  const bad = (m) => m.indices.some((g) => g !== undefined && (splitsPair(input, g[0]) || splitsPair(input, g[1])));
  if (op === "x") {
    const m = withIndices().exec(input);
    return m !== null && bad(m);
  }
  if (op === "a") {
    return [...input.matchAll(withIndices())].some(bad);
  }
  if (op === "r") return halfPair(decode(rendered.slice(2)));
  return rendered.split(" ").slice(2).some((part) => part !== "-" && halfPair(decode(part)));
}

/** One operation's result, rendered. Factored out so it can be run twice - see hasDotAllInconsistency. */
function runOp(re, op, input, extra) {
  if (op === "x") {
    re.lastIndex = 0;
    const m = re.exec(input);
    return m === null ? "N" : `M ${renderMatch(m)}`;
  }
  if (op === "a") {
    // matchAll is the spec's own definition of "every non-overlapping match",
    // including how it steps past an empty one.
    const all = [...input.matchAll(re)];
    return `A ${all.length} ${all.map(renderMatch).join(" | ")}`;
  }
  if (op === "r") {
    re.lastIndex = 0;
    return `R ${encode(input.replace(re, decode(extra)))}`;
  }
  re.lastIndex = 0;
  const limit = Number(decode(extra));
  const parts = limit < 0 ? input.split(re) : input.split(re, limit);
  const rendered = parts.map((x) => (x === undefined ? "-" : encode(x)));
  return `S ${parts.length} ${rendered.join(" ")}`;
}

const LINE_TERMINATOR = /[\n\r\u2028\u2029]/;

/**
 * A fifth V8 defect, detected by catching V8 contradicting itself.
 *
 * The `s` flag changes one thing: whether `.` matches a line terminator. So on
 * an input containing no line terminator, adding or removing `s` cannot change
 * any result - the two patterns accept exactly the same strings. V8 disagrees:
 *
 *   /(.*?\.^|)/.exec("")   is ""  at 0
 *   /(.*?\.^|)/s.exec("")  is null
 *
 * On the empty string there is not even a character for `s` to reinterpret, so
 * no reading of the specification makes both of those right. The empty
 * alternative matches at 0 and the answer is "".
 *
 * Rather than guess at the syntactic trigger - a lazy `.*?` before `\.` and a
 * `^`, as far as the minimisation goes - this runs the case both ways and
 * flags it when V8's own two answers differ. That is narrow by construction:
 * it can only fire where V8 is provably self-inconsistent.
 */
function hasDotAllInconsistency(re, op, input, extra) {
  if (!re.flags.includes("s")) return false;
  if (LINE_TERMINATOR.test(input)) return false;
  try {
    const withS = new RegExp(re.source, re.flags);
    const withoutS = new RegExp(re.source, re.flags.replace("s", ""));
    return runOp(withS, op, input, extra) !== runOp(withoutS, op, input, extra);
  } catch {
    return false;
  }
}

/**
 * A sixth V8 defect, detected by catching V8 contradicting itself.
 *
 * Under v, a modifier group that changes i leaks that setting into character
 * classes after it, which V8 then builds with the group's i instead of their
 * own - in both directions:
 *
 *   /(?i:x)[A-Z]/v.exec("xc")       // matches in V8; [A-Z] is not under i
 *   /(?i:x)(?:[A-Z])/v.exec("xc")   // null, as it should be
 *   /(?-i:x)[^A-Z]/vi.exec("xc")    // matches in V8; [^A-Z] is under i
 *
 * Which classes are hit does not follow the syntax in any simple way
 * ((?i:x)[c] leaks, (?i:x)[\q{c}] and (?i:x)(?:[c]) do not), so rather than
 * guess, this reruns the case with every class wrapped in (?:...) - which by
 * the specification changes nothing - and flags it when V8's two answers
 * differ. Like hasDotAllInconsistency, it can only fire where V8 is provably
 * self-inconsistent. It runs only under v with a modifier group that sets or
 * clears i, the one place the leak has been seen; u is unaffected.
 */
function hasModifierClassLeak(re, op, input, extra) {
  if (!re.flags.includes("v")) return false;
  // A modifier group whose flags, on either side of the dash, include i.
  if (!/\(\?(?=[ims-]*i)[ims]*(?:-[ims]*)?:/.test(re.source)) return false;
  const wrapped = wrapClasses(re.source);
  if (wrapped === null || wrapped === re.source) return false;
  try {
    const plain = new RegExp(re.source, re.flags);
    const other = new RegExp(wrapped, re.flags);
    return runOp(plain, op, input, extra) !== runOp(other, op, input, extra);
  } catch {
    return false;
  }
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

/**
 * Writes each result as it is produced.
 *
 * Buffering everything until close held the whole run in memory and, worse, hid
 * how far the oracle had got when a pattern made V8 spin — the output was lost
 * with the process.
 */
const emit = (line) => process.stdout.write(line + "\n");

/**
 * With FUZZ_TRACE=1, logs each case before running it, so the last line on
 * stderr names the case that hung.
 *
 * V8's regular expression engine cannot be interrupted from JavaScript and has
 * no step limit, so a catastrophic pattern runs until the process is killed.
 * Tracing is the only way to find which case it was.
 */
const trace = process.env.FUZZ_TRACE === "1";

rl.on("line", (line) => {
  if (!line) return;
  if (trace) process.stderr.write(`CASE ${line}\n`);
  const [op, p, f, s, extra] = line.split(" ");
  try {
    const pattern = decode(p);
    const flags = decode(f);
    const input = decode(s);

    let re;
    try {
      re = new RegExp(pattern, flags);
    } catch {
      emit("E");
      return;
    }

    // A "!" prefix marks a result the comparison should skip: see
    // hasSurrogateSplitMatch.
    const global = op === "a" || op === "s" || re.flags.includes("g");
    // "!" = V8 places a match inside a surrogate pair; "~" = V8 mis-folds a
    // single-character \q{} element. Both are skipped by the comparison, and
    // counted separately so the report stays honest about which is which.
    const mark = hasSurrogateSplitMatch(re, input, global)
      ? "!"
      : hasSingleCharQuotedString(pattern, flags)
        ? "~"
        : hasModifierWithWordEscape(pattern, flags)
          ? "%"
          : hasEndAnchorAstralMiss(pattern, flags, input)
            ? "&"
            : hasDotAllInconsistency(re, op, input, extra)
              ? "@"
              : hasModifierClassLeak(re, op, input, extra)
                ? "*"
                : "";

    const rendered = runOp(re, op, input, extra);
    emit(mark + (splitsResult(re, op, input, rendered) ? "U" : rendered));
  } catch {
    emit("T");
  }
});

rl.on("close", () => {
  // Nothing to flush: every result was written as it was produced.
});
