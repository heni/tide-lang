package sema

// Type — closed sum of Tide-side type representations.
// See docs/internals/sema.md §5.
type Type interface {
	typeMarker()
	String() string
}

// Builtin — predeclared primitive (int, string, …) or Never / unit.
type Builtin struct{ N string }

func (*Builtin) typeMarker()      {}
func (b *Builtin) String() string { return b.N }

// Named — user type (class / sum / alias) or opaque predeclared (Dynamic, Any).
// Decl is the AST source node, nil for opaques.
type Named struct {
	N    string
	Decl any
}

func (*Named) typeMarker()      {}
func (n *Named) String() string { return n.N }

// Unknown — placeholder until Sema-2/3 fill in the concrete Type.
type Unknown struct{}

func (*Unknown) typeMarker()    {}
func (*Unknown) String() string { return "<unknown>" }
