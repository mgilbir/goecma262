package compiler

import (
	"slices"
	"sync"
	"unsafe"

	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// The members of the properties of strings are fixed, so what the compiler
// derives from them, their reversal for a lookbehind and their evaluation as
// a set, is built once per process and shared read-only by every compile, as
// the members themselves are (vm.PropertyOfStringsRunes). Naming
// \p{RGI_Emoji} then costs a compile no more than the code it emits; it used
// to evaluate nearly 4,000 strings into a set every time.

type propertyKey struct {
	name                 string
	ignoreCase, backward bool
}

var (
	propertyValues sync.Map // propertyKey -> classSetValue, at most 4 per property
	reversedTables sync.Map // name -> [][]rune
)

// sharedTable reports whether n holds a shared table, the members of the
// property it names or their reversal, and which. A caller-built node with
// other strings is evaluated as written.
func sharedTable(n *parser.ClassStrings) (backward, ok bool) {
	if n.Property == "" || len(n.Strings) == 0 {
		return false, false
	}
	if t, ok := vm.PropertyOfStringsRunes(n.Property); ok && sameTable(t, n.Strings) {
		return false, true
	}
	if t, ok := reversedTable(n.Property); ok && sameTable(t, n.Strings) {
		return true, true
	}
	return false, false
}

func sameTable(a, b [][]rune) bool {
	return len(a) == len(b) && unsafe.SliceData(a) == unsafe.SliceData(b)
}

// table returns the members of a property of strings, reversed if backward.
func table(name string, backward bool) ([][]rune, bool) {
	if backward {
		return reversedTable(name)
	}
	return vm.PropertyOfStringsRunes(name)
}

// reversedTable returns the members of a property of strings with each
// reversed, for a lookbehind body.
func reversedTable(name string) ([][]rune, bool) {
	if v, ok := reversedTables.Load(name); ok {
		return v.([][]rune), true
	}
	members, ok := vm.PropertyOfStringsRunes(name)
	if !ok {
		return nil, false
	}
	rev := make([][]rune, len(members))
	for i, s := range members {
		r := slices.Clone(s)
		slices.Reverse(r)
		rev[i] = r
	}
	v, _ := reversedTables.LoadOrStore(name, rev)
	return v.([][]rune), true
}

// propertyValue returns a property's members evaluated as a class set. Its
// strings and chars are shared, and like every class set value never
// modified.
func propertyValue(key propertyKey) classSetValue {
	if v, ok := propertyValues.Load(key); ok {
		return v.(classSetValue)
	}
	members, _ := table(key.name, key.backward)
	c := &Compiler{ignoreCase: key.ignoreCase}
	value := c.evalStrings(&parser.ClassStrings{Strings: members, Property: key.name})
	value.prop = &key
	v, _ := propertyValues.LoadOrStore(key, value)
	return v.(classSetValue)
}
