package ecma262_test

// test262KnownFailures lists test case names (tc.name values from the generated
// test table) that are known to be impossible to pass in a Go implementation.
// These tests are skipped rather than failed so that "go test" reports clean
// results without hiding real regressions.
//
// Add entries here when a test262 case requires semantics that cannot be
// expressed in a static Go regex API.  Each entry MUST include a comment
// explaining WHY the test cannot pass.
//
// This file is hand-maintained and is NOT overwritten by the test generator
// (tools/test262_from_json).  It will survive regeneration of
// tests/test262_generated_test.go.
var test262KnownFailures = map[string]string{
	// -------------------------------------------------------------------------
	// Functional replacement with arrow functions
	// -------------------------------------------------------------------------
	// These tests call String.prototype.replace with a JS arrow function as the
	// replacement argument.  The test extractor records String(fn) as the
	// replaceWith string, which is the *source code* of the arrow function.
	// A Go implementation has no JS runtime to evaluate that source, so the
	// replacement is always treated as a literal string instead of a callable.
	"functional-replace-global.js#15":     "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-global.js#17":     "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-global.js#32":     "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-global.js#34":     "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-non-global.js#9":  "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-non-global.js#11": "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-non-global.js#20": "replacement is a JS arrow function (not executable in Go)",
	"functional-replace-non-global.js#22": "replacement is a JS arrow function (not executable in Go)",

	// -------------------------------------------------------------------------
	// groups-object-subclass: JS subclass / prototype overriding
	// -------------------------------------------------------------------------
	// These tests subclass RegExp or override Symbol.replace to inject a custom
	// groups object into the replacement machinery.  The expected replacement
	// text ("b", "c") comes from values stored in that custom JS object, which
	// has no equivalent in a Go API.
	"groups-object-subclass-sans.js#3": "requires JS RegExp subclass with custom groups object",
	"groups-object-subclass-sans.js#4": "requires JS RegExp subclass with custom groups object",
	"groups-object-subclass.js#3":      "requires JS RegExp subclass with custom groups object",
	"groups-object-subclass.js#4":      "requires JS RegExp subclass with custom groups object",
}
