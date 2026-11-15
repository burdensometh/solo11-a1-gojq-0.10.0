package gojq_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"
	"testing"

	"github.com/itchyny/gojq"
)

// listInputIter is a resettable iterator over a fixed list of values.
// It records how far the cursor has advanced, so tests can assert that two
// runs reusing the same compiled Code keep independent input cursors.
type listInputIter struct {
	values   []interface{}
	index    int
	err      error
	errIndex int // inject err before returning the value at this index (-1 to disable)
}

func (i *listInputIter) Next() (interface{}, bool) {
	if i.errIndex == i.index && i.err != nil {
		return i.err, true
	}
	if i.index >= len(i.values) {
		return nil, false
	}
	v := i.values[i.index]
	i.index++
	return v, true
}

func compileWithInput(t *testing.T, src string) *gojq.Code {
	t.Helper()
	query, err := gojq.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	// WithInputIter(nil) only allows input(s)/0; the actual iterator is
	// bound to each run using the WithRunInputIter run option.
	code, err := gojq.Compile(query, gojq.WithInputIter(nil))
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func drain(iter gojq.Iter) ([]interface{}, error) {
	var vs []interface{}
	for {
		v, ok := iter.Next()
		if !ok {
			return vs, nil
		}
		if err, ok := v.(error); ok {
			return vs, err
		}
		vs = append(vs, v)
	}
}

// A and B use separate input iterators against the same compiled Code;
// interleaving the two runs must advance only the cursor of each run.
func TestRunInputIter_IndependentCursors(t *testing.T) {
	code := compileWithInput(t, "input, input, input")
	iterA := &listInputIter{values: []interface{}{"a1", "a2", "a3"}}
	iterB := &listInputIter{values: []interface{}{"b1", "b2", "b3"}}

	runA := code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterA))
	runB := code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterB))

	got, expected := []interface{}{}, []interface{}{"a1", "b1", "a2", "b2", "a3", "b3"}
	for i := 0; i < 6; i++ {
		var v interface{}
		var ok bool
		if i%2 == 0 {
			v, ok = runA.Next()
		} else {
			v, ok = runB.Next()
		}
		if !ok {
			t.Fatalf("iteration %d ended unexpectedly", i)
		}
		if err, isErr := v.(error); isErr {
			t.Fatalf("iteration %d got error: %v", i, err)
		}
		got = append(got, v)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, got)
	}
	if _, ok := runA.Next(); ok {
		t.Error("run A must be exhausted")
	}
	if _, ok := runB.Next(); ok {
		t.Error("run B must be exhausted")
	}
	if iterA.index != 3 || iterB.index != 3 {
		t.Errorf("cursors must be independent: A=%d B=%d", iterA.index, iterB.index)
	}
}

// Exhausting A must not affect B. The inputs builtin catches the "break"
// error raised when the iterator is exhausted and terminates gracefully.
func TestRunInputIter_ExhaustionIsolation(t *testing.T) {
	code := compileWithInput(t, "[inputs]")
	iterA := &listInputIter{values: []interface{}{1, 2}}
	iterB := &listInputIter{values: []interface{}{10, 20, 30}}

	vsA, errA := drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterA)))
	if errA != nil {
		t.Fatalf("the inputs builtin must swallow the break error, got: %v", errA)
	}
	if expected := []interface{}{[]interface{}{1, 2}}; !reflect.DeepEqual(vsA, expected) {
		t.Errorf("A expected: %#v, got: %#v", expected, vsA)
	}

	vsB, errB := drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterB)))
	if errB != nil {
		t.Fatalf("B must be unaffected by A being exhausted, got: %v", errB)
	}
	if expected := []interface{}{[]interface{}{10, 20, 30}}; !reflect.DeepEqual(vsB, expected) {
		t.Errorf("B expected: %#v, got: %#v", expected, vsB)
	}
}

// The raw "break" error must still surface when input is called past the
// end of the iterator (this is the existing behavior relied on by range).
func TestRunInputIter_ExhaustionBreakError(t *testing.T) {
	code := compileWithInput(t, "range(10) | input")
	iter := &listInputIter{values: []interface{}{1, 2}}
	vs, err := drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iter)))
	if expected := []interface{}{1, 2}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, vs)
	}
	if err == nil || err.Error() != "break" {
		t.Errorf("expected break error, got: %v", err)
	}
}

// An error surfaced by A's iterator must not touch B's cursor.
func TestRunInputIter_ErrorIsolation(t *testing.T) {
	code := compileWithInput(t, "input, input")
	boom := errors.New("boom")
	iterA := &listInputIter{values: []interface{}{"a1", "a2"}, err: boom, errIndex: 1}
	iterB := &listInputIter{values: []interface{}{"b1", "b2"}}

	runA := code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterA))
	v, ok := runA.Next()
	if !ok || v != "a1" {
		t.Fatalf("expected a1, got %#v (ok=%v)", v, ok)
	}
	if v, ok := runA.Next(); !ok || v != boom {
		t.Fatalf("expected the iterator error, got %#v (ok=%v)", v, ok)
	}
	// A remains at its own cursor; the error was injected without consuming
	// a value.
	if iterA.index != 1 {
		t.Errorf("A cursor must stay at 1, got %d", iterA.index)
	}

	vs, err := drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterB)))
	if err != nil {
		t.Fatalf("B must be unaffected by A's error, got: %v", err)
	}
	if expected := []interface{}{"b1", "b2"}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("B expected: %#v, got: %#v", expected, vs)
	}
	if iterB.index != 2 {
		t.Errorf("B cursor must be independent at 2, got %d", iterB.index)
	}
}

// Cancelling A's context must not prevent B from running.
func TestRunInputIter_CancellationIsolation(t *testing.T) {
	code := compileWithInput(t, "input")
	iterA := &listInputIter{values: []interface{}{"a1", "a2"}}
	iterB := &listInputIter{values: []interface{}{"b1", "b2"}}

	ctxA, cancel := context.WithCancel(context.Background())
	runA := code.RunWithContextAndOptions(ctxA, nil, nil, gojq.WithRunInputIter(iterA))
	v, ok := runA.Next()
	if !ok || v != "a1" {
		t.Fatalf("expected a1, got %#v (ok=%v)", v, ok)
	}
	cancel()
	if v, ok := runA.Next(); !ok || v != context.Canceled {
		t.Fatalf("expected context.Canceled, got %#v (ok=%v)", v, ok)
	}

	// B is a separate batch with its own iterator and context.
	vs, err := drain(code.RunWithContextAndOptions(
		context.Background(), nil, nil, gojq.WithRunInputIter(iterB),
	))
	if err != nil {
		t.Fatalf("B must be unaffected by A's cancellation, got: %v", err)
	}
	if expected := []interface{}{"b1"}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("B expected: %#v, got: %#v", expected, vs)
	}
}

// The per-run input binding must reach input(s) used inside reduce and
// foreach, which resume through backtracking forks.
func TestRunInputIter_ReduceAndForeach(t *testing.T) {
	reduceCode := compileWithInput(t, "reduce range(2) as $_ (0; . + input)")
	vs, err := drain(reduceCode.RunWithOptions(
		nil, nil, gojq.WithRunInputIter(&listInputIter{values: []interface{}{1, 10}}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{11}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("reduce expected: %#v, got: %#v", expected, vs)
	}

	foreachCode := compileWithInput(t, "[foreach range(2) as $_ (0; . + input)]")
	vs, err = drain(foreachCode.RunWithOptions(
		nil, nil, gojq.WithRunInputIter(&listInputIter{values: []interface{}{1, 10}}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{[]interface{}{1, 11}}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("foreach expected: %#v, got: %#v", expected, vs)
	}
}

// The inputs/0 builtin must consume the iterator bound to the run.
func TestRunInputIter_InputsBuiltin(t *testing.T) {
	code := compileWithInput(t, "[inputs]")
	vs, err := drain(code.RunWithOptions(
		nil, nil, gojq.WithRunInputIter(&listInputIter{values: []interface{}{1, 2, 3}}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{[]interface{}{1, 2, 3}}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, vs)
	}
}

// input(s)/0 inside a user-defined function must use the run's iterator.
func TestRunInputIter_FunctionDefinitions(t *testing.T) {
	query, err := gojq.Parse("def f: input; [f, f]")
	if err != nil {
		log.Fatalln(err)
	}
	code, err := gojq.Compile(query, gojq.WithInputIter(nil))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := drain(code.RunWithOptions(
		nil, nil, gojq.WithRunInputIter(&listInputIter{values: []interface{}{"x", "y"}}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{[]interface{}{"x", "y"}}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, vs)
	}
}

// inputModuleLoader serves a module whose function reads from input(s)/0.
type inputModuleLoader struct{}

func (*inputModuleLoader) LoadModule(name string) (*gojq.Module, error) {
	if name != "inputmod" {
		return nil, fmt.Errorf("module not found: %q", name)
	}
	return gojq.ParseModule(`def readtwo: [input, input];`)
}

// input(s)/0 reached through a module function must resolve against the
// iterator bound to the run (the allow-input flag and the per-run binding
// are threaded into the module's sub-compiler).
func TestRunInputIter_ModuleFunction(t *testing.T) {
	query, err := gojq.Parse(`import "inputmod" as m; m::readtwo`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := gojq.Compile(
		query,
		gojq.WithModuleLoader(&inputModuleLoader{}),
		gojq.WithInputIter(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	iterA := &listInputIter{values: []interface{}{"a1", "a2"}}
	iterB := &listInputIter{values: []interface{}{"b1", "b2"}}

	vsA, err := drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterA)))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{[]interface{}{"a1", "a2"}}; !reflect.DeepEqual(vsA, expected) {
		t.Errorf("A expected: %#v, got: %#v", expected, vsA)
	}
	vsB, err := drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(iterB)))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{[]interface{}{"b1", "b2"}}; !reflect.DeepEqual(vsB, expected) {
		t.Errorf("B expected: %#v, got: %#v", expected, vsB)
	}
}

// The existing WithInputIter compiler option must keep working unchanged:
// the bound iterator is the default for every run.
func TestRunInputIter_DefaultFromCompilerOption(t *testing.T) {
	query, err := gojq.Parse("input")
	if err != nil {
		t.Fatal(err)
	}
	code, err := gojq.Compile(query, gojq.WithInputIter(
		&listInputIter{values: []interface{}{1, 2}},
	))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := drain(code.Run(nil))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{1}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("first run expected: %#v, got: %#v", expected, vs)
	}
	vs, err = drain(code.Run(nil))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{2}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("second run expected: %#v, got: %#v", expected, vs)
	}
}

// The run option overrides the default iterator given at compile time.
func TestRunInputIter_OverridesCompilerOption(t *testing.T) {
	query, err := gojq.Parse("input")
	if err != nil {
		t.Fatal(err)
	}
	code, err := gojq.Compile(query, gojq.WithInputIter(
		&listInputIter{values: []interface{}{"default"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := drain(code.RunWithOptions(
		nil, nil, gojq.WithRunInputIter(&listInputIter{values: []interface{}{"per-run"}}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if expected := []interface{}{"per-run"}; !reflect.DeepEqual(vs, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, vs)
	}
}

// Many goroutines reuse the same compiled Code, each binding its own
// iterator; every run must observe only its own input cursor.
func TestRunInputIter_ConcurrentRuns(t *testing.T) {
	code := compileWithInput(t, "input, input, input")
	const n = 32
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			values := []interface{}{g * 10, g*10 + 1, g*10 + 2}
			iter := code.RunWithOptions(
				nil, nil, gojq.WithRunInputIter(&listInputIter{values: values}),
			)
			got, err := drain(iter)
			if err != nil {
				t.Errorf("goroutine %d: %v", g, err)
				return
			}
			if !reflect.DeepEqual(got, values) {
				t.Errorf("goroutine %d expected: %#v, got: %#v", g, values, got)
			}
		}(g)
	}
	wg.Wait()
}

func ExampleWithRunInputIter() {
	query, err := gojq.Parse("input")
	if err != nil {
		log.Fatalln(err)
	}
	// WithInputIter(nil) allows input(s)/0; each run binds its own iterator.
	code, err := gojq.Compile(query, gojq.WithInputIter(nil))
	if err != nil {
		log.Fatalln(err)
	}
	iter := code.RunWithOptions(
		nil, nil, gojq.WithRunInputIter(&testInputIter{}),
	)
	v, ok := iter.Next()
	if !ok {
		log.Fatalln("no value")
	}
	fmt.Println(v)
	// Output:
	// 1
}

// Compiling without WithInputIter must still reject input(s)/0.
func TestRunInputIter_NotAllowedAtCompileTime(t *testing.T) {
	query, err := gojq.Parse("input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gojq.Compile(query); err == nil ||
		err.Error() != "input(s)/0 is not allowed" {
		t.Fatalf("expected inputNotAllowedError, got: %v", err)
	}
}

// When input(s)/0 is allowed but no iterator is bound to the run, calling
// input must report the error at runtime instead of panicking.
func TestRunInputIter_NilAtRuntime(t *testing.T) {
	code := compileWithInput(t, "input")
	_, err := drain(code.Run(nil))
	if err == nil || err.Error() != "input(s)/0 is not allowed" {
		t.Fatalf("expected runtime inputNotAllowedError, got: %v", err)
	}
}

// An explicit nil run option binds no iterator, overriding the default
// compiler iterator and surfacing the error at runtime.
func TestRunInputIter_NilRunOptionOverridesDefault(t *testing.T) {
	query, err := gojq.Parse("input")
	if err != nil {
		t.Fatal(err)
	}
	code, err := gojq.Compile(query, gojq.WithInputIter(
		&listInputIter{values: []interface{}{42}},
	))
	if err != nil {
		t.Fatal(err)
	}
	_, err = drain(code.RunWithOptions(nil, nil, gojq.WithRunInputIter(nil)))
	if err == nil || err.Error() != "input(s)/0 is not allowed" {
		t.Fatalf("expected runtime inputNotAllowedError, got: %v", err)
	}
}
