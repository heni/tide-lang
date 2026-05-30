package sema

// SymKind classifies what a Symbol stands for. See docs/internals/sema.md §1.
type SymKind uint8

const (
	SymInvalid SymKind = iota
	SymBuiltinType
	SymBuiltinFunc
	SymBuiltinVariant
	SymBuiltinModule
	SymTypeDecl
	SymClass
	SymInterface
	SymFunc
	SymUserVariant
	SymTypeParam // generic type parameter (T in func f<T>(...))
	SymLocal
	SymField  // class field accessible via implicit receiver
	SymMethod // class method accessible via implicit receiver
)

// Symbol is the resolution result attached to every name-position node.
type Symbol struct {
	Name string
	Kind SymKind
	Type Type // Unknown until Sema-2 fills it
	Decl any  // *ast.TypeDecl / *ast.ClassDecl / *ast.FuncDecl / *ast.LetStmt / *ast.Param / *ast.Variant / nil
}

// Scope is one frame in the lexical-scope chain.
type Scope struct {
	parent *Scope
	names  map[string]*Symbol
}

func newScope(parent *Scope) *Scope {
	return &Scope{parent: parent, names: map[string]*Symbol{}}
}

// declare adds sym; returns the prior occupant for duplicate-decl reporting.
func (s *Scope) declare(sym *Symbol) *Symbol {
	prev := s.names[sym.Name]
	s.names[sym.Name] = sym
	return prev
}

// lookup walks the scope chain.
func (s *Scope) lookup(name string) *Symbol {
	for f := s; f != nil; f = f.parent {
		if sym, ok := f.names[name]; ok {
			return sym
		}
	}
	return nil
}
