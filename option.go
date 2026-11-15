package gojq

// CompilerOption ...
type CompilerOption func(*compiler)

// RunOption is an option for Code.Run and Code.RunWithContext. Unlike the
// compiler options, a run option binds its configuration to a single run.
// Reusing a compiled *Code across several runs therefore keeps each run
// isolated; for instance the input iterator used by input(s)/0 advances only
// the cursor of the run it is bound to.
type RunOption func(*runOptions)

type runOptions struct {
	inputIter Iter
}

// WithModuleLoader is a compiler option for module loader.
func WithModuleLoader(moduleLoader ModuleLoader) CompilerOption {
	return func(c *compiler) {
		c.moduleLoader = moduleLoader
	}
}

// WithEnvironLoader is a compiler option for environment variables loader.
// The OS environment variables are not accessible by default due to security
// reason. You can pass os.Environ if you allow to access it.
func WithEnvironLoader(environLoader func() []string) CompilerOption {
	return func(c *compiler) {
		c.environLoader = environLoader
	}
}

// WithVariables is a compiler option for variable names. The variables can be
// used in the query. You have to give the values to query.Run or code.Run in
// the same order.
func WithVariables(variables []string) CompilerOption {
	return func(c *compiler) {
		c.variables = variables
	}
}

// WithInputIter is a compiler option for input iterator used by input(s)/0.
// Note that input and inputs functions are not allowed by default. We have
// to distinguish the query input and the values for input(s) functions. For
// example, consider using inputs with --null-input. If you want to allow
// input(s) functions, create an Iter and use WithInputIter option.
//
// The iterator is used as the default input iterator for the runs of the
// compiled Code. It may be nil when the iterator is bound to each run using
// the WithRunInputIter run option; compiling a query that uses input(s)/0
// is still allowed as long as this option is given.
func WithInputIter(inputIter Iter) CompilerOption {
	return func(c *compiler) {
		c.inputIter = inputIter
		c.allowInput = true
	}
}

// WithRunInputIter is a run option for the input iterator used by
// input(s)/0. It binds the iterator to a single run of a compiled Code; the
// iterator is not shared with other runs reusing the same Code. This makes
// it safe to interleave runs over separate input batches, where exhausting,
// failing or cancelling one run must not affect the input cursor of the
// others.
//
// The query still has to be compiled with the WithInputIter compiler option
// to allow input(s)/0; pass nil there when the iterator is supplied for each
// run using this option.
func WithRunInputIter(inputIter Iter) RunOption {
	return func(o *runOptions) {
		o.inputIter = inputIter
	}
}
