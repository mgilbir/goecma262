package parser

import (
	"math/rand"
	"strings"
	"testing"
)

// The term stack must behave as a plain slice across its chunk boundaries,
// with nested pushes and pops at every depth.
func TestTermStackMatchesSlice(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var s termStack
	var ref []Expression
	var bases []int
	for step := 0; step < 200_000; step++ {
		switch r := rng.Intn(100); {
		case r < 2 || len(bases) == 0 && r < 10:
			bases = append(bases, len(ref)) // open a nested sequence
		case r < 12 && len(bases) > 0:
			base := bases[len(bases)-1]
			bases = bases[:len(bases)-1]
			got := s.pop(base)
			want := ref[base:]
			if len(got) != len(want) || cap(got) != len(want) {
				t.Fatalf("step %d: pop(%d) returned len %d cap %d, want %d", step, base, len(got), cap(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("step %d: pop(%d)[%d] differs", step, base, i)
				}
			}
			ref = ref[:base]
		default:
			e := &Literal{Char: rune(step)}
			s.push(e)
			ref = append(ref, e)
		}
		if s.n != len(ref) {
			t.Fatalf("step %d: stack holds %d entries, want %d", step, s.n, len(ref))
		}
	}
	if len(ref) < 2*chunkLenMax+firstChunksLen {
		t.Errorf("the walk peaked below the chunk sizes it should cross")
	}
}

func TestLocate(t *testing.T) {
	c, off := 0, 0
	for i := 0; i < 5000; i++ {
		gc, goff := locate(i)
		if gc != c || goff != off {
			t.Fatalf("locate(%d) = %d, %d; want %d, %d", i, gc, goff, c, off)
		}
		if off++; off == chunkLen(c) {
			c, off = c+1, 0
		}
	}
}

// A long pattern with nested groups and alternatives straddling the chunk
// boundaries parses to its literals in source order.
func TestLongPatternKeepsOrder(t *testing.T) {
	var sb strings.Builder
	var want []rune
	for i := 0; i < 3000; i++ {
		switch i % 97 {
		case 10:
			sb.WriteString("(?:")
		case 60:
			sb.WriteString(")")
		case 80:
			sb.WriteString("|")
		}
		r := rune(0x4E00 + i)
		sb.WriteRune(r)
		want = append(want, r)
	}
	pat, err := New(sb.String(), Flags{}).Parse()
	if err != nil {
		t.Fatal(err)
	}
	var got []rune
	var walk func(Expression)
	walk = func(e Expression) {
		switch n := e.(type) {
		case *Literal:
			got = append(got, n.Char)
		case *Sequence:
			for _, el := range n.Elements {
				walk(el)
			}
		case *Disjunction:
			for _, a := range n.Alternatives {
				walk(a)
			}
		case *NonCapturingGroup:
			walk(n.Body)
		default:
			t.Fatalf("unexpected node %T", e)
		}
	}
	walk(pat.Body)
	if string(got) != string(want) {
		t.Fatalf("literals out of order: got %d runes, want %d", len(got), len(want))
	}
}
