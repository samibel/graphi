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
| observed pass count (as reviewed) | 38 |
| observed incidence | 38/64 (resolution 1/64) |
| observed pass rate | 59.4% |
| exact Clopper-Pearson 95% interval | [0.463676877707, 0.714852987189] |
| lower bound clears the 3/4 floor | false |
| primary disagreements | 4/64 |
| adjudications | 4 |
| missing, empty or refused primary responses | 0 |
| **RELEASE** | **NO** |

- the reviewed pass count is 38 of 64, below the pre-registered k=56 (reviewed 38, corrected 38, and the release is decided on the smaller); there is no override, exception or waiver

## Capture binding

The rated bytes are bound to the candidate implementation and the indexed checkout this run
names: candidate `8b80523a10e281dd9edaf36f45c0cc62dfccfa34`, checkout `a0a6ae020bb3899ff0276067863e50523f897370`, both worktrees clean at capture.

## How `k` was derived, before any response was opened

`k` is the smallest integer in `[0, N]` whose two-sided exact Clopper-Pearson 95% lower bound is
at least `3/4`. It was derived by code from `N`, not written into this document.

- `N` = 64, read from the sealed dataset (`a96cfa7d002dad127ff0a1fe7bdffed2515615c7e4cd14fb66dba45fffcf0190`): count of answerable holdout queries in the sealed dataset: split=holdout, stratum!=no_hit, at least one grade-3 span
- `k` = 56, whose lower bound is 0.768473694033
- `k-1` = 55, whose lower bound is 0.749763164375 — below the floor, which is what fixes `k`
- method: two-sided exact Clopper-Pearson binomial interval; the floor comparison is exact rational arithmetic on the upper tail at p = 3/4, and rendered endpoints are bisection brackets of width 2^-64
- pre-registered at 2026-09-15T08:22:59Z, naming precondition record `e247d584b8dcaf15ec11c00281789060da8fc1f0fa40a7af127b2892ba2b85b4` at commit `8b80523a10e281dd9edaf36f45c0cc62dfccfa34`

## Per-stratum counts

Counts are authoritative; each stratum states its own `1/n` resolution.

| stratum | passed/total | resolution |
|---|---|---|
| ambiguous | 1/10 | 1/10 |
| architecture_flow | 2/11 | 1/11 |
| config_docs | 7/10 | 1/10 |
| exact_identifier | 11/11 | 1/11 |
| exact_path | 6/11 | 1/11 |
| nl_behaviour | 11/11 | 1/11 |

## Participants

| role | id | provider | model | took part in this track | basis |
|---|---|---|---|---|---|
| primary | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Stable third-run primary-A configuration: concrete non-alias model gpt-6-astra, reasoning high, one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item in an empty directory with the single prompt file as its only input. It did not implement, tune or measure the candidate and did not curate any dataset. The same model configuration served as a primary rater in the first and second fresh holdouts; each item is a new process with no memory of those runs. OpenAI exposes no immutable backend snapshot/build digest. |
| primary | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Stable third-run primary-B configuration: concrete non-alias model gpt-5.6-sol, reasoning high, one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item in an empty directory with the single prompt file as its only input. It did not implement, tune or measure the candidate and did not curate any dataset. The same model configuration served as a primary rater in the first and second fresh holdouts; each item is a new process with no memory of those runs. OpenAI exposes no immutable backend snapshot/build digest. |
| grader | third-grader--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Stable third-run grader configuration: concrete non-alias model gpt-6-astra, reasoning high, one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per grader packet in an empty directory. It did not implement, tune or measure the candidate, did not curate any dataset, and does not serve as a primary in this run. The same model configuration graded the first and second fresh holdouts; each packet is a new process with no memory of those runs. OpenAI exposes no immutable backend snapshot/build digest. |
| adjudicator | third-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Stable third-run adjudicator configuration: concrete non-alias model gpt-5.6-sol, reasoning high, one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per disagreement in an empty directory. It did not implement, tune or measure the candidate, did not curate any dataset, and does not serve as a primary in this run. It receives no primary response or grade before its own answer is sealed. The same model configuration adjudicated the first and second fresh holdouts; each item is a new process with no memory of those runs. OpenAI exposes no immutable backend snapshot/build digest. |

## Adjudicated queries

| query | primary outcomes | adjudicator | final |
|---|---|---|---|
| fhv6c-b7e4-q011 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | pass | pass |
| fhv6c-b7e4-q014 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | fail | fail |
| fhv6c-b7e4-q043 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | pass | pass |
| fhv6c-b7e4-q044 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | fail | fail |

## Frozen inputs and the end-of-run comparison

| role | path | frozen sha256 | observed sha256 | matches |
|---|---|---|---|---|
| dataset | `docs/eval/retrieval/runs/2026-09-15-product-compact-v14-third-fresh-sealed-holdout/sealed-dataset.json` | `a96cfa7d002dad127ff0a1fe7bdffed2515615c7e4cd14fb66dba45fffcf0190` | `a96cfa7d002dad127ff0a1fe7bdffed2515615c7e4cd14fb66dba45fffcf0190` | true |
| budgets | `docs/eval/retrieval-budgets.json` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | true |
| targets | `docs/eval/retrieval-targets.json` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | true |
| grading_rubric | `docs/eval/retrieval/runs/2026-09-15-product-compact-v14-third-fresh-sealed-holdout/grading-rubric.md` | `f132ee59f254d57590542eb0ea9d769ecab7062d79540dfcbcf8707c68195918` | `f132ee59f254d57590542eb0ea9d769ecab7062d79540dfcbcf8707c68195918` | true |
| methodology | `docs/eval/retrieval/methodology-v2.md` | `47fae9f7c4df86d41eae6ae2f12aeb8be5019fe30ac0398fd80d05100fa7212e` | `47fae9f7c4df86d41eae6ae2f12aeb8be5019fe30ac0398fd80d05100fa7212e` | true |

Compared at 2026-09-15T08:37:10Z. All match: true.

## Capture provenance

- capture instrument: `sw280-candidate-mcp-capture/4`
- transport: MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)
- payload boundary: `mcp_jsonrpc_response_bytes`
- candidate: `task_context/2-compact/14` at a 1200-token budget
- repository: cobra at `a0a6ae020bb3899ff0276067863e50523f897370`
- embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (model `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b`, index `147:static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b
40:e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
64:75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c
64:107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45
3:256
2:v3
0:
32:470651ea0c5848902cebf14f704bd942`, generation `g-110b70a88a6d33c2`, 768 persisted vectors, state ready)
- tokenizer: `tiktoken:cl100k_base:ordinary` (vocabulary `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`)

## Content addresses

| query | query text sha256 | bundle sha256 | bundle bytes |
|---|---|---|---|
| fhv6c-b7e4-q001 | `224ef6274d03c46c54755799e7347cd1be4a4faffc6de08df4bdf101026abfa8` | `1536221d0457d34203a01b78156292245988f9ae38dd59e0c0168a0b7fa4a012` | 3472 |
| fhv6c-b7e4-q002 | `dd06c8aa0e31273b3aba79d1f0ea03093153ecddc2c462d8011e8bb621b750ed` | `41bf18966721f25bafd650ecee6242173a45ce360ecc59a7b48ebb422975e7d6` | 3583 |
| fhv6c-b7e4-q003 | `49ba3031a04c537a4e0cd0ae9b41b82d9c3c22f8f799593c534607a9422b930d` | `516b9d63affbb414aa2089072f892c88162e3e366183baf77f884e07ec4e5471` | 3934 |
| fhv6c-b7e4-q004 | `4d3d12fd67629979116fd14de0f7c3a4082db42efc40562f2ca1a32c41304bbe` | `8775388eee59d3035bcbf9f90796a17e4b2015524a49d5d934543bdb700c7f4c` | 2051 |
| fhv6c-b7e4-q005 | `7431a0c832b242d2aa0e28d748b787991407a864fe257169388454c6d40f1b7b` | `c6662235ffd62b596e70e5c137aed976670ded05f7af5b9329759517d66f86fe` | 2062 |
| fhv6c-b7e4-q006 | `9dc76070df905fd563f438f21fb22122d9a367d45bbf3dd1595bcea8e44d2f04` | `ebad8e5cb83437884f7983f84e0b20e4691723dd8be5729c63ab69225877f289` | 2055 |
| fhv6c-b7e4-q007 | `ab6d1e573e946713d075d6f937820ecbc5cdd6f35d735095f2bc40c604155065` | `12124b9ee2c5a613b87c2902bb7a8789e861de71813b3d68d4231507f6df6176` | 2048 |
| fhv6c-b7e4-q008 | `e8569c229d05315971bd0ce3f03bf260bc62c59576418dedbec1e4f336715fe5` | `59b92c58e28bc398b862148b1bcbd640497ecce4c3fc99fff396d0289cedbfd8` | 2063 |
| fhv6c-b7e4-q009 | `2a648cab5c71bba2a317bd9e210e6feb2735a48a18f131e11d33c495aec26374` | `6739ea273f051195efcbb7c627a291947cb45efe475db6cea60ceb330d6fda5f` | 3408 |
| fhv6c-b7e4-q010 | `e157588bfbbde13654d37ca8c45879082b7246569ece543ad00ddaf8cc1df802` | `9129e4d43ad4c024e07a90f5e7601b9864b78058c1e1f46159d327251dcdf944` | 3550 |
| fhv6c-b7e4-q011 | `5c2582cd27a283fefe4c19ba0573272606d4da04c7c7190991e1edcb458fe05a` | `4a9d694ac8a073bb30b02fc16fe86e6cf39315a98c48e2e68e67b19f7f203dcd` | 3389 |
| fhv6c-b7e4-q012 | `6737ad498a6769f6222ff01983eaf730117b98ce7d4267706f0ebdc91f3046cc` | `4b0e2ba9c23cba2c5a2a17fcd32084e6634c711ca9613e6346dedaf5f865d157` | 1523 |
| fhv6c-b7e4-q013 | `a48034865676903aed3da7be846f56efab79615c941b4b0089d0eb17584d391a` | `d9736ed5a72f12b40438af7e559197e897f833f56b27d12024218ef9bfb04ba3` | 3652 |
| fhv6c-b7e4-q014 | `6ce055cd4b826efcda65e783804ba1fdee53aec5935191cf9e9a99a66b9827a6` | `dc10df1804f59a96119e955af0e8b709696d168c1c9d8e7c6a2df46ebeb57a1a` | 1237 |
| fhv6c-b7e4-q015 | `3152f251ff3a40e61508219a9fc3aa4b4ffd4df61b8e003baedf5b2b2443992d` | `bd72571d189b771bc034c25796b308a02243753e06286ca68e30826bd0ec4477` | 3541 |
| fhv6c-b7e4-q016 | `4c08781a1c6c69898cdd3a21c0c759d846fc32148e6b5aaf70ad2db146e9f145` | `64999bfcad4b3121c29f481effc1a7c3834ec1b0e08196898a9f3e4c5019984b` | 3607 |
| fhv6c-b7e4-q017 | `a48adb6340d8ba7edd34d788b0ae96063b01168ff3bcb9f37fa413d7bf6125a5` | `93325f34a1481a6dd363b58d29b1da8433cd184771d56939ab81bedce4bee064` | 1490 |
| fhv6c-b7e4-q018 | `00c5597ff0b5e85ba33b2f4214ea239846e4ad19c29bdf24f6e6b574c0c3f647` | `b303f641dfb98960c5afd1d111195fb52d097cbaa87c26456b4fb631b8685ef3` | 3971 |
| fhv6c-b7e4-q019 | `5acfae2dd73d93a849b53f0ec7861f67962b4c43d6dafb8b99ac3c8e53435f5f` | `ed1ae7b5f8043edc5f8db6ba4dc3bcc0d4f6372766022e5a06939b3a5c1d75db` | 3594 |
| fhv6c-b7e4-q020 | `1574ac3150d15b5bd829d672b0664c69c418b0966d1e0f8ca3c2a4548b644d57` | `cffc3548931110f05af513beccddcff3a9fd15b3842fbb25200a876515836195` | 3135 |
| fhv6c-b7e4-q021 | `1f46257b0dc56a885f70e2e14dc04b71d4a50666c96d6539780780313a77c04a` | `ad40c79494a73b109bdbfe71ee06ec7210a54020690581f10179dde597e3a3fa` | 1238 |
| fhv6c-b7e4-q022 | `365ef0af29a84d4fd004ba94e379c420a4b5822c6ac6ae66f7aeb6b5904aa691` | `a56d64425baebfc4e6624c7c9ac4e2ce6ebce183168c4ca6b9068d22273a6d6e` | 1219 |
| fhv6c-b7e4-q023 | `5a9dfbbec93e2b9766e59f2e083622bfd698c374bf2f3b92642091794e21b48c` | `f80c7cad99fcf481b6c8e0c05daa821ffecf54a08bb3711016e3f74a1564015b` | 2912 |
| fhv6c-b7e4-q024 | `53e69a41ca3a0bc723700cdc80b9f49396848280457a273cb1f1c2549a79e04a` | `2d80de156b714c2e1099e9d09a9a0bccd298b634c66b07cc2254ba6d0acedc38` | 3522 |
| fhv6c-b7e4-q025 | `06043169bd59909fdc03044b8bed8e24439ec5129bf7c288864b09a8a9ea5ebc` | `a8e8295cd2462114537df04c4c013c5b7d5f5ac2927ff0a453c4d74ac1eaf6e2` | 2449 |
| fhv6c-b7e4-q026 | `578a3ff26a571aa1ac9df68a69e342ead3d22afec5ce016d1fc1deae7ec46b18` | `9861bdb8f2a31517dd8604f743933f571f73d54919133b4ab1894df859bdf199` | 2786 |
| fhv6c-b7e4-q027 | `e4fc0049fa70111723dea188500ef098f80b98e4b757fa1a35eb0a7d35618c9f` | `60b6309b0f7eb1b508b12e7198626afd2320cf99e0cacd991bf26a5b051f4868` | 2589 |
| fhv6c-b7e4-q028 | `32e45716088fc22064179f49665e5b6b8a3b35dc90c9b74153dd6fc59716a424` | `9308d9ce912c03b72693feeefcf4e80f20ad6e359b4817f8bd678663eb936ddc` | 4105 |
| fhv6c-b7e4-q029 | `6ae35c6ea3cc0cbc71dff277cea0d0d12439a3108e394ee4a00ebb6172904a54` | `10707a5f3ff35ce521c5a7abc07d1309b5faa666710f9fd6c33e1bae23ffbd34` | 3500 |
| fhv6c-b7e4-q030 | `497a8ac850d4ffbbddb9b5499ce367ce1a184c194812130266ee89bbd902efeb` | `9fa4d2a939d1a3e9cacce179afa87a8c336c132f897b2c61739a00023f57b51f` | 3026 |
| fhv6c-b7e4-q031 | `e9c95e51e6ba150f188bc61a0a7a53af15e922b549e08725d6111bc239c5baca` | `d0b058ada220b2347ee6b1708738dce9ac488c7f9ec6980bbe9e53cb0ca6cc5e` | 2185 |
| fhv6c-b7e4-q032 | `ac437d114d5b135bfe1fb370f5f404961351bfb464ecbb68e5d456de2a2b3e60` | `a8a545e0834402cbc581285494d1913aa69ace19989e2b499f3a3e0ce7cd8e03` | 2118 |
| fhv6c-b7e4-q033 | `2c53b1ea5dbab9f8cd08cffc8443ca236439d46b4bf190453a281d8d0f54fa4b` | `ec5eb39962965926b35a053be6119b0a0bbaab4a20f716ef63d74bd9a4c306dd` | 3306 |
| fhv6c-b7e4-q034 | `7a3a420ff34e7305fb0fd3c4a6e123f065c573f5c25089ad201f5b59df1b648f` | `0716ede2634a61bd80f20378ec817b6322bd9e7dfe4e4ac55571d978b8feebb3` | 2956 |
| fhv6c-b7e4-q035 | `21b98d7e94c5a98b4be87c88ac455d089ccd89602448c2744f81fa41109e5b49` | `9372892a09ef3d44f1ea5a1e2807b91f8d94f1f681b4b5b6b0e53bec799c18e8` | 3905 |
| fhv6c-b7e4-q036 | `bc94f4ae7860b579f370f6a67f66ba0a97ba15e17c79adcee3ac2eaaefb43665` | `81d99f8e40c5dd863f60dd7d392e932552cab4f3d7adb8bcc1b628479e134579` | 4037 |
| fhv6c-b7e4-q037 | `7301a3bcaf3cfa9646711bfc149dc6b80119b9cefed9784afaf3eb3de9987318` | `3a27a19ebe4affe2849a6c28579010f0c3b763676394bc1c269425d244306524` | 3933 |
| fhv6c-b7e4-q038 | `70847452476574addef9790c67b2cf87a8783adb2ed22bd61626797cf7d29feb` | `1b20cf91265ed5e5805e498eacf95d136ea70c39812d819efa6e3fe8c7630368` | 3501 |
| fhv6c-b7e4-q039 | `acdc86bbedfc92b7686d1ddf8fbc4e6daa06646f7d7624c2095f925298ec6945` | `269b76e48751b5e23ad846ed4d145aadc1fd7c137a9c31b478c3c55bce08ee20` | 3727 |
| fhv6c-b7e4-q040 | `b07d715839d9b6a7b4e1ddeae2e0f0426f69ce62ae725d92d5c9ce3299eeab40` | `96cc9839a71fa39220ab1162de1e2e7107ef0b351c428eb8c9df9e54d3e327d9` | 3889 |
| fhv6c-b7e4-q041 | `c1e1be5e41f70fad2ee5dca70d5b2eab3b8b880aee00ba3335d38b2093034d0b` | `7eb9c91a568e6b4c7e2f3f3e2e9380827dc6c328c512705dc359b17eea357d36` | 3904 |
| fhv6c-b7e4-q042 | `2d8332762c3d7595f28e457f2b619199a76c74fe9cc317b62c7990c0510d609f` | `c6c40d13166734485b4485e0be2c7582c95c0f0736506821a7a1060aff769e94` | 3834 |
| fhv6c-b7e4-q043 | `f16b7758e799321e97a5de626fdcda2eb543db5a340fabf28b76a2010800be1f` | `17f69bbb573b1e4838a5a18b4b53b0c5df8e19746d18f3cf51d57113e72b7599` | 3513 |
| fhv6c-b7e4-q044 | `a0d408aa201daac68e5e782565a20392b485e79d4b9ef5f7912a5d25125fd6ee` | `b08c1137c40fe10759e695097b355144e2ba5191a0fc0dc505de5d618e9cc66e` | 3517 |
| fhv6c-b7e4-q045 | `88e096afe86eebda80c0969800eb8e6ac19751d8c59edad71675cdb3060b0c01` | `0fe5e0242f642e836d432b566d348549d28bc0bb873f9ed612559288d03588a8` | 3419 |
| fhv6c-b7e4-q046 | `2b26bee768b773f5e4ab797b15cbc062c188699ab0e768112ec2aa6c85ac7587` | `1b5a1ae84bb133716363166c96bdd870c31bfbfc4ee250b5e9408d2862875485` | 3528 |
| fhv6c-b7e4-q047 | `a9a85f83a0522bafb5142e357d53c355c90851eaecc51935f2ac4cdaf0d8652f` | `b4130cf47108b74f04f6425826cefae8a0066ed82017152402148e7efc1c556e` | 3404 |
| fhv6c-b7e4-q048 | `70f3f395e841fd9d27691387f57b2988266f8e924c7b3304dd7ae7081c0a0d73` | `6b5d3ff92abcd6ff4e643bfa611d09d6e7cf6ee0f8bb0ec7ae65ab6cb3befe46` | 3878 |
| fhv6c-b7e4-q049 | `868f2f2935044e857981f32580209d3b3151014ea313535ea797e7fe854c639f` | `8750f645716d083639519eb5f1a859bb5bcdf35c121a3bdcf1c7de4ea9f4b87d` | 4120 |
| fhv6c-b7e4-q050 | `3a38f92216efbbdfa3057517741578aabd05279a14a835d3686ac810a95b283b` | `a01fbf90b85aee4845269936eef074c3fa2c2bb04f60490462d79c899e9f7022` | 4220 |
| fhv6c-b7e4-q051 | `d4000b6d39bec4f735492c084645aebde598f0c41bfcb420b1b5ceabe7ac1621` | `8dfde16310c3f75d241d9b3f8b998fb35c33014b78bd39a1c1327cafdc173321` | 3811 |
| fhv6c-b7e4-q052 | `ac3e21f0b0cbe00ecdda6bc43c6ba330ca80bf7a14bc6a5ce028fb5d333c1fc4` | `51747060b3aefec49394b83950f18a39092b7d6bc2f9a554225386cf79963930` | 3863 |
| fhv6c-b7e4-q053 | `544ab1d03740f47a1d3a18a6b80075756718dfefdd922625eadc6809e35541c3` | `0202048ada59d8982a0b5d783501d0a233b5ef123d1be8372ff78bee0e57fd04` | 3121 |
| fhv6c-b7e4-q054 | `d9d8ffb674ada0ddb7c8bddbd5e58e72c1f87da57ec3a67a3ddbc895f19d4bc7` | `270fab08beab0f1aba11a0f637b6abb888308c5eaf366beb7c27c20b86a4889d` | 3722 |
| fhv6c-b7e4-q055 | `5d1ccf8dc2c0534c760fe9ae11cdd723c7a0ea44f2eac9f0c299aa8ebb12da04` | `5f90a2c68bb4d69ead27bda6af63344ec14f11d33d69c1f0508f79f3c1ecf1c6` | 3384 |
| fhv6c-b7e4-q056 | `a5b62118935fda4850ea6e872ff3013412de31537b230f21438dd73b69c0ab63` | `dd9c10f3aca5daf5796c919dbe259b030c42fbac4281361642415dd9415af458` | 2817 |
| fhv6c-b7e4-q057 | `0467fcd5df3692a9c7d0536313ed4c854bc6358a2a847692cb2d0d2b83e529e0` | `e97f81c50f678dbf4c21b7967e3f60778e7262e8821506e4ceae9f5b454326f7` | 2527 |
| fhv6c-b7e4-q058 | `f736be5bdeada5f42085fbf14464aed02745975878fe52658c8dec7934540109` | `720c524115d150873a8b0ffdc8bb6c567b16c8ab14338ba1cf499422de87cb13` | 2009 |
| fhv6c-b7e4-q059 | `62626df7b8eaafb4d69b7ee4e0a2521c1bee45acb16112c70a6cbd5f2643b7f4` | `a48770d242defda51bd257e178a3c2a59e5b4a22499fc553d40ee80a19221002` | 3897 |
| fhv6c-b7e4-q060 | `2d0a7ea8dbf92cab366fe20c1b6e4c52a0fe2db920648ede96587dee258d0b20` | `24fbd546f755c2b3f99bdb743e24eb3904a162b810664a01bfe378b0b64c9785` | 3456 |
| fhv6c-b7e4-q061 | `a9f4dbe673610a7975795e93eef17a0ae558f2955d4ccff135110b23bd1ac5b5` | `d6685aa76611442bbf9cdb4ddc537196bd242d611ad03b0a891f46644fe7fb92` | 2604 |
| fhv6c-b7e4-q062 | `063375b0f8c3fc803c7adf38adc0488e0c1da395c22cd6dc549aa477a1a4af0b` | `455aa7262e69f9d6f7e6cf6116fd4848ac74e94ccb41dc40691e1d8dd0e6fca7` | 3763 |
| fhv6c-b7e4-q063 | `dad823b6d59441484e9ec68d6731f9a6b4df672beb38537921d88f7aa44ab6d7` | `89fe167f692279b552536e0f526ea688185aef5f20f92f7e3e24467728a4ae91` | 3563 |
| fhv6c-b7e4-q064 | `de65b73438541c5f4ac7c84ebab4ef034352a6f00938f18074ddf418855287ee` | `0685147eb0774d474ab41be8f5b086d7a13ff1e7720ba59f431f6c10a86abd68` | 3553 |

| query | rater | response sha256 | status | grade sha256 | outcome |
|---|---|---|---|---|---|
| fhv6c-b7e4-q001 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bae8276fb067f3dde1804e6c674d710b7dfe0e590b5881c5ca755a2049721781` | answered | `174e83666431e79667eb2589bd66263e53f11f3b8cf0ab6f4e2e0448a2bc1752` | pass |
| fhv6c-b7e4-q001 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f550d1de0247dab36e9013124777fac2fe3c7d5eae7e1c72665d654fc49f7745` | answered | `22cf3ed4f371449becf2defbd2beaf0065a9c8d0967e0d17bbc70a4f46c54783` | pass |
| fhv6c-b7e4-q002 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3cb87f05495f45b0e519cf7d7e2f93f5025facb58004e015911dcd0af475eea4` | answered | `5b682bf5f0901c9b0e51b47b9127031c067d6b5d71c12aa4e1c4c17977d770ad` | pass |
| fhv6c-b7e4-q002 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2be2f325b8f05131537468be6a119744a79ea2b91167b4d374775228266cc8ba` | answered | `82b3376642d5d1702b7201f055a34d0256fafe0a82378499d0001300276b7be8` | pass |
| fhv6c-b7e4-q003 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `33521018aaf5e475db810aa63f4ac82d782cc6c23960b06fcb091bf5cc6db904` | answered | `25af4f2794d109406e226e2da7bf160fae19e8e6a86b110991ea8d8753bb7b1d` | pass |
| fhv6c-b7e4-q003 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `fcbde7534bd52d7c52499fc83d58a55dfd2b356ce21e9707a19777640ff42cfa` | answered | `812cbb416dd7a2672a16ff6e73f2c07f93968f2ef7daf8bf0701b882afe83fa4` | pass |
| fhv6c-b7e4-q004 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1c4bdde4862771df5f120a0f0568429f80ffb1e22a0b3e0662f88b1135839c4e` | answered | `bc9c50249cb68c805fad1f12e9321523061789aa891c502b1c0d5a1d258908ca` | pass |
| fhv6c-b7e4-q004 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4b5c5eac2ad3a3937208d995ea18766c23915d9361b85d3db53744cc558d6cda` | answered | `56992ff5489a13fa0e0717cbdc075f91b56c2684bdb1cc80231a0d7b84bda1be` | pass |
| fhv6c-b7e4-q005 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3fd03ee9f85c5a97c9893d3d5b6c2dd28d5e0ae5c037b76b1fd42cb317c2ce59` | answered | `e50626e5dd8378ad0b60956b912372061a1d738fd2880773381bc6b03610dc7b` | pass |
| fhv6c-b7e4-q005 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f8d45fa437114ace1826d5e5918690f0ddc965814abfaaa56e6c4c6988deff96` | answered | `3605e54a5d3d7cd4470e86a6abd9fbbd24effcb37999c58cc7353d74d8fb81b2` | pass |
| fhv6c-b7e4-q006 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2c78ee34e39d1e1d4ae1584ccbf85806bda101b8037e1e1613a91ef16012faad` | answered | `6751b8e286bde3b09099b5239d7a236fc919e6dd1db661cf2650c424ee8df01f` | pass |
| fhv6c-b7e4-q006 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `0be35ec9683c2d8fc0a1c4bfdedaf62b51ea8c307193128f427fae6fff20a03d` | answered | `824ce11f7c83c0e6856c741c3ea92c257e510d9f4d122306996593a3ea2ddfc0` | pass |
| fhv6c-b7e4-q007 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2d77b17ae37746b14baf32d2feaa4b7dead91d17abb0d3a4f1df8b471f1b7747` | answered | `90884fd5867230c18ad7bed20a936ca4bab3203794c6d759321fb791dbbeba24` | pass |
| fhv6c-b7e4-q007 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ca558bcbd2760042521f7d8e7d036176081ce2ca72b4142bef0ae38ba427d14b` | answered | `77dbd9188335e244291508fc488e0b70352baa5cfe6938758d59c1c5d8255bf6` | pass |
| fhv6c-b7e4-q008 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `8bf9f7a68e69aae979aa1e0efb96478aa206dabdcbd8b181ae9a6a0031e010af` | answered | `6fc257b363618f6a6926aeb290f9deaa20964f226dfabd28e3a36795b1ee5f9d` | pass |
| fhv6c-b7e4-q008 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e635e6d3e5a8cc1d93796b071717d08c05bb5f51bdc8939cd7ea4ca0d43f7dd6` | answered | `6cb1c1a4f60a909a60c1bf461ae9fc8da993527de10a71434042c1e0370eebfc` | pass |
| fhv6c-b7e4-q009 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0e280ca854b3cc93bf34e5882a812401039224a097a0b6f606a1ce214941e834` | answered | `f026a24bfc40976daad80b5d765fccaf24790aa756ec547b23a482b270667421` | pass |
| fhv6c-b7e4-q009 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f081ac57bc72f7167fdd2ef801643a42720195383f90019a319154947a36b03a` | answered | `819222d16c27f8ebca2f1802f83d074fdde63e2d43567b8adc53b12fcf152759` | pass |
| fhv6c-b7e4-q010 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `91bcd08ed10b5067b64c8e583416952db41ef1fe3b3054905a370d091be3b617` | answered | `9f166aa727e4730a965b7fe16181f576326320087a69558df7fa3506ab5071e3` | pass |
| fhv6c-b7e4-q010 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `3ba79f610a2256800977098996674bb77acb7b4d6f05817d13142d9a6fb1e9af` | answered | `d55cf6132599d2491cdaa20f20caf426cb668aafdb825b967bdece45a5efcafd` | pass |
| fhv6c-b7e4-q011 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e6d3455da90b6793f1426af0a5e06eeec12836080e29d4286b06fb3743417b9a` | answered | `f1007ffac146d1842053fa09a20449c8085ac702675a4fc78915ab7d6ace2a60` | pass |
| fhv6c-b7e4-q011 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `25412240bedbe7e3b7c59fc754a6f5cbc9dc54a123246841652db40a44e0c0a3` | answered | `24e1e5ee248cead61689fa041948fdeab60227c953f827b73092cf6bb27a29ef` | fail |
| fhv6c-b7e4-q011 | third-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `7ccd76ce73fa1fd558809c469299e70472cb44c82dd42688f436921d2ffe7210` | answered | `6d84d63a9a8870d55bc8598c4af74e00c95ebf70a96e024797deb45c73ecbc79` | pass |
| fhv6c-b7e4-q012 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `eace4c3b96f6be44d164ca8c4242127c94a4cbb602ba14db5ee891346c822c67` | answered | `80a06f4c4299489d4f6af6c5d9959310afe73cba0d5d5b57dba6524cb337a32a` | pass |
| fhv6c-b7e4-q012 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e489157ee0aa0844e0d84d7e0b73dbd6306dfb6488468ff02b63cc4021957ecb` | answered | `b7a8a3760dc88acc020475574a0355208260e120ac5b70b4e43431ca10f8dfe9` | pass |
| fhv6c-b7e4-q013 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `88010cb416d45e00562d47461719748c96f40e4a96813a3f1d74d8a4f76b1f2c` | answered | `e0a8390d159733e2a8b0cb4ded12d9c9b3dfba15a5b42396a5c6c80fee5e31a1` | pass |
| fhv6c-b7e4-q013 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b2d1cafd7074d10e70e55efd031f963767ccbbf1417a388a559221a5c83baeb9` | answered | `6c26240926426068b2f5843e61eac80986981c1bda98489cdc64dd790d32645f` | pass |
| fhv6c-b7e4-q014 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `6fbf20acd77e571af7a40ecd9e7901832d363604d9eef879f227fdb6c152cefd` | answered | `6aaeca017ac8f0af0d94f1ba52fb45931fb5821fd486812178cfd6ed7d682b2f` | pass |
| fhv6c-b7e4-q014 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `bef690dbeec1367202f2061f7f15ab749ac74fcc3a5fed723d436d4ec870d540` | answered | `05e3c7a40f643125c973f28c98bcc498ebc6ec5b45613998a9d97a1ee95c7190` | fail |
| fhv6c-b7e4-q014 | third-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `35457bd54021ff545df544428917592ab8948c5e493d2ad987bff908a31f6208` | answered | `d1555da844c6b0f123e5ce4e7c2c931f8155b500be03ee341f315f337d573785` | fail |
| fhv6c-b7e4-q015 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a1ec85228e5613d22890af4973ff403321598f50ad915ad9244e0037a06cfe76` | answered | `33e2c285e8de71559ffc23a04293af653bf5967793abf92e6c59e25a46999411` | fail |
| fhv6c-b7e4-q015 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6de90f03f842a7eb9bf3522d4d935ec28e89a102fa55e376b3469af922fcac01` | answered | `cab82f9d1e81f526cb36307c5718b5ad88a45ec9ea1c427ded7fe6e38e0450ac` | fail |
| fhv6c-b7e4-q016 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `27a140bb60b421db1597220727b2a76db4bc7fe82dfaa91e1ece405c2e70024a` | answered | `3f76cefcd21a5487a53c0108044fde086f19ecc816dd7ca655514b01a275107f` | fail |
| fhv6c-b7e4-q016 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c67c79b53a7b8ab3d955d14fd6eae37bc9f7868aa5046cdfd0a0dbb473b6dc1e` | answered | `7b41403e4280a0ed615d50d3501a777901f7fe5f7bf1a320ba2bf637cc850e92` | fail |
| fhv6c-b7e4-q017 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `74e0241b057f9b40e0b8a3e51d581b7d69577444aca3f54782360ce9176c3cdb` | answered | `85a747ae9555e17b58bdc4cd72f6e58692a6f671d6667dae80d79108e736054b` | pass |
| fhv6c-b7e4-q017 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1c57d955658939be411dc18bafa7d9b63c88fd33d687124d9ad0c01a8b22169d` | answered | `545a29f3d13c9198381122bb339898548a6fae428b06afa719a40010320e16fd` | pass |
| fhv6c-b7e4-q018 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5d1a677d92449311d79c538faed1a75af2beb1dae17574c0bb3762713a51a771` | answered | `a7b6e8f107397ee7b0c218f9c4f11b33ef6070bbe03594c4d47218cae31c9b14` | fail |
| fhv6c-b7e4-q018 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `994ab7c97c49c584126f7a73437566399587633c80b306a1bdaa2623f6af1712` | answered | `97fdf38a4a4a1d6bcc9b2dd14614e7a725e173625cca9db7cb6559f4bb156c41` | fail |
| fhv6c-b7e4-q019 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a11adcd1bec3721842b36814d7b2985aa87bba17de9741bee9c34a3aa13a129a` | answered | `c57a5dc666fc215c44917fd456fe6be3255a4131fbbe1d77024ee706a2127865` | pass |
| fhv6c-b7e4-q019 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ef1f4f22a73037604c6b04f614e1c9b68d1e14d14eddd63e8bac87f4c3722cfc` | answered | `f36303cd6ea2a66b805f66a2c3e219f5eaad88b24012c524c19391d24eadfea2` | pass |
| fhv6c-b7e4-q020 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `988aa2be6a3959a8e261d9c58b68fb9f0e1083f4ee7ca48cedcfe59a4680abdc` | answered | `491022a4820eb7a41fc7102cecf768bd778a6e07592efb4b1098f04c97d836a2` | fail |
| fhv6c-b7e4-q020 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `0b79c8600c958d1e73bcafc2e48fbf9e82bed83ffb65a9db2dc9686234325a89` | answered | `db377f6b2f1c67221d745cbb4eae20c3f4199b36d248171c0f86dc59e74d6ed0` | fail |
| fhv6c-b7e4-q021 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `18a929a857f1ed71c155fde0c9dc302426cb762fc3fad5c8a8cf271ef24f2803` | answered | `29b7fea1ae4d9e650cf02e81dd5e0c5d1d8e72e461d1a9d9c84c43adf1433c50` | pass |
| fhv6c-b7e4-q021 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `571a98d355608052885ce05f56a9d231a66e1e17494295e938e17d8b76fdfb6b` | answered | `fa7b6fa1c2b3e7ce83152c5ee0dd0addb175d8812ee1643b1d976dcb134a4f30` | pass |
| fhv6c-b7e4-q022 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f3b34191cde815f616bd6bca24e98026f2f814a38f715dc495d3f41b90bd6628` | answered | `eb49b3cd9aabf2e5f377c06394051aea062e6bacb6176be031a845b5d7cb97b0` | pass |
| fhv6c-b7e4-q022 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `dcbbe3647bd9ce2fb9f6b32d21ec3509b304d140348aa532fb5ef1eacbbbe5f4` | answered | `7f20a5edddd90fbf702e9a8c508c37f449208dc48b50cde5bb0e6d23eae500a2` | pass |
| fhv6c-b7e4-q023 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4c716b1a2b9b5f3e6a1cd0bfe592af5dd5b30ecca08f09134bde2b5a8ab0ee04` | answered | `ce457d0d0bfb2b763dfa97382bc85e0e82013b3eb6c67d9e37af82574498adf7` | pass |
| fhv6c-b7e4-q023 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `bd1a535fc1c8e80194c6f349634de2119f4f33ba700bb3b27022bb363e71a141` | answered | `22f6c8f11b4266020c3430d011a0c19515ba127c10e37c9766ace36f4ecf95f9` | pass |
| fhv6c-b7e4-q024 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b228a46ab3f43e977a894a2cec9858934c99de8eb3841576331e408fd93ce868` | answered | `27caa4a6a4ce89daba080f8078bd09c3649a572b421c4db7a655731fa83c18b5` | pass |
| fhv6c-b7e4-q024 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `65590c7b2b675277ce834d4819fb84952970455da5b62ac98303f2063663eeb7` | answered | `2a12c75adb24923e0345dfb268e39316319ea7256beacd655e873df4eb011a06` | pass |
| fhv6c-b7e4-q025 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `409e3a6c53271aadd992818a2c140d977182d9a8fcd7987a74dacab46ed96808` | answered | `4957e207696c1ec66bf605bcfbec96e30475b9c69fd3a5f795caca141fa25c07` | pass |
| fhv6c-b7e4-q025 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `568634bd700cba49cc8d217193a663a67b2be737e6e7952abd50b36ae81201b1` | answered | `d31a3d07762ac3d284930022fd9b3394c7f0b2eea39c848fa20301136f83bac0` | pass |
| fhv6c-b7e4-q026 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a20bc555375e96d36fe9e13c96ae2902aafbd29b61208fae10e6a63437ba8760` | answered | `109670ab53c10525463a7b42e59b082a5523909c2b0fa3306d3ab383d0de8ecd` | pass |
| fhv6c-b7e4-q026 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `50ac86509baab28523b82c0dd0ff6c459d11c05b31541e1b3f06a479e2f6d02c` | answered | `70769338948353a8651901bbfde7e6696a0e62f3cc16a02c32dee9cdb49d751b` | pass |
| fhv6c-b7e4-q027 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `96ede6e545bcee3638f620ee8aa523324f2dabe166cbca168bb0272b50b3d514` | answered | `7fa6a02cfc941117afcba8d0ac44e53e451b289a61ae7d3eefde803d23adc4e8` | pass |
| fhv6c-b7e4-q027 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `badc00dd1ca52cd51725c75aaa3f68577d8a5da6a46ad5467dcae2f4b9e8ee20` | answered | `cbf8387f9fb87ab4417b379d8a89993cfc9db689b26b1ac7d67c62604c80188a` | pass |
| fhv6c-b7e4-q028 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2d79aadf48385bce34f86fed93b01a4cfa23da450d3b92aed35b44d617cf3091` | answered | `af5aca7bc576e7e74ebbb16531d5de229a1f31eafa805fb48a7f73125c3b78c5` | pass |
| fhv6c-b7e4-q028 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f59ed5f79f572960e4fce38d1da9b14e661e5902bce34fc507c408ef9f57b181` | answered | `b877e5b0d49100825c6efd79c4606e292c95d015975f2c0fa7cae4b8ee49dee4` | pass |
| fhv6c-b7e4-q029 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `09016d22d151bfa5559f83d3aa919ccbc1a4124575d1a3d5f9fd9ab95eb12b63` | answered | `ca382c74a994db999044d589254eda9d881ed583d77fe6dee605e23237f6219c` | pass |
| fhv6c-b7e4-q029 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `84cc4fe06e15d447e4d04e91b40c6dc0b73e8f6904669b0cf8768d52e947eae9` | answered | `eaeba5b52198e152add8b336e1b6330f98fa0e359c41b1bf05f68dcb1fd32b47` | pass |
| fhv6c-b7e4-q030 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `697306542ac4c0bdddfafa0580d93b41c94588be4c42a1026eec0e2925ab2e24` | answered | `934042f3a025e1e518e2b8cb29b99c595f4e5f9aeae844ee77f13d47e05430c2` | pass |
| fhv6c-b7e4-q030 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `efa4acf4559febe37c664d9744ed9cd453150ab08debeacf3dd58fb82f0da7a2` | answered | `9ad28e989f278e6337f98af443b3409eaed932b49f8f054595466f25df76c48d` | pass |
| fhv6c-b7e4-q031 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `211f9b09b07d520107226fbd992ca5c6234b71b446e384f78d1cd1dbbf2b76cf` | answered | `22a5a45c7bff2423806874a5c77db9f5d6f50d169219cffd9b14dc4ffc8dd5bf` | pass |
| fhv6c-b7e4-q031 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9f0a7a54c3201701bc63fabdde63ff24f4a6868899300e559320272114a779ea` | answered | `c3a2426f9407286031fac46369790f2d351ee69ea98796311e8d52a58821f235` | pass |
| fhv6c-b7e4-q032 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bc8c3141576962263c3bfd221aa2101d843b4e75b9a5faf78c1998e73f7838dc` | answered | `789f504bb63b22fb44010afcf79a0c62909126d9ac70b8757ff76e6578ed009c` | pass |
| fhv6c-b7e4-q032 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `40748c97dab6854d3ba22dc8ef9dd4bdedb70f6d467de54018eb7b082ec1f992` | answered | `deb549879ad0defdc8b86468490f23a6912f335861fe79af1ecc5848693d2b57` | pass |
| fhv6c-b7e4-q033 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `78f269f75a0adc82521ecd380dbbc69192fe0b8a4fe51cc22c91567ca43dc188` | answered | `9ed91d4348736a8c6f5c9f508d87a78d844f7fb3d47da5c63a890412ca90f618` | pass |
| fhv6c-b7e4-q033 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `0102a794d0a38e60f4eccb02545b3a1c5925176609dd8dbbd7fc4f6d1a3b402e` | answered | `7e02467390abf756411e176e0e429a1928b350d8b052012cc11fcd36488e2f64` | pass |
| fhv6c-b7e4-q034 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ad8a0015c330b7121f8417ddd9f525b1afe1ea8d7d81928b59684b9995fa9623` | answered | `6c225ff3ca39f8a98a3b0ab76af0c0220f686a56889871bb1206beb8b0b198ab` | fail |
| fhv6c-b7e4-q034 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9ef875cee9d0d0fa6a8ac5c116ab879737f760e20b20f11d4a0b694da5e4017f` | answered | `c96a0d635d75e9103b8c4a8652f2133b889b180f34fdb96a30ed2cff94743862` | fail |
| fhv6c-b7e4-q035 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `837ff7fb2946845a16405de47024b4befc8959ff8a83765b6fb8fc8983295ebe` | answered | `91fa8337e81339eaa955a293afe5e18705c71329b9ea5601081e24c665f3f79a` | fail |
| fhv6c-b7e4-q035 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `69f24c5c1f2af8c3cb9a9f0b94b620e84e5d6b64a7e6166d9f006ff43f9737bd` | answered | `cc16fb73eaa1ed7ff19918d43abc06656fe6a6ffefb23aad4699b581dc7c5411` | fail |
| fhv6c-b7e4-q036 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `34cd74e5ea5c378a2ad22d4e845d929369e365cf7b8eb89451a193e4f9262bdf` | answered | `383f0a90ca415e4b93a11c78062384e7817deda07858232c14a6b9260216d1b8` | fail |
| fhv6c-b7e4-q036 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `aba9c59e8224455d2aaec3fa5d15bda5c9d1cb06dcd7d95169310477f6a59136` | answered | `54971b30d21d08de37c6c54f8a19b9689e9269c2237fbefca44e7a3baf9a1be1` | fail |
| fhv6c-b7e4-q037 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `30c5926dee6b1ff2c474ff404d609082215c77de2484f50929cccdb2e7437aa4` | answered | `255834dc52f678517f6696d28409285f3430b6a4e365302b9cc7450756fa89aa` | fail |
| fhv6c-b7e4-q037 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `8b2ec1c5088d2e4532073e433b8a4f91eaf0e7706a9bb2999b4b290d977e98a2` | answered | `4811ec33ae84ea88e111e785b9464ba0239333ea3f6ac824eba77cf1b97f79fc` | fail |
| fhv6c-b7e4-q038 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e24709ac3dec603b6e96e57eaa932157d142a3feeadf74dee0d013a042d92b30` | answered | `3076187bc43b6877eab40c4c2180217e6af70ee300eb82373fa0f363a2c4c61b` | fail |
| fhv6c-b7e4-q038 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ec3a92fdb1887975d11c63fe8b93a070ee4444b413efd17c59624d396953773f` | answered | `05a2d37f2998982a57174a016c4f520ebd578434f846f7bf61d500119e7de6fa` | fail |
| fhv6c-b7e4-q039 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f892a5b808232c778601edcafa6f46576d0f84009fbeeccd96534e9340151811` | answered | `9332207b644c657724eddfd2e79f08eb4a1121a1eace3543d6e8b49d709c8a1e` | fail |
| fhv6c-b7e4-q039 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `3335f965961a6b73c832bc4d23e21c31e91cf12c9d0f10785eb94c8da550f8cf` | answered | `3805675da232f45622442a641d9063364d6343bbb55248ff58f4526dfb4f9446` | fail |
| fhv6c-b7e4-q040 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `57a7243928d8b60bbf8196a981ab5d48d44171e2ff9df298329645e29ed50982` | answered | `63cb79a3bcda63eabc3436084c40be824aca78033e1bdc809183ca06259fbeb2` | fail |
| fhv6c-b7e4-q040 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a38241792b7abdc1748c01f3babbba2ec900bff40d6214d709e4ff360b2524f2` | answered | `1fc6a130ceeb8acac4b9d36221612c6c9fcc49220540ae26ffbf8a18db3e9a62` | fail |
| fhv6c-b7e4-q041 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `10c51d0fbb16d2a2f33f0c9b903c37156f53f0c6e3eefe184147ff6f2ade95c4` | answered | `c4399cfdb1385260c9d81247aa536e57acef2ba63a0f0b148764ad5b381774ef` | fail |
| fhv6c-b7e4-q041 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `83e7eb53a83f2475e1f1764a1413e451ceecee728959a83437e5c54534e58f1c` | answered | `c8d598042f3f64f6d7e64559042b608d334f5948072484f5b896e03bfe73d92a` | fail |
| fhv6c-b7e4-q042 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ac4e69a84ec1166b488d6856ed723d795f1c064b67db9a9cbfd4ef88a9a7bad7` | answered | `76b501410ce4952125afa39dee099665346adcf5144815497e24dcc2a51565c1` | pass |
| fhv6c-b7e4-q042 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `72af967690cdf637d5107589300dc2e784a9680f1348ce04e76e33c18e166021` | answered | `ff67599bcf7afaf41b504ab1cd4b1d4a67f5a1d46d27ac193a8dc7a30af28333` | pass |
| fhv6c-b7e4-q043 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9ff28ef43e87e81b8ac22f039ab7189aae24300e01f66e7cac50be9a81040bb9` | answered | `9c3025966ac5628c8979ff12da6029329602406aba57a88ff17409f16509ca06` | pass |
| fhv6c-b7e4-q043 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `098ffabd9db851339bb594d695de77b96e8c81aeb5d5d1b7e184628f0c415d32` | answered | `d819aa6b378eda74f3038c1d6e30fb27156a00d266b06999ce497e4728b3fc8d` | fail |
| fhv6c-b7e4-q043 | third-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `a1a7371bdb322b8660265f570c202605afcb6a954ed2c7fbfa0cefe87cd5316f` | answered | `017eb66b27d65f9bf3590361edee858618e5e8607a6d084ddba428551637bd55` | pass |
| fhv6c-b7e4-q044 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1854c1c848fb1191020e066c81df6660d8f27f49f67466b3f227c5d932103b3b` | answered | `b092a64f6e52b8bd78b25d18a18c34fb0aed752c5875d1dda1559b533049fa56` | pass |
| fhv6c-b7e4-q044 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4cb58318ebd0aca69957a70b3b7a134ca0a5fe1753bd2020b69026b299dbc4fd` | answered | `aba8487aa783a8b010700a1dd57d9180df4305bcfbc5f5e1594e8808c21fd3e8` | fail |
| fhv6c-b7e4-q044 | third-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `5f17d8d0d3b68f247eab945f5d11f7e3ffcf5928137a200e663bc2ec48978958` | answered | `c4df15c7ff28b36d7f9c3f1e97ca44713cfa45f61710c9fa752fe0cce9119e2e` | fail |
| fhv6c-b7e4-q045 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `872dd949c4822b7c2c39b052381e0dba67de58992a8702e61015a27c9bb0f150` | answered | `5b27d9d6d2d138f0f7ff7d34cd0f0e4a4d3e8e2839f47feccc3b829fa12af3ad` | fail |
| fhv6c-b7e4-q045 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d8eec7fb3f353ea9e6df56d26d02ca992c0807902f13e8ecc5268db629f77b7e` | answered | `283bfba8bc5416a2d2eaae86f401c77f4238308c76a2c0c4313e7e626a12cf16` | fail |
| fhv6c-b7e4-q046 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7be148952e782ffc2c51b276759cd661c93e934b9825819c40bc5bcc37570391` | answered | `7593c3983302fc8e03e1932d6edad8c76b428fa6ed75963a27c195f22cd6958e` | pass |
| fhv6c-b7e4-q046 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `bc00e5288560649097f77557f6ef68aaa0310ed3b28178100977c769b03d82d1` | answered | `efa9b6d85814eca6da0065e31e99c62acf2f46beba2bea7ba90694cbb7a49382` | pass |
| fhv6c-b7e4-q047 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d52ccef32b601a2b79345cfb62f962f49dbbab1f809b17f014a662abdfa2fc5d` | answered | `46221350f7cc5f92290ef97b8d1228ba6351d575ec37e67afe72696dfa01962d` | pass |
| fhv6c-b7e4-q047 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `5f834a7763f943ca827c62e31c392db22c66306fe77218b5a3db23aa9342d841` | answered | `b77bca2d0ea5920a5563c094ee66d3df0f8151a29f870bbf2f66fe6f9e390551` | pass |
| fhv6c-b7e4-q048 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9d327c6a4015dbe80736373f393c1091cbdd1406ab361e1a5f4ee18429f7e558` | answered | `c82a4b50fe525591bcc1563c863793c6a3404aa082d2c0dd8010f554194b0efd` | fail |
| fhv6c-b7e4-q048 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d12b11b5683ca80a0bbb73abc988d9685f59318737d80f14ff7ef9d1b0888361` | answered | `3cba8968dbef303bcc1f408472661404ffb28f2ba1fbf8f85c0c11efb2d11aa9` | fail |
| fhv6c-b7e4-q049 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `dc7ddfa1b67dfb7b9535a9bfe194cc4f57a6298e120acbb0482c9f5703286d1e` | answered | `3a91c31ecc30dd026047ac73fd792dd91ba5671e3e5b7fe05f42e072f4448edf` | pass |
| fhv6c-b7e4-q049 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `cb9f3801fbb4970f6bd26028e63e72e3caee58e57bda5a487fd97bc6d832e37c` | answered | `f952c676219035e49effd39c40157f9e72a704817b4111c685197fa60b51483c` | pass |
| fhv6c-b7e4-q050 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e1b962c41077fcbe26cf077273009c10737ee4dd8f15b25dae1a67afff4a323b` | answered | `b9da35b034eb712fa5514fbef6738e3cb6f3aac494eaa6a7b076b741b4cc6985` | pass |
| fhv6c-b7e4-q050 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2ff950b27312662c5c6ab8c86b94b16d0f8b9e8dea92cfec13af35a71b4373db` | answered | `ada4a4d63a6c538e2d6d6bb0fb2445fd9b4c5a9a12db461d923030444dce88f8` | pass |
| fhv6c-b7e4-q051 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `da525e30eb605fd0c3f3caa8bc1bec9daf262909b7f9e9f658dbcd35446ff61e` | answered | `5c48654c0e555da244df38e7c23231ae26339efcc6ae272ce1a5d27251f8ee93` | fail |
| fhv6c-b7e4-q051 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c61f55efef0002816b6a28a91e31142639ee71f04722ed34cff9bb266323b976` | answered | `916a7ead77e72a917570196647aef0539b2b8fe0ef530abde3eb65a80b949042` | fail |
| fhv6c-b7e4-q052 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ee5557ae47f2b45a8533fca2b0852154af4a8f28b2fb14f0208118160dba00e4` | answered | `d5bc0131b4536d3c52d2d7d4e56ca0b8124ab81889e8b639ff6c4377a020f053` | pass |
| fhv6c-b7e4-q052 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4f12c1fbf6fe348b3728881724fef27be14610698f53ee57cb99fb419d6b8a38` | answered | `afacae20c95267f2146a5df6b4e619cc3a1f76a0477307070e24a5f5d99fa8e9` | pass |
| fhv6c-b7e4-q053 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `556fff0515a28d6609cdb0f2052a8d27813a678f112ff3ded796ab1ad49fa518` | answered | `b6a10e3d497ac4372bf49e095741980a4f11295936b4c4e7b9017a439b8d756e` | pass |
| fhv6c-b7e4-q053 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `47c89a718541973e97f7da623376e06f96b995f1cac96d85646b137a9654d44e` | answered | `eb6985636470d831c4203c296254fed510f0fd2d78adb85407a558635de559a8` | pass |
| fhv6c-b7e4-q054 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f6038913aa5dbf0756a890a5d674048c135dda3149b2711955212f4422ec0492` | answered | `d7632fb317f95a33e2c7bee90f0d19c68b7c394954a9a94e0dcfc3b4d14bd146` | pass |
| fhv6c-b7e4-q054 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `8676e252be37bdc0843bdc8e0328dfa86bfbb53f242cbfc349bbd98cdb89bd76` | answered | `5d2a577f8f9e078e57d3407ecf120934acae88f7d2686d86b325a3cd5eeef91e` | pass |
| fhv6c-b7e4-q055 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4f9225b4553e0407c577d4b83980f57e61058747357cc4cc992d2d7bba71c3c0` | answered | `21d15ba6f7f392931cc433547b95b4fbad954f4fbe2675461d07905519854d77` | pass |
| fhv6c-b7e4-q055 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ac36ce4d0787dbd80ed30d5db0eddec196521682ee5ed53400221c77c931511b` | answered | `78293d31175b1e8e84dcf433736d416ec1f528bd5f23c40c5fbfc4294f1fb46f` | pass |
| fhv6c-b7e4-q056 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `da4ad6e5b8a3056e0e8cab4f61f8ef22d8726143fd7c1cc5c7ff17f942d7640c` | answered | `510d467cf970d6905b8ac66fd0dfa7de8a5d0a3564a2e26eead268d9a20c3c2a` | fail |
| fhv6c-b7e4-q056 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9133a518e2296f0f1720e78bd9c2fd81938f9a9cacb974b3cafc0b68b2efdf23` | answered | `19d25bd2b61309daec827aa31b5ea4b376229972f6da95e9b8dbe6f88239041d` | fail |
| fhv6c-b7e4-q057 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5315cffc3f9c02b1c52bffd85214e2c524da85a11cf0ab3ce41e34eaabb86215` | answered | `27e67a6fbb2ff9e8e95984071fd11de87e4bf32e0826e7f9c8f1ce041e477c9f` | fail |
| fhv6c-b7e4-q057 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c89a5f8b5f3e16f364ad05a1b3442a8202dd0bd02e061b97c39e9dac6b9e3ea9` | answered | `39d0e67549597cc8ed50d02e76f659929864cada3c13edbb8e7f04676ca48d12` | fail |
| fhv6c-b7e4-q058 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `66a69ccbd3a8c44b846cd0edd383444db79ee6519460f8bcd4540ee39af4a7b7` | answered | `f4f79beb87ce04d2910302bc871fd50e15cedf2cbe69fe134c3287085260e7e5` | fail |
| fhv6c-b7e4-q058 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e0db034a28028cae34f69d47df2d55c2894c296344adfc64f26014d966476f91` | answered | `6ca616e88b931752cc24e50077c9fdc9f4dbdc2a87a15bb3b550d6c449f302d5` | fail |
| fhv6c-b7e4-q059 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `8a901528b0496ee8b47ee88eb9199466c8c34e2222bcb9845e16b14a12a6c567` | answered | `0fae33d30364227c053f0536eb03ffdeb0c42b3cdc688962cbe1fa4e86df224f` | fail |
| fhv6c-b7e4-q059 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `85b25b88b893b8cec5564a87641232f756577ebbc1f79daed0ede6b0fed431b6` | answered | `26e8506facfb4c28ddc77cb61aecf47789a97a27adb2459a80df7a9f9fcc36bd` | fail |
| fhv6c-b7e4-q060 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `73c2f9a87882468b42832f252985617131cfa78e1fc4640658f0ebc1c3bb13d6` | answered | `bc60ff7006a0c0b2157364c8bd875e65e3769a407f7757d8cf97e9f148d3c624` | fail |
| fhv6c-b7e4-q060 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `8a2a0170587beec817bfda75a7bfa7e03d9e3a5997fe6c074f580d98ae47bae0` | answered | `6faebadfd631f95d104abbbae7bc225c428d5d8989b70bdda73583aaa3329b30` | fail |
| fhv6c-b7e4-q061 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ac3c5c07d2aae41db4eef5c3b54a33ecc5d525016c25856e0acfbd2823caaaa3` | answered | `2eda3aff9942a4151bb3f99f89380c0e36c57e138dcec80386c3f09334a3da18` | fail |
| fhv6c-b7e4-q061 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e8699631d2603593f83951d4b27f7e769f8c14315a3bc356ad1009b713c29b79` | answered | `a2343904303a2be1cf38a49b981b9f73298f49813d271e24860d1b68f817619f` | fail |
| fhv6c-b7e4-q062 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e1babeb2fb373bfcd2a6b7efc27c192390bd03e6245727330828558c8a55e711` | answered | `6661eb801238d51b3f24f753843d221bcdedccafd5566d55f0158daa6401e624` | fail |
| fhv6c-b7e4-q062 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `83e644c0d078943d864af1b393efa018ebb1e2909ab67e06849e2c691ada83c0` | answered | `adfd180ba04a977e02d633dbcf25bd972d777ce332608759e6fb5eb75b8cdc5a` | fail |
| fhv6c-b7e4-q063 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `37b34b69039d7dc9da2d5a4b3220aa3aa249734497322a625368c19d7d79ac88` | answered | `283499f588ce033f510b112e0229093a90cb961a849ee5604d7e494f129532a0` | fail |
| fhv6c-b7e4-q063 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `753ee54a840854408332929bb51f014fa01746f36a13cb388cc2df53b101ae79` | answered | `be9116c6f41e3d8507af168e1aa93a8b36a6749f8fba4b1e7542566801326534` | fail |
| fhv6c-b7e4-q064 | third-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b16550edf107c98015c15d904a2f04f484e573aec2fbf9cbfa776c5f93d0a43d` | answered | `2d61d745c80a7e5dfaa0b5e6670ed42ec9a6eae9c5ba2282b008ed0cbd36d626` | fail |
| fhv6c-b7e4-q064 | third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `48eab53419af8972ad46c607f465d8dffd4decedaabb200b92ee46376fe2ba71` | answered | `c3d12a7756fcff848f1c00db0464e3929b5c94d4aa66196e6293443aada35060` | fail |

## Per-query outcomes

| query | stratum | outcome | reason |
|---|---|---|---|
| fhv6c-b7e4-q001 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q002 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q003 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q004 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q005 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q006 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q007 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q008 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q009 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q010 | exact_identifier | pass | both primary grades passed |
| fhv6c-b7e4-q011 | exact_identifier | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| fhv6c-b7e4-q012 | exact_path | pass | both primary grades passed |
| fhv6c-b7e4-q013 | exact_path | pass | both primary grades passed |
| fhv6c-b7e4-q014 | exact_path | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| fhv6c-b7e4-q015 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q016 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q017 | exact_path | pass | both primary grades passed |
| fhv6c-b7e4-q018 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q019 | exact_path | pass | both primary grades passed |
| fhv6c-b7e4-q020 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q021 | exact_path | pass | both primary grades passed |
| fhv6c-b7e4-q022 | exact_path | pass | both primary grades passed |
| fhv6c-b7e4-q023 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q024 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q025 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q026 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q027 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q028 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q029 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q030 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q031 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q032 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q033 | nl_behaviour | pass | both primary grades passed |
| fhv6c-b7e4-q034 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q035 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q036 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q037 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q038 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q039 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q040 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q041 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q042 | architecture_flow | pass | both primary grades passed |
| fhv6c-b7e4-q043 | architecture_flow | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| fhv6c-b7e4-q044 | architecture_flow | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| fhv6c-b7e4-q045 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q046 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q047 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q048 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q049 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q050 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q051 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q052 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q053 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q054 | config_docs | pass | both primary grades passed |
| fhv6c-b7e4-q055 | ambiguous | pass | both primary grades passed |
| fhv6c-b7e4-q056 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q057 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q058 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q059 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q060 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q061 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q062 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q063 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv6c-b7e4-q064 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |

## No override

There is no flag, environment variable, configuration key or report field that lowers `k`,
waives a query, excludes a query from `N`, retries a graded response or forces a pass. A
missing, empty or refused response is a failure for that rater and a failure for its query,
and is not adjudicated, re-requested or replaced. A pass count below `k` records
`RELEASE: NO` and exits non-zero.
