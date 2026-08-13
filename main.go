// telegraph-wasm-check validates a Telegraph scoring module before you spend gas
// registering it.
//
// It loads your .wasm through wazero — the same runtime the node uses — with no host
// imports registered, then runs the published Stage 1 gates, a set of Stage 2 behavioural
// probes, determinism checks, and any ordering assertions you supply for your own intent.
//
//	telegraph-wasm-check module.wasm
//	telegraph-wasm-check module.wasm --cases examples/financial-data.json
//	telegraph-wasm-check module.wasm --json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

const version = "0.1.0"

func main() {
	var (
		casesPath = flag.String("cases", "", "path to a JSON file of ordering assertions for your intent")
		asJSON    = flag.Bool("json", false, "emit machine-readable JSON instead of a report")
		strict    = flag.Bool("strict", false, "treat Stage 2 advisories as failures too")
		noColor   = flag.Bool("no-color", false, "disable ANSI colour")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = usage
	// Go's flag package stops parsing at the first positional argument, so the natural
	// `telegraph-wasm-check module.wasm --cases f.json` would silently drop the flag.
	// Reorder so flags come first and both orderings work.
	flag.CommandLine.Parse(reorderArgs(os.Args[1:]))

	if *showVer {
		fmt.Println("telegraph-wasm-check", version)
		return
	}
	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	path := flag.Arg(0)

	if *noColor || os.Getenv("NO_COLOR") != "" {
		disableColor()
	}

	info, err := os.Stat(path)
	if err != nil {
		fatal("cannot read %s: %v", path, err)
	}
	// The node rejects anything larger than 32 MB.
	const maxSize = 32 << 20
	oversize := info.Size() > maxSize

	m, err := Load(path)
	if err != nil {
		if *asJSON {
			emitJSON(&Report{Stage1: []Result{{"module instantiates", false, err.Error(), Hard}}}, 1)
			os.Exit(1)
		}
		fmt.Printf("\n%s  %s\n\n", red("FAIL"), "module failed to instantiate")
		fmt.Printf("  %v\n\n", err)
		fmt.Println("  The scoring sandbox provides linear memory and nothing else — no network,")
		fmt.Println("  no filesystem, no clock. A module that imports anything from the environment")
		fmt.Println("  cannot load on a node either. Build for wasm32-unknown-unknown as a")
		fmt.Println("  freestanding binary with no host imports.")
		os.Exit(1)
	}
	defer m.Close()

	report := &Report{}
	report.Stage1 = RunStage1(m)
	report.Stage1 = append(report.Stage1, RunDeterminism(m, path)...)
	if oversize {
		report.Stage1 = append(report.Stage1, Result{
			"under the 32 MB size limit", false,
			fmt.Sprintf("%.1f MB", float64(info.Size())/(1<<20)), Hard})
	}
	report.Stage2 = RunStage2(m, report)

	if *casesPath != "" {
		cf, err := LoadCases(*casesPath)
		if err != nil {
			fatal("%v", err)
		}
		report.Custom = RunCases(m, cf)
	}

	exit := 0
	if report.hardFailures() > 0 {
		exit = 1
	} else if *strict && report.softFailures() > 0 {
		exit = 1
	}

	if *asJSON {
		emitJSON(report, exit)
		os.Exit(exit)
	}

	printReport(report, path, info.Size(), m, *casesPath, *strict)
	os.Exit(exit)
}

func printReport(r *Report, path string, size int64, m *Module, casesPath string, strict bool) {
	fmt.Printf("\nmodule: %s (%.1f KB)\n", path, float64(size)/1024)

	section("Stage 1 — structural validation", "a failure here is a hard reject at registration")
	for _, x := range r.Stage1 {
		line(x)
	}

	section("Stage 2 — behavioural probes", "advisory; the real thresholds are unpublished")
	for _, x := range r.Stage2 {
		line(x)
	}

	if len(r.Profile) > 0 {
		fmt.Println()
		for _, p := range r.Profile {
			fmt.Printf("      %-20s %.4f  %s\n", p.Label, p.Score, bar(p.Score))
		}
		fmt.Printf("\n      spread %.4f · stddev %.4f · mean %.4f\n", r.Spread, r.StdDev, r.Mean)
	}

	if len(r.Custom) > 0 {
		section("Your cases — "+casesPath, "ordering assertions for your intent")
		for _, x := range r.Custom {
			line(x)
		}
	}

	fmt.Printf("\noptional exports:\n")
	for _, n := range RunOptional(m) {
		fmt.Printf("  · %s\n", n)
	}

	hard, soft := r.hardFailures(), r.softFailures()
	fmt.Println()
	switch {
	case hard > 0:
		fmt.Printf("%s  %d hard failure(s). Fix these before registering — Stage 1 rejects outright.\n\n",
			red("NOT READY"), hard)
	case soft > 0 && strict:
		fmt.Printf("%s  Stage 1 clean, but %d advisory failed and --strict is on.\n\n",
			yellow("NOT READY"), soft)
	case soft > 0:
		fmt.Printf("%s  Stage 1 clean. %d advisory worth a look — see Stage 2 above.\n\n",
			yellow("READY, WITH NOTES"), soft)
	default:
		fmt.Printf("%s  Stage 1 clean and every probe passed.\n\n", green("READY"))
	}
}

type jsonOut struct {
	Version  string       `json:"version"`
	ExitCode int          `json:"exit_code"`
	Hard     int          `json:"hard_failures"`
	Soft     int          `json:"soft_failures"`
	Stage1   []Result     `json:"stage1"`
	Stage2   []Result     `json:"stage2"`
	Custom   []Result     `json:"custom,omitempty"`
	Profile  []ProfileRow `json:"profile,omitempty"`
	Spread   float64      `json:"spread"`
	StdDev   float64      `json:"stddev"`
}

func emitJSON(r *Report, exit int) {
	out := jsonOut{
		Version: version, ExitCode: exit,
		Hard: r.hardFailures(), Soft: r.softFailures(),
		Stage1: r.Stage1, Stage2: r.Stage2, Custom: r.Custom,
		Profile: r.Profile, Spread: r.Spread, StdDev: r.StdDev,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// ── output helpers ───────────────────────────────────────────────────────────

var (
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cDim    = "\033[2m"
	cReset  = "\033[0m"
)

func disableColor() { cRed, cGreen, cYellow, cDim, cReset = "", "", "", "", "" }

func red(s string) string    { return cRed + s + cReset }
func green(s string) string  { return cGreen + s + cReset }
func yellow(s string) string { return cYellow + s + cReset }
func dim(s string) string    { return cDim + s + cReset }

func section(title, note string) {
	fmt.Printf("\n%s  %s\n", title, dim(note))
}

func line(x Result) {
	tag := green("PASS")
	if !x.Passed {
		if x.Severity == Hard {
			tag = red("FAIL")
		} else {
			tag = yellow("WARN")
		}
	}
	fmt.Printf("  %s  %-46s %s\n", tag, x.Name, dim(x.Detail))
}

func bar(v float64) string {
	n := int(v * 40)
	if n < 0 {
		n = 0
	}
	if n > 40 {
		n = 40
	}
	return strings.Repeat("█", n)
}

// valueFlags are the flags that consume the following argument, so it is not mistaken
// for the positional module path.
var valueFlags = map[string]bool{"-cases": true, "--cases": true}

func reorderArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			if valueFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, `telegraph-wasm-check %s — validate a Telegraph scoring module before registering it

usage:
  telegraph-wasm-check <module.wasm> [flags]

flags:
  --cases <file>   JSON ordering assertions for your own intent
  --json           machine-readable output for CI
  --strict         treat Stage 2 advisories as failures
  --no-color       disable ANSI colour
  --version        print version

exit codes:
  0  Stage 1 clean
  1  a hard check failed (or --strict and an advisory failed)
  2  usage or input error
`, version)
}
