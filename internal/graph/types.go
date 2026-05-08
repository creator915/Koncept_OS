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
	Consumes []string `json:"consumes"`
	Produces []string `json:"produces"`
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
