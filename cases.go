package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// CaseFile lets an author encode what "better" means for their own intent.
//
// Assertions are ordinal, never absolute. You cannot meaningfully assert that a good
// answer scores above 0.8 — the Stage 2 thresholds are unpublished, every module has its
// own scale, and a module that spreads scores between 0.2 and 0.5 can rank perfectly
// well. What you can assert is that a better answer outranks a worse one, which is the
// only thing the leaderboard actually consumes.
//
// So each answer is tagged with a tier, and the check is that every answer in a higher
// tier outscores every answer in a lower one.
type CaseFile struct {
	Intent string `json:"intent"`
	Cases  []Case `json:"cases"`
}

type Case struct {
	Question    string   `json:"question"`
	GroundTruth string   `json:"ground_truth"`
	Answers     []Answer `json:"answers"`
}

type Answer struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	// Tier is "high", "mid" or "low". Higher tiers must outscore lower ones.
	Tier string `json:"tier"`
}

var tierRank = map[string]int{"low": 0, "mid": 1, "high": 2}

func LoadCases(path string) (*CaseFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf CaseFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, c := range cf.Cases {
		if len(c.Answers) < 2 {
			return nil, fmt.Errorf("case %d needs at least 2 answers to compare", i)
		}
		for _, a := range c.Answers {
			if _, ok := tierRank[a.Tier]; !ok {
				return nil, fmt.Errorf("case %d answer %q: tier must be high, mid or low (got %q)",
					i, a.Label, a.Tier)
			}
		}
	}
	return &cf, nil
}

type scored struct {
	Answer
	score float64
}

// RunCases scores every answer in every case and reports each tier boundary that the
// module got backwards. Failures are Hard: an author asserting their own intent's
// semantics is making a stronger claim than any generic probe can.
func RunCases(m *Module, cf *CaseFile) []Result {
	var out []Result

	for _, c := range cf.Cases {
		var all []scored
		trapped := false
		for _, a := range c.Answers {
			v, err := m.Score(c.Question, c.GroundTruth, a.Text)
			if err != nil {
				out = append(out, Result{
					Name:     truncate(c.Question, 44),
					Passed:   false,
					Detail:   fmt.Sprintf("trapped on %q: %v", a.Label, err),
					Severity: Hard,
				})
				trapped = true
				break
			}
			all = append(all, scored{a, float64(v)})
		}
		if trapped {
			continue
		}

		// Compare every cross-tier pair. Reporting the worst inversion is more useful than
		// reporting the count: it names the two answers to go and look at.
		worst := ""
		worstGap := 0.0
		for _, hi := range all {
			for _, lo := range all {
				if tierRank[hi.Tier] <= tierRank[lo.Tier] {
					continue
				}
				if hi.score <= lo.score {
					gap := lo.score - hi.score
					if worst == "" || gap > worstGap {
						worst = fmt.Sprintf("%q (%s, %.4f) did not beat %q (%s, %.4f)",
							hi.Label, hi.Tier, hi.score, lo.Label, lo.Tier, lo.score)
						worstGap = gap
					}
				}
			}
		}

		sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
		order := ""
		for i, a := range all {
			if i > 0 {
				order += " > "
			}
			order += fmt.Sprintf("%s(%.2f)", a.Label, a.score)
		}

		if worst == "" {
			out = append(out, Result{truncate(c.Question, 44), true, order, Hard})
		} else {
			// Include the full ordering on failure too — the worst inversion names the
			// pair to look at, but debugging needs the whole ranking in one glance.
			out = append(out, Result{truncate(c.Question, 44), false, worst + "  |  " + order, Hard})
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
