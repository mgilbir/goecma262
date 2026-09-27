#!/usr/bin/env node
// Writes the structured corpus from corpus.mjs as oracle request lines, one
// case per line, for `go run ./tools/difftest -cases <file>`.
//
//   node tools/difftest/gen-corpus.mjs > /tmp/corpus.txt
//
// Every pattern is crossed with every flag set and every input as a single
// exec ("x"), plus the random grammar-driven patterns over the short inputs.
// The oracle decides which flag combinations are valid, so nothing is
// filtered here.

import { PATTERNS, FLAGSETS, INPUTS, fuzzPatterns, FUZZ_INPUTS } from "./corpus.mjs";

/** base64 of UTF-16LE code units, so lone surrogates survive the transport. */
function encode(s) {
  const buf = Buffer.alloc(s.length * 2);
  for (let i = 0; i < s.length; i++) buf.writeUInt16LE(s.charCodeAt(i), i * 2);
  return buf.toString("base64");
}

const out = [];
const emit = (pattern, flags, input) =>
  out.push(`x ${encode(pattern)} ${encode(flags)} ${encode(input)}`);

for (const pattern of PATTERNS) {
  for (const flags of FLAGSETS) {
    for (const input of INPUTS) emit(pattern, flags, input);
  }
}
for (const pattern of fuzzPatterns(250)) {
  for (const flags of ["", "iu"]) {
    for (const input of FUZZ_INPUTS) emit(pattern, flags, input);
  }
}
process.stdout.write(out.join("\n") + "\n");
