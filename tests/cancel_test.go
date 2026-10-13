package ecma262_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	"github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// errStop is what pollCtx reports once done.
var errStop = errors.New("stop")

// pollCtx is a context that is done from its n-th poll on, which makes the
// point where work is interrupted deterministic. It counts its polls.
type pollCtx struct {
	context.Context
	n, polls int
}

func newPollCtx(n int) *pollCtx {
	return &pollCtx{Context: context.Background(), n: n}
}

func (c *pollCtx) Done() <-chan struct{} { return make(chan struct{}) } // can be cancelled
func (c *pollCtx) Err() error {
	c.polls++
	if c.polls >= c.n {
		return errStop
	}
	return nil
}

// polls returns how many times f polls a context that is never done.
func polls(f func(ctx context.Context)) int {
	c := newPollCtx(int(^uint(0) >> 1))
	f(c)
	return c.polls
}

func checkInterrupted(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, errStop) {
		t.Fatalf("%s: error %v, want one wrapping the context's", what, err)
	}
	if errors.Is(err, ecma262.ErrStepLimit) || errors.Is(err, ecma262.ErrLimit) {
		t.Fatalf("%s: interruption %v reads as a limit", what, err)
	}
}

func TestCompileContextInterruptsEachPhase(t *testing.T) {
	long := strings.Repeat("(?:)", 1<<18) // long, but compiles to nothing
	cases := []struct {
		name, pattern string
		f             flags.Flags
		message       string
	}{
		{"parse", long, 0, "pattern parsing interrupted"},
		{"compile", `(?:a{1000}){190}`, 0, "compiling interrupted"},
		{"class sets", `(?:[\q{abc|abd}]){10000}`, flags.UnicodeSets, "compiling interrupted"},
	}
	for _, tc := range cases {
		total := polls(func(ctx context.Context) {
			if _, err := ecma262.CompileContext(ctx, tc.pattern, tc.f); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
		if total < 20 {
			t.Fatalf("%s: compiling polls only %d times", tc.name, total)
		}
		ctx := newPollCtx(5)
		_, err := ecma262.CompileContext(ctx, tc.pattern, tc.f)
		checkInterrupted(t, tc.name, err)
		if !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: error %q, want it to say %q", tc.name, err, tc.message)
		}
		// Once more, to tell the error from a pattern error, but no more
		// work.
		if ctx.polls > 6 {
			t.Errorf("%s: polled %d times after being done", tc.name, ctx.polls-5)
		}
	}
}

func TestCompileContextAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ecma262.CompileContext(ctx, `a`, 0)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ecma262.ErrLimit) {
		t.Fatalf("error %v, want one wrapping context.Canceled", err)
	}
	if strings.Contains(err.Error(), "parse error") || strings.Contains(err.Error(), "compile error") {
		t.Errorf("interruption %q reads as a pattern error", err)
	}
}

// Evaluating one large class set polls, though it is one node of the AST:
// the context is done just after parsing.
func TestCompileContextInterruptsClassEvaluation(t *testing.T) {
	// Strings of two characters make a trie, which Bound bounds rather than
	// counts, so the class is evaluated before any code is emitted; strings
	// of one are characters, and the class is evaluated as it is emitted.
	for _, width := range []int{2, 1} {
		var sb strings.Builder
		sb.WriteString(`[\q{`)
		for i := 0; i < 100_000; i++ {
			if i > 0 {
				sb.WriteByte('|')
			}
			sb.WriteRune(rune(0x4e00 + i))
			if width == 2 {
				sb.WriteRune(rune(0x4e00 + i/7))
			}
		}
		sb.WriteString(`}]`)
		pattern := sb.String()
		parsing := polls(func(ctx context.Context) {
			if _, err := parser.New(pattern, parser.Flags{UnicodeSets: true}).ParseContext(ctx); err != nil {
				t.Fatal(err)
			}
		})
		// CompileContext polls once as it starts, then as it evaluates. The
		// class is the whole pattern: compiling must not return what it had
		// evaluated when interrupted.
		re, err := ecma262.CompileContext(newPollCtx(parsing+3), pattern, flags.UnicodeSets)
		if re != nil {
			t.Fatalf("strings of %d: compiled although interrupted", width)
		}
		checkInterrupted(t, "class evaluation", err)
		if !strings.Contains(err.Error(), "compiling interrupted") {
			t.Errorf("strings of %d: error %q, want it to come from compiling", width, err)
		}
	}
}

// The analysis of a compiled program polls too.
func TestNewProgramContextInterrupts(t *testing.T) {
	ast, err := parser.New(`(?:(?:[a-z\p{L}]x?){5000}){9}`, parser.Flags{IgnoreCase: true, Unicode: true}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := compiler.Compile(ast)
	if err != nil {
		t.Fatal(err)
	}
	total := polls(func(ctx context.Context) {
		if _, err := vm.NewProgramContext(ctx, code, true, false, false, true, false); err != nil {
			t.Fatal(err)
		}
	})
	// Its two passes over the program each poll every 1,024 instructions.
	if total < 2*len(code)/1024 {
		t.Fatalf("the analysis of %d instructions polls only %d times", len(code), total)
	}
	_, err = vm.NewProgramContext(newPollCtx(3), code, true, false, false, true, false)
	checkInterrupted(t, "analysis", err)
}

// A match that would run its whole budget stops once its context is done:
// (a|aa)*b on 6 MiB of a ran 1.2 s and allocated 2.8 GB before.
func TestWithContextInterruptsMatching(t *testing.T) {
	re := ecma262.MustCompile(`(a|aa)*b`, 0)
	input := strings.Repeat("a", 6<<20)
	ctx := newPollCtx(10)
	var err error
	allocated := allocatedBy(func() { _, err = re.WithContext(ctx).MatchStringErr(input) })
	checkInterrupted(t, "MatchStringErr", err)
	if allocated > 64<<20 {
		t.Errorf("interrupted after %d polls, having allocated %d bytes", ctx.polls, allocated)
	}
	// The forms without an error report no match, and re itself is not
	// affected.
	if re.WithContext(newPollCtx(10)).MatchString(input) {
		t.Error("MatchString matched")
	}
	if ok, err := re.MatchStringErr("aab"); !ok || err != nil {
		t.Errorf("re matched %v, %v after a copy was interrupted", ok, err)
	}
}

// An iteration of many cheap searches, each taking fewer steps than separate
// two polls, still polls.
func TestWithContextInterruptsIteration(t *testing.T) {
	re := ecma262.MustCompile(`a`, flags.Global)
	input := strings.Repeat("a", 1<<20)
	ctx := newPollCtx(100)
	_, err := re.WithContext(ctx).FindAllStringSubmatchIndexErr(input, -1)
	checkInterrupted(t, "FindAll", err)
	_, err = re.WithContext(newPollCtx(100)).ReplaceAllStringErr(input, "b")
	checkInterrupted(t, "ReplaceAll", err)
	_, err = ecma262.MustCompile(`a`, 0).WithContext(newPollCtx(100)).SplitErr(input, -1)
	checkInterrupted(t, "Split", err)
}

// A real deadline, end to end.
func TestWithContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	re := ecma262.MustCompile(`(a|aa)*b`, 0)
	_, err := re.WithContext(ctx).MatchStringErr(strings.Repeat("a", 6<<20))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want context.DeadlineExceeded", err)
	}
}

func TestWithContextHasItsOwnLastIndex(t *testing.T) {
	re := ecma262.MustCompile(`a`, flags.Global)
	re.SetLastIndex(1)
	cp := re.WithContext(context.Background())
	if cp.LastIndex() != 1 {
		t.Fatalf("copy starts at lastIndex %d, want 1", cp.LastIndex())
	}
	if !cp.MatchString("aaa") || cp.LastIndex() != 2 || re.LastIndex() != 1 {
		t.Errorf("after a match on the copy, its lastIndex is %d and re's %d; want 2 and 1", cp.LastIndex(), re.LastIndex())
	}
}

func TestSetMaxMemory(t *testing.T) {
	re := ecma262.MustCompile(`(a|aa)*b`, 0)
	re.SetMaxMemory(1 << 20)
	var err error
	allocated := allocatedBy(func() { _, err = re.MatchStringErr(strings.Repeat("a", 1<<20)) })
	if !errors.Is(err, ecma262.ErrStepLimit) {
		t.Fatalf("error %v, want ErrStepLimit's memory form", err)
	}
	// The default would be 256 MiB plus 32 bytes per byte of input.
	if allocated > 16<<20 {
		t.Errorf("a 1 MiB memory budget allocated %d bytes", allocated)
	}
	re.SetMaxMemory(0)
	if ok, err := re.MatchStringErr(strings.Repeat("a", 1000)); ok || err != nil {
		t.Errorf("with the default restored: %v, %v", ok, err)
	}
}

// A compile whose context ends before its program is allocated allocates
// none: on v0.3.0, a context that ended at the compiler's first poll, after
// Bound, still cost the whole program, 19.3 MB here.
func TestCancelledCompileAllocatesNoProgram(t *testing.T) {
	ast, err := parser.New(`(a{1000}){150}`, parser.Flags{}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		var err error
		allocated := allocatedBy(func() { _, _, err = compiler.CompileContext(newPollCtx(n), ast) })
		checkInterrupted(t, "compile", err)
		if allocated > 64<<10 {
			t.Errorf("cancelled at poll %d: allocated %d bytes", n, allocated)
		}
	}
}

// Bound walks the whole AST, which a long pattern makes large, so it polls.
func TestBoundContextPolls(t *testing.T) {
	const n = 40_000
	ast, err := parser.New(strings.Repeat("(?:a|b)", n), parser.Flags{}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	nodes := 4 * n // a group's disjunction and two literals, and the sequence
	total := polls(func(ctx context.Context) {
		if _, err := compiler.BoundContext(ctx, ast); err != nil {
			t.Fatal(err)
		}
	})
	if total < nodes/1024 {
		t.Errorf("Bound polled %d times over %d nodes", total, nodes)
	}
	_, err = compiler.BoundContext(newPollCtx(3), ast)
	checkInterrupted(t, "Bound", err)
	if !strings.Contains(err.Error(), "bounding interrupted") {
		t.Errorf("error %q", err)
	}
	// Compiling bounds the pattern first; interrupted there, it allocates
	// no program.
	var cerr error
	allocated := allocatedBy(func() { _, _, cerr = compiler.CompileContext(newPollCtx(3), ast) })
	checkInterrupted(t, "compile", cerr)
	if allocated > 64<<10 {
		t.Errorf("a compile cancelled while bounding allocated %d bytes", allocated)
	}
}

// A pattern whose classes Bound bounds rather than counts is walked three
// times by a compile, to bound it, to count it exactly and to emit it, and
// each walk polls.
func TestCompilePollsEachWalk(t *testing.T) {
	const n = 30_000
	ast, err := parser.New(`[\q{ab}]`+strings.Repeat("(?:a|b)", n), parser.Flags{UnicodeSets: true}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	nodes := 4 * n
	total := polls(func(ctx context.Context) {
		if _, _, err := compiler.CompileContext(ctx, ast); err != nil {
			t.Fatal(err)
		}
	})
	if total < 3*nodes/1024 {
		t.Errorf("compiling polled %d times over three walks of %d nodes", total, nodes)
	}
}
