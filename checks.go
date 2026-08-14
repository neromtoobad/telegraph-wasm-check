package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type Severity int

const (
	Hard Severity = iota // Stage 1: published, a failure is a hard reject
	Soft                 // Stage 2: advisory, real thresholds are unpublished
)

type Result struct {
	Name     string
	Passed   bool
	Detail   string
	Severity Severity
}

type Report struct {
	Stage1 []Result
	Stage2 []Result
	Custom []Result

	Profile []ProfileRow
	Spread  float64
	StdDev  float64
	Mean    float64
}

type ProfileRow struct {
	Label string
	Score float64
}

func (r *Report) hardFailures() int {
	n := 0
	for _, x := range append(append([]Result{}, r.Stage1...), r.Custom...) {
		if !x.Passed && x.Severity == Hard {
			n++
		}
	}
	return n
}

func (r *Report) softFailures() int {
	n := 0
	for _, x := range append(append([]Result{}, r.Stage2...), r.Custom...) {
		if !x.Passed && x.Severity == Soft {
			n++
		}
	}
	return n
}

const (
	sampleQ  = "What is the capital of France?"
	sampleGT = "Paris is the capital of France."
)

// RunStage1 executes the five published structural checks.
//
// These are quoted from the Scoring Reference: the module must export rank_answer, alloc
// and dealloc; an empty answer and a whitespace-only answer must each return exactly 0;
// a self-match must beat an unrelated cross-match; and adversarial input must not trap.
// Any failure here is a hard reject at registration time.
func RunStage1(m *Module) []Result {
	var out []Result
	add := func(name string, ok bool, detail string) {
		out = append(out, Result{name, ok, detail, Hard})
	}

	add("exports linear memory", m.HasMemory(), "")
	add("exports alloc", m.Alloc != nil, "")
	add("exports dealloc", m.Dealloc != nil, "")
	add("exports rank_answer", m.Rank != nil, "")
	if !m.HasMemory() || m.Rank == nil || m.Alloc == nil {
		return out
	}

	if v, err := m.Score(sampleQ, sampleGT, ""); err != nil {
		add("empty answer returns exactly 0", false, err.Error())
	} else {
		add("empty answer returns exactly 0", v == 0, fmt.Sprintf("got %v", v))
	}

	if v, err := m.Score(sampleQ, sampleGT, "   "); err != nil {
		add("whitespace-only returns exactly 0", false, err.Error())
	} else {
		add("whitespace-only returns exactly 0", v == 0, fmt.Sprintf("got %v", v))
	}

	self, e1 := m.Score(sampleQ, sampleGT, sampleGT)
	cross, e2 := m.Score(sampleQ, sampleGT,
		"Diesel engines convert chemical energy through compression ignition.")
	if e1 != nil || e2 != nil {
		add("self-match beats cross-match", false, "module trapped")
	} else {
		add("self-match beats cross-match", self > cross,
			fmt.Sprintf("self=%.4f cross=%.4f", self, cross))
	}

	// "No panic or trap on adversarial input." The host caps a single text input at
	// 128 KiB, so ~104 KB stays inside the limit while still being far larger than
	// anything a real answer contains.
	big := strings.Repeat("lorem ipsum dolor sit amet ", 4000)
	_, err := m.Score(sampleQ, sampleGT, big)
	add("large repeated input does not trap", err == nil, sizeNote(len(big), err))

	uni := "Café ☕ 東京都は日本の首都です — naïve résumé 🎉🚀 Ω≈ç√∫˜µ"
	_, err = m.Score(uni, uni, uni)
	add("unicode / emoji / CJK does not trap", err == nil, errNote(err))

	for _, tc := range []struct{ name, q, gt, ma string }{
		{"empty question", "", sampleGT, "Paris"},
		{"empty ground truth", sampleQ, "", "Paris"},
		{"all three empty", "", "", ""},
		{"embedded null bytes", sampleQ, sampleGT, "Paris\x00\x00Paris"},
		{"answer longer than truth by 1000x", sampleQ, "Paris", strings.Repeat("Paris ", 5000)},
	} {
		_, err := m.Score(tc.q, tc.gt, tc.ma)
		add("degenerate input: "+tc.name, err == nil, errNote(err))
	}

	return out
}

// RunStage2 probes the properties the Scoring Reference describes in prose: that a module
// discriminates rather than returning near-constant values, recognises a correct answer,
// and prefers a close paraphrase to an off-topic one.
//
// The real Stage 2 thresholds are deliberately unpublished, and the docs are explicit
// about why: "a scoring module that is tuned to clear a published bar is not the same
// thing as a scoring module that ranks well." Everything here is advisory. Treat a WARN
// as a prompt to look, not as a failure to fix.
func RunStage2(m *Module, r *Report) []Result {
	var out []Result
	add := func(name string, ok bool, detail string) {
		out = append(out, Result{name, ok, detail, Soft})
	}

	para, _ := m.Score(sampleQ, sampleGT, "The capital city of France is Paris.")
	off, _ := m.Score(sampleQ, sampleGT, "France is a country in western Europe.")
	add("prefers paraphrase over off-topic", para > off,
		fmt.Sprintf("paraphrase=%.4f off-topic=%.4f", para, off))

	add("recognises a correct answer", para >= 0.5, fmt.Sprintf("paraphrase=%.4f", para))

	wrong, _ := m.Score(sampleQ, sampleGT, "Berlin is the capital of France.")
	add("wrong entity scores below correct", wrong < para,
		fmt.Sprintf("wrong=%.4f correct=%.4f", wrong, para))

	// Deterministic-intent shape: the truth is a value, and prose around it is normal.
	const pq, pgt = "What is the current price of Bitcoin in USD?", "42137.51"
	right, _ := m.Score(pq, pgt, "Bitcoin is currently trading at $42,137.51 USD.")
	wrongNum, _ := m.Score(pq, pgt, "Bitcoin is currently trading at $61,020.00 USD.")
	add("value-shaped truth: right beats wrong", right > wrongNum,
		fmt.Sprintf("right=%.4f wrong=%.4f", right, wrongNum))

	// Distribution. A module returning everything near one value ranks nothing, which is
	// the failure Stage 2 is built to catch.
	corpus := []struct{ label, answer string }{
		{"exact", sampleGT},
		{"paraphrase", "The capital city of France is Paris."},
		{"correct, terse", "Paris"},
		{"correct, verbose", "If you are asking about France, the capital of that country is Paris, its largest city."},
		{"partial", "It is a city in northern France."},
		{"related but wrong", "Lyon is the capital of France."},
		{"off-topic", "France is a country in western Europe."},
		{"nonsense", "Purple monkey dishwasher gradient."},
		{"single char", "x"},
	}
	var vals []float64
	for _, c := range corpus {
		v, err := m.Score(sampleQ, sampleGT, c.answer)
		if err != nil {
			continue
		}
		vals = append(vals, float64(v))
		r.Profile = append(r.Profile, ProfileRow{c.label, float64(v)})
	}
	if len(vals) > 1 {
		for _, v := range vals {
			r.Mean += v
		}
		r.Mean /= float64(len(vals))
		for _, v := range vals {
			r.StdDev += (v - r.Mean) * (v - r.Mean)
		}
		r.StdDev = math.Sqrt(r.StdDev / float64(len(vals)))
		s := append([]float64(nil), vals...)
		sort.Float64s(s)
		r.Spread = s[len(s)-1] - s[0]
	}
	add("discriminates: spread > 0.50", r.Spread > 0.50, fmt.Sprintf("spread=%.4f", r.Spread))
	add("discriminates: stddev > 0.15", r.StdDev > 0.15,
		fmt.Sprintf("stddev=%.4f mean=%.4f", r.StdDev, r.Mean))

	exact, _ := m.Score(sampleQ, sampleGT, sampleGT)
	terse, _ := m.Score(sampleQ, sampleGT, "Paris")
	nons, _ := m.Score(sampleQ, sampleGT, "Purple monkey dishwasher gradient.")
	add("ranks exact >= correct > nonsense", exact >= terse && terse > nons,
		fmt.Sprintf("exact=%.4f terse=%.4f nonsense=%.4f", exact, terse, nons))

	// Range discipline. The host clamps to [0,1] and collapses NaN/Inf to 0, so a module
	// returning out-of-range values is not rejected — it is silently corrected, and its
	// ranking quietly stops meaning what its author intended.
	inRange := true
	for _, v := range vals {
		if v < 0 || v > 1 || math.IsNaN(v) || math.IsInf(v, 0) {
			inRange = false
		}
	}
	add("all scores within [0,1], no NaN/Inf", inRange, "host clamps silently otherwise")

	return out
}

// RunDeterminism checks that identical inputs produce identical outputs, both on one
// instance and across a separately instantiated one.
//
// This matters more than it looks. Validators independently run the same script and must
// reach consensus on the score, and the node drives several module instances in a pool so
// concurrent scoring does not serialise. A module carrying state between calls, or
// seeding a hash from anything ambient, will rank differently on different validators and
// break consensus rather than merely scoring oddly.
func RunDeterminism(m *Module, path string) []Result {
	var out []Result
	const answer = "The capital city of France is Paris."

	first, err := m.Score(sampleQ, sampleGT, answer)
	if err != nil {
		return append(out, Result{"determinism: repeat calls", false, err.Error(), Hard})
	}
	stable := true
	for i := 0; i < 100; i++ {
		v, err := m.Score(sampleQ, sampleGT, answer)
		if err != nil || v != first {
			stable = false
			break
		}
	}
	out = append(out, Result{"deterministic across 100 repeat calls", stable,
		fmt.Sprintf("value=%.6f", first), Hard})

	// A second, independently instantiated module: catches state that persists in a
	// module's own memory across calls but resets on a fresh instance.
	m2, err := Load(path)
	if err != nil {
		out = append(out, Result{"deterministic across a fresh instance", false, err.Error(), Hard})
		return out
	}
	defer m2.Close()
	second, err := m2.Score(sampleQ, sampleGT, answer)
	out = append(out, Result{"deterministic across a fresh instance", err == nil && second == first,
		fmt.Sprintf("instance A=%.6f  instance B=%.6f", first, second), Hard})

	return out
}

// RunMemoryStability calls the module many times with large inputs and NEVER calls
// dealloc, then watches linear-memory growth.
//
// Why this is worth 300 calls of runtime: a module built on a general-purpose allocator
// (Rust's global allocator, malloc) only reclaims memory if the host actually calls
// dealloc after every scoring call — and whether the node does that is not observable
// from outside. Measured on a real module, the leaky version grew ~200 KB per call:
// it passes every published Stage 1 gate, then traps on linear-memory exhaustion after
// ~20k calls — a few days of spot-check traffic on a long-lived instance. A wrap-around
// bump allocator over a fixed region (as in the docs' own example) is bounded no matter
// what the host does.
//
// Advisory rather than hard, because a std-alloc module is correct under a host that
// deallocs. But the detail message says what is at stake.
func RunMemoryStability(m *Module) []Result {
	var out []Result

	big := strings.Repeat("stress payload for memory growth measurement ", 2300) // ~103 KB
	const q = "memory stability probe"

	warm := 20
	measured := 280

	for i := 0; i < warm; i++ {
		if _, err := m.Score(q, big, big); err != nil {
			return append(out, Result{"memory: survives sustained calls", false,
				fmt.Sprintf("trapped during warmup call %d: %v", i, err), Hard})
		}
	}
	before := m.MemorySize()
	for i := 0; i < measured; i++ {
		if _, err := m.Score(q, big, big); err != nil {
			return append(out, Result{"memory: survives sustained calls", false,
				fmt.Sprintf("trapped at call %d: %v", warm+i, err), Hard})
		}
	}
	after := m.MemorySize()

	growth := int64(after) - int64(before)
	perCall := growth / int64(measured)

	// A bounded allocator settles to zero growth after warmup. Anything above ~4 KB/call
	// sustained is the signature of per-call leakage.
	ok := perCall < 4*1024
	detail := fmt.Sprintf("%d calls, no dealloc: %+.1f MB total, ~%d KB/call", warm+measured,
		float64(growth)/(1<<20), perCall/1024)
	if !ok {
		detail += fmt.Sprintf(" — at this rate the module traps after ~%dk calls if the host never deallocs",
			(4<<30)/max64(perCall, 1)/1000)
	}
	out = append(out, Result{"memory: bounded under sustained calls without dealloc", ok, detail, Soft})
	return out
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// RunOptional reports which optional exports are present. Nothing here affects whether a
// module passes; embed and rank_answer_cached are checked independently by the node, so
// implementing exactly one of the pair is the one arrangement worth flagging.
func RunOptional(m *Module) []string {
	var notes []string
	if m.Breakdown != nil {
		notes = append(notes, "breakdown_answer present (debug introspection)")
	}
	switch {
	case m.Embed != nil && m.Cached != nil:
		notes = append(notes, "embed + rank_answer_cached present (Stage 2 replay speedup)")
	case m.Embed != nil:
		notes = append(notes, "embed present but rank_answer_cached missing — implement both or neither")
	case m.Cached != nil:
		notes = append(notes, "rank_answer_cached present but embed missing — implement both or neither")
	}
	if len(notes) == 0 {
		notes = append(notes, "none (all optional exports are safe to omit)")
	}
	return notes
}

func errNote(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sizeNote(n int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("~%d KB", n/1024)
}
