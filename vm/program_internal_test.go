package vm

import (
	"fmt"
	"math/rand"
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

// TestLeafBytesCacheKeepsInstructionsApart computes the leaf bytes of every
// single-rune instruction in one program, whose cache is shared by
// instructions with the same class data but different negation or flags,
// and checks each against the bytes computed alone.
func TestLeafBytesCacheKeepsInstructionsApart(t *testing.T) {
	insts := singleRuneInstructions()
	insts = append(insts,
		Instruction{Op: OpUnicodeProp, Prop: "Lu"}, Instruction{Op: OpNotUnicodeProp, Prop: "Lu"},
		Instruction{Op: OpUnicodeProp, Prop: "Nd"}, Instruction{Op: OpNotUnicodeProp, Prop: "Nd"},
		Instruction{Op: OpClassSet, Set: NewCharSetRanges([]RuneRange{{'a', 'c'}})},
		Instruction{Op: OpClassSet, Set: NewCharSetRanges([]RuneRange{{'x', 'z'}})},
		Instruction{Op: OpRuneSwitch, Switch: &RuneSwitch{Keys: []rune{'a'}, Targets: []int{0}}},
		Instruction{Op: OpRuneSwitch, Switch: &RuneSwitch{Keys: []rune{'q'}, Targets: []int{0}}},
		Instruction{Op: OpUnicodeProp, Prop: "Lu", Mode: ModeSet | ModeIgnoreCase},
		Instruction{Op: OpNotUnicodeProp, Prop: "Lu", Mode: ModeSet | ModeIgnoreCase})
	for mask := 0; mask < 16; mask++ {
		p := testProgram(mask)
		p.code = insts
		for pc := range insts {
			got := p.leafBytes(pc)
			alone := testProgram(mask)
			alone.code = insts[pc : pc+1]
			if want := alone.leafBytes(0); got != want {
				t.Errorf("flags %04b, %s %v negate %v mode %04b: %x in the program, %x alone", mask, insts[pc], fmt.Sprint(insts[pc].Class), insts[pc].Negate, insts[pc].Mode, got, want)
			}
		}
	}
}

// TestDirectionsAreTheInnermostLookaround checks computeDirections against
// the innermost lookaround enclosing each pc, on random programs of nested
// and adjacent lookarounds of many lengths.
func TestDirectionsAreTheInnermostLookaround(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for trial := 0; trial < 200; trial++ {
		var code []Instruction
		var gen func(depth int)
		gen = func(depth int) {
			for n := rng.Intn(4); n >= 0; n-- {
				if depth == 0 || rng.Intn(3) == 0 {
					for k := rng.Intn(3); k >= 0; k-- {
						code = append(code, Instruction{Op: OpChar, Char: 'a'})
					}
					continue
				}
				jmp := len(code)
				code = append(code, Instruction{Op: OpJmp})
				start := len(code)
				gen(depth - 1)
				op := []Opcode{OpLookahead, OpNegLookahead, OpLookbehind, OpNegLookbehind}[rng.Intn(4)]
				code = append(code, Instruction{Op: op, A: start, B: len(code)})
				code[jmp].A = len(code) - 1
			}
		}
		gen(4)
		backward := rng.Intn(2) == 0
		p := &Program{code: code, backward: backward}
		p.computeDirections()
		for pc := range code {
			want, size := backward, len(code)+1
			for _, inst := range code {
				if isLookaround(inst.Op) && inst.A <= pc && pc < inst.B && inst.B-inst.A < size {
					want, size = inst.Op == OpLookbehind || inst.Op == OpNegLookbehind, inst.B-inst.A
				}
			}
			if p.dir[pc] != want {
				t.Fatalf("trial %d: pc %d of %d runs backward %v, want %v", trial, pc, len(code), p.dir[pc], want)
			}
		}
	}
}
