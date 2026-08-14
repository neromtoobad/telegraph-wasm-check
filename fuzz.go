package main

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

// RunFuzz hammers the module with seeded-random triples and checks the invariants that
// hold for every valid scoring module, whatever its formula:
//
//  1. No trap, ever. The node treats a trap as a module failure.
//  2. Every score is a finite number (the host clamps [0,1] and collapses NaN/Inf to 0,
//     so violations aren't fatal — but they mean the module's ranking is silently being
//     rewritten by the host, which its author almost certainly doesn't know).
//  3. An empty answer scores exactly 0 against ANY question/ground-truth pair — Stage 1
//     tests this once with one fixture; production hits it with every shape of input.
//  4. Determinism holds on random inputs, not just on the fixtures.
//
// The seed is fixed so a failure reproduces exactly — report the iteration number and
// anyone can replay it.
func RunFuzz(m *Module) []Result {
	var out []Result
	rng := rand.New(rand.NewSource(42))

	words := []string{
		"price", "bitcoin", "revenue", "the", "of", "increased", "42137.51", "-3.2%",
		"bullish", "scam", "safe", "Paris", "東京", "naïve", "🎉", "null", "undefined",
		"<script>", "{\"k\":", "]]}", "\\u0000", "0x5a2324aA", "1e308", "NaN", "∞",
		"a", "I", "not", "no", "true", "false", "£4,000,000", "", " ", "\t", "\n",
	}
	gen := func(maxTokens int) string {
		n := rng.Intn(maxTokens) + 1
		parts := make([]string, n)
		for i := range parts {
			parts[i] = words[rng.Intn(len(words))]
		}
		return strings.Join(parts, " ")
	}

	const iters = 500
	rangeViolations := 0
	firstRangeDetail := ""
	for i := 0; i < iters; i++ {
		q, gt, ma := gen(30), gen(40), gen(40)

		v, err := m.Score(q, gt, ma)
		if err != nil {
			return append(out, Result{"fuzz: no trap across random triples", false,
				fmt.Sprintf("trapped at iteration %d (seed 42): %v", i, err), Hard})
		}
		if float64(v) < 0 || float64(v) > 1 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			rangeViolations++
			if firstRangeDetail == "" {
				firstRangeDetail = fmt.Sprintf("first at iteration %d: score=%v", i, v)
			}
		}

		// Invariant 3, on this random q/gt rather than the fixture.
		if i%10 == 0 {
			ev, err := m.Score(q, gt, "")
			if err != nil || ev != 0 {
				return append(out, Result{"fuzz: empty answer is 0 for ALL inputs", false,
					fmt.Sprintf("iteration %d (seed 42): empty answer scored %v (err=%v)", i, ev, err), Hard})
			}
		}

		// Invariant 4: replay every 25th triple.
		if i%25 == 0 {
			v2, err := m.Score(q, gt, ma)
			if err != nil || v2 != v {
				return append(out, Result{"fuzz: determinism on random inputs", false,
					fmt.Sprintf("iteration %d (seed 42): %v then %v", i, v, v2), Hard})
			}
		}
	}

	out = append(out, Result{"fuzz: no trap across random triples", true,
		fmt.Sprintf("%d seeded-random triples", iters), Hard})
	out = append(out, Result{"fuzz: empty answer is 0 for ALL inputs", true, "", Hard})
	out = append(out, Result{"fuzz: determinism on random inputs", true, "", Hard})
	out = append(out, Result{"fuzz: scores finite and in [0,1]", rangeViolations == 0,
		func() string {
			if rangeViolations == 0 {
				return ""
			}
			return fmt.Sprintf("%d violations — host will silently rewrite these (%s)",
				rangeViolations, firstRangeDetail)
		}(), Soft})
	return out
}

// RunPerformance times worst-case-sized calls. Spot checks fire roughly every 20 seconds
// and epoch tournaments score every miner concurrently, so a scorer that takes hundreds
// of milliseconds per call is a liveness problem for the node running it, not just slow.
func RunPerformance(m *Module) []Result {
	big := strings.Repeat("performance measurement corpus token stream ", 2900) // ~127 KB, at the input cap
	const q = "worst case timing probe"

	// Warm once so instantiation cost isn't billed to the measurement.
	if _, err := m.Score(q, big, big); err != nil {
		return []Result{{"performance: worst-case call completes", false, err.Error(), Hard}}
	}

	const n = 15
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := m.Score(q, big, big); err != nil {
			return []Result{{"performance: worst-case call completes", false, err.Error(), Hard}}
		}
	}
	per := time.Since(start) / n

	ok := per < 100*time.Millisecond
	detail := fmt.Sprintf("%s per call at 128 KiB inputs", per.Round(time.Microsecond*100))
	if !ok {
		detail += " — spot checks fire every ~20s; a slow scorer is a node liveness problem"
	}
	return []Result{{"performance: worst-case inputs under 100ms", ok, detail, Soft}}
}
