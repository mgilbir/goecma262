package vm

import (
	"fmt"
	"testing"
)

// TestASCIISetFastMatchesSlow checks the set-arithmetic fast path against
// evaluating the matcher on every ASCII rune, for every single-rune opcode and
// flag combination.
func TestASCIISetFastMatchesSlow(t *testing.T) {
	insts := singleRuneInstructions()
	fast := 0
	for mask := 0; mask < 16; mask++ {
		p := testProgram(mask)
		for i := range insts {
			got, ok := p.asciiSetFast(&insts[i])
			if !ok {
				continue
			}
			fast++
			lo, hi := p.asciiSetSlow(&insts[i])
			if got != [2]uint64{lo, hi} {
				t.Errorf("flags %04b, %s %v mode %04b: fast %x, slow %x", mask, insts[i], fmt.Sprint(insts[i].Class), insts[i].Mode, got, [2]uint64{lo, hi})
			}
		}
	}
	if fast < len(insts) {
		t.Fatalf("fast path covered only %d cases", fast)
	}
}

// TestASCIIOnlyIsSound checks that an instruction the analysis calls
// ASCII-only (whose first-byte set then excludes every non-ASCII byte)
// matches no non-ASCII rune, for every single-rune instruction and flag
// combination. The runes include those that case-fold to ASCII, and the
// surrogates a character above U+FFFF is read as without u or v.
func TestASCIIOnlyIsSound(t *testing.T) {
	nonASCII := []rune{0x80, 0xe9, 0xc9, 0x17f, 0x212a, 0x131, 0x130, 0x2028, 0xa0, 0xfeff, 0xfffd, 0xd83d, 0xde00, 0x1f600, 0x10ffff}
	insts := singleRuneInstructions()
	checked := 0
	for mask := 0; mask < 16; mask++ {
		p := testProgram(mask)
		m := &VM{IgnoreCase: p.ignoreCase, Multiline: p.multiline, DotAll: p.dotAll, Unicode: p.unicode}
		for i := range insts {
			if !p.asciiOnly(&insts[i]) {
				continue
			}
			checked++
			for _, c := range nonASCII {
				if m.matchOne(&insts[i], c) {
					t.Errorf("flags %04b, %s %v mode %04b: called ASCII-only but matches %U", mask, insts[i], fmt.Sprint(insts[i].Class), insts[i].Mode, c)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no instruction was ASCII-only")
	}
}

// testProgram returns a Program with the flags of mask's four bits.
func testProgram(mask int) *Program {
	return &Program{ignoreCase: mask&1 != 0, multiline: mask&2 != 0, dotAll: mask&4 != 0, unicode: mask&8 != 0}
}

// singleRuneInstructions covers every single-rune opcode, with characters and
// classes around the ASCII boundary and the case-folding exceptions, each
// also with the flags of a modifier group.
func singleRuneInstructions() []Instruction {
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
	insts = append(insts, Instruction{Op: OpChar, Char: 0x212a}, Instruction{Op: OpChar, Char: 0x17f}, Instruction{Op: OpChar, Char: 0xd83d})
	for _, op := range []Opcode{OpAny, OpDigit, OpNonDigit, OpWord, OpNonWord, OpSpace, OpNonSpace} {
		insts = append(insts, Instruction{Op: op})
	}
	for _, cl := range classes {
		insts = append(insts, Instruction{Op: OpClass, Class: cl}, Instruction{Op: OpClass, Class: cl, Negate: true})
	}
	n := len(insts)
	for _, mode := range []Mode{ModeSet, ModeSet | ModeIgnoreCase, ModeSet | ModeDotAll, ModeSet | ModeIgnoreCase | ModeDotAll, ModeSet | ModeMultiline} {
		for _, inst := range insts[:n] {
			inst.Mode = mode
			insts = append(insts, inst)
		}
	}
	return insts
}
