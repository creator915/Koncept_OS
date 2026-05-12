package graph

const (
	StatusDeclared     = "declared"
	StatusImplementing = "implementing"
	StatusConfirmed    = "confirmed"
)

// Graph is the hypergraph: attributes are nodes, objects are hyperedges
// connecting consumed and produced attributes.
type Graph struct {
	Attributes map[string]*Attribute `json:"attributes"`
	Objects    map[string]*Object    `json:"objects"`
}

type Attribute struct {
	Def           string         `json:"def"`
	Refines       []string       `json:"refines"`
	Intent        string         `json:"intent"`
	ValueSpace    map[string]any `json:"valueSpace"`
	ConfirmedOps  []string       `json:"confirmedOps"`
	Laws          []string       `json:"laws"`
	Status        string         `json:"status"`
	StatusSession *string        `json:"statusSession"`
}

type Object struct {
	Def      string   `json:"def"`
	Impl     *string  `json:"impl"`
	// ImplFragment (v9.0.3) is the per-object writing target for
	// single-file deliverables. When N graph objects all share the
	// same Impl (e.g. all set impl="index.html" because the deliverable
	// is one HTML file), each object's actual code lives in its own
	// fragment file (e.g. implFragment="K/frags/WorldGen.js"). Child
	// sessions write to ImplFragment; the R2 build step concatenates
	// every fragment into Impl in topological order.
	//
	// Empty for multi-file projects (TypeScript / Go) where Impl is
	// already per-object (e.g. src/WorldGen.impl.ts). v9.0.2 Terraria
	// batch died because no ImplFragment mechanism existed: every
	// object shared impl=index.html and the parent agent had to write
	// the entire 4000-8000-line file in one LLM stream, which
	// overflowed the response deadline.
	ImplFragment *string `json:"implFragment,omitempty"`
	// ImplSymbol is the name the test harness looks up in IMPL[...] to
	// find the actual function to invoke. Empty defaults to the object
	// ID — the historical assumption pre-v8.8. v8.7 pong-05 ran a
	// PascalCase object (UpdatePhysics) against a camelCase impl
	// (function updatePhysics) and the harness's implicit
	// IMPL[objectID] lookup failed across all 13 test cases. v8.8
	// makes the mapping explicit: an agent writing camelCase functions
	// declares it via graph_merge_object patch='{"implSymbol":"updatePhysics"}'.
	ImplSymbol string `json:"implSymbol,omitempty"`
	Consumes   []string `json:"consumes"`
	Produces   []string `json:"produces"`
	// Mutates names attributes the object reads AND writes in place
	// (mutation-style semantics, e.g. JS object property assignment).
	// Distinct from Produces (pure functional output) and from Consumes
	// (read-only). preflight ignores Mutates edges in cycle detection,
	// since mutation creates a self-loop in the produce/consume DAG that
	// would otherwise spuriously trip cycles. See
	// KonceptOS_kcpos_analysis.md §5.3.
	Mutates []string `json:"mutates"`
	// PortObservation maps each port (consumes/produces/mutates) to an
	// extraction expression telling kcpos HOW to read that port's value
	// at runtime (D2 redesign). Without this, the test harness has to
	// guess — historically that meant "look up globalThis[port_name]",
	// which only works for one specific impl style.
	//
	// Recognised values:
	//   - "global"          — read from globalThis[<port>] (legacy default)
	//   - "return.<path>"   — extract from the call's return value
	//   - "args.<n>.<path>" — read the n-th argument after the call (mutation)
	//   - "side_effect"     — port has only externally-observable effects
	//                         (canvas drawing, network); cannot be runtime-
	//                         checked, requires waiver
	//
	// Confirmed objects MUST have a PortObservation entry for every
	// port in produces and mutates. Missing coverage trips the
	// `port-observation-required` gate rule.
	PortObservation map[string]string `json:"portObservation,omitempty"`
	Intent          string            `json:"intent"`
	Temporal        *Temporal         `json:"temporal"`
	Preconditions   string            `json:"preconditions"`
	Postconditions  string            `json:"postconditions"`
	Status          string            `json:"status"`
	StatusSession   *string           `json:"statusSession"`
}

type Temporal struct {
	FrameVar string     `json:"frameVar"`
	Consumes []FrameRef `json:"consumes"`
	Produces []FrameRef `json:"produces"`
}

type FrameRef struct {
	Attribute string `json:"attribute"`
	Frame     string `json:"frame"`
}

// NewGraph returns an empty graph with initialized maps.
func NewGraph() *Graph {
	return &Graph{
		Attributes: map[string]*Attribute{},
		Objects:    map[string]*Object{},
	}
}

// NewAttribute returns an attribute in declared state with empty arrays
// (so JSON renders as [] rather than null).
func NewAttribute(def, intent string) *Attribute {
	return &Attribute{
		Def:          def,
		Refines:      []string{},
		Intent:       intent,
		ValueSpace:   nil,
		ConfirmedOps: []string{},
		Laws:         []string{},
		Status:       StatusDeclared,
	}
}

// NewObject returns an object in declared state with empty arrays.
func NewObject(def, intent string) *Object {
	return &Object{
		Def:      def,
		Consumes: []string{},
		Produces: []string{},
		Mutates:  []string{},
		Intent:   intent,
		Status:   StatusDeclared,
	}
}
