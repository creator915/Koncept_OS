package commands

import (
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

func RunGraph(args []string) int {
	if len(args) == 0 {
		printGraphUsage()
		return 1
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "show":
		return runGraphShow(rest)
	case "create":
		return runGraphCreate(rest)
	case "link":
		return runGraphLink(rest)
	case "validate":
		return runGraphValidate(rest)
	case "preflight":
		return runGraphPreflight(rest)
	case "autowire":
		return runGraphAutowire(rest)
	case "render":
		return runGraphRender(rest)
	case "-h", "--help", "help":
		printGraphUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown graph subcommand: %s\n\n", sub)
		printGraphUsage()
		return 1
	}
}

func printGraphUsage() {
	fmt.Fprintln(os.Stderr, `kcpos graph — direct CLI on K/graph.json

Usage:
  kcpos graph show <id>                                show node + neighbors
  kcpos graph create attribute --id ID --intent ...    add attribute
  kcpos graph create object --id ID --intent ...       add object
  kcpos graph link refine --child A --parent B         A <: B
  kcpos graph link consume --object O --attribute A    O reads A
  kcpos graph link produce --object O --attribute A    O writes A
  kcpos graph validate                                 run all 8 checker rules
  kcpos graph preflight ID1 ID2 ...                    parallel-safety analysis
  kcpos graph autowire --producer P --consumer C       compatible-flow query
  kcpos graph render [--format mermaid|dot]            export diagram source`)
}

func runGraphShow(args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: kcpos graph show <id>")
		return 1
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	out, err := g.Show(args[0])
	if err != nil {
		return printErr(err)
	}
	fmt.Print(out)
	return 0
}

func runGraphCreate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: kcpos graph create attribute|object --id ID --intent \"...\" [--def PATH]")
		return 1
	}
	kind := args[0]
	if kind != "attribute" && kind != "object" {
		fmt.Fprintln(os.Stderr, "first arg must be 'attribute' or 'object'")
		return 1
	}
	fs := flag.NewFlagSet("kcpos graph create "+kind, flag.ExitOnError)
	id := fs.String("id", "", "node id")
	intent := fs.String("intent", "", "design intent")
	def := fs.String("def", "", "definition file path (default defs/<id>.ts per TS-first spec)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *id == "" || *intent == "" {
		fmt.Fprintln(os.Stderr, "--id and --intent required")
		return 1
	}
	if *def == "" {
		*def = "defs/" + *id + ".ts"
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	if kind == "attribute" {
		if err := g.AddAttribute(*id, graph.NewAttribute(*def, *intent)); err != nil {
			return printErr(err)
		}
	} else {
		if err := g.AddObject(*id, graph.NewObject(*def, *intent)); err != nil {
			return printErr(err)
		}
	}
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		return printErr(err)
	}
	fmt.Printf("created %s %s\n", kind, *id)
	return 0
}

func runGraphLink(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: kcpos graph link refine|consume|produce ...")
		return 1
	}
	verb := args[0]
	fs := flag.NewFlagSet("kcpos graph link "+verb, flag.ExitOnError)
	child := fs.String("child", "", "child attribute (refine)")
	parent := fs.String("parent", "", "parent attribute (refine)")
	object := fs.String("object", "", "object id (consume/produce)")
	attribute := fs.String("attribute", "", "attribute id (consume/produce)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	switch verb {
	case "refine":
		if *child == "" || *parent == "" {
			fmt.Fprintln(os.Stderr, "--child and --parent required")
			return 1
		}
		if err := g.LinkRefine(*child, *parent); err != nil {
			return printErr(err)
		}
		fmt.Printf("linked %s <: %s\n", *child, *parent)
	case "consume":
		if *object == "" || *attribute == "" {
			fmt.Fprintln(os.Stderr, "--object and --attribute required")
			return 1
		}
		if err := g.LinkConsume(*object, *attribute); err != nil {
			return printErr(err)
		}
		fmt.Printf("%s consumes %s\n", *object, *attribute)
	case "produce":
		if *object == "" || *attribute == "" {
			fmt.Fprintln(os.Stderr, "--object and --attribute required")
			return 1
		}
		if err := g.LinkProduce(*object, *attribute); err != nil {
			return printErr(err)
		}
		fmt.Printf("%s produces %s\n", *object, *attribute)
	default:
		fmt.Fprintf(os.Stderr, "unknown link verb: %s (refine|consume|produce)\n", verb)
		return 1
	}
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		return printErr(err)
	}
	return 0
}

func runGraphValidate(args []string) int {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	cwd, _ := os.Getwd()
	r := g.Validate(cwd)
	fmt.Print(r.String())
	if r.HasErrors() {
		return 1
	}
	return 0
}

func runGraphPreflight(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: kcpos graph preflight <ObjectId>...")
		return 1
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	r := g.Preflight(args)
	fmt.Print(r.String())
	if r.Status == "UNSAFE" {
		return 1
	}
	return 0
}

func runGraphAutowire(args []string) int {
	fs := flag.NewFlagSet("kcpos graph autowire", flag.ExitOnError)
	producer := fs.String("producer", "", "producer object id")
	consumer := fs.String("consumer", "", "consumer object id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *producer == "" || *consumer == "" {
		fmt.Fprintln(os.Stderr, "--producer and --consumer required")
		return 1
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	matches, err := g.Autowire(*producer, *consumer)
	if err != nil {
		return printErr(err)
	}
	if len(matches) == 0 {
		fmt.Printf("no compatible flow from %s to %s\n", *producer, *consumer)
		return 0
	}
	fmt.Printf("%s → %s (%d match):\n", *producer, *consumer, len(matches))
	for _, m := range matches {
		if m.Kind == "direct" {
			fmt.Printf("  %s → %s (direct)\n", m.ProducerAttr, m.ConsumerAttr)
		} else {
			fmt.Printf("  %s <: %s (refines)\n", m.ProducerAttr, m.ConsumerAttr)
		}
	}
	return 0
}

func runGraphRender(args []string) int {
	fs := flag.NewFlagSet("kcpos graph render", flag.ExitOnError)
	format := fs.String("format", "mermaid", "mermaid | dot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return printErr(err)
	}
	switch *format {
	case "mermaid":
		fmt.Print(g.RenderMermaid())
	case "dot":
		fmt.Print(g.RenderDot())
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q (mermaid|dot)\n", *format)
		return 1
	}
	return 0
}

func printErr(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}
