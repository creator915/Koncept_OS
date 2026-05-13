package graph

import "fmt"

// AutowireMatch describes one compatible data flow from a producer's output
// attribute to a consumer's input attribute.
type AutowireMatch struct {
	ProducerAttr string
	ConsumerAttr string
	Kind         string // "direct" when ProducerAttr == ConsumerAttr; "refines" when ProducerAttr <: ConsumerAttr in the partial order
}

// Autowire returns the set of (X, Y) pairs where producer produces X and
// consumer consumes Y, and X is compatible with Y via the partial order
// (X == Y or X refines Y transitively). This is a query — no graph
// mutation. Compatibility implies the producer's output can feed the
// consumer's input without additional wiring.
func (g *Graph) Autowire(producerID, consumerID string) ([]AutowireMatch, error) {
	p, ok := g.Objects[producerID]
	if !ok {
		return nil, fmt.Errorf("object %q not found", producerID)
	}
	c, ok := g.Objects[consumerID]
	if !ok {
		return nil, fmt.Errorf("object %q not found", consumerID)
	}
	var matches []AutowireMatch
	for _, x := range p.Produces {
		for _, y := range c.Consumes {
			switch {
			case x == y:
				matches = append(matches, AutowireMatch{x, y, "direct"})
			case g.Refines(x, y):
				matches = append(matches, AutowireMatch{x, y, "refines"})
			}
		}
	}
	return matches, nil
}
