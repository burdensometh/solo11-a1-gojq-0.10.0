package gojq

import (
	"context"
	"errors"
)

type env struct {
	pc        int
	stack     *stack
	scopes    *stack
	paths     *stack
	values    []interface{}
	codes     []*code
	codeinfos []codeinfo
	forks     []*fork
	backtrack int
	offset    int
	expdepth  int
	ctx       context.Context
	inputIter Iter
}

func newEnv(ctx context.Context) *env {
	return &env{
		stack:  newStack(),
		scopes: newStack(),
		paths:  newStack(),
		ctx:    ctx,
	}
}

// input reads the next value from the input iterator bound to this run.
// It returns an error "break" when the iterator is exhausted, which the
// inputs builtin catches to terminate the stream.
func (env *env) input() interface{} {
	if env.inputIter == nil {
		return &inputNotAllowedError{}
	}
	v, ok := env.inputIter.Next()
	if !ok {
		return errors.New("break")
	}
	return normalizeNumbers(v)
}

type scope struct {
	id        int
	offset    int
	pc        int
	saveindex int
}

type fork struct {
	op         opcode
	pc         int
	stackindex int
	stacklimit int
	scopeindex int
	scopelimit int
	pathindex  int
	pathlimit  int
	expdepth   int
}
