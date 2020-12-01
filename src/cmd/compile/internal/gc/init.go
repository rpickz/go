// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gc

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
)

// A function named init is a special case.
// It is called by the initialization before main is run.
// To make it unique within a package and also uncallable,
// the name, normally "pkg.init", is altered to "pkg.init.0".
var renameinitgen int

// Function collecting autotmps generated during typechecking,
// to be included in the package-level init function.
var initTodo = ir.NewFunc(base.Pos)

func renameinit() *types.Sym {
	s := lookupN("init.", renameinitgen)
	renameinitgen++
	return s
}

// List of imported packages, in source code order. See #31636.
var sourceOrderImports []*types.Pkg

// fninit makes an initialization record for the package.
// See runtime/proc.go:initTask for its layout.
// The 3 tasks for initialization are:
//   1) Initialize all of the packages the current package depends on.
//   2) Initialize all the variables that have initializers.
//   3) Run any init functions.
func fninit(n []ir.Node) {
	nf := initOrder(n)

	var deps []*obj.LSym // initTask records for packages the current package depends on
	var fns []*obj.LSym  // functions to call for package initialization

	// Find imported packages with init tasks.
	for _, pkg := range sourceOrderImports {
		n := resolve(ir.AsNode(pkg.Lookup(".inittask").Def))
		if n == nil {
			continue
		}
		if n.Op() != ir.ONAME || n.Class() != ir.PEXTERN {
			base.Fatalf("bad inittask: %v", n)
		}
		deps = append(deps, n.Sym().Linksym())
	}

	// Make a function that contains all the initialization statements.
	if len(nf) > 0 {
		base.Pos = nf[0].Pos() // prolog/epilog gets line number of first init stmt
		initializers := lookup("init")
		fn := dclfunc(initializers, ir.NewFuncType(base.Pos, nil, nil, nil))
		for _, dcl := range initTodo.Dcl {
			dcl.Curfn = fn
		}
		fn.Dcl = append(fn.Dcl, initTodo.Dcl...)
		initTodo.Dcl = nil

		fn.PtrBody().Set(nf)
		funcbody()

		typecheckFunc(fn)
		Curfn = fn
		typecheckslice(nf, ctxStmt)
		Curfn = nil
		xtop = append(xtop, fn)
		fns = append(fns, initializers.Linksym())
	}
	if initTodo.Dcl != nil {
		// We only generate temps using initTodo if there
		// are package-scope initialization statements, so
		// something's weird if we get here.
		base.Fatalf("initTodo still has declarations")
	}
	initTodo = nil

	// Record user init functions.
	for i := 0; i < renameinitgen; i++ {
		s := lookupN("init.", i)
		fn := ir.AsNode(s.Def).Name().Defn
		// Skip init functions with empty bodies.
		if fn.Body().Len() == 1 && fn.Body().First().Op() == ir.OEMPTY {
			continue
		}
		fns = append(fns, s.Linksym())
	}

	if len(deps) == 0 && len(fns) == 0 && ir.LocalPkg.Name != "main" && ir.LocalPkg.Name != "runtime" {
		return // nothing to initialize
	}

	// Make an .inittask structure.
	sym := lookup(".inittask")
	nn := NewName(sym)
	nn.SetType(types.Types[types.TUINT8]) // fake type
	nn.SetClass(ir.PEXTERN)
	sym.Def = nn
	exportsym(nn)
	lsym := sym.Linksym()
	ot := 0
	ot = duintptr(lsym, ot, 0) // state: not initialized yet
	ot = duintptr(lsym, ot, uint64(len(deps)))
	ot = duintptr(lsym, ot, uint64(len(fns)))
	for _, d := range deps {
		ot = dsymptr(lsym, ot, d, 0)
	}
	for _, f := range fns {
		ot = dsymptr(lsym, ot, f, 0)
	}
	// An initTask has pointers, but none into the Go heap.
	// It's not quite read only, the state field must be modifiable.
	ggloblsym(lsym, int32(ot), obj.NOPTR)
}
