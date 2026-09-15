# SW-280 — qrel-blind smoke evaluation over the sealed cobra-v2 holdout

Contract: `sw280-qrel-blind-smoke-evaluation/2`. Measurement contract: `sw266-measurement-contract/2`.

## What this is, and what it is not

This is a **qrel-blind smoke evaluation**. The raters saw our bundle format and our question set, and were given
the query text and the exact serialized `task_context/2` bundle. It is not a system-blind
evaluation, not a human panel, and not an estimate of general answerability.

**Blindness here is instruction-enforced, not sandbox-enforced.** Each rater was a subagent with
file tools, running in a checkout that contains the answer key for all of these questions
(`internal/eval/retrieval/testdata/datasets/cobra-v2.json` carries every grade-3 span), and it was
instructed to read exactly one file. Nothing prevented a rater from reading more; an instruction
discouraged it. No tool-call transcript was preserved, so this cannot be checked after the fact.
What can be checked, and was: every committed prompt rebuilds byte-for-byte from the
pre-registered bundle and question and carries no judgement, qrel, expected answer or rubric.
What the behaviour shows, and it points away from peeking: 38 of the 128 primary responses
declined with `INSUFFICIENT`, and the graded outcomes agree with a naive "was a grade-3 span
inside a retrieved snippet?" predictor on 54 of the 64 queries. A panel holding the key would
not produce that shape. Treat the count as a smoke reading taken under instructed blindness,
not as a number no rater could have inflated.

Its pass count is a separate gate. The pass count does not enter the token estimand or its
interval (`docs/eval/retrieval/methodology.md`, "Estimand and claim boundary").

## What this evidence is designed to establish, and what it is not

> The evidence for this number is designed to detect error, accident and drift. It is **not** designed to detect deliberate falsification by someone with write access to this repository, and it should not be read as establishing that none occurred.

The threat model this procedure is built to is `docs/eval/retrieval/threat-model.md`.
It defends against **error, accident and drift** — a stale or programmatically wrong value, an
artifact deleted or overwritten without intent, a frozen input changing under a run. It does
**not** defend against deliberate falsification by someone with write access to this repository,
and no check that lives inside the repository can: that actor owns the repository, can author any
commit and can rewrite any history.

The threat model records the controls that remain in force, and records one known open
gap deferred by an explicit owner decision — the run directory's containment check is lexical and
does not resolve symlinks. It is out of scope for this run, which fails by 26 passes and whose
outcome no relocated artifact could reach, and it is required before any run reporting
`RELEASE: YES` is published.

What binds an author is what a reader can recompute from outside: the committed digests below,
this run directory's git history, byte-identical reproduction from the committed inputs, and —
above all — publication of the raw per-query data and the scoring code.

## Result

| quantity | value |
|---|---|
| answerable holdout population `N` | 64 |
| pre-registered minimum passing count `k` | 56 |
| observed pass count (as reviewed) | 47 |
| observed incidence | 47/64 (resolution 1/64) |
| observed pass rate | 73.4% |
| exact Clopper-Pearson 95% interval | [0.609123870846, 0.837023948531] |
| lower bound clears the 3/4 floor | false |
| primary disagreements | 1/64 |
| adjudications | 1 |
| missing, empty or refused primary responses | 0 |
| **RELEASE** | **NO** |

- the reviewed pass count is 47 of 64, below the pre-registered k=56 (reviewed 47, corrected 47, and the release is decided on the smaller); there is no override, exception or waiver

## Capture binding

The rated bytes are bound to the candidate implementation and the indexed checkout this run
names: candidate `3899309e75b40f62cf81cfd7552238c3cb33b570`, checkout `a0a6ae020bb3899ff0276067863e50523f897370`, both worktrees clean at capture.

## How `k` was derived, before any response was opened

`k` is the smallest integer in `[0, N]` whose two-sided exact Clopper-Pearson 95% lower bound is
at least `3/4`. It was derived by code from `N`, not written into this document.

- `N` = 64, read from the sealed dataset (`c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6`): count of answerable holdout queries in the sealed dataset: split=holdout, stratum!=no_hit, at least one grade-3 span
- `k` = 56, whose lower bound is 0.768473694033
- `k-1` = 55, whose lower bound is 0.749763164375 — below the floor, which is what fixes `k`
- method: two-sided exact Clopper-Pearson binomial interval; the floor comparison is exact rational arithmetic on the upper tail at p = 3/4, and rendered endpoints are bisection brackets of width 2^-64
- pre-registered at 2026-09-15T20:20:20Z, naming precondition record `be52e33151cde1b7431a932a0650fe868e83e9267fae91cec738f8ae2eeaf7dc` at commit `3899309e75b40f62cf81cfd7552238c3cb33b570`

## Per-stratum counts

Counts are authoritative; each stratum states its own `1/n` resolution.

| stratum | passed/total | resolution |
|---|---|---|
| ambiguous | 2/10 | 1/10 |
| architecture_flow | 6/11 | 1/11 |
| config_docs | 10/10 | 1/10 |
| exact_identifier | 11/11 | 1/11 |
| exact_path | 11/11 | 1/11 |
| nl_behaviour | 7/11 | 1/11 |

## Participants

| role | id | provider | model | took part in this track | basis |
|---|---|---|---|---|---|
| primary | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Independent logical primary-A configuration; one fresh ephemeral read-only process per item in an empty Git repository with the exact frozen prompt only; no candidate implementation or dataset curation participation. Provider exposes no immutable backend snapshot. |
| primary | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Independent logical primary-B configuration; one fresh ephemeral read-only process per item in an empty Git repository with the exact frozen prompt only; no candidate implementation or dataset curation participation. Provider exposes no immutable backend snapshot. |
| grader | v4-grader--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Independent logical grader; one fresh ephemeral read-only process per frozen packet in an empty Git repository; no primary, implementation or curation role. Provider exposes no immutable backend snapshot. |
| adjudicator | v4-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Independent logical adjudicator; one fresh ephemeral read-only process per necessary disagreement receives only the original frozen prompt before its answer is sealed; no primary, implementation or curation role. Provider exposes no immutable backend snapshot. |

## Adjudicated queries

| query | primary outcomes | adjudicator | final |
|---|---|---|---|
| c17u4-q-05b91a64d7e2 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | pass | pass |

## Frozen inputs and the end-of-run comparison

| role | path | frozen sha256 | observed sha256 | matches |
|---|---|---|---|---|
| dataset | `docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v4/sealed-dataset.json` | `c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6` | `c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6` | true |
| budgets | `docs/eval/retrieval-budgets.json` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | true |
| targets | `docs/eval/retrieval-targets.json` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | true |
| grading_rubric | `docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v4/grading-rubric.md` | `7adf59a0bf9578a91a80796d9e5d7f6016de803f155c3ceb36a08f5db55f7cd2` | `7adf59a0bf9578a91a80796d9e5d7f6016de803f155c3ceb36a08f5db55f7cd2` | true |
| methodology | `docs/eval/retrieval/methodology-v2.md` | `47fae9f7c4df86d41eae6ae2f12aeb8be5019fe30ac0398fd80d05100fa7212e` | `47fae9f7c4df86d41eae6ae2f12aeb8be5019fe30ac0398fd80d05100fa7212e` | true |

Compared at 2026-09-15T20:58:17Z. All match: true.

## Capture provenance

- capture instrument: `sw280-candidate-mcp-capture/4`
- transport: MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)
- payload boundary: `mcp_jsonrpc_response_bytes`
- candidate: `task_context/2-compact/17` at a 1200-token budget
- repository: cobra at `a0a6ae020bb3899ff0276067863e50523f897370`
- embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (model `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b`, index `147:static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b
40:e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
64:75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c
64:107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45
3:256
2:v3
0:
32:61022b20c10600c4c2f3e45c36059122`, generation `g-f7af739666e8a093`, 768 persisted vectors, state ready)
- tokenizer: `tiktoken:cl100k_base:ordinary` (vocabulary `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`)

## Content addresses

| query | query text sha256 | bundle sha256 | bundle bytes |
|---|---|---|---|
| c17u4-q-01a41c9e72d0 | `6638aa528fda5b7318489ef9d15630e60d422fa1aec409b4df72db37929ab24b` | `b19dd1386f362fba231c18c455e64ddc36dcb15cbbec2edab94ce1380e058e47` | 3621 |
| c17u4-q-0275d3a9b18f | `ba87b1405b12ceeb0cb17bc2dc1b598d1bbfb7dd4eacac2daa7882570df85daa` | `ba342fefde4ef0f0da6c8013f4af3fc20c37a6a074fb45560a62d4c64761213a` | 3672 |
| c17u4-q-03c87e512aa4 | `ca6aa86e915b55e3aff5fb40ef48abe647b1d7f5b41a686704ddf1f430cd7b66` | `53d3bf7825c983cb2b8009e31e9fd6289120cf7246a1c244cad311efef493ba0` | 3607 |
| c17u4-q-045ef20bc931 | `74dc21b2234e6d128dfb619d623736cccdf3430b9dcd9a7343927b97f9c09b3d` | `21ec9fb39bc07add3e8313f9538f26e98e745329d945de6b9127d93b1e5a147d` | 3828 |
| c17u4-q-05b91a64d7e2 | `1ce678547c6f9807f885ef569d773a0ed030428953bc01930482cd4ed71872e1` | `a61bd7239fc60f0a5d9d374c46852c82419ce5182c8a48659e6167ef0589465d` | 3421 |
| c17u4-q-061de8f34ab7 | `90f0330a916cf3bd523526e3e3a53c0ab79c3b35dd133e145b7a193551dcef2d` | `85fac1bfc970711c5a61570e1c02d372276ea0aa2b759bb368f6700b82ee6721` | 3119 |
| c17u4-q-07643fb908ce | `9a4a01b31420b62954cc796a9bc3dda65a550f6501aa4243a1139d3a23a08e06` | `da4c78b2422ab89b00cf58e6f97c893fe6baf8d24f49737a9c38f9fefb89728f` | 3439 |
| c17u4-q-08f2ad751c63 | `af0354672647d40a5b7cb2d25ca34f51ab5a57b7d399c78fd75f4b26cbce1749` | `dfd97a97fdacbe395c52ed03593dbd5584ae29a21932a41700404e09fbba1107` | 3448 |
| c17u4-q-09d5802be146 | `0dc68a492d368ae9fcb609b1d02a97b6208d1bff87fe519f6ccf28954c133005` | `522abbbfafe4095dfc51c41706ee7f3a4cc4e9b913dca4699c0e88ba86d557b6` | 3697 |
| c17u4-q-0a76ce41f953 | `3cb3bc41fa67614e5b2fa1f3ceed9bc1c7a02efbb6f7d43a512bd49784cf7fc9` | `838a3212d6b559d7820e04f34e6c57d06c7fb2e1c7015779253e306f3eeb02ff` | 4052 |
| c17u4-q-0bb13d8a6f20 | `9a928c2ebe17f78382f882b534c209bcd3844684cbb2e5a716f462005c050193` | `e89584e3fe25e8d90c0d411ebf9f837a1b8f1b4ee0cafcf4a293a5993cd08dd2` | 3660 |
| c17u4-q-0c94af37e815 | `a8e323a15597bdf8ce645f2e8771453576b0c80e1b76c49d1e7295a35f875c1e` | `0d6f15d24d39928e69ddf8ffd03eaebeb62f708776de41d73178379d917d5af9` | 3496 |
| c17u4-q-0d31eab9627c | `398938e3c72b84a7770fbd2c69909e5ac8458cd0450940c5a0735588c52bd783` | `ea0e5141d5cd076a2130e2622c249ea6cfebd68de9048cdd98ad65c81e528e5c` | 2922 |
| c17u4-q-0e8f147ac350 | `e270c2e2282dd57d5e39d3a447d19ef18d0c5db0ae3f12631400676792ab24df` | `fc006f4131435738fc36563552a064e8692af50c0cba7cbf9c498118077ec9f9` | 3335 |
| c17u4-q-0f5ca7904bd1 | `db6bea58096f39f93e72752a9e84ad5c203910f947ceceb71c5de17200b966b6` | `8b4d273690a8572c0cd42eb39ab74ef34925d0fd0992afbd35b361507537be13` | 3830 |
| c17u4-q-10e29b461f8a | `8721fc8c2ef661d54f3d55b50efa8073006b6a00f40b7041ff4a5fea4cfe8d4c` | `45d989c2930162b590ca57f06b959500ca1b033a402e35aa954f1618a115ddeb` | 2665 |
| c17u4-q-117acd53e609 | `277a6f71fa1110478babf36029c1a20bba80674b0518faeeee36252f1e9e0fba` | `c7d606865bfc1fec320e674dc3a57d18cb1dcdad2c45d9d269c707126f4323a9` | 3479 |
| c17u4-q-12b084f26cd7 | `249b96a55a2c8e4a1affa2b8d2a28fb3755636fab3272d974e8d3d047fe01c06` | `a18eb25c10edc4c7b362a241adca08c8614177c9d89891c911f5f522648fe598` | 2780 |
| c17u4-q-13cf691a85e2 | `f80c8907079e1b783b5ac768fdd321a99de67070737a2a02bdfcc294bea0b15e` | `0d631c888cc7f68d384d9f5c609e83a8ea7283504f59ad63638501abdfc9753a` | 2727 |
| c17u4-q-1426dbe970af | `deab4a4cd01353181b382f61d9a66cdb48eb4947d3544ddc77b2ee17375d97dd` | `2101d22b06095131038ba60b0286d631ce81fb781ba29e34e2d69ac27ebd1adc` | 3591 |
| c17u4-q-159d30a24c61 | `47f1e6b02e89d58a205dadb3e30d20647ba8a0190effa15ab40ee661cbe75937` | `ddef82661a77f1884b5b455c38b67bec768ad087495f8ad89fb15f8876ed05b6` | 3491 |
| c17u4-q-16f047bc385d | `4c3b14e11e2d8ef7d6bffda9c7e544f27424740ef1234073454d199096e13046` | `bfa4ee228897a7e1536dd9671b2aaed7c4a06cc3a562b99856468a0d618d86a7` | 3595 |
| c17u4-q-178ab4315fe0 | `c3b0bf93c9ddeb8c0588932003c244f2db861b0dbf28c28b103dd1c05b6f5492` | `6f4b2313983f4e47e0b8fc928f6adfb79cdd457778cd64c9421b739342cbec93` | 3232 |
| c17u4-q-1851ce7a02d4 | `f70d382634a5cb282b474eee18082b141c7c502b54d329e2a68afdbd8975c5e4` | `e2b995ae0152d756c937198b55e6db0340b80f50e8b7eecb6b7e77fbaa069bb3` | 2714 |
| c17u4-q-19d60a8f3b27 | `2d4b75dd74b092d268997c6b4afb40a029517788aa2483a644a99cf1291f4961` | `ebe509541d9a758b293ba3f16a60d0fbf5163f0863cd971afa0d0d6d4dec2c02` | 3418 |
| c17u4-q-1ac4936e71f8 | `a2bc4ac9e655d54e7c672c0764de176f6a602f2df53898f5dd452023dbc73d1c` | `23259c5cbb69edbdda383fb552aacd5301e40b2a5d7c9e1577fff696941981e2` | 3344 |
| c17u4-q-1b2fe805ac64 | `2ff7e9da8ba8d275d6c56ec3fcfff8e1368a71c3883ed7edcda977012e729fab` | `49b57ace95bda488ccc3d0e7d1e02d4a766a57010627906872a05da46864c392` | 2631 |
| c17u4-q-1ce7419b50d3 | `0a6dda45b96ef019ffe8079ace01f23a8e96f684d9d69c13a1ccd5e0f0a10154` | `bb75d347ff317f342aec14bba34ce32fcbb109529a46a87a8834dedee3802bd3` | 4065 |
| c17u4-q-1d09bc627e45 | `588404cff9587811175c724fa5b6066ba757fe4742d74bfdb984585d6e53219d` | `08ccfaa8b1bdbae12efc391ff4c661b52296e895a7ef65e11579be353eb54345` | 3201 |
| c17u4-q-1e6a50f918cb | `5793bb9b780abf445dc3b5a83c9d18f7a4c2e1c0dc746eccf80fe8117ff026bf` | `08e9b6fd2724a04c03b1f9f06ef8dde6a803da7c2685295dadcfc218f5fb2398` | 3222 |
| c17u4-q-1f83d2ae0469 | `4cdeaf9f1cc763aafac84e31e7b24c3f08fb3389eb6cbd9fc84ca71217b0eacf` | `b0aa46ceb5c320c759ee8f9b8aa5e5dd31d1bb364646bfa3dcce7c5d9ecea2b8` | 3402 |
| c17u4-q-20b5794c31ea | `ccff80ea177a4a19e17884c6afb8df7e8aa60aa041fc2e67497338247a058b48` | `0f0286d8663c2001d3cd517d19ab5e6c2614a054b0d3ad3e2261055744329a5d` | 3309 |
| c17u4-q-21ed0468a792 | `bf58c2e5c11612eba5ca9599b1d6fdcaa4dc90974c3a6599a65cc151080c0f70` | `5fc88e381c256fde85f290628e118320d34de69c7ec89d077bf46e193859e839` | 2836 |
| c17u4-q-22a73c5f90bd | `2ce9320e1c325a203c257f495f3a6c934f62466ea5b229b0699a6dc7f31ad8cb` | `78e8bcedf4c482e5d6ccce964faff9a08e7b4fa0d202a9481d92e7db24ead00a` | 3495 |
| c17u4-q-2349eb61d708 | `f7a7cf27da643a1589f739e7459b17f052e3e57040a5d5031aff4eb6cad49b02` | `1b3335ed5db4afe7e1dcb0c626b00712cd27ac01405cf9433f8a55557e7ab4c2` | 3534 |
| c17u4-q-24d0257ac4f1 | `eb643e83266fe0609380eceabc9aa8dac712858122097bbc5ddf00046ca5e5a2` | `2429fa62b8ed8f475626a258199e574cef06e370502d80c473b3a6079400030f` | 3500 |
| c17u4-q-25f37b91486c | `a9743869ee5381fb4ea2de5c66606ece79ab1f93dc47f9bee691ab8aad99e86f` | `16317ef5f6f127ba0a9a836f814617cd24eda6595977235ba461d51162c60911` | 3329 |
| c17u4-q-268c51e30ab9 | `5dffb91de2a5071d9664e88fc39ed7dd6a6c11565a0ed9111ab4f56df3425ee6` | `f67c6b939bdf43bc7517bb038fd878dd52dee58d448a5bb2cd239080c41e0022` | 2817 |
| c17u4-q-27b9146fd352 | `46f4b38e05b68bcd2f9ef3bbbb9c0b14f2d6672c5b35d7253217c54d68f3022e` | `5d1b777b636d5602aae102e6ec5c478741777ba9d9b1153a15ea6e9e6292fe68` | 3576 |
| c17u4-q-281fc0a67e94 | `af35c91a1d237b3590cf2b6b5784e8c1cd598ddd02eaf89f09d5556e03133857` | `0fdb3a0f64cf3e4c13cb3f72560cdd1012962856fbe325c613828e61413a4581` | 3881 |
| c17u4-q-2968ad35f1c0 | `87cc8adb23325d7d6552093aae63411169251893f22c8b07149aa930670425e5` | `2dbf5836bc45885b14348f2fe3274d1df754425ea4ecbf40eec4b52b918147bc` | 3222 |
| c17u4-q-2af503c8726e | `b325e717b6a6f1c9c73dcb1f9148e2e1bed9a3d364f83def056915e6ac4d132d` | `e60028baf3f8a32bc5e273a6a2443f7f32008de8cddcf4a13607e99622f4f76c` | 3769 |
| c17u4-q-2b7ce1490ad3 | `2478043459193f0c3876f894c0151a33d2884301e43426c01a16a811400cd898` | `2ef8089d55106d6331ba698636347dbc9fa583963a27534903c9ed08f8ad5bac` | 3703 |
| c17u4-q-2c04eb7d5638 | `7ece0ec992440d93b5b6752da29b003d77d1164d11db8bd8ea9d09a440f141e0` | `ddbe1b2f3f10ca08f43cc17c3dc56cc136ed5e96a3a4fe7d4ba2b6ace65d719b` | 3788 |
| c17u4-q-2d913fa42bc0 | `ca691b9bcc26bbbedacbd7cdae466b15774ecb95bd31465b8ec46e9b2fe89098` | `30ba6a47ecadf11ecf3f905de70f113bf566bb8b2df0f02d75cf7694aa130380` | 3799 |
| c17u4-q-2e6ac50719f4 | `5090141de0bd676dcc0fa1f5c237d4ae7dfe76b80f8de543e13ba76a0374d402` | `196d06c48132c5cfd2f58aece8b3477c1961374179c4f257bb020016ed9cdbb0` | 3746 |
| c17u4-q-2f108dcb753a | `6cc74fefb38393cc8d92daa6e3c6fdac0b320577fdb935812c14e2d5ff2f9a0d` | `8ac2bb8c03b850226f8842d8da0a4587f61f689b3703537c5c4a89f7812f8bbf` | 3442 |
| c17u4-q-302bc1e8496d | `b31970f8b9b55cfe0621ee283bf92130ba86cc3e896396d7176a62ba1811167c` | `d58b41ee5a71b9e511f6280990b7973680e29725914f4b2b98bcd6d3e821b41e` | 2844 |
| c17u4-q-31e74a50d2b8 | `8c812f619276719656915de47fb382b9d0481f69a8f7b3ff17e00825b70511ef` | `7aae8603377f7bac105ef44f5d599324a1013d68510dabe81403cd96b218f192` | 3614 |
| c17u4-q-32a59f06ce43 | `46dd4050265d3cd996004218fc13f5b91c25df38ce98ddc6e43203f4729ef097` | `c7fba31de89747f460d152f9992c3473d25dc9250fcb64de5a13d44a55e11c16` | 3833 |
| c17u4-q-33d14872b50f | `2a323ee464552ff1a0cb4ec49f901c63eceb08d09eedf1a3a6bc070125ff9118` | `94e2bf5a981ec46b922f2921c5ef6c9b98faaba715adecd05b750cca0a1eb620` | 3517 |
| c17u4-q-34f80c3a927e | `ad0f2e405806b4cd7fdbac6d2f07bdad8c8309b79802477b72fb499ede197806` | `18ecbf2f16a08a2e43e9932100249a901a84d5edc4faf2864eb081e01655d506` | 3622 |
| c17u4-q-359bd6140aec | `c0ad80b4b168d1c2d31e21329fc7b6c01582f1c6472a44e5bc346453d98fc8e0` | `c2d9cd3722eae24cb72ba35a028733d0e740412e51ce3205eff1fc1d84d0effc` | 3986 |
| c17u4-q-36c025e93f71 | `5166acafb97394c8f28d5ecdcf80d5cebe7741cfa86727fc780eef3de1e958b8` | `c83a99ecd429e4ae5c0fed101635e2d1d9e85848984f2a73ded8e75d7e986053` | 3667 |
| c17u4-q-37a64f28de05 | `51b9bbb0f98213d474d81827249e7fd55a2ce4df7e2894a3237884cb0c82a33c` | `00dfec8d9bfac685cfe992c08850454080f61ebfc81844feceb6af2ce17c38da` | 3711 |
| c17u4-q-38db105ec742 | `7f4ba8547e8b248601e6cc04690945bb0599173096d7667462fa1badb9136440` | `3b4d99b4c4b0d709f30aa195cf25598b0481bfb121faed8e57b1374adac9adda` | 3724 |
| c17u4-q-39e7a03d68b1 | `8a04d0ad1bade4a7fac3c1404e5491cfcd1fa5521fa1019bbc21cd2b8e7ef1ad` | `c7223687f3bda9c8f312a9e2968ea6500d03043b44f479753893f075291a133f` | 2853 |
| c17u4-q-3a519cf2740e | `9f1063f500a3443e3699499ca41c06d056c0488af614aea176066697eeaf5d76` | `90e8cceadbd8542162bf9fba9a35d960dfeb37425d8b36754fc983bf87a2f0f0` | 3824 |
| c17u4-q-3b06ed8a951c | `448a3720bb6545c4bafca2f5b590f88ce6f341e45a60e59e5978225120da5b92` | `59e8843aed540fd730681f3dfdd72a4a6a2d887de1c44d7159ae11a47a2ff123` | 3259 |
| c17u4-q-3cd92471a6f0 | `3bc1026bed65d74470a4d22f994d43951a84d083a83c82a29b14f8f09b00bccb` | `51bed3f7aeea2f45dc11c043612b397a13a390291197dbbbcf03fa336fee9297` | 3029 |
| c17u4-q-3d78a2b50ce4 | `39236b836727ad3578c3fbc30b07bd03b51dbcf9ddd6055bcd0eadac0e683fa9` | `7df123ba385b09bc48debf3e6c252af3c3aec7bdb1764e5a204f99f0f957ce7d` | 3249 |
| c17u4-q-3e14fc6972ab | `386115952a5d2b93eb8eb4710e29e364229017335fbf70c5e74c5eee2a408144` | `a3770b31d58b2dda68fcb43e2ab08795c70b790088014b336a23b20feeb42783` | 3267 |
| c17u4-q-3f8b052cd169 | `b6b36680170efcf2d1ddc6df14058f277ce7838d1566d93eb434fadb9fb391b9` | `33281d546a8abc8bca05a33d853a31bf904d5445ec425092c4645dda29d19785` | 3926 |
| c17u4-q-40c63ea817f5 | `daacd3e9bfd77017b96cbb7651e628c0acccbf5c7a5f0f05eb8eddb44215b3d4` | `280a586ffac3257c5e96f573f35d509451bd76e6f34ac2da64ea8b4925bad289` | 3769 |

| query | rater | response sha256 | status | grade sha256 | outcome |
|---|---|---|---|---|---|
| c17u4-q-01a41c9e72d0 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `c8d7ab9450330a46ddeb550dd934e3762b920ec2a6f95afd38913c0bfef6a530` | answered | `fe11cf82a2a3e3c0b393a66dbdcec5445b1b91517fefc1c32ae038acf109b2e3` | pass |
| c17u4-q-01a41c9e72d0 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a48915880f7c2bc6d330d4a2494acf0dafbf5df3a755dd0ab8ae6795f20f3bb5` | answered | `cecfd6b2adfed046861d6e2af4774e2cf3dccfc196106a7b0e6dd14eacfadc32` | pass |
| c17u4-q-0275d3a9b18f | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e49ad3bd89f702a68c02670fb1226916d69d98324e062eac6753babd53af35ca` | answered | `457fe92a11d7a3568084141f602a920e192375c29018969ff4f1137e63ae1fff` | pass |
| c17u4-q-0275d3a9b18f | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `8b48ac63a006b85193f80923c7e7e92c58383c67e0e0b0cfa39df8de01584c2b` | answered | `e44819f3580f14d93b2b41ce704d77bc8b796f90d35c6f937e1c10b2d61ab654` | pass |
| c17u4-q-03c87e512aa4 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `31aa28e2baf76a993b23dc94ac5f0a827b5289ed53f8f2315cc4071fc74c509b` | answered | `bfae8bdedaf78d9829541ca2c278217563b90a12765ff198230446789ec61c6c` | pass |
| c17u4-q-03c87e512aa4 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `664ce78d87f53562b25f79b3bc1bc88add15b46b7da65c6d41b972b561db1ade` | answered | `9f9ccf5561b144100f8d7c99cb01af7558ff047962960f12a2a21715a4411593` | pass |
| c17u4-q-045ef20bc931 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9771f7f0881fa1f8032784945b9217ad6fc4ae7b2469b5aea0983021c565d5e7` | answered | `5d0f2e1d0f1e6e7bbafecfdfe1529c4dc8d0617012363a4a4ee84435a94f9bfc` | pass |
| c17u4-q-045ef20bc931 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `384aa65e563f60813181a8d41a99b8e888bb5c619e24e9062ad0af9305ab0b02` | answered | `970d06ae582115d3b696f009fc18561d9912355eb8c84748f1b8ab3ac709cd60` | pass |
| c17u4-q-05b91a64d7e2 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `adf6d1dbc45fc21a719b291ef1759736d4f2a103a1e40404950ac0eb2f25b53b` | answered | `b748f88f4e990737a6f9b21b53270474c4708ffe80c805ec2d2672e8a8273896` | pass |
| c17u4-q-05b91a64d7e2 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `82c82e5ca97acc12019fa0f7227a0526257c1dff040dbdf81732a5c80ba15fd0` | answered | `5af084a255b8d97efea69a9a9d566336b2bc5a804eac9e9a246278db440c9a1d` | fail |
| c17u4-q-05b91a64d7e2 | v4-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `aee31d79ee70a64f0192a16ae877c11af57f8730ba2bfe582bed0cd1ab9d43a4` | answered | `4e3fc41d967d2c72e32c6496415a15d37eee1bbe7cd897db781aee9d1a55d7ee` | pass |
| c17u4-q-061de8f34ab7 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `c06e5e8f9a475dec4cca82ee8131c81efd0f7bfd1c124722c456026b1bf94d61` | answered | `6d8c5d8abed9d8594ac177335d5ed744fc35623522f95f323182d15b4107e49b` | pass |
| c17u4-q-061de8f34ab7 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `966fa5e0362ca044cb379ed2554cadffcab405e8b57912930832d87de425476f` | answered | `01a83c6112d8d44b06792eaf9815bfd475f5ed678ef13e49adaa69208545254d` | pass |
| c17u4-q-07643fb908ce | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7b484b2c02402d6d973be218a0c890d05e767c1264036afde2384d447f8da183` | answered | `68f84bb145da04930ecc7e3ea77bd8bf3531422df67c8a4617dc92b839fe2d90` | pass |
| c17u4-q-07643fb908ce | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `fb3fec6f17a5297409d4e820b31c8e14d3e4ca77376ba760accf75ce3340f962` | answered | `495daa04de0d1e407ccfb1bcfdd3d41614316a116c86c0b902ec60bc4f209346` | pass |
| c17u4-q-08f2ad751c63 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1b16b713f8b3e0528259c1caf0841bfa4ca666071ff49f06df9d722bea5cc1d2` | answered | `c574187649117f9b0903ee72885e8cf0e9f36feb3fbd225297304608e2285ffe` | pass |
| c17u4-q-08f2ad751c63 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6105d0b98ff2252dc200e6a3486af286ee2974f0e314686e894bd46b7f9844c5` | answered | `aeb6b71b1b4ed55dc044d3a1a9e49171c9db27ae40c0921933101acc8b224b0f` | pass |
| c17u4-q-09d5802be146 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7420aa9499e359f4da65cba656259a668011fca4d91bc861b0420fc6b39ef6c8` | answered | `96defee04025b0c1ab46d16c7db999203dc2b72b08797011caab56ba37bd7c69` | pass |
| c17u4-q-09d5802be146 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a2c150a16f4ff230de33e8a69e85f2febf7bf475c06cae3e953e559c3935fffb` | answered | `13b6eeb066866c9a3c878a3b9ab03d32593bd1e8b0f45b34bff0752116845516` | pass |
| c17u4-q-0a76ce41f953 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `56581643b1cf6c6f75b9004de3a690e57696e6fde6d04905d647b4ba19085378` | answered | `f45f6bf2e1023f6ce0dd70cefd84448a2da4a7810f0f7918b9ee5fd9e8586c67` | pass |
| c17u4-q-0a76ce41f953 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `af757a5e8ba973b901f14fb1993b5935d25d038a3ba31d38a936ae504789e122` | answered | `38f84ab365f635d3c12a2b1f286aeca23ac51a84e118f11a8fc6e5d9448147ad` | pass |
| c17u4-q-0bb13d8a6f20 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `138e4e872030bbbcf0561be67cf4229943368e43717945fe9472b3df1c6b3b89` | answered | `d6b5f373a902f722723d2d937ed0b22966077adf17ba3755b8fffe96503acd08` | pass |
| c17u4-q-0bb13d8a6f20 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c32fb8c6b689ee3bff21786f21b95dd3ace162468b04465738fd8880a028b115` | answered | `f9176aa99bf8458248bf3cf79b33ce934204d9b1cd4851962922ae9b36a7f20a` | pass |
| c17u4-q-0c94af37e815 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `64e9c1b7bd4d78e54541210f64ea3a94a10ef1592165832deaf65724029df559` | answered | `6f91e9ec5690c466c4c8ed7f24add5a417dd73b855929dae4b07da7993a5a50a` | pass |
| c17u4-q-0c94af37e815 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2a4aa1b1cf6e72a809b956ee49424d57a29530775a27d080d7ea54bd3d178e7e` | answered | `22048cb24da20711e1c79f708592bbd7835a9b4480a7501c3f8a03e5aedf5e0f` | pass |
| c17u4-q-0d31eab9627c | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1f97b358ef2edae18624cd0e8978170839ed1df66ba036eb7b210791a6b02c57` | answered | `09a4092de5d82a6dab3da4dd91a45b1eb51819b3533294294df7a0b7003f7c83` | pass |
| c17u4-q-0d31eab9627c | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ee637e12854f028d5cbe1fa624dc962057c0929791fd2b940acb55d1aba806d1` | answered | `65416e1a080f7184228b604ff1579ab2de0ba8b5392805b3c253ad3208980573` | pass |
| c17u4-q-0e8f147ac350 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `05cfe420186e6718633445f2bc5d5e4b7aaf53c030e9faf26fb9a15fedd07e56` | answered | `39aa320a088293b4aa6107d7331873196e0da86f0c70a7c3074afd62bd606350` | pass |
| c17u4-q-0e8f147ac350 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b0ec23b9092a796d4baee682e6dd7056209646ea3e2e4b3108d9ce1a33abf9ed` | answered | `7054cd83734c56c204ea9aae72d52bd4894015ab84e6dcf62281e64948079b53` | pass |
| c17u4-q-0f5ca7904bd1 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `284b509d33cde608f8356fb1d807732f06a7d8d66c0bda3b38f1caf400eabf23` | answered | `2f3411a96be58722ebce87345472c7734efda7e13f2c1961bf4ef7714cef1786` | pass |
| c17u4-q-0f5ca7904bd1 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `dcbbd48dc97246e67602104e30ad9ffdd7ee791ceb2b64a40954be5abebb307c` | answered | `8fa608cdcafc8cd0f2c7166f1a3b4f6557453b9d421a8dccc9ddcb111a4be587` | pass |
| c17u4-q-10e29b461f8a | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `082f1130da7cb3132266d6434c6c2332959cf685dffaa8ada5ed82bf5cf02398` | answered | `3d8b44b4b1b1d1061c7450cf7647bae84d282b58eb08534df149ba22fe3d9ac5` | pass |
| c17u4-q-10e29b461f8a | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e6e10ac0a45c9dbee2ef49399f35aa62a63410f541d7bf5275a877589fad38ae` | answered | `df92ac112c861d90eb55c72c3695bd24267c822601c047058cc6a17831de95cd` | pass |
| c17u4-q-117acd53e609 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `627558980f2d871e95c0eab26caec1d3e8a33a04cd7cea575929ecb1bf8b3bf7` | answered | `4be2997c66b5724b673b0f0188f4d2ac7e2515be53861b6e4e3dff55a565ddf3` | pass |
| c17u4-q-117acd53e609 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1dc97285c29eaaf736b54feb5901765894e12402c59969f2d160bc0847d980d9` | answered | `f393bcea351f34323b9710bc221f332e256dcaef51addc1d76d26dc8f30c112b` | pass |
| c17u4-q-12b084f26cd7 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ae94897804bdf0b748f42acdeb6247030778f56abffcac7e704772f180e6f4c2` | answered | `ac519ac74182b373a2822ae6311bb5a7912e10393fe7ace70a254025744815f2` | pass |
| c17u4-q-12b084f26cd7 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `025f69344d05a96f4d0c28da32bc29d7f7fad7702fac068f65f89c3863115fd9` | answered | `42a9a276244884e64c7c1b80add604bc9bd3535658f7e7919c39cff8b800355f` | pass |
| c17u4-q-13cf691a85e2 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b1df35fb015184700ccf7368dadd77de18d3e5737861cb31ff592654c7e94b44` | answered | `2487b74373ce7c0ee8fc55a1b7cbc97585fbef47a314e4d5a1d884f043f5dea3` | pass |
| c17u4-q-13cf691a85e2 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `63a5f265b10d54dedc73fe5fd4617e6ca66063b061eb0ff8963d5df85a1ca848` | answered | `4629eef6a126b4f61790a565ba49ede29ebe97bcf5e7b700217c248a578960e7` | pass |
| c17u4-q-1426dbe970af | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4b4ef5445621b34bac2abc2d9cda6116a697672c9544e3b864efbe50d8a227c5` | answered | `9d2de43221371a7bd0e900312a128ee67a564b20e7b52a4a9c741b8bcaaf4b53` | pass |
| c17u4-q-1426dbe970af | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `810eb88f780ee98635454ca3db4c9cbead8c4076c8d6e109025503f93118454b` | answered | `89e1d96981d0975c27ef4572db1fde734aba25383a8ce6c4a786296dbce23002` | pass |
| c17u4-q-159d30a24c61 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f43cadd1e4500750e5a61e39c67225ccb682ac2ba5392c781cc726f272d6133b` | answered | `3048239ec7eea16e4e8628b814edfaf1ae2c62ca4e400b7cbfec2362affb1c62` | pass |
| c17u4-q-159d30a24c61 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `41a7bb1143a2ee8103ab4fc050a4f6ba4624a70fc5d7b19e05bc58363b793289` | answered | `7f403caf9c15936ab429a8ca2e24b893b0fa03e690c7f31702cc8e96633ee31d` | pass |
| c17u4-q-16f047bc385d | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `298d62d96323fba9a3a1249d820d162c225b9e906b660c9a52cad908b3d32b80` | answered | `d45b6d87286338734c63550392d71884367147b9f3c95db048943a7628c669f4` | pass |
| c17u4-q-16f047bc385d | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `663546c66f8f91d7d1188f14ae3b193d4cba0eed7e8fcd5e3c899a2c4556c092` | answered | `6fce7789dc6611f099510c24ca82bc2e7d43ab3663a7f6db598cb5bc0504dcd9` | pass |
| c17u4-q-178ab4315fe0 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f9f0c4449a14e6997db68dd0ceeed2a0c4251cfd02599d23be0b8f544ef77e5f` | answered | `44ee4479597bd6b1afbfca52f3417faefcdd853409aefbc63dafa3fb3e8782fa` | fail |
| c17u4-q-178ab4315fe0 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `932060e9d2bd8f92c91ffa8de8c0425cc8f9d49d002335c1b6bbc17da6db4ead` | answered | `c66ccf115693a64b5980e22f639edaafd1858df0d850c14783702e6aa1adc3d7` | fail |
| c17u4-q-1851ce7a02d4 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f0576a1a34d8913538bf5149561d4d3c8fe3a50323212212517e0e400261ed5a` | answered | `75f628489314dec5870b707f8fe6fe77edff2a890000d19140bad6f385b2e17b` | pass |
| c17u4-q-1851ce7a02d4 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1fdb876328786240dd0c207bcaaa9fc23e8829814e0b115a196abe72f9e8a298` | answered | `d94b000169651fbb64ebfce002ed6a04f5fbff369022a3f60813f1f6338e4370` | pass |
| c17u4-q-19d60a8f3b27 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `83f9ea20dea4c75df35d71be722c1576639a3077cb070694d6b2c81a88beb86f` | answered | `4c6ef510111d33ef5ba936fd5561ee737fc939b12ffe37d0229ae59ee4b19927` | fail |
| c17u4-q-19d60a8f3b27 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7df2cf3b22e519d2f9faef74e07fb9e31edb44914eb46303cd3c2fbe1c851d56` | answered | `8e8fa5a8cf96663948dc3c4c8341c2d5ee5f052a300e2a5c8ec69873bacf920e` | fail |
| c17u4-q-1ac4936e71f8 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7539552e6b8f57e985d56047a23e2a49bc4e15e43f7059a963098ad6efa3fa79` | answered | `4969e7ddff620fb0d9eecdb56493f590a8b786e8387ed20626a4bb931ba2db80` | pass |
| c17u4-q-1ac4936e71f8 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f77ea70b1319e56a56d8723fe4736730d8dbab2bfcb8b3dfa6c50deb5c879a84` | answered | `54785b847ed77f938da5db605f49845288dba609aadddfb26b008aadb96b989b` | pass |
| c17u4-q-1b2fe805ac64 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `426ac03505faec8cf10cf465a836c29237c3e4788d06f82f4e96c33d15116dc1` | answered | `a547089e338441642a63b989bc4c6582d80b46e31f04dc8334f9d28a8bbbee3c` | fail |
| c17u4-q-1b2fe805ac64 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `95b6c56af4714235dbab9454e630c6da2be04a2decc3da7163a62a0f2293952b` | answered | `9adcdc92a5534199121aa0c6b77eb430eea5ea1f6a17e711d0cd9279bc8ec6de` | fail |
| c17u4-q-1ce7419b50d3 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `16eb528dd2385e2b4491a25a5af2144be01ee9a33e27e5dd85ad5f3594041dbc` | answered | `bd410c32b71067fe843ffcad518830a48d51751aac31642b85b15951600e0611` | pass |
| c17u4-q-1ce7419b50d3 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `105ef0dcaad5c73a6019990adc8e1e01cbf9288732383deef5821aa42d0ebe26` | answered | `66aec4c94be102e57a2b39dd4a83184baf34a5d74fc7d1a12419c4593ad981b9` | pass |
| c17u4-q-1d09bc627e45 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ce82c3662cd8571f88f134b86e733933389f95cfcbdaa0db28862f524539b66c` | answered | `2d4ac49c623597950770827fe4be67a766d008fbdaad3916ff678b9a28fa8717` | pass |
| c17u4-q-1d09bc627e45 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `051fe7705f87d9f3516fd9080f484433e2b14ab2290a848706f72b85c51e618c` | answered | `f9ec71ee36888f241b64fde5b2bea2cef71888774747b9453987cb68263d5130` | pass |
| c17u4-q-1e6a50f918cb | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b1301f2a745d8960942729c3cf1ad064665c92a27461a75ce9334c701c8140ea` | answered | `9bdb547f061e88699309bdb55a7714cc1b04d9004331539bce4e24344a040e84` | pass |
| c17u4-q-1e6a50f918cb | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d036d52aa1124209622fef86e57b7b6177d00ae34b61be451cb420cbf489537e` | answered | `1a73d1a0696e28b275ef6253e5c9ce7f826c42cfdc1954cdd085ecdff74f9484` | pass |
| c17u4-q-1f83d2ae0469 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `077a3a30b6ade2bb88dcb6032887578b22fa3975f0b90618b12152df757d512c` | answered | `e85e0d9715d5740958fcf6062982fe7e5d0ea3ebc864b386bf3d7c8c2fb7ae46` | pass |
| c17u4-q-1f83d2ae0469 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9a9fa3f34783c245e680bb2c888d12730491fe81ac4ab32e110a37b5d944e917` | answered | `ac1ccd362b35c534ea027489286b1df3b9b45ca61388db99b088c6d99be0da9b` | pass |
| c17u4-q-20b5794c31ea | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `6b32adb76c8961a785a93849ecda289a6b12d831981b843344ee350ab265a8b7` | answered | `c33ae13dc19235df5ff019aa6ddecdecfbc134cbc96898b3b9cde495186d3ced` | fail |
| c17u4-q-20b5794c31ea | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `40e48935c63227b366f753523b6a19907ef2cb325221bc1377c192d5d7f4e125` | answered | `dc667d54c8f0cb825203dc20c4fa8f53de0b4778f137c626628ce746faee7338` | fail |
| c17u4-q-21ed0468a792 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b5e862065a7d3fae66d49a1ab3c8162294fc430e7a27f5af803c724dd6c7ae13` | answered | `9e43312eb39e18784f25f891f5994af045c4dc93559296d8d57f362d1ee22123` | pass |
| c17u4-q-21ed0468a792 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b6b5b927b09e806f911badf61a1468e3fee6cff55efaf55cce28123bb8662233` | answered | `707731aa32acf4a246451031824874152c17db21ad51fbc9fd9b6061f32a88a3` | pass |
| c17u4-q-22a73c5f90bd | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `490f7ba35ff420de7c62d02fd44c82bf21cac57e208091457b01cf847ee1c9b7` | answered | `a92b26e9f45c678a2ef6461f818ea63d92e6f69c88d1bb79b4b97902b965b8b8` | fail |
| c17u4-q-22a73c5f90bd | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b5a5d8b6751b83f6e38ee49015e4f64bb765547827b24fcd61dce53c44f2fcbb` | answered | `8a2d27be19bb6992ae053c486499b0c5e94ca36b538bd46d5fb90e8887e08baa` | fail |
| c17u4-q-2349eb61d708 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `c9f8dba5d33d17ce086436da3ff51df92421baaaaea209af73e472896a3ad1a3` | answered | `4e664081adab4621177064d8ba392e746230b051a97a32bab54a7d080e106fff` | pass |
| c17u4-q-2349eb61d708 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1c52291579e2a9f170f9b82a59127c88a0e5c13f8d4673bc59050d77c7113062` | answered | `ac4caf119390c290f864996f6898cc7661e13ab9945212a235d04ba9f041d708` | pass |
| c17u4-q-24d0257ac4f1 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b7d8e51e5c138e7983abb1a11e6ea229c1044f67e321f19d26aba47bb049cb65` | answered | `0bee31795bb36ec0a46d9d22dba4ea814197d457236ea3c7a65a80d7dd8734a0` | fail |
| c17u4-q-24d0257ac4f1 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `32aff4954d60f926a3f47e0e57279426c91dd3b260afc0438f14c391a221a23d` | answered | `a6f1f3f39282fcc69aa080a92e67086cee1ceb69447b45086352e8ba13c69b0f` | fail |
| c17u4-q-25f37b91486c | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bf1d2e1afde0a8af5014f8966d56647f350b9149a36bfcc8d1d48534404690e4` | answered | `5f3e0583316f64cf45a587061d49b36d2141ecae2ebc79565bec63c3ae38dc2b` | pass |
| c17u4-q-25f37b91486c | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `31067cb5a563cc27bbfb194f414c7d5d7c5c7eb4b831990a16e9ec5b81693356` | answered | `2ce079f75382545147d86632958245825c0e84b2fc080354bde9158f3cda8916` | pass |
| c17u4-q-268c51e30ab9 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bab407434fce20b12fcb7eec66f300ad9bbe84a470ea256e2568d5c8cc701846` | answered | `002e3a5d459080d17b439f9f031c8b1d367b4a6c6c29f069e7fb92585e54e7d0` | pass |
| c17u4-q-268c51e30ab9 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9f13f4a8356589de7372fd8399fbac50869fa65fa35ea30ef25e455346e59220` | answered | `c871e52423a1825940c9cc6be52572c3950ddb303732d4b2e455a605f1050553` | pass |
| c17u4-q-27b9146fd352 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `23b6e692031345e2b4a08fe4a5e54e301c1ba8a5ec1e32c67b3e47a289a7e600` | answered | `d223089f725d8cc9b9540082a28449ac6df87c374807644e1158cf90c3c55be6` | pass |
| c17u4-q-27b9146fd352 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `aae665b57fe4c104059c8d788b49c2735e36fbe92254b21e55b0a0e36caebaf3` | answered | `1091943d07459ce70e21b0ba4c1dc5bdded2afae62f35b7875eb48a2e0e6078a` | pass |
| c17u4-q-281fc0a67e94 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7001225ae8921511660021efc26c3b44f5ab2e127f34d16d7d514c125affab7a` | answered | `2e2d91e83fc9cdf2c07bfeecf9d62416f03a64350e047ff411ba359808a9e2a8` | pass |
| c17u4-q-281fc0a67e94 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `19210a657eafba6d158ee9a2fea3368a570515f72ba65157a65b67ec3cbdb4e5` | answered | `20315da9fba67b59844100cc72724097cf14a5fcc815080916cccb0672328def` | pass |
| c17u4-q-2968ad35f1c0 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `505a78078ee56e815d4681f0a7a8de91347f0c3f6d90a34e39bb53ca68de1cac` | answered | `d4acc582bcb94cda57da75b48b5c19f7cabd6b636057e8f312a9e3ac515d2510` | pass |
| c17u4-q-2968ad35f1c0 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `5012b9bbe238324403b9d80c8b214722d3458a831ab95a42fa46d4c3570a3ea5` | answered | `8ddcfde2ea91eb24915891c3ff885b9bd711d6b725556adb460269e4c1cfc5c3` | pass |
| c17u4-q-2af503c8726e | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7c4ec2b70300427d8826ec6f743408ebf971b75e10a412c0a00ddf55f19b4d44` | answered | `08a19b8e04e31d50ae345da4c76394ee081c69367ffab11e686108c44d095861` | fail |
| c17u4-q-2af503c8726e | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `0debc95a28411fb2d5d81e8ddb22ebe334db32be8f3b9506c0908dfd3ddde45e` | answered | `51c28b27a5146291f073b9b4651c132986ffcea51fa6bc7b630aa9cc360e3463` | fail |
| c17u4-q-2b7ce1490ad3 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2ad0922bd0ac0d9b1203293ac4107098ff44d779bedbb22b7de598c106e478e3` | answered | `33e1bbece407d9a15bd168b65f22e84936561bb796dcd66d8e6df6eb7dff6385` | fail |
| c17u4-q-2b7ce1490ad3 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `57dde0a6b33247fc6f8d7b06db907196ca2e2963c00418ce84aabb4587e3ddae` | answered | `d547bb3b78102dc20fbfcb31c1de55623ac14f0bb076155799c651fcf5d25506` | fail |
| c17u4-q-2c04eb7d5638 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bbcf8d4cbb94239bf69384f274a80025ad010f6659c646f11cf00b1e26c63770` | answered | `51283657e288d618def6b29ee16552a2c73cd446ee4a43bf5830f38e9482c1f4` | fail |
| c17u4-q-2c04eb7d5638 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `175fe3a705fccd305ddb4e3cb3bec44c14c363ed63e04f17c60ce0bb6d8f0272` | answered | `467404e1c4aa0b4d015a223b18e5c888881e85c7c68b18e07b6df0a7341301b3` | fail |
| c17u4-q-2d913fa42bc0 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `01d8ef1ead30789e409b54105b48fb407cf79ce72110b593340d772e6c0af436` | answered | `e8553121b76507b68d8d639bfed319ef94f0c3f69dea00fb79e951cdb702c21b` | pass |
| c17u4-q-2d913fa42bc0 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `32bac65d9539cab67999f20dddb7837f004c031bfb8fa882e2c45ff8b0035591` | answered | `40525f0a15d6b4026919335bb953a2c9bb5c275ccd2714f1173c2a6af3fd66ae` | pass |
| c17u4-q-2e6ac50719f4 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `459fca785b7eeef4cefb3346d93a58ec5d877763c269663051b9babb57a00a6c` | answered | `b6e44dbeb5a500918079dff5f9353092c72b8a1fc71689c2673f6211f51c22f9` | pass |
| c17u4-q-2e6ac50719f4 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4dbfc6e6b05b424d663ec9ae45a0ce044a8ae56f2f44276dd03df64921160b50` | answered | `3eae0c53eb639c93b7aa5112f02487fa903e52fd12e34b470419adea4519d5fc` | pass |
| c17u4-q-2f108dcb753a | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e0262194d8565881d5993c9a59d8e90e251028f686788b020efeef81ed98e764` | answered | `ebeb32ee0af8ed61d39d567864ac2d5c624e8f8f0795731b83cb4d9f788a8a5d` | pass |
| c17u4-q-2f108dcb753a | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `244ee2c970a59f69c919b6810a73a63dbcb38ec660fcc90ae3e649cd7320ac54` | answered | `2c49e9431ba9f837179ba30f2219bc15bdbbe87b700fae2dc3ef551cee667024` | pass |
| c17u4-q-302bc1e8496d | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ee795cebf9a38a3b220672038b2214d4580660d68f992bc7ee24700e16ac7c36` | answered | `127fd8e112d2ddb43c087c3d56e0fc27cd6fefe5d1d551df2cb6da7a2301fb5c` | pass |
| c17u4-q-302bc1e8496d | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a20cce85a44f61e9859f21ce9293e06bce1a1d5a11353b34bfbdc7cd58f6eba4` | answered | `4a474c34eabdd0899f0dd1dd0c45ede641c6c8161870e2b87813df269fc627d5` | pass |
| c17u4-q-31e74a50d2b8 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d98c245bb4263f98b6140e94159352210e0d8e5575c4cd68668455796484297d` | answered | `39fadd538799de16f115c0b3fe171791f8a51ee87991080e4b7b1613b9296133` | pass |
| c17u4-q-31e74a50d2b8 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `bf885a0075960e7dc1bc4fae2bbd0c381bb4aa272fc814e493a9430958c7b500` | answered | `58a905f0113536475ae95d5c948df0890a3a01c4f52b5852134d2a9506245c12` | pass |
| c17u4-q-32a59f06ce43 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `eeffb7842d05398042c89db857619c3ba1bb3c68db8018a18766b9529ce25585` | answered | `015df5e28da000b1dc86cedfc3a53e6365185a8c677ea502ff071960df3ec938` | pass |
| c17u4-q-32a59f06ce43 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `59c2d39247cbb3038e22985db901522fc0898cfe44ab17c0d67df6073d047ad7` | answered | `521d52f124794860978bab510602d0ad9354b6dd993b424c43682d690a5cc71c` | pass |
| c17u4-q-33d14872b50f | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `20413d466fdadff0c2097c04fcc26b77e17d7044bdc9b16ba0aabe704504c302` | answered | `fcf10046637f90213b259dbf7a398d52ff81eea954b0d35dbe1145c4f82fca66` | pass |
| c17u4-q-33d14872b50f | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c315802815b719ef099b8e63c96a01f53629589fcfb1da6f6ac63cc31cbbc5a4` | answered | `e1a7fabb0836640ebdbcb58ab9f71d79a9212b37f1a61e8dfcda487342c5fc67` | pass |
| c17u4-q-34f80c3a927e | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4b1ec262921b04701971e879e237f0b2593a22b0bc0d237c174f9ebf59a0b838` | answered | `9dceffa9ecd18f7ffe06d06324a2babf89047aa31b3f57dcadcdb06559c08fa7` | pass |
| c17u4-q-34f80c3a927e | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1f2b252ae7b8fb1a2a602b2595fa3de55e350bdf2ab2fb126f289f40420617b5` | answered | `b8365deced45553289da5140d24c8ab7ba914c3c5f0672466644f9d490116c05` | pass |
| c17u4-q-359bd6140aec | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5486bc7eaf4a5dbcc038277cf9f2955d00efe7b4ae45d208b4741731b38921c2` | answered | `39e18f1b6279148f6801d3869b5eed5af06f3094f24b2e4cabe6df6a314cae39` | pass |
| c17u4-q-359bd6140aec | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c3988a9a6e0dd4a7e5b5dc91f26dcdde585b2f69c9c2e57967ed1efbf750bd81` | answered | `4292e3a045fec80dc7ca094c6de4441bf02904cb8ea210f3fb4c30acc3c55cc7` | pass |
| c17u4-q-36c025e93f71 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `76c2ba9c34b1a2b53ce1c862274c7e4dd29608429c31f4522df3279992e4bbc3` | answered | `aa73b9d13e5eb39a20567d69be1f6826488cf70a268fd3e9664a365bd32b04b9` | pass |
| c17u4-q-36c025e93f71 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6c3e824d7cddacce65c312b2f1464fcdfbedf11ab6c066ecf21ba7cd44853b57` | answered | `69ebab8838612fe458d6b5eacaa4e960a9bc88c01c6ef20c460876d17267b97f` | pass |
| c17u4-q-37a64f28de05 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0753f73b2eeae21720deab5ab713d24a9754243a508da1fef4697b202fa10cce` | answered | `47f6593be5948a23a1606b3e45db7884efe881de12cdc0b9589344154ae4223c` | fail |
| c17u4-q-37a64f28de05 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4f7af14133ef074da0639b08b4b3c7a786c041b06f2b6c85474c59ac962c7e17` | answered | `157b74a6e1bf17cb774dc3eb375bc894043a5566bbec13e2d66deea58a11d441` | fail |
| c17u4-q-38db105ec742 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `8c4426054f236dfd404518d299610bb847a310f7809a75952ade686e566dcdad` | answered | `92b7eddac540c04d45cf45c239470fc130aa028cf230fabb9f7af7aaa1ca614c` | pass |
| c17u4-q-38db105ec742 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2beabf964717a93d545abe90ef709c8a81146cc35e2e71e9f7fa96206fa2fd0b` | answered | `429f8b465ec0925316337f63e8f9f91ddfa8c82c05c96d01a8f56a4327c0f29b` | pass |
| c17u4-q-39e7a03d68b1 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d6c91702847affd679ac7868a8ce5ee0bfd5e63df9689d53bdf9a31a81143c88` | answered | `10878815fadcd33579b37d4874b542ccea4152ae33c8f7c85d407952b72c6991` | fail |
| c17u4-q-39e7a03d68b1 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6fd48fc5f16f758b3d62a66561a98e4e5a55ea2411b12c38a9485257d0780e98` | answered | `236a9e88565ae3fdd6be37840052bd8a5d6a4f74b78b02fe2d53ca7423a7749d` | fail |
| c17u4-q-3a519cf2740e | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e06d939179067c212beaee22129d0a4ef4f180fb1a532151dae30dea46ad280d` | answered | `567877813b6d66f03092660e8c8476002c9e76f7afb280756f78a86b302bb5d7` | fail |
| c17u4-q-3a519cf2740e | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `448737fbb97ce7164a92ba167e24dc15f53b7d37ad2f3b35ea584ac101370aba` | answered | `ab81ee64f4c084d86a1f4163a2007b3597eb278d3cfd204fb55ca89dd1eea4a5` | fail |
| c17u4-q-3b06ed8a951c | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0869ef44a098e4a65e81c8923ecf16dd8453307adb5135da316fa233e571631a` | answered | `4cbe267d09fe02717439446c80c11cd6c94bda5a0b4f5af1932ebce7bcc096fa` | pass |
| c17u4-q-3b06ed8a951c | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7396bfba65f63d4d3fb96b26ca9f278878b0f46832222aa059b1bf086bb1039d` | answered | `d5c462597878a0c2b0351d07596137a01f4d59b1b020874d674d564476303285` | pass |
| c17u4-q-3cd92471a6f0 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3980e376e6af907eb43aa4fa9123e47e49d74673d94de6385dcb9d3a4bc31fca` | answered | `28672a351b4fad2077556e96a340af91979380793268ab1c33137c4c16363509` | fail |
| c17u4-q-3cd92471a6f0 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ce406ef1ff3a8cc32663b63c2e58086f56042d6318f051a07a5fe08a6850f96d` | answered | `cec09f839d88a1d08a91ca2ba3d8c36ada56c9d923f8fd3712e29c331af87cae` | fail |
| c17u4-q-3d78a2b50ce4 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `473df2da25e78898ac7ab748228f5aa78e76bedac434d264f77d001067b20dea` | answered | `01fbd38ed49b3391e543ccfd28745c9c9ee2891a1b2b8e988d9528c0b36061fa` | fail |
| c17u4-q-3d78a2b50ce4 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `46af58f835dabc1c09d5d2bc2a5c3f1c6ce91fcbfa480f47a5fdcd9924bc0ca3` | answered | `996cbd0541297e13677be71b3d8c19fff2c5aa15614734cacbbabfeff0d9cae2` | fail |
| c17u4-q-3e14fc6972ab | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `159feed41371d0dace98e1dbc2593dafc5df56c4d9a9b7bf2e2d758df4bbfffc` | answered | `75576d9509a8eecc198c6a8c0968c21fa19d21868a3e6a10e2a9171eca7ce725` | fail |
| c17u4-q-3e14fc6972ab | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f4146e6a446a24207684c3d1ea8b1e7fd00ee1408d95c5c485c343efa645bcda` | answered | `c5cbe5ba2a9d357fcec26feb13861930c1fb110794296843cd29b6913bb6006f` | fail |
| c17u4-q-3f8b052cd169 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d44a2ec46ea707a741ebdd399b4bb69a9f2f92ef227fc0e89d6e8e61fa7c096d` | answered | `35a103db0b188d9f4181a16001810f063648a050536ab92b43a3eac3410531d6` | fail |
| c17u4-q-3f8b052cd169 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `237d10ff323399a2c8dba67d88b95be3d7d935fae5e553e73889840b8eac776f` | answered | `a9ae402a265aff96464c6ab378a010f73913a56579efdde8e2b0b2b85b9ba474` | fail |
| c17u4-q-40c63ea817f5 | v4-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7ffae396c15f1eb5873403a3a65dc9840090b36a7763d753d2482c69506f3fad` | answered | `0ba065fff5c4164cb0b1f3cf7e4c1415bce07d6ec728d322a5279b58e16371d5` | fail |
| c17u4-q-40c63ea817f5 | v4-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `70f1e4b8562f940512f1f058a9379baa50ea887c2e094806b9e05f610c3550d0` | answered | `5272908e7a01470d2495ce4359b2a64c4c195bd404ba3ea5f2a9feb75ea54a7e` | fail |

## Per-query outcomes

| query | stratum | outcome | reason |
|---|---|---|---|
| c17u4-q-01a41c9e72d0 | exact_identifier | pass | both primary grades passed |
| c17u4-q-0275d3a9b18f | exact_identifier | pass | both primary grades passed |
| c17u4-q-03c87e512aa4 | exact_identifier | pass | both primary grades passed |
| c17u4-q-045ef20bc931 | exact_identifier | pass | both primary grades passed |
| c17u4-q-05b91a64d7e2 | exact_identifier | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| c17u4-q-061de8f34ab7 | exact_identifier | pass | both primary grades passed |
| c17u4-q-07643fb908ce | exact_identifier | pass | both primary grades passed |
| c17u4-q-08f2ad751c63 | exact_identifier | pass | both primary grades passed |
| c17u4-q-09d5802be146 | exact_identifier | pass | both primary grades passed |
| c17u4-q-0a76ce41f953 | exact_identifier | pass | both primary grades passed |
| c17u4-q-0bb13d8a6f20 | exact_identifier | pass | both primary grades passed |
| c17u4-q-0c94af37e815 | exact_path | pass | both primary grades passed |
| c17u4-q-0d31eab9627c | exact_path | pass | both primary grades passed |
| c17u4-q-0e8f147ac350 | exact_path | pass | both primary grades passed |
| c17u4-q-0f5ca7904bd1 | exact_path | pass | both primary grades passed |
| c17u4-q-10e29b461f8a | exact_path | pass | both primary grades passed |
| c17u4-q-117acd53e609 | exact_path | pass | both primary grades passed |
| c17u4-q-12b084f26cd7 | exact_path | pass | both primary grades passed |
| c17u4-q-13cf691a85e2 | exact_path | pass | both primary grades passed |
| c17u4-q-1426dbe970af | exact_path | pass | both primary grades passed |
| c17u4-q-159d30a24c61 | exact_path | pass | both primary grades passed |
| c17u4-q-16f047bc385d | exact_path | pass | both primary grades passed |
| c17u4-q-178ab4315fe0 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-1851ce7a02d4 | nl_behaviour | pass | both primary grades passed |
| c17u4-q-19d60a8f3b27 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-1ac4936e71f8 | nl_behaviour | pass | both primary grades passed |
| c17u4-q-1b2fe805ac64 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-1ce7419b50d3 | nl_behaviour | pass | both primary grades passed |
| c17u4-q-1d09bc627e45 | nl_behaviour | pass | both primary grades passed |
| c17u4-q-1e6a50f918cb | nl_behaviour | pass | both primary grades passed |
| c17u4-q-1f83d2ae0469 | nl_behaviour | pass | both primary grades passed |
| c17u4-q-20b5794c31ea | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-21ed0468a792 | nl_behaviour | pass | both primary grades passed |
| c17u4-q-22a73c5f90bd | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-2349eb61d708 | architecture_flow | pass | both primary grades passed |
| c17u4-q-24d0257ac4f1 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-25f37b91486c | architecture_flow | pass | both primary grades passed |
| c17u4-q-268c51e30ab9 | architecture_flow | pass | both primary grades passed |
| c17u4-q-27b9146fd352 | architecture_flow | pass | both primary grades passed |
| c17u4-q-281fc0a67e94 | architecture_flow | pass | both primary grades passed |
| c17u4-q-2968ad35f1c0 | architecture_flow | pass | both primary grades passed |
| c17u4-q-2af503c8726e | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-2b7ce1490ad3 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-2c04eb7d5638 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-2d913fa42bc0 | config_docs | pass | both primary grades passed |
| c17u4-q-2e6ac50719f4 | config_docs | pass | both primary grades passed |
| c17u4-q-2f108dcb753a | config_docs | pass | both primary grades passed |
| c17u4-q-302bc1e8496d | config_docs | pass | both primary grades passed |
| c17u4-q-31e74a50d2b8 | config_docs | pass | both primary grades passed |
| c17u4-q-32a59f06ce43 | config_docs | pass | both primary grades passed |
| c17u4-q-33d14872b50f | config_docs | pass | both primary grades passed |
| c17u4-q-34f80c3a927e | config_docs | pass | both primary grades passed |
| c17u4-q-359bd6140aec | config_docs | pass | both primary grades passed |
| c17u4-q-36c025e93f71 | config_docs | pass | both primary grades passed |
| c17u4-q-37a64f28de05 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-38db105ec742 | ambiguous | pass | both primary grades passed |
| c17u4-q-39e7a03d68b1 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-3a519cf2740e | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-3b06ed8a951c | ambiguous | pass | both primary grades passed |
| c17u4-q-3cd92471a6f0 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-3d78a2b50ce4 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-3e14fc6972ab | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-3f8b052cd169 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| c17u4-q-40c63ea817f5 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |

## No override

There is no flag, environment variable, configuration key or report field that lowers `k`,
waives a query, excludes a query from `N`, retries a graded response or forces a pass. A
missing, empty or refused response is a failure for that rater and a failure for its query,
and is not adjudicated, re-requested or replaced. A pass count below `k` records
`RELEASE: NO` and exits non-zero.
