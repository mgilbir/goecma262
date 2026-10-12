package vm

import (
	"context"
	"strings"
	"testing"
)

// countingCtx is a context that can be cancelled but never is, and counts
// its polls.
type countingCtx struct {
	context.Context
	polls int
}

func (c *countingCtx) Done() <-chan struct{} { return make(chan struct{}) }
func (c *countingCtx) Err() error            { c.polls++; return nil }

// A backreference may compare a long text in one step, so the comparing is
// polled as well as the steps: in ^(a+)\1{10}z on 40,000 a, the
// backreferences compare tens of millions of characters in a few hundred
// thousand steps.
func TestBackreferencesArePolled(t *testing.T) {
	code := []Instruction{
		{Op: OpSaveStart, A: 0},
		{Op: OpStartLine},
		{Op: OpSaveStart, A: 1},
		{Op: OpChar, Char: 'a'},
		{Op: OpSplit, A: 3, B: 5},
		{Op: OpSaveEnd, A: 1},
	}
	for i := 0; i < 10; i++ {
		code = append(code, Instruction{Op: OpBackref, A: 1})
	}
	code = append(code, Instruction{Op: OpChar, Char: 'z'}, Instruction{Op: OpSaveEnd, A: 0}, Instruction{Op: OpMatch})
	ctx := &countingCtx{Context: context.Background()}
	m := &VM{Code: code, NumGroups: 1, Context: ctx}
	if ok, _, _ := m.Match(strings.Repeat("a", 40_000), 0); ok || m.Err != nil {
		t.Fatalf("matched %v, error %v", ok, m.Err)
	}
	stepPolls := m.steps/PollSteps + 1
	if ctx.polls < 2*stepPolls {
		t.Errorf("polled %d times in %d steps: no more than the steps alone (%d)", ctx.polls, m.steps, stepPolls)
	}
	t.Logf("%d polls in %d steps", ctx.polls, m.steps)
}

// Polling is free when the context cannot be cancelled, and successive
// MatchAt calls share the poll schedule even when each takes fewer steps
// than separate two polls.
func TestPollsAcrossStartPositions(t *testing.T) {
	code := []Instruction{{Op: OpSaveStart, A: 0}, {Op: OpChar, Char: 'b'}, {Op: OpSaveEnd, A: 0}, {Op: OpMatch}}
	ctx := &countingCtx{Context: context.Background()}
	m := &VM{Code: code, Context: ctx}
	input := strings.Repeat("a", 100_000)
	for pos := 0; pos <= len(input); pos++ {
		if ok, _, _ := m.MatchAt(input, pos); ok || m.Err != nil {
			t.Fatalf("matched %v, error %v", ok, m.Err)
		}
	}
	if want := m.steps / PollSteps; ctx.polls < want {
		t.Errorf("polled %d times in %d steps over %d start positions, want %d", ctx.polls, m.steps, len(input)+1, want)
	}
}

// A greedy loop gives characters back inside backtrack, without passing a
// split, so that is polled too: in ^[ab]*bc on a…a, no character given back
// can begin bc, and half the steps are spent there.
func TestGreedyRetreatsArePolled(t *testing.T) {
	code := []Instruction{
		{Op: OpSaveStart, A: 0},
		{Op: OpStartLine},
		{Op: OpSplit, A: 3, B: 5},
		{Op: OpClass, Class: []ClassAtom{{Kind: ClassAtomRange, Range: RuneRange{'a', 'b'}}}},
		{Op: OpJmp, A: 2},
		{Op: OpChar, Char: 'b'},
		{Op: OpChar, Char: 'c'},
		{Op: OpSaveEnd, A: 0},
		{Op: OpMatch},
	}
	ctx := &countingCtx{Context: context.Background()}
	m := &VM{Code: code, Context: ctx}
	if ok, _, _ := m.Match(strings.Repeat("a", 1_000_000), 0); ok || m.Err != nil {
		t.Fatalf("matched %v, error %v", ok, m.Err)
	}
	if want := m.steps / PollSteps * 9 / 10; ctx.polls < want {
		t.Errorf("polled %d times in %d steps, want at least %d", ctx.polls, m.steps, want)
	}
}
