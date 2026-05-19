package sessiontools

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.3.4: session_show renders status / children / expandsObject /
// sub-hypergraph digest (objects, confirmed, validate). Three snapshots:
// a plain root, a one-layer expansion, and a rolled-back session.

func show(t *testing.T, id string) (string, error) {
	t.Helper()
	return sessionShowTool().Run(context.Background(), map[string]interface{}{"id": id})
}

func TestSessionShow_RootHasNoExpansionSection(t *testing.T) {
	startExpFixture(t) // creates s_root (plain, no expansion)
	out, err := show(t, "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "session s_root") || !strings.Contains(out, "status:") {
		t.Errorf("root show must render id+status: %s", out)
	}
	if strings.Contains(out, "expansion:") {
		t.Errorf("plain root must NOT have an expansion section: %s", out)
	}
}

func TestSessionShow_OneLayerExpansionDigest(t *testing.T) {
	finishFixture(t) // s_exp expands Target, empty sub
	putSub(t, func(g *graph.Graph) {
		g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "x")
		o := graph.NewObject("defs/Make.ts", "x")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"out_a"}
		g.Objects["Make"] = o
	})
	out, err := show(t, "s_exp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "expansion: Target") {
		t.Errorf("expansion session must show expandsObject: %s", out)
	}
	if !strings.Contains(out, "sub-objects: 1 (confirmed 1)") {
		t.Errorf("must digest sub-object/confirmed counts: %s", out)
	}
	if !strings.Contains(out, "sub-validate: PASS") {
		t.Errorf("must report sub-validate status: %s", out)
	}
}

func TestSessionShow_RolledBackSessionGone(t *testing.T) {
	finishFixture(t)
	putSub(t, func(g *graph.Graph) {
		g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "x")
		o := graph.NewObject("defs/Make.ts", "x")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"out_a"}
		g.Objects["Make"] = o
	})
	if _, err := finish(t); err != nil {
		t.Fatalf("precondition finish: %v", err)
	}
	rollback(t, "s_exp")
	// After rollback the session JSON is gone — show must error, not
	// surface a stale expansion digest.
	if _, err := show(t, "s_exp"); err == nil {
		t.Error("session_show on a rolled-back session must error (session deleted)")
	}
	if persistence.ExistsSession(persistence.SessionDefaultDir, "s_exp") {
		t.Error("rolled-back expansion session must not still exist")
	}
}
