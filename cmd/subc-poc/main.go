// Command subc-poc checks and runs Subsea Cable programs with the POC
// Carousel Runtime and a scripted Host.
//
//	subc-poc check FILE
//	subc-poc run [flags] FILE
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
	"github.com/on-the-ground/subsea_cable_runtime/runtime/policyexamples"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
)

type kv map[string]int

func (m kv) String() string { return fmt.Sprint(map[string]int(m)) }
func (m kv) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	n, err := strconv.Atoi(v)
	if !ok || err != nil {
		return fmt.Errorf("expected name=N, got %q", s)
	}
	m[k] = n
	return nil
}

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	switch os.Args[1] {
	case "check":
		check(os.Args[2])
	case "run":
		run(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: subc-poc check FILE | subc-poc run [flags] FILE")
	os.Exit(2)
}

func load(path string, cb *codebase.Codebase) *sema.Unit {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	u, err := sema.Check(src, cb)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return u
}

func check(path string) {
	u := load(path, nil)
	fmt.Printf("valid: %d Goal definitions, Root %s/%d\n", len(u.Goals), u.Root.Ident, len(u.Root.Args))
	for _, d := range u.Unsupported {
		fmt.Printf("not runnable by this POC: %v\n", d)
	}
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	prefetch := fs.Int("prefetch", 0, "additional Touchdowns to keep ahead of evaluation")
	conc := fs.Int("concurrency", 4, "maximum in-flight attempts")
	demand := fs.String("demand", "ready", "baseline Scheduler demand mode: ready | manual")
	examples := fs.Bool("example-policies", false, "register the ILLUSTRATIVE @retry/@timeout interpreters")
	jsonl := fs.Bool("jsonl", false, "print the trace as JSON lines")
	fail := kv{}
	ticks := kv{}
	fs.Var(fail, "fail", "make the next N direct attempts of a leaf fail (name=N, repeatable)")
	fs.Var(ticks, "ticks", "virtual duration of a leaf (name=N, repeatable)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	cb := codebase.New()
	u := load(fs.Arg(0), cb)
	h := host.NewScripted()
	h.Fallback = host.Echo
	for k, v := range fail {
		h.FailNext(k, v)
	}
	for k, v := range ticks {
		h.SetTicks(k, v)
	}
	cfg := runtime.Config{Prefetch: *prefetch, Concurrency: *conc,
		Demand: runtime.DemandMode(*demand), Host: h, Codebase: cb}
	if *examples {
		cfg.Policies = policyexamples.Registry()
	}
	r, err := runtime.Start(cfg, u)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res := r.RunToCompletion()
	if *jsonl {
		r.Trace().WriteJSONL(os.Stdout)
	} else {
		r.Trace().WriteText(os.Stdout)
	}
	fmt.Printf("\nresult: %s", res.Status)
	if res.Status == host.Succeeded {
		fmt.Printf(" %s", res.Output)
	} else if res.Diag != nil {
		fmt.Printf(" %v", res.Diag)
	}
	fmt.Printf(" (virtual time %d, %d deductions)\n", res.Time, len(cb.Ledger()))
	if res.Status != host.Succeeded {
		os.Exit(1)
	}
}
