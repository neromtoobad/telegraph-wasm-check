# Baselines

Reference modules to compare your candidate against:

```bash
telegraph-wasm-check mine.wasm --cases ../examples/hard.json --compare official-example.wasm
```

## official-example.wasm

Built from [`telegraph-examples/wasm-scoring-module/rust-module`](https://github.com/telegraphprotocol/telegraphexamples/tree/master/wasm-scoring-module)
— the starter module the docs walk through. Word overlap against the ground truth, exact
match scores 1.0.

**This is the starter example, not the Canonical Script.** Its own README calls it "a
bare-bones module... a legitimate starting point." The Canonical Script — the actual
Track 2 baseline — is described in the scoring docs as blending semantic similarity
against both question and ground truth, and had not been published when this was added.
Beating this module is table stakes, not the improvement claim.

Useful as a floor because its failure modes are the ones worth knowing:

| behaviour | why it matters |
|---|---|
| returns `0.0000` for every non-exact answer on numeric and label truths | cannot rank at all on those intents — every miner ties |
| prefers a negated answer (0.89) over a correct paraphrase (0.38) | one flipped word inverts meaning; token overlap doesn't see it |
| prefers a wrong-entity answer (0.78) over a correct paraphrase (0.33) | copying the truth's phrasing beats being right |

Also note: it exports only `alloc`, `dealloc`, `rank_answer`. The integration platform's
WASM wizard states modules must also export `breakdown_answer` or they are "rejected on
arrival". Run this tool against the example and you'll see that check fail — resolve which
is authoritative before registering.

Rebuild from source rather than trusting this binary if you prefer:

```bash
git clone https://github.com/telegraphprotocol/telegraph-examples
cd telegraph-examples/wasm-scoring-module/rust-module
cargo build --release --target wasm32-unknown-unknown
```
