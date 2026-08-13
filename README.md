# telegraph-wasm-check

Validate a Telegraph scoring module before you spend gas registering it.

```bash
go run . module.wasm
```

Loads your `.wasm` through [wazero](https://wazero.io) — the same runtime the node uses —
with **no host imports registered**, then runs the published Stage 1 gates, a set of
Stage 2 behavioural probes, determinism checks, and any ordering assertions you write for
your own intent.

Built for the [Telegraph hackathon](https://hackathon.telegraphprotocol.com) Track 2.
No dependencies beyond wazero.

---

## Why

Registration is on-chain and cannot be edited. A module that fails Stage 1 is a hard
reject, and fixing it means another transaction. Every one of the Stage 1 checks is
published and testable locally, so there is no reason to discover a failure on-chain.

Stage 2 is different, and the docs are explicit about why the thresholds are withheld:

> "A scoring module that is tuned to clear a published bar is not the same thing as a
> scoring module that ranks well."

So nothing here claims to predict Stage 2. The probes test the properties the docs
describe in prose — that a module discriminates rather than returning near-constant
values, recognises a correct answer, prefers a paraphrase to an off-topic answer — and
report them as advisory. Treat a `WARN` as a prompt to go and look.

## What it checks

**Stage 1 — hard.** A failure here is a reject at registration.

- Exports `alloc`, `dealloc`, `rank_answer`, and linear memory
- Empty answer returns *exactly* `0`; whitespace-only returns *exactly* `0`
- Self-match beats an unrelated cross-match
- No trap on ~104 KB of repeated text, or on emoji / CJK / accented input
- No trap on degenerate input: empty question, empty ground truth, all three empty,
  embedded null bytes, an answer 1000× longer than the truth
- Under the 32 MB size limit

**Determinism — hard.** Identical inputs must produce identical outputs, both across 100
repeat calls and across a **separately instantiated module**.

This one is easy to overlook and expensive to get wrong. Validators independently run the
same script and must reach consensus on the score, and the node drives a pool of module
instances so concurrent scoring doesn't serialise. A module that carries state between
calls, or seeds a hash from anything ambient, doesn't score oddly — it breaks consensus.
The fresh-instance check catches state that survives calls but resets on reload, which a
repeat-call loop alone will not.

**Stage 2 — advisory.** Discrimination (spread, standard deviation), correctness
recognition, paraphrase over off-topic, ordering, and a check that every score lands in
`[0,1]` with no NaN or Inf. That last one matters more than it sounds: the host silently
clamps out-of-range values and collapses NaN to `0`, so a module returning `1.4` isn't
rejected — its ranking just quietly stops meaning what you intended.

## Your own cases

Generic probes only get you so far. `--cases` lets you encode what "better" means for the
intent you're targeting:

```bash
go run . module.wasm --cases examples/financial-data.json
```

```json
{
  "intent": "FINANCIAL_DATA",
  "cases": [{
    "question": "What is the current price of Bitcoin in USD?",
    "ground_truth": "42137.51",
    "answers": [
      { "label": "exact",       "text": "42137.51",                     "tier": "high" },
      { "label": "in a sentence","text": "Bitcoin is at $42,137.51 USD.","tier": "high" },
      { "label": "rounded",     "text": "About $42,137",                "tier": "mid"  },
      { "label": "stale price", "text": "Bitcoin is at $38,904.10.",    "tier": "low"  }
    ]
  }]
}
```

Every answer in a higher tier must outscore every answer in a lower one. The check reports
the worst inversion by name:

```
PASS  What is the current price of Bitcoin in USD?
      exact(1.00) > in a sentence(0.90) > rounded(0.73) > stale price(0.09)

FAIL  What was Apple's revenue in Q3 2026?
      "correct but bare" (mid, 0.4231) did not beat "right shape, wrong number" (low, 0.4917)
```

**Assertions are ordinal, never absolute — that's deliberate.** You cannot meaningfully
assert that a good answer scores above 0.8. The thresholds are unpublished, every module
has its own scale, and a module spreading scores between 0.2 and 0.5 can rank perfectly
well. What you *can* assert is that a better answer outranks a worse one, which is the
only thing the leaderboard consumes.

### Blind-spot probes

`examples/hard.json` collects cases that most modules get wrong. It is meant to fail — run
it to find out where your module is weak, not to pass it.

The reference module fails all three:

| case | what it exposes |
|---|---|
| `bullish` vs `positive` / `bearish` | the antonym shares characters with the truth; the synonym shares none, so the *opposite* answer outranks the correct one |
| `scam` vs `fraudulent` / `safe` | same shape, security labels |
| "reduced mortality by 30%" vs "**increased** mortality by 30%" | a negated answer scores **0.938** against the correct paraphrase's **0.903** |

Closed-set label intents (`SENTIMENT_ANALYSIS`, `CONTENT_MODERATION`, `FRAUD_DETECTION`)
and anything where negation flips meaning are where surface-similarity scoring quietly
breaks. If your module beats these, it is doing something the character- and token-based
approaches cannot.

## Output

```
0  Stage 1 clean
1  a hard check failed (or --strict and an advisory failed)
2  usage or input error
```

```
--cases <file>   ordering assertions for your intent
--json           machine-readable output for CI
--strict         treat Stage 2 advisories as failures
--no-color       disable colour (also honours NO_COLOR)
```

## Building a module

Minimum viable export surface, in any language that compiles to freestanding
`wasm32-unknown-unknown`:

| export | signature |
|---|---|
| `alloc` | `(size: i32) -> i32` |
| `dealloc` | `(ptr: i32, size: i32)` |
| `rank_answer` | `(q_ptr, q_len, gt_ptr, gt_len, ma_ptr, ma_len: i32) -> f32` |

`breakdown_answer`, `embed` and `rank_answer_cached` are optional and have no effect on
whether you pass. `embed` and `rank_answer_cached` are checked independently by the node,
so implement both or neither — this tool warns if you ship exactly one.

The three strings are standardised before they reach your module: `question` is the query,
`ground_truth` the correct answer, `miner_answer` what the miner generated. You will not
receive raw upstream API payloads.

## Contributing

Cases are the useful contribution. If your intent has a failure mode the probes miss, open
a PR adding it to `examples/`.

MIT.
