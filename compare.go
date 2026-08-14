package main

import (
	"fmt"
	"sort"
)

// Comparison holds a side-by-side run of two modules over the same case file.
//
// This exists for one sentence in the Track 2 judging criteria: 50% of the score is
// "improvement over baseline — how accurately and effectively the script evaluates Miner
// outputs vs the current Canonical Script." That claim needs evidence, and the evidence
// is exactly this: same cases, both modules, who orders them correctly.
type Comparison struct {
	PerCase   []CaseComparison `json:"per_case"`
	Candidate int              `json:"candidate_passed"`
	Baseline  int              `json:"baseline_passed"`
	Total     int              `json:"total_cases"`
	// Resolved: cases the candidate orders correctly and the baseline does not.
	// Regressed: the reverse — the honest column; a comparison that can't show
	// regressions can't be trusted on improvements either.
	Resolved  []string `json:"resolved"`
	Regressed []string `json:"regressed"`
}

type CaseComparison struct {
	Question        string `json:"question"`
	CandidatePass   bool   `json:"candidate_pass"`
	BaselinePass    bool   `json:"baseline_pass"`
	CandidateDetail string `json:"candidate_detail"`
	BaselineDetail  string `json:"baseline_detail"`
	// Agreement is the pairwise ranking agreement between the two modules over this
	// case's answers, in [0,1]. Low agreement on a case both "pass" means they pass it
	// for different reasons — worth a look.
	Agreement float64 `json:"ranking_agreement"`
}

// RunComparison scores every case with both modules.
func RunComparison(candidate, baseline *Module, cf *CaseFile) *Comparison {
	cmp := &Comparison{Total: len(cf.Cases)}

	candResults := RunCases(candidate, cf)
	baseResults := RunCases(baseline, cf)

	for i, c := range cf.Cases {
		cc := CaseComparison{Question: c.Question}
		if i < len(candResults) {
			cc.CandidatePass = candResults[i].Passed
			cc.CandidateDetail = candResults[i].Detail
		}
		if i < len(baseResults) {
			cc.BaselinePass = baseResults[i].Passed
			cc.BaselineDetail = baseResults[i].Detail
		}
		cc.Agreement = rankingAgreement(candidate, baseline, &c)

		if cc.CandidatePass {
			cmp.Candidate++
		}
		if cc.BaselinePass {
			cmp.Baseline++
		}
		switch {
		case cc.CandidatePass && !cc.BaselinePass:
			cmp.Resolved = append(cmp.Resolved, c.Question)
		case !cc.CandidatePass && cc.BaselinePass:
			cmp.Regressed = append(cmp.Regressed, c.Question)
		}
		cmp.PerCase = append(cmp.PerCase, cc)
	}
	return cmp
}

// rankingAgreement computes pairwise order agreement between two modules over one case:
// of all answer pairs the candidate orders strictly, what fraction does the baseline
// order the same way.
func rankingAgreement(a, b *Module, c *Case) float64 {
	type sc struct{ va, vb float64 }
	var scores []sc
	for _, ans := range c.Answers {
		va, e1 := a.Score(c.Question, c.GroundTruth, ans.Text)
		vb, e2 := b.Score(c.Question, c.GroundTruth, ans.Text)
		if e1 != nil || e2 != nil {
			return 0
		}
		scores = append(scores, sc{float64(va), float64(vb)})
	}
	pairs, agree := 0, 0
	for i := range scores {
		for j := i + 1; j < len(scores); j++ {
			da := scores[i].va - scores[j].va
			db := scores[i].vb - scores[j].vb
			if da == 0 {
				continue // candidate expresses no preference; nothing to agree with
			}
			pairs++
			if (da > 0) == (db > 0) && db != 0 {
				agree++
			}
		}
	}
	if pairs == 0 {
		return 1
	}
	return float64(agree) / float64(pairs)
}

func printComparison(cmp *Comparison, candPath, basePath string) {
	fmt.Printf("\nComparison — candidate vs baseline\n")
	fmt.Printf("  candidate: %s\n  baseline:  %s\n", candPath, basePath)

	for _, c := range cmp.PerCase {
		fmt.Printf("\n  %s\n", truncate(c.Question, 76))
		fmt.Printf("    candidate  %s  %s\n", passTag(c.CandidatePass), dim(c.CandidateDetail))
		fmt.Printf("    baseline   %s  %s\n", passTag(c.BaselinePass), dim(c.BaselineDetail))
		if c.CandidatePass && c.BaselinePass && c.Agreement < 0.7 {
			fmt.Printf("    %s\n", yellow(fmt.Sprintf(
				"note: both pass but ranking agreement is only %.0f%% — they pass for different reasons", c.Agreement*100)))
		}
	}

	fmt.Printf("\n  %-22s %d/%d cases ordered correctly\n", "candidate:", cmp.Candidate, cmp.Total)
	fmt.Printf("  %-22s %d/%d cases ordered correctly\n", "baseline:", cmp.Baseline, cmp.Total)

	if len(cmp.Resolved) > 0 {
		fmt.Printf("\n  %s\n", green(fmt.Sprintf("resolved by candidate (%d):", len(cmp.Resolved))))
		for _, q := range sorted(cmp.Resolved) {
			fmt.Printf("    + %s\n", truncate(q, 72))
		}
	}
	if len(cmp.Regressed) > 0 {
		fmt.Printf("\n  %s\n", red(fmt.Sprintf("REGRESSED vs baseline (%d):", len(cmp.Regressed))))
		for _, q := range sorted(cmp.Regressed) {
			fmt.Printf("    - %s\n", truncate(q, 72))
		}
	}
	if len(cmp.Resolved) == 0 && len(cmp.Regressed) == 0 {
		fmt.Printf("\n  no ordering differences on this case file\n")
	}
}

func passTag(ok bool) string {
	if ok {
		return green("PASS")
	}
	return red("FAIL")
}

func sorted(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}
