package notify

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Chans TODO
type Chans []chan EventInfo

// Foreach TODO
func (c Chans) Foreach(fn func(chan<- EventInfo, Node)) {
	for i, ch := range c {
		fn(ch, Node{Name: strconv.Itoa(i)})
	}
}

// NewChans TODO
func NewChans(n int) Chans {
	ch := make([]chan EventInfo, n)
	for i := range ch {
		ch[i] = make(chan EventInfo, 16)
	}
	return ch
}

// FuncType represents enums for Watcher interface.
type FuncType string

const (
	FuncWatch            = FuncType("Watch")
	FuncUnwatch          = FuncType("Unwatch")
	FuncDispatch         = FuncType("Dispatch")
	FuncRewatch          = FuncType("Rewatch")
	FuncRecursiveWatch   = FuncType("RecursiveWatch")
	FuncRecursiveUnwatch = FuncType("RecursiveUnwatch")
	FuncRecursiveRewatch = FuncType("RecursiveRewatch")
	FuncStop             = FuncType("Stop")
)

// TreeType TODO
type TreeType uint8

const (
	TreeWatcher   TreeType = 1 << iota // implements Watcher only
	TreeRewatcher                      // implements Watcher + Rewatcher
	TreeRecursive                      // implements Watcher + Rewatcher + Recursive
)

// TreeAll TODO
//
// NOTE(rjeczalik): For use only as a key of Record.
const TreeAll TreeType = TreeWatcher | TreeRewatcher | TreeRecursive

// Strings implements fmt.Stringer interface.
func (typ TreeType) String() string {
	switch typ {
	case TreeWatcher:
		return "TreeWatcher"
	case TreeRewatcher:
		return "TreeRewatcher"
	case TreeRecursive:
		return "TreeRecursive"
	}
	return "<unknown tree type>"
}

// IsDir reports whether p points to a directory. A path that ends with a trailing
// path separator is assumed to be a directory.
func IsDir(p string) bool {
	return strings.HasSuffix(p, sep) || strings.HasSuffix(p, sep+"...")
}

// Call represents single call to Watcher issued by the Tree
// and recorded by a spy Watcher mock.
type Call struct {
	F  FuncType
	C  chan EventInfo
	P  string // regular Path argument and old path from RecursiveRewatch call
	NP string // new Path argument from RecursiveRewatch call
	E  Event  // regular Event argument and old Event from a Rewatch call
	NE Event  // new Event argument from Rewatch call
}

// TreeEvent TODO
type TreeEvent struct {
	P string
	E Event
}

// TreeEvent implements EventInfo interface.
func (e TreeEvent) Event() Event     { return e.E }
func (e TreeEvent) Path() string     { return e.P }
func (e TreeEvent) IsDir() bool      { return IsDir(e.P) }
func (e TreeEvent) String() string   { return e.E.String() + " - " + e.P }
func (e TreeEvent) Sys() interface{} { return nil }

// Record TODO
type Record map[TreeType][]Call

// CallCase describes a single test case. Call describes either Watch or Stop
// invocation made on a Tree, while Record describes all internal calls
// to the Watcher implementation made by Tree during that call.
type CallCase struct {
	Call   Call   // call invoked on Tree
	Record Record // intermediate calls recorded during above call
}

// NativeCallCases TODO
func NativeCallCases(cases []CallCase) []CallCase {
	if runtime.GOOS == "windows" {
		for i := range cases {
			cases[i].Call.P = filepath.FromSlash(cases[i].Call.P)
			for _, calls := range cases[i].Record {
				for i := range calls {
					calls[i].P = filepath.FromSlash(calls[i].P)
				}
			}
		}
	}
	return cases
}

// EventCase TODO
type EventCase struct {
	Event    TreeEvent
	Receiver []chan EventInfo // receiver only
}

// NativeEventCases TODO
func NativeEventCases(cases []EventCase) []EventCase {
	if runtime.GOOS == "windows" {
		for i := range cases {
			cases[i].Event.P = filepath.FromSlash(cases[i].Event.P)
		}
	}
	return cases
}

// MockedTree TODO
type MockedTree struct {
	Spy                   // implements Watcher, RecursiveWatcher or RecursiveRewatcher
	Tree *Tree            // actual tree being tested
	N    int              // call start offset
	C    chan<- EventInfo // event dispatch channel
}

// Dispatch implements Watcher interface.
func (mt *MockedTree) Dispatch(c chan<- EventInfo, _ <-chan struct{}) {
	mt.C = c
}

// Invoke TODO
func (mt *MockedTree) Invoke(call Call) error {
	switch call.F {
	case FuncWatch:
		return mt.Tree.Watch(call.P, call.C, call.E)
	case FuncStop:
		mt.Tree.Stop(call.C)
		return nil
	}
	panic("(*TreeFixture).invoke: invalid Tree call: " + call.F)
}

// TreeFixture represents a fixture for Tree testing.
type TreeFixture map[TreeType]*MockedTree

// TreeFixture gives new fixture for Tree testing. It initializes a new Tree
// with a spy Watcher mock, which is used for retrospecting calls to it the Tree
// makes.
func NewTreeFixture(types ...TreeType) TreeFixture {
	if len(types) == 0 {
		types = []TreeType{TreeWatcher, TreeRewatcher, TreeRecursive}
	}
	tf := make(map[TreeType]*MockedTree, len(types))
	for _, typ := range types {
		// TODO(rjeczalik): Copy FS to allow for modying tree via Create and
		// Delete events.
		mt := &MockedTree{}
		tf[typ] = mt
		mt.Tree = NewTree(SpyWatcher(typ, mt))
		mt.Tree.FS = MFS
	}
	return tf
}

// ExpectCalls translates values specified by the cases' keys into Watch calls
// executed on the fixture's Tree. A spy Watcher mock records calls to it
// and compares with the expected ones for each key in the list.
func (tf TreeFixture) TestCalls(t *testing.T, cases []CallCase) {
	var record []Call
	for i, cas := range NativeCallCases(cases) {
		for typ, tree := range tf {
			// Invoke call and record underlying calls.
			if err := tree.Invoke(cas.Call); err != nil {
				t.Fatalf("want Tree.%s(...)=nil; got %v (i=%d, typ=%v)",
					cas.Call.F, err, i, typ)
			}
			// Skip if expected and got records were empty.
			got := tree.Spy[tree.N:]
			if len(cas.Record) == 0 && len(got) == 0 {
				continue
			}
			// Find expected records for typ Tree.
			if rec, ok := cas.Record[typ]; ok {
				record = rec
			} else {
				for rectyp, rec := range cas.Record {
					if rectyp&typ != 0 {
						record = rec
						break
					}
				}
			}
			if !reflect.DeepEqual(got, Spy(record)) {
				t.Errorf("want recorded=%+v; got %+v (i=%d, typ=%v)",
					record, got, i, typ)
			}
			tree.N = len(tree.Spy)
		}
	}
}

var timeout = 50 * time.Millisecond

func str(ei EventInfo) string {
	if ei == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{Name()=%q, Event()=%v, IsDir()=%v}", ei.Path(), ei.Event(),
		ei.IsDir())
}

func equal(want, got EventInfo) error {
	if (want == nil && got != nil) || (want != nil && got == nil) {
		return fmt.Errorf("want EventInfo=%s; got %s", str(want), str(got))
	}
	if want.Path() != got.Path() || want.Event() != got.Event() || want.IsDir() != got.IsDir() {
		return fmt.Errorf("want EventInfo=%s; got %s", str(want), str(got))
	}
	return nil
}

// TestEvents TODO
func (tf TreeFixture) TestEvents(t *testing.T, cases []EventCase) {
	for i, cas := range NativeEventCases(cases) {
		for typ, tree := range tf {
			n := len(cas.Receiver)
			done := make(chan struct{})
			ev := make(map[<-chan EventInfo]EventInfo)
			go func() {
				cases := make([]reflect.SelectCase, n)
				for i := range cases {
					cases[i].Chan = reflect.ValueOf(cas.Receiver[i])
					cases[i].Dir = reflect.SelectRecv
				}
				for n := len(cases); n > 0; n = len(cases) {
					j, v, ok := reflect.Select(cases)
					if !ok {
						t.Fatalf("notify/test: unexpected chan close (i=%d, "+
							"typ=%v, j=%d", i, typ, j)
					}
					ch := cases[j].Chan.Interface().(chan EventInfo)
					ev[ch] = v.Interface().(EventInfo)
					cases[j], cases = cases[n-1], cases[:n-1]
				}
				close(done)
			}()
			tree.C <- cas.Event
			select {
			case <-done:
			case <-time.After(timeout):
				t.Fatalf("ExpectEvents has timed out after %v (i=%d, typ=%v)",
					timeout, i, typ)
			}
			for _, got := range ev {
				if err := equal(cas.Event, got); err != nil {
					t.Errorf("want err=nil; got %v (i=%d, typ=%v)", err, i, typ)
				}
			}
		}
	}
}

// Spy is a mock for Watcher interface, which records every call.
type Spy []Call

// SpyWatcher TODO
func SpyWatcher(typ TreeType, tree *MockedTree) Watcher {
	switch typ {
	case TreeWatcher:
		return struct {
			Watcher
		}{tree}
	case TreeRewatcher:
		return struct {
			Watcher
			Rewatcher
		}{tree, tree}
	case TreeRecursive:
		return struct {
			Watcher
			Rewatcher
			RecursiveWatcher
			RecursiveRewatcher
		}{tree, tree, tree, tree}
	}
	panic(fmt.Sprintf("notify/test: unsupported runtime type: %d (%s)", typ, typ))
}

// Watch implements Watcher interface.
func (s *Spy) Watch(p string, e Event) (err error) {
	*s = append(*s, Call{F: FuncWatch, P: p, E: e})
	return
}

// Unwatch implements Watcher interface.
func (s *Spy) Unwatch(p string) (err error) {
	*s = append(*s, Call{F: FuncUnwatch, P: p})
	return
}

// Rewatch implements Rewatcher interface.
func (s *Spy) Rewatch(p string, olde, newe Event) (err error) {
	*s = append(*s, Call{F: FuncRewatch, P: p, E: olde, NE: newe})
	return
}

// RecursiveWatch implements RecursiveWatcher interface.
func (s *Spy) RecursiveWatch(p string, e Event) (err error) {
	*s = append(*s, Call{F: FuncRecursiveWatch, P: p, E: e})
	return
}

// RecursiveUnwatch implements RecursiveWatcher interface.
func (s *Spy) RecursiveUnwatch(p string) (err error) {
	*s = append(*s, Call{F: FuncRecursiveUnwatch, P: p})
	return
}

// RecursiveRewatch implements RecursiveRewatcher interface.
func (s *Spy) RecursiveRewatch(oldp, newp string, olde, newe Event) (err error) {
	*s = append(*s, Call{F: FuncRecursiveRewatch, P: oldp, NP: newp, E: olde, NE: newe})
	return
}
