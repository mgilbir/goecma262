package vm

import (
	"fmt"
	"testing"
)

// TestASCIISetFastMatchesSlow checks the set-arithmetic fast path against
// evaluating the matcher on every ASCII rune, for every single-rune opcode and
// flag combination.
func TestASCIISetFastMatchesSlow(t *testing.T) {
	r := func(a, b rune) ClassAtom { return ClassAtom{Kind: ClassAtomRange, Range: RuneRange{a, b}} }
	classes := [][]ClassAtom{
		nil,
		{r('a', 'z')},
		{r('A', 'Z'), r('0', '9'), r('_', '_')},
		{r(0, 0x7f)},
		{r('x', 0x10ffff)},
		{r(0x80, 0xff)},
		{r('Y', 'b')},
		{{Kind: ClassAtomDigit}, {Kind: ClassAtomWord, Negated: true}},
		{{Kind: ClassAtomSpace}, r('-', '-')},
		{{Kind: ClassAtomDigit, Negated: true}, {Kind: ClassAtomSpace, Negated: true}},
		{{Kind: ClassAtomUnicodeProp, Prop: "L"}},
	}
	var insts []Instruction
	for c := rune(0); c < 0x90; c++ {
		insts = append(insts, Instruction{Op: OpChar, Char: c})
	}
	insts = append(insts, Instruction{Op: OpChar, Char: 0x212a}, Instruction{Op: OpChar, Char: 0x17f})
	for _, op := range []Opcode{OpAny, OpDigit, OpNonDigit, OpWord, OpNonWord, OpSpace, OpNonSpace} {
		insts = append(insts, Instruction{Op: op})
	}
	for _, cl := range classes {
		insts = append(insts, Instruction{Op: OpClass, Class: cl}, Instruction{Op: OpClass, Class: cl, Negate: true})
	}
	fast := 0
	for mask := 0; mask < 16; mask++ {
		p := &Program{ignoreCase: mask&1 != 0, multiline: mask&2 != 0, dotAll: mask&4 != 0, unicode: mask&8 != 0}
		for i := range insts {
			got, ok := p.asciiSetFast(&insts[i])
			if !ok {
				continue
			}
			fast++
			lo, hi := p.asciiSetSlow(&insts[i])
			if got != [2]uint64{lo, hi} {
				t.Errorf("flags %04b, %s %v: fast %x, slow %x", mask, insts[i], fmt.Sprint(insts[i].Class), got, [2]uint64{lo, hi})
			}
		}
	}
	if fast < len(insts) {
		t.Fatalf("fast path covered only %d cases", fast)
	}
}
