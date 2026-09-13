# SW-280 — qrel-blind smoke evaluation over the sealed cobra-v2 holdout

Contract: `sw280-qrel-blind-smoke-evaluation/1`. Measurement contract: `sw266-measurement-contract/1`.

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
| observed pass count (as reviewed) | 42 |
| observed incidence | 42/64 (resolution 1/64) |
| observed pass rate | 65.6% |
| exact Clopper-Pearson 95% interval | [0.526976961221, 0.770536757500] |
| lower bound clears the 3/4 floor | false |
| primary disagreements | 2/64 |
| adjudications | 2 |
| missing, empty or refused primary responses | 0 |
| **RELEASE** | **NO** |

- the reviewed pass count is 42 of 64, below the pre-registered k=56 (reviewed 42, corrected 42, and the release is decided on the smaller); there is no override, exception or waiver

## Capture binding

The rated bytes are bound to the candidate implementation and the indexed checkout this run
names: candidate `94de3233d38deb5bff2e372341d9c853e8533b0c`, checkout `a0a6ae020bb3899ff0276067863e50523f897370`, both worktrees clean at capture.

## How `k` was derived, before any response was opened

`k` is the smallest integer in `[0, N]` whose two-sided exact Clopper-Pearson 95% lower bound is
at least `3/4`. It was derived by code from `N`, not written into this document.

- `N` = 64, read from the sealed dataset (`331bd256c3097c61b34b3ccbc7716a161a315c76f2a41e22c4a4f88085eb85ae`): count of answerable holdout queries in the sealed dataset: split=holdout, stratum!=no_hit, at least one grade-3 span
- `k` = 56, whose lower bound is 0.768473694033
- `k-1` = 55, whose lower bound is 0.749763164375 — below the floor, which is what fixes `k`
- method: two-sided exact Clopper-Pearson binomial interval; the floor comparison is exact rational arithmetic on the upper tail at p = 3/4, and rendered endpoints are bisection brackets of width 2^-64
- pre-registered at 2026-09-13T15:15:38Z, naming precondition record `e287398747c0b3d263a1ea5a33083ef6aaa424217cc61b18575709d4ca04a17d` at commit `94de3233d38deb5bff2e372341d9c853e8533b0c`

## Per-stratum counts

Counts are authoritative; each stratum states its own `1/n` resolution.

| stratum | passed/total | resolution |
|---|---|---|
| ambiguous | 9/10 | 1/10 |
| architecture_flow | 3/11 | 1/11 |
| config_docs | 2/10 | 1/10 |
| exact_identifier | 11/11 | 1/11 |
| exact_path | 9/11 | 1/11 |
| nl_behaviour | 8/11 | 1/11 |

## Participants

| role | id | provider | model | took part in this track | basis |
|---|---|---|---|---|---|
| primary | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Stable second-run primary-A configuration: concrete non-alias model gpt-6-astra, reasoning high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item. It did not implement the candidate, curate the dataset, or participate in the first holdout. OpenAI exposes no immutable backend snapshot/build digest. |
| primary | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Stable second-run primary-B configuration: concrete non-alias model gpt-5.6-sol, reasoning high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item. It did not implement the candidate, curate the dataset, or participate in the first holdout. OpenAI exposes no immutable backend snapshot/build digest. |
| grader | second-grader--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Stable second-run grader configuration: concrete non-alias model gpt-6-astra, reasoning high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per packet. It did not implement the candidate, curate the dataset, serve as a primary, or participate in the first holdout. OpenAI exposes no immutable backend snapshot/build digest. |
| adjudicator | second-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Stable second-run adjudicator configuration: concrete non-alias model gpt-5.6-sol, reasoning high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per disagreement. It did not implement the candidate, curate the dataset, serve as a primary, or participate in the first holdout. It receives no primary response or grade before its own answer is sealed. OpenAI exposes no immutable backend snapshot/build digest. |

## Adjudicated queries

| query | primary outcomes | adjudicator | final |
|---|---|---|---|
| fhv5b-c7e2-q013 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | pass | pass |
| fhv5b-c7e2-q022 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | fail | fail |

## Frozen inputs and the end-of-run comparison

| role | path | frozen sha256 | observed sha256 | matches |
|---|---|---|---|---|
| dataset | `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-second-fresh-sealed-holdout/sealed-dataset.json` | `331bd256c3097c61b34b3ccbc7716a161a315c76f2a41e22c4a4f88085eb85ae` | `331bd256c3097c61b34b3ccbc7716a161a315c76f2a41e22c4a4f88085eb85ae` | true |
| budgets | `docs/eval/retrieval-budgets.json` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | true |
| targets | `docs/eval/retrieval-targets.json` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | true |
| grading_rubric | `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-second-fresh-sealed-holdout/grading-rubric.md` | `f0420f756d3a5742ca684916965921bbb6794010af781aaf129e61ba7f07457c` | `f0420f756d3a5742ca684916965921bbb6794010af781aaf129e61ba7f07457c` | true |
| methodology | `docs/eval/retrieval/methodology.md` | `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d` | `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d` | true |

Compared at 2026-09-13T15:51:20Z. All match: true.

## Capture provenance

- capture instrument: `sw280-candidate-mcp-capture/4`
- transport: MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)
- payload boundary: `mcp_jsonrpc_response_bytes`
- candidate: `task_context/2-compact/5` at a 1200-token budget
- repository: cobra at `a0a6ae020bb3899ff0276067863e50523f897370`
- embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (model `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b`, index `147:static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b
40:e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
64:75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c
64:107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45
3:256
2:v3
0:
32:897690b7333b463d67f36a45603e00f3`, generation `g-6bd6388ccc37544e`, 768 persisted vectors, state ready)
- tokenizer: `tiktoken:cl100k_base:ordinary` (vocabulary `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`)

## Content addresses

| query | query text sha256 | bundle sha256 | bundle bytes |
|---|---|---|---|
| fhv5b-c7e2-q001 | `a65fef8b9afeb10a5b76e615834de1c9d31292c8c5987289469c692a7acdab0d` | `7206a50c83794c83fa2577954d90d70891ff40ffc3a1327e6f2a50b9c315ffe9` | 3349 |
| fhv5b-c7e2-q002 | `7daba0b6b9cd214fbb9922f3475111dfa38cf7556cc77139ae7e2576ec820756` | `2e39f41cbf668fae047278d30460f43ebd0e37eccc58a102b74c64419d81417a` | 3651 |
| fhv5b-c7e2-q003 | `4078a075b5e6bcc1789c626b49ceca4a6511c43dcbf4edfbe9ed199b4209c610` | `07fb742c1e495d716b476355629b53e65de943d47d9d6a95d369d363dad301f0` | 3692 |
| fhv5b-c7e2-q004 | `ed729dda5140bd2942b3e139e4fb41a2ac9f8ad5b7c0bf0d0ba1771ae0b92813` | `1465a65a82b0fec2a35cc2ad40a43980d99fb513330906b76f55a3d61e6affa8` | 3895 |
| fhv5b-c7e2-q005 | `0e93310c45710cb9574f7f8527b7b308931ee87c47102c05dcb56718b6d8b425` | `336caca82ffdeb6dc36e120abfee6506ba736ff8509ba2f94442efe2f907e707` | 3512 |
| fhv5b-c7e2-q006 | `fbf2e6cdff22461e48abfa843a97f44ad80b3c2fc583c425629a956870dd541d` | `8a2c682f5b9896f4433ffa5fc00231bd6fe6032e1232c48141e19079b3c75236` | 3511 |
| fhv5b-c7e2-q007 | `26ec289d5e5ceebb6069a58181838243590e7f912732de0db87e0bc80ae2e2dc` | `ce195b314ac6628f07dec09fb269c0823370785d2dad864d1c03a3aebce3279b` | 3416 |
| fhv5b-c7e2-q008 | `20e834bcdbcf889cfb17a034458af2f53138a5d9ebc300db755def0c9ee2befb` | `981a7e07dc211f6a0f1f0240dec7d29c8e8bc2e48663853d5a6653d10c19dc7f` | 3823 |
| fhv5b-c7e2-q009 | `95da10dfae32e69e59ea15c49daeed3bb0c19438c4bad531ecf6bcd3a153432f` | `3d445fe38a2c82583d8669aaa1334722b4b9753688840fe0114de9036d4040ea` | 3908 |
| fhv5b-c7e2-q010 | `1c5e3077e831b2d5d802f14b0dd03b3cad07098c4a4fb5594415716e022e4ab0` | `29decdfd215afd740af31bbd038527a886b7feeced597fbb72ac9119123fa00f` | 3442 |
| fhv5b-c7e2-q011 | `7289c0a3b6f8989579cf72c3aad648cf21a3ec6451368951776325d7171a4b95` | `f3dc02652afff536aefd7f87b6be13983e88f39c4348c1cf13563c6c59e637af` | 4056 |
| fhv5b-c7e2-q012 | `e11f2271160624c24f4f8bc34f346182705edb816f586a6b7c9669de0d76f981` | `5516fb55c84766b4a1b84c570d89fa6a58cd7bf3c750f99b4c171d26806c7083` | 3891 |
| fhv5b-c7e2-q013 | `b7d863bf73924a308cf65ceab7bbee9abd0022a03ad7cbbc9fa6110706e366a8` | `4f565f5ced132387887bb7996ea1115b670612e3c0d928fea8b6e3fb56be91cf` | 3930 |
| fhv5b-c7e2-q014 | `26388747a77338b849534710807ed57d01a6c25103e0b906399bf6e3d0f1769c` | `e440b07835b9585eefcb5df416bc8d625c6c9da577bc1ef45a82326afa148f47` | 3938 |
| fhv5b-c7e2-q015 | `f6c2a10a98ccf6fe751a8b79ad789df31eed9ef361c0c23e78f8a03e0b794954` | `8c30b25af5cae1e30897b8bc4e9981067175f4b88e02b0ad00a3164c9bdbc771` | 3356 |
| fhv5b-c7e2-q016 | `b3cba92530605b651291fb7ac99e61edbea7a29da0bb7ebd409648ec4c5f3b7e` | `8239d676c6a83bacf9d4a2263d3c60a5cdfeeb5b8a86e851f6d6cbe94fe6ebae` | 3677 |
| fhv5b-c7e2-q017 | `7d4a4cb2cf6abef9de7f25f747fc49ef1002ee60f5f92538cb5fbff8f9a7194f` | `dd1150b6d6a3ed13df952ace9f9f4cb50c0aea9a55e32b81626a638bca0c7e93` | 4216 |
| fhv5b-c7e2-q018 | `fa783f6f3f4e09982ff4bc59a6feb0ae583c946d8ca4d2fbbc508ea2280d6d44` | `fac6f824bfa1981702c7ca674da37c1ba7b3c3d6bcde71b2775d14d8cedb0052` | 4359 |
| fhv5b-c7e2-q019 | `65658c8469edd8952a66d0278abc1eceaf05d354799ea63a047dfd69c89344bd` | `ca6cf835f5237b924c8f01f4806828050c34bb62161992dbcd2b441457867de6` | 4008 |
| fhv5b-c7e2-q020 | `cef9dafa491267df3606b9d850aa34cf1e37496af66ff4b49b5d0547705b8fe2` | `1062c31b467cf99735ef7faab519e750f802793b6be528189d83cfd210a11632` | 3824 |
| fhv5b-c7e2-q021 | `3b3fd521db0be7c44d002a8612ede793dddbd279311d955748f312736b9a5b69` | `fec9854389b1ebe99c66464bd84aab1d9d7f670af3afcd184de0a4608aa39c9b` | 4011 |
| fhv5b-c7e2-q022 | `39669b5ce2a37c65edc036263345b2befbc456de221f517af06061bc34fbb4b8` | `4958fc2348626b9fe3ce89636de358bb2f2bdc708246ef00da82108111c64487` | 3566 |
| fhv5b-c7e2-q023 | `1c5a02ce144fe36c168cdac5ad153583eefe378a68bd9626fef852144ca2ee97` | `8dda51b0ec3a68bd90259b3a2691eaac3ae7ace3ac19c8a2dca3b34f479f2a52` | 3340 |
| fhv5b-c7e2-q024 | `52f7a7aeec47aad9037c3f6895020e543a3e3a1344b445a0cdc7283d19a8318f` | `0b67dc46f19cb3e29769b62f4ae4229be9c87c23c4e7458aad3803e974608438` | 3087 |
| fhv5b-c7e2-q025 | `76523b0d6c71887a29c484ff62fc4aceb8024a7a1381aff971ca6c1bb4fd6224` | `d1d645e9474b9eee264c0867ca1f9edeeb189f3c87780a3e38833d9090505b70` | 3800 |
| fhv5b-c7e2-q026 | `0bda4c851374530a59a1a7fccc8f3841da29b6e61a5de3c85df0c1261979f261` | `c57a3bd6f6c3265653ac210eead092342e6428c158a0e3347263e4e63cee2357` | 3878 |
| fhv5b-c7e2-q027 | `8034d8bb5fbd375a3d2b7e539dbd5156d0938d3721a429a41019a3706742dd6e` | `e883df877499dd5d6674bfb6c8cc4dafa9ee2318c30d40fe0712753084852ca2` | 3647 |
| fhv5b-c7e2-q028 | `82905ec4ec937cee93a1762a4cf4945937bdcdef9117dd04a170b8cedfac55a7` | `637000eb06947ff46f87aae52209740b91c3a222c380d3ba3652c9acf30bb7be` | 3555 |
| fhv5b-c7e2-q029 | `0750825844c0f4f2d5c1b2136b0eec2645b269ed8764249609169776c795448c` | `cfc1e147b2195f95c04058cecd229e38633c46c9092c49d91848217304f27c79` | 3720 |
| fhv5b-c7e2-q030 | `c9589057b50aa983b6e0c1a9ae28286a21e35d0ad8e217afc2b7fc645b6d0f87` | `561cd1e0fe17df87629cfe852e2856c9370634dc8c990db8d4e1ac5afe6e939a` | 3522 |
| fhv5b-c7e2-q031 | `625e78ec6de74375edc46631538c2e23f1f4b7411827082f3e4c15863fcae94a` | `13f533bfe2b241fed5a5590d350d6552c7a83bfc572f43e1a3b323624d13cf6a` | 3607 |
| fhv5b-c7e2-q032 | `8d5bcc54937dd878dea2f8f642d6eb0872dda9e4f3b0b816dc9f7520743c2272` | `58f1c91271f3a6f0d2f220a85454664fef7494c71230d87b4a56b52ccbbd43ee` | 4313 |
| fhv5b-c7e2-q033 | `4ccf55d66d723e5261021423d2529af031e29dc257883400937fb361601b71b4` | `47a53910ab1acbc8ff233ca07574e4e910c5d508b3ddb77dab270a85e5cc0b51` | 3853 |
| fhv5b-c7e2-q034 | `bfb4ca1570874b20a65acc6616a23e4f8c0e2f71fe8d070d8c800c56ccf66385` | `938303a454f56fb4c336eca40b799105218f3023d451dfd4784a69f4e6c2c914` | 3813 |
| fhv5b-c7e2-q035 | `733bc8af4f6bf5668a0d1d0cfe339610f3d4a417abe2692bf097635c0a3a8cdd` | `5beec56fd206c3893be682f42d72750ae0fca22f684e4d23638b1ada902545f0` | 3891 |
| fhv5b-c7e2-q036 | `1f7195ee20f9bc7c76a955ecebff8aa1c677f437703046adfcc21d7e11e0560c` | `fe4d5f824d7e822b6a37a3bec47313467d92332e2919cb73bd755c7d72307931` | 3758 |
| fhv5b-c7e2-q037 | `bf6297d09a504acb874a905f175afe0f46aa59b0738260d158d4678380ca542a` | `1b3f39136bb9196e4a13c57be3fbc132bc53dafe9d1ac1b10f8a08c4168cf0bc` | 4243 |
| fhv5b-c7e2-q038 | `2af1991a22aaff7001712f0b8585b2621f1bab3550765c0f65a6e3012106bd3d` | `1fe165b1dd8ee9d6a7b89863c2d80750cec4778eac463568c3d3b3dd9bad05d2` | 4444 |
| fhv5b-c7e2-q039 | `3dbfb27b8bf5b26cbede68d5c4c81456b31681fda5332e6912ef63cb8a0a4718` | `bc293a86d10c55debe343ba00d9bf60c69aa93f77f548f12b43db6c1a253d7b2` | 3924 |
| fhv5b-c7e2-q040 | `a4f7ac36f8171f2b0a24205c54018035de83cccf070166ea7c71fb763f21627e` | `569194971d0fd377cb2c357a3ce5e76171a880805658ed76f99acc3901f6e66a` | 3717 |
| fhv5b-c7e2-q041 | `850ce17d0004870f61ac5d012c8d2f29e5dfef31957ad12a70d9ede6dff02535` | `6e4c94b143e6dd46715a322951af4a84c3ab57e7827d9a4ce4f23e7f0d5b3c47` | 4058 |
| fhv5b-c7e2-q042 | `50598c60e9a8352bb8edac564377a561f88b11d6b7e7b742caaf5c22ced5829d` | `056f1e05df61d8edd64912e0733d6ef116bcba613f7e3d3fccaa2f1f1e77f5a0` | 4247 |
| fhv5b-c7e2-q043 | `d3d5a503526b254174c392efeda7aedca418cf585306f6ca67c8cdaeb80ecfd9` | `886f995592a120429448548c4726258cd38f7d8b151a902f58322d3928fe8f47` | 4452 |
| fhv5b-c7e2-q044 | `eccaa7e06ceabdc9d9236681b9d16e294c67a181946282472f6e900bb0153a4f` | `708f5f05695c09362b6948f51fbb691fc8f8b8697aa921ac383ec384b8f3ff97` | 4200 |
| fhv5b-c7e2-q045 | `399e59119cb9d839a7e6a6d6553e5656ef975815a872d8c69c236942a8b0dc72` | `8c426e3a5358bb937fbc52cfb59647086afad554921204a00723e12771162207` | 3596 |
| fhv5b-c7e2-q046 | `e1e762abd886add1f464bb29eee7dfad9dfb34bfac9beb6c913d7c4a7b09276f` | `177942405a64169119e51cc95b0d33b4a48592760c30b67af326a16c4af6077c` | 3885 |
| fhv5b-c7e2-q047 | `55fcb8988421a0c1efd2a63ee6c7e2cd74df4e228a1d142a6cd104fd44a28bb1` | `6836f92ad4728cdae3dfd946430804c99bd5aaa93fb185fd6c2e133e27a69989` | 3710 |
| fhv5b-c7e2-q048 | `0897a767079d61f80d52bee221d846cb5698431819cd5dd194240844ec52ef83` | `e02f1794be3245d9213a0cc20ea8968c246f538b1422aed8717afb4285cbf9e6` | 3934 |
| fhv5b-c7e2-q049 | `8bcce992d013dfe2f4dce96599d9383bba0cc69d0e75c8d566e8eace40188c4e` | `8242cb56e8e529040918e5474113b4e18444b523556e94090223eeac5ef4ea97` | 3586 |
| fhv5b-c7e2-q050 | `471a50882154fcc1fdd5c4b3bd15d5cdcbcd741597bdd798935fc892eeca81ce` | `b2771b8a7d214887d57051113f2de235adedf77c7e9a75327518e401abe3be81` | 3837 |
| fhv5b-c7e2-q051 | `c84eb40980fdd812d96a42c7c59f10390f8338e63f5b7870a570c8d86bdfc542` | `04030c26cdd3a06566c01a855543c53e655eeb60b969af394ae195665b8b1e70` | 3527 |
| fhv5b-c7e2-q052 | `62ca26585701c7a63219376d6dda9b02b7611404d217b27c58390bb6227d488b` | `734fcb1f6319b1bee83e464124f6e7d782f2122abac35aa6196d5aee1a438b94` | 3448 |
| fhv5b-c7e2-q053 | `c0c335cfcee5a6139face81894053faaa9317d26b14f0812039174d0288fc3a3` | `81f6c8b3791aaa256103724914fe3d1f29c7ab69585dfdb155cdd79f49912439` | 3801 |
| fhv5b-c7e2-q054 | `f3402672cc20c16101a7293ea09d057fff253a596a9bf83b5e234f00c42512c0` | `30fff047037b3fc1fa3cb327450389462a546fa1343132b0deded878048e7fde` | 3848 |
| fhv5b-c7e2-q055 | `637b1037f4289a88cfb0c6dd245b387abeebb95372760b1507b1bed8ed5e5ed0` | `3a56861688ca283380fdea2e24a5613b67654f593246abcd19028306b38cc0a6` | 3725 |
| fhv5b-c7e2-q056 | `196568b1fb18265da1d526caecd67328526f71c9b2302f30d7fee407c0a49cfa` | `9c34caabecb9272226ce7abd2988762434ad68eb888437cc96a5510b60fa1c94` | 3222 |
| fhv5b-c7e2-q057 | `1196777dda371a9cc7df566b69eda08c807075e1cec921c4d5363be1c0dcea24` | `17daf0fc4806fb5af02e5cd47d959e927ec80051aa64c9f3476d85eff922bb6d` | 3666 |
| fhv5b-c7e2-q058 | `06833f86ef4bc934e96b5bf07f9958047960b459c74f9ec564c16ab39f3e87ed` | `39122afc3ce49bb1f11e4b3f159a678dcd33574e659a9280b5f9569ac3eb3235` | 3695 |
| fhv5b-c7e2-q059 | `34a7cf291bfffd0ceb06855d4c3b5878a6ce70281192c21371b7a19ca7dd0a28` | `cd003089cc4ea9bbdcccc3db9a3e0e95ec98a427cceb6bd77352917fe24c23d6` | 3716 |
| fhv5b-c7e2-q060 | `0d7a74518ea9274639bb86b7a8a1cbf5f302706a7093999e02e3eb869fa9da59` | `ca6b43d4cde4d5c64dde7c0ff563e9322e07f361b37ae8fc8d9e6565833bbf0c` | 3283 |
| fhv5b-c7e2-q061 | `cbd790697c14f5045d0a3daa494d15a4ff6d863c7caefd07b01bb399c26c5615` | `7c10f9777ed047b37c4a4566e27c8258e5818064243e1b980a112ce6ed4c4e85` | 3833 |
| fhv5b-c7e2-q062 | `11088ed9589163c7ca22c6d4eb04042a95688cb0398b6b8cd03cc247b83687d1` | `ccba8180304ccc5b05ba7a93c6bbffe5262b5eb614b172f3ade11dcee7ecf716` | 3381 |
| fhv5b-c7e2-q063 | `8076a89d2465fe34b17451f25c48828daa0e9105939eb1b3bf01e0a31b96c025` | `5474bf19058f4b7a172c29fff343e7fc02a2779929ef803cc1246133d76d94b8` | 3726 |
| fhv5b-c7e2-q064 | `2f0c652d35925dbee7fd773e737383c10c24f42ed7141ea9d7a16f906ab9ff76` | `5efe919f2c89b2381cf5fae71cda3120dcac9edd680c187f5952ce16e9367eea` | 4079 |

| query | rater | response sha256 | status | grade sha256 | outcome |
|---|---|---|---|---|---|
| fhv5b-c7e2-q001 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1ae44693e652ab94fcca9e372fb5b9efc66d8ee1544025e01178142fd55026f9` | answered | `820640cb9f6566ce3d973d70a1c1b0fceac8596bdf3a88a964b2944354072333` | pass |
| fhv5b-c7e2-q001 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `554ec36e041dc144d0b0cb83cf894c91fce1aa63e3a670eb91682f79c1ce76ea` | answered | `e6a27644aa5c16ca4de4c983f3e2bc717fc8fe41f1432e71e87fdc5697e79cb7` | pass |
| fhv5b-c7e2-q002 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `72c67fafbcc652cb624ec1ccd9a0831574b22d488e0c96bc1498fdf3889c6179` | answered | `2f576d217b25f91ab9234cd2e8b2ef20ac070703083ed8a9a155f68264bf3087` | pass |
| fhv5b-c7e2-q002 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e8c2704a159350b0ceaeef094137e23d300992dba8d13d5ea6ad0b50fd24434d` | answered | `9503d9608924bfc533308b53c908a534a56865d487efa71755bb01a3f49ac366` | pass |
| fhv5b-c7e2-q003 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3687223068b907a85db6f06c180ce6fa522189f4778e890156211a1daa7e6dd0` | answered | `085934e76d2d95c155bdb74100d703303d034a27e6b9f96f4c56608927a8af1a` | pass |
| fhv5b-c7e2-q003 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7b82788d147e0086300a8f178f4fa4caebf9711cd5c9ca5c8927d56853b0d25b` | answered | `2d6b3aa180b02120ac327132782ba304368929432744c898ee83c20879275acb` | pass |
| fhv5b-c7e2-q004 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0ed34a94201b222e07d692fa12b985c94f8a13d4dc3e0666b12e62bd6c986dea` | answered | `bf0cbc6133bcb8e8b0b32e8da975611856e1619b206186d9fe3d722acd6a226a` | pass |
| fhv5b-c7e2-q004 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6688c44b7c2beacc7ea31b12b85214077ec596738e18f9df56f9adf9c924602a` | answered | `ba5e8dc6454f654766c747c747b3c2f3b98018d2616b1249e277f9d102b0fbda` | pass |
| fhv5b-c7e2-q005 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4c724ba26b4c6d47206ae08f13a177a80add59e5009d70b761ced0c85e4bd3ab` | answered | `a03c3501d9975e57d9aab22f855bd73047f0372eea8eb31b782eb05fed8878a8` | pass |
| fhv5b-c7e2-q005 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ee82e92b5fa045bcf362dbce04378b1f3326553d8c5445ffe15bbc1f96b42f9a` | answered | `a6f6d72a5b1f48cac89fa1f5cb4f6e53b9216e965a8295de73ef35ef526cf215` | pass |
| fhv5b-c7e2-q006 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0714cfcc4aaeb20756e679e7a15e78d549a649b7b8cce80171566c57f4c5a419` | answered | `76eeb8df2adf1f89dd100065a69c2f392176203d6d278ae89fd9683538781305` | pass |
| fhv5b-c7e2-q006 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `78111ee42a2fb38e2d75270c2632baf73fcce7705f82581a99d763e62949166f` | answered | `cebdf6d40665a1834a1047a193e836cd511adb381fe8eebc0dd622fbd0365d3c` | pass |
| fhv5b-c7e2-q007 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b62d56ad6708b186e91d46aa445c94081ab05cfd8728fa09a4d2b76d60e19e7c` | answered | `aa2da27a1d2631436da096ab28dfebcf1dd06f2359bc52e37cf4ad0eafc5a659` | pass |
| fhv5b-c7e2-q007 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `62d74e82d110304fafb8ffc53b23c8ca5ba53a469e54b0bb5282162ba2eae44c` | answered | `676d8a7d5fbd815c22a314f43c84426a8e3810f1aa2d492709022f7a97e5cb4c` | pass |
| fhv5b-c7e2-q008 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1530753dc4e9d8d29c91a812e80f59ede2573a79cf936756966903d85317b1e5` | answered | `dfa97d609ea3af6d9b6e87af36ccff4e77d119c0331fced1de1fc84c02a50bc0` | pass |
| fhv5b-c7e2-q008 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9b63508a6841d1392962b7e0409dbd32c7793902166113a66fee0722cceffab7` | answered | `25919c8395259808abdf009b86e068740be62d6fc41ba2338d7d5443ccbb98ea` | pass |
| fhv5b-c7e2-q009 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `c3d9aaffde9f6c06fa8ad7270feefa7a7da98b00749fc1aea5106ba490e091fa` | answered | `758b9a47c15f6bd26f1e33e24fed73e366c8f0078d19932da62503783d0e6965` | pass |
| fhv5b-c7e2-q009 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e8cd1644dcfbc2fbc49ea04ebd50d84b49e71db8c798dbf94f004a27bd918540` | answered | `cba46b78201c46f508d3b180edb8a5c8f89523a32630f2e0431eec1e70b6436a` | pass |
| fhv5b-c7e2-q010 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `681391acaf33593ad9430334990429042fc4b9a8974a987b933d9732ad9f455c` | answered | `da86a9165242f77ad244b7fa431f6a009fa36e58ee3d75649a78e2fad1c52df1` | pass |
| fhv5b-c7e2-q010 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `62144406884c89368318dbaaa2493b73479a9d63dbf0b5b334e5ad7e7f0f873e` | answered | `d859bd01bdc105ca7a8f74d5ed195854814aff3389d44b7a3bcf258c68475036` | pass |
| fhv5b-c7e2-q011 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `976c055e031a181ae8671e9d9cfbc17165f47a374b43fdc4260bf28c48cc7a74` | answered | `932c4fb575b4758648cbaa82c01c55cdc2f979f0e419e720bef23392bac8ff9b` | pass |
| fhv5b-c7e2-q011 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b9966c84589eb0087ae833c96c0d644cc77b5317eed2d8b41ed41915716f7b94` | answered | `14ae33c5f64f39c3e844252fdd1fd766e60675ec32326d7c33933af3c49355c8` | pass |
| fhv5b-c7e2-q012 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `41c42f134388a44a7366437a0be131859b5e9367790a83ff82228a3ef81a06fb` | answered | `baba518e21bba08aab89db2384ebbe331d806f1830ae67863d1322ddea43dbd7` | pass |
| fhv5b-c7e2-q012 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a95a2bc9bdc410c7b2e9ea09068337a59b5fce4d1e530162a448747cf4f443d5` | answered | `6d34deedd15ba5a6baa7c1513fdea8886a5081b49ad052a58d7fb814eebbe2be` | pass |
| fhv5b-c7e2-q013 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0533cdc27e39f6f0222f30096bdaebc7ffc1c6f7b4db98e9fe27eb905bbd8260` | answered | `a29e8e967703b29dc3474ea057f4c176c55748418a6491031b47b63f612aad47` | pass |
| fhv5b-c7e2-q013 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2b94607f23fb949811ba728f0b764a05d24e32010b2f13f9efa313eacf43016c` | answered | `296cfa30d280f3b19bba0fc0c9c96baa387caa4f590478b134c203fb8ba0e39a` | fail |
| fhv5b-c7e2-q013 | second-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `0688553d71563b706fab2d3d34a9d816d5afdd99ccde9657356af934dee614a5` | answered | `2bfa1111ba3b7941a03fd5891a5e310280567243304736c42f1257d958672673` | pass |
| fhv5b-c7e2-q014 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9a1188124b66f0daa808c345389b0c29795df1ddbb4b8f2264a0b29b757edabe` | answered | `c56dd4c5341f4c74c9c50576f8a3127091d727e325447890f9e6c64ceac45aac` | fail |
| fhv5b-c7e2-q014 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `abcf7d6f4ba5d95979a418470a8cbe0b9563074bf83aa597f3b60111650c08dc` | answered | `486b5cf4bbf5a9759903d2acd01faaa110859efcde836c5430597e832afd8089` | fail |
| fhv5b-c7e2-q015 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9d975ae3417f5aef0537713ad26b50555c2aba04799868b40647fa9777d171d6` | answered | `e756ec060d0c84b41120b79cff90ec3141065c8d0fbd8749b3f825e8bca9de1a` | pass |
| fhv5b-c7e2-q015 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `05cd333bf6ea34c91fa5b19488a25f20c60c51394d80d03b49b70833fa1ffa7b` | answered | `8de93242a55e14f41ac3e13bdaf9aabedfa4677d0c9e161b398114b541945667` | pass |
| fhv5b-c7e2-q016 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1261a63e99b530a74250f0ca68f644b60632abcbc7944b497806afa108d7f695` | answered | `979b5daa5c815c15cffa46c5f93c4ada8fcf66c0bcd8c07e341a8b717ad6c786` | pass |
| fhv5b-c7e2-q016 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `69b79e3fb492c097453179289b920526ca2f9f5a9fac9e4cd072a7c63653b23d` | answered | `f10f7fed76e15e14fd1fe3b56ac70509e0c31fc6affd06e7991154b9b70f013c` | pass |
| fhv5b-c7e2-q017 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1d02c14cb3d7b742d73d8ba20669e8439ff0628bc7132359414601ec53570750` | answered | `c3a494b29c0be655bd5bf269bdb26299b8f0e66dd9cd185f4d782300c502e62b` | pass |
| fhv5b-c7e2-q017 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d806230adaeaa5bc78308e0308fe628e66e2a7e17716088c1f624d9243995680` | answered | `5ca2fed87320861591d4153cb343b2f7fe4a0c84102e51d0f91280505f2112fc` | pass |
| fhv5b-c7e2-q018 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0f38f29c0ade0e9e0ce00ff3b8a6e835d25e10cfad028efb7bc782d900219b8d` | answered | `b1a020ff5ccde962e92ba3dcf9cc1531f256b3d795e1fa1cbf3694b8900a27dc` | pass |
| fhv5b-c7e2-q018 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7136a88b5b2b9fff8ed571853f56d1e3ca68978a25f0672a8828d5d9901c407b` | answered | `949087cdff4d0b869f0a23decdd8a8b12571c7a9993b85a264f3182198945587` | pass |
| fhv5b-c7e2-q019 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `330dfa4a99a499fac85ccf35684e35e48f9b6ff93e8b6b3551970bc865c5ec8b` | answered | `32f6289fbb3118064eae74c20e726f34c328b102f413626eb5b0e6b6a92812c6` | pass |
| fhv5b-c7e2-q019 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `765f3ed655feaa05b8447ee36daeeb9a17eca64c8fbe184bc7db8c52a9ef482b` | answered | `1a7558b5bfc77556ba0368a832b14633f49bfd305d10a798ecd1b9c155fb4eff` | pass |
| fhv5b-c7e2-q020 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ae5903689d764ba62c06a7ec9aaedd737160338f14ef869841ee9825627102b1` | answered | `4d4c82e08c127ddc1ab93fa2c21d23d1eb524ed9860e33e3ce30d78efd097ab3` | pass |
| fhv5b-c7e2-q020 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c0b4e9c0ff0fccfccbdea849ee11409ae87545bd9329ef03490bb64d22e2a374` | answered | `e820c955b722a1105a353b309b9d673f515c2bb792ac84f553a975691b6233d8` | pass |
| fhv5b-c7e2-q021 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2f845562717a73a1f4c2108c052a1c79d9d9e0948530debda724e9f8caebba04` | answered | `cf36f4ff0279fa3897b99e7551f46acd850fcf6b02d1fb50e2efce8a94c7ca1f` | pass |
| fhv5b-c7e2-q021 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `13d43511c0ef453574ce8ce114d93406ea740e74b195687c6532dae7299f3852` | answered | `731943ac303b8b84bd91d5e964945a74f114335903dc203b79403bd17659e3a9` | pass |
| fhv5b-c7e2-q022 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `764be6e9e1ecfaef5e18127712e3b08952f5d7598354dc595b1b37825eb7b5cd` | answered | `390ae228d1477bf8e0f5d646e7e2ebf59179f323f1193853913caf97f2e70947` | pass |
| fhv5b-c7e2-q022 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4c17c4c6981981e7c316ceb0f74ce99785fa20c306e9f3ea691dc9a27c7b7dda` | answered | `eacc1bf3ab7b65c040acec2f17cc7c39a65e4d97e5449d41f0eea6db8b554fe7` | fail |
| fhv5b-c7e2-q022 | second-adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `07ff455e1f69108106bdceaeb499e52d5ab6e7cca90b588af522c6432241f820` | answered | `b57d4456399db0a92439e4abebd74c517ebbf337916b12910ffc0d8252d0435b` | fail |
| fhv5b-c7e2-q023 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b7338158d28498e6cd6b102bd6bec47e6f364cef49f4b464e12871b42c4b339a` | answered | `7586b4574fc4d2fd5961b279c70ba5b9ddd2b9afa8c80e202ce230d00422b8c1` | pass |
| fhv5b-c7e2-q023 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e93cd35f33dde9bbf39270544eb6169505caaa101afa1d8d7635cbd3a3698a32` | answered | `7e5804231ac1fe53d587543058ffaa61ad572ea2693565920cb38eb5e25ca45a` | pass |
| fhv5b-c7e2-q024 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1e790b1d4f123ffaac9286ea4d8d5c6385fa18215869cbf0a1ea6659eeb5c726` | answered | `2f41965013823aa9321e7cbbff9f13bd3762602a66fbe3093d65f2ea6fc10f0d` | pass |
| fhv5b-c7e2-q024 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b45a864319234c8c9e55f4ea59367e74a2223cd178b9ee5a770a3a75d9ba8e02` | answered | `2299dffb938b60c11b765c31f953ceb62716e3e178eae463e82aa153e9b97842` | pass |
| fhv5b-c7e2-q025 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f52a0ef49264a7da477640f6e3537dee40c3eb67a90d57b31ae12786428d1f13` | answered | `6a3e1863a4d5c71adba0a930cd1642b916effefd681fef6980146bf9c63eea2d` | fail |
| fhv5b-c7e2-q025 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `136389f9c2490024798c17434313da78c392824d36c78e93e490360fd77c4983` | answered | `b6ac6513806edd4cb616cfe7216c4e94f7853c2dd4c9f57e0967dbfea2ebfbe1` | fail |
| fhv5b-c7e2-q026 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9c7ccf17c5645b1fdd44d3b5e45385fcd6b3a6af13bd746242eeaf8a3dc23b05` | answered | `c6ac600f7c951431b4a9438dffddfd7fcefe4ff94f513898332ab8781532d751` | pass |
| fhv5b-c7e2-q026 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b2961b0404af5e702e5317ce25bd5ee6fef18a1e425242328fc23b5ec1049899` | answered | `3d02e0cc4c143bda4df1131d79e791cc4658939b8d93f0692b2d4c100a1d9547` | pass |
| fhv5b-c7e2-q027 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ac4730f1e11e894150d4f55063e99141cd22b303f8d7e51be63a0cecc7db7b72` | answered | `63209aea2ec28bac512fe47c39801ecfc439c23960db862e0493aae07f83edbf` | fail |
| fhv5b-c7e2-q027 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f1cc0f78d092fc235e8c139d41d425d7971f932a1dbc9d376a297f9f992a5939` | answered | `2f0d70aae97950403869a998a0e7aa90e327586e93acd79e4b5946b27061a23f` | fail |
| fhv5b-c7e2-q028 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1336a636b0e5d306940542afe19f7c95dab1c5e9f76e1b8067418a2d6264018c` | answered | `70114593deb63d020d77e87e471a5e48c88b1ce4bdf485068f351fd4dfad1df1` | pass |
| fhv5b-c7e2-q028 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `cf7b62914dad4959582f1d854fa38b855354fef706f5850606fbccada586a192` | answered | `ed0bfad2d7dacadf78eb6af9ab56aa2c075694b2d1ccf15f43401ae264d55715` | pass |
| fhv5b-c7e2-q029 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `210d0fc5ce453b0ece42f5c608035905c0de0609aa03d3880dfbeb22c02355df` | answered | `fbfb38c26c1fd8a9a57c170941174119af754374c24d09f9cc2145a118f27883` | pass |
| fhv5b-c7e2-q029 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7f7ec17137d8e428056d1574d7dd484427fed686ef02cbd75e77ff0a2d1cbb79` | answered | `79446ba031cb76484c738cc99ad538db7257b4fd5ca5ee8efe0cad8670414ea9` | pass |
| fhv5b-c7e2-q030 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3481032b6e5115e4404d418879c4e72e0857953cfa9235342eb9c477cb99a1c1` | answered | `5525d26b3adbaa962216d85f0ad4508df92dc0a631f8bb30e5b41ac040e633e6` | fail |
| fhv5b-c7e2-q030 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6023198accd13d41e75238065f7c833a9da81230ce40813fe7203dd1b8c2c0d6` | answered | `d2544e2c6e921f1a3bd761a4d052a97170eeec0e274cd80e30bda66668deb584` | fail |
| fhv5b-c7e2-q031 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `8cad48276d4509ebbb2a93b67b8e0b0e2e03543472be7d614fdaf4d9d5304f89` | answered | `95a322cd39b5b896a891c84d6446702f204da47956170ed21e978990f8a98e53` | pass |
| fhv5b-c7e2-q031 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a024749fecb6c2e86628ea63a0c21283362694cb58f6e5ff8ec64119628bc74e` | answered | `bb908c093da257c2c3990fdfab93722e3f01640b63f522a7c5fb2d044eccf47c` | pass |
| fhv5b-c7e2-q032 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5e7f1cbd3d7c7f777b51d84778f2097f4c65b68b821cf725e84ceecc14505362` | answered | `5d3df6cdf9d48da75dbe04014bdbd19a496fb0e04eab1449cfa0bbb362dd64a4` | pass |
| fhv5b-c7e2-q032 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2433272eec86cab6aadac5ad5fa9e7ae5876dc2f30e7c53423dae795b0eb2421` | answered | `6a5f49ec0544d4c9057227976aa61c1c8f43b8521e1379e31b65e0e004a92c31` | pass |
| fhv5b-c7e2-q033 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `63710493fd378db600046adfc64a1d82cf0de387f671ce8448e8f5c38512c496` | answered | `d8f35eeccb2774332c4363221097835f5b5d54dea2c0013e7dc30049540570a9` | pass |
| fhv5b-c7e2-q033 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `183e0144e9b640f810af40caea727321eb498eabd272ba01b5db7a07d3a22b8f` | answered | `2e201d06f2879cb8664df2f944d017abc57091edc45caf276d4211f5e1842374` | pass |
| fhv5b-c7e2-q034 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0d9d48c8021c70ac9520810c5f426b02529cf553a1c43dea405dbf7f23e785bc` | answered | `146a67cc446b368c15b291c2866d52501f5298e959350d18fb1a3f45f366bcee` | fail |
| fhv5b-c7e2-q034 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4f64019e916db46428a22fbefaf634e8edd89af840906f0b7cc183c3a6b4fc3d` | answered | `59eb86c62ad17746b4a41450adbf813e81988719718cb8c1de4eb2072d767564` | fail |
| fhv5b-c7e2-q035 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5f457ae355a0e23f83af3d0a3f971050fbbe4ddb7f3e86de334d2413b4f4551a` | answered | `2a1d95bb6bd4ff00adf6e4bf58e8fd6a118f9c46245ee71d3a4a58f5b6ade5f9` | fail |
| fhv5b-c7e2-q035 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `cd1c7c17e784e839055e89b4077659462ed63ab754e0e1b1c0be0300fac80b84` | answered | `ec81490558afdcb7bbad57093a59a81c7d55b1398aacd5093becd40b7135ebd6` | fail |
| fhv5b-c7e2-q036 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `6d054f8af576d9a222de7212809bf5d79f3d6a1383bab944c9079f11c31ddd63` | answered | `09f109acc6829acb871ff0a335b019de4e2ccfa28973eed667a1e3d4cb1401e8` | fail |
| fhv5b-c7e2-q036 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `8b95d62c68c3a5d65dd5cdc5090d477fcca9ac717e7664585ff9c05927c53e69` | answered | `ca96d2ce7377507e893a99371cb77d183770fff3e6264eaec621c13de7b968ca` | fail |
| fhv5b-c7e2-q037 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `887c53f81db1dc74adc98967a61dfa9ba4a7a5f195cfa1b645362191a14d83d0` | answered | `b8b6e40a9373fc7c51a7c9d791a5ba9ebea36a30eeccb7c77386cd46ba9b3591` | fail |
| fhv5b-c7e2-q037 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7d62c0f591fe53a4c4990cc3ea1e5436c836e31159f13d39330ee6bb79c62a90` | answered | `cfecfa73e88909baef3fc2069a1f18e7c79f84154c41f3c63420fc590434c5f2` | fail |
| fhv5b-c7e2-q038 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e9ab6d11b52264a391be3fefe0930e827a127f4aa98bc949f1ccd69bbc9a9898` | answered | `081686bf4b9cfff3abb446f78de46eddfeb5ff144ce61e7430e70346eae9088c` | fail |
| fhv5b-c7e2-q038 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `da43961e62e7d7f2bf059ac7312439328e27535564a3a4994f763ac48f71b568` | answered | `244b16d4f94e40eaba2f9edd5732d6c3e6061318a7e6679e8df56ed50bf40811` | fail |
| fhv5b-c7e2-q039 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4fa8ec8a2dad07ddacb1b2e2f175582dab6672cf8fda6d6cd6a8a01a8e1970b2` | answered | `b2b7870bbcbab1ee63449f6986aa7a90c81aef462a1fd5242bb1b8c71ddc34ab` | pass |
| fhv5b-c7e2-q039 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `75053746f00592ed8b40afdacf5cceddd6381aa239782998562cc29ea174c0ce` | answered | `63a5b34a431028edc26274bbc2af6293aa4dc2438a7ea420470b78769d120a1e` | pass |
| fhv5b-c7e2-q040 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ab1c1e2b1990991063c76e173971e11d54bf8fb799bad6592033fdad19ebe03f` | answered | `7cb92f527a6d3c84513f880db37e09f2fe7032434704e673fe0c6b7cbbcbeb8c` | fail |
| fhv5b-c7e2-q040 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `85ed66cc0eee5262e0aff4a1f0f454e3f0fc32c0912fbd0842c7fb518397679a` | answered | `15fa5399eb35d5424a29f72893c2c2a36c0cb1d0ccb084463eb8f5bf1698da75` | fail |
| fhv5b-c7e2-q041 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3af4f820bb23a30302ee56e195c9a35980bf61c6b15c00bf5b3c03eeda7f8bc2` | answered | `d8c75605428f126853c46f3576f73647943b998f522a699617ffe39f878b70ab` | pass |
| fhv5b-c7e2-q041 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2902c5bf13ae6b97f7b49b0ad20b03486591cd6dc1acb105b2c43979e6547854` | answered | `92dba1f43617e73a10aca496394d1da9af1d36227bc3b5fc38de347519d1084f` | pass |
| fhv5b-c7e2-q042 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `378af1c5d6e4d207a66c17ae7bf9f65a95b4a9ac4dd1c4cd9fa24391aa9232a1` | answered | `138ef9f1655643f7950b7e3be3093fd99b4137bf741dea4c715264e61fe0839c` | pass |
| fhv5b-c7e2-q042 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b11b721f20cb10ffb3df961e925761209dae2c1394d024b2243ea82344bebf68` | answered | `87045874fa35e263bea1761663d5c4142321a56831931d60c87b23dc3c68e8f7` | pass |
| fhv5b-c7e2-q043 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a50390793ec2cfc5fc88e9c9fd8aa88ffc46bfc37794b49789cabc03dc435796` | answered | `88adb6b42a104408a065068facb5c501db22f9397c5841abd15c44b85efc9ea7` | fail |
| fhv5b-c7e2-q043 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `39beca5aecfa8e042495cc8ccb84f866bfb7d915b1c2576a43a6db5fe631384c` | answered | `ae4e7ec12c1ea36c20113e850a07d741f0999248450b2a91c3c87afbb6ccd95d` | fail |
| fhv5b-c7e2-q044 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e7818c1e3034b9a3d8da63a9ab4702d5d10c81933cfe8ce32758cdc608b498c0` | answered | `d97c6345fe91763b8ea5eef47a525297f47161e52c65e87484fceb1a69f856f4` | fail |
| fhv5b-c7e2-q044 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a1c69d23e266fbe18ab5d22c4479ef27dec27e6d6faa05e2d22eab351a500931` | answered | `3aee5f146302a889354b1a3f7364620c2a1d4f738a39faa4f811b2da58d50ae8` | fail |
| fhv5b-c7e2-q045 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0e20097610615d0e22632fbca0204aa971a2e418cf26726865f6e7350115c3f8` | answered | `c6d4731da6221cdb0838b4f722465d9acbccd940d0d2e6762a040bc497e437bf` | fail |
| fhv5b-c7e2-q045 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f467a224f1855a14f5262509b7a89daceaa7695fa1ed0699526b139c1b64e84e` | answered | `d9ce2d908f9a75fbf9eec161632a338279bd704eb320b8e6db3b99d2b01faaf1` | fail |
| fhv5b-c7e2-q046 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `8ee7878a84a53802e66c1a1030602f795c668689b80eceb4dd822796e79adabb` | answered | `cfba3026fb7c2972f101d793e4fc2b1dc06637fa4bcb7ff2313da383497833be` | fail |
| fhv5b-c7e2-q046 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ab7086b5950749d5a53ab80bfcb313a33271c00a935c723007e547dad2773eb8` | answered | `db6e1a4b8988bf300a297f169abb7ecd2edea3865bcc09000b1e1b516caf9421` | fail |
| fhv5b-c7e2-q047 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `42ba32c152bdbe7f68691b5b03c68ceb74781355c8f9b13af214b2cec49098f6` | answered | `dc6f671551cd18cfaed2dc01d7bfa19ff66bb4801dab1efb07dd2a2069ba2e0a` | fail |
| fhv5b-c7e2-q047 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c131fcb0b5aa4a9f6ac7fa73a7f8212b5b7fd4787b502b168736a63dc7e1653e` | answered | `a993c96dbe442bf8122245fd0284a9ee639ae0dff0ffa156a2a347e608a963fb` | fail |
| fhv5b-c7e2-q048 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f425c7dfdcf5f21ae324e6293cab79d5a43abcd35a08aeef00d719b21483141a` | answered | `5b6395d33128290b40c7fcfa58dd6b53c39f5ca3a6489f257f6c35091490ba42` | fail |
| fhv5b-c7e2-q048 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2b6cf8d6f2e12cff6a22e625b58257d479620d2740980ff35d43e6fb6204b375` | answered | `f87109ac28ba2e99df522ee5bc9d5810136e6fac9d68e5a6e1bdf41c5cd6ae34` | fail |
| fhv5b-c7e2-q049 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `90426ad710a5c3f6b6f01561240c19b86096a51ccf3f22cf970a5ac3860acdd2` | answered | `7f14c9c6dc9523a319a75e42d0a192441b18005f5164a99fd450a669d9527795` | pass |
| fhv5b-c7e2-q049 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `fe52d453e9a615dc88664bf1da8275e55c6b00f9a6e59462a29358edac0b0ce1` | answered | `de3cea95edb44f03f7a26d17f87e3aa5ff37507c1c76db509e512996d44c97b4` | pass |
| fhv5b-c7e2-q050 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `09020a3adc8c2f5bf3ae5024865a14b55ced2d1252f237984dd4583fd8e1dd99` | answered | `e186662efcc349b3521c29d986e132fbbae44ea3a10bd81a15cc20cd7639f46c` | fail |
| fhv5b-c7e2-q050 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `3e20bf044486d4602879accaa3e23b6382827cc375518f0e38ca818fd3acded7` | answered | `2717947bc061ef98a2b288ae70eb429465efde09cc281f2be44b0100f1d15e98` | fail |
| fhv5b-c7e2-q051 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7b5e742ff46c6c524940ff3602aa6dd91455c4c81da8f45e463355a833b4ffbc` | answered | `ff6d20d84f44835613eebc731afdc25882bce3633e8532dc927772d5bd25a042` | fail |
| fhv5b-c7e2-q051 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a237697fa0366a517ee131d1ecb4c0a70cf0af8de0f4e56939615b6981890b48` | answered | `281850f7caad737f10c28fd3c3bbbf5425dad8aec86b68d0650a87824a1703a4` | fail |
| fhv5b-c7e2-q052 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3363ad5fa0943fe4026b17a0dc5e99a15b9ed5c7b482df3d740a8301e319eee6` | answered | `b22d840aec0f15b3ddbffc66bf8abb658a97599a350e21be995b80491af1d1b2` | fail |
| fhv5b-c7e2-q052 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9ff4ec0dad765fa36e564c72b6f89250b4d4ba17caa611e25efe4b2ff9d83e8d` | answered | `93fa4c9eb45769e233beb54529490f03ae89c67f6246fce252be85265e5be6cd` | fail |
| fhv5b-c7e2-q053 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `24596572e50c22837c49acf061985d6ad5fb6b1823068decb95cc6db49a2b37e` | answered | `0549c14bb4831f82a033236e74f4be03775144478ec626575d38fa8aa4fd4cea` | pass |
| fhv5b-c7e2-q053 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `294f7dfcc93bb1affa0ee726172ad24d23a19c04a64627e09b75d7d3d7bb89df` | answered | `26d843dee336ddf118c1da9a6b737f00cbc0326adcf1c29d5a133e87d548d239` | pass |
| fhv5b-c7e2-q054 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7c645ef49ab578595043b5915ea21f2aaa8d7a9756dd90705949bf0530222cbb` | answered | `89f53d76d5300496387a65b9d48c05db353efd4cf6a1d7e02b46f83202f668ae` | fail |
| fhv5b-c7e2-q054 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6ff6e4a3cb605aa31320d6ccdfc8d784ddfc26b7e1794be26614254159d961a8` | answered | `9b0273235a03669bf25fa593ae74cb13be21a433cc9ecefd66b5e1faab01ea3b` | fail |
| fhv5b-c7e2-q055 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bf2094e0e87dee3df6e7bce074d49f8761394463042b1f0fa91a18e7c628c329` | answered | `23a5694ed9809fb46a78b710f7b4d405d22dfe8668c52795a124f2e64565d56c` | pass |
| fhv5b-c7e2-q055 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `759b3b89b535bb8e2d2dff39c3e7f9a88f5422792ae871c948dce5915f050e3a` | answered | `c0fae574175ae8ef6bf9c1151cc7199f14c6f922481c2bfa42dc8882c5b0a8b3` | pass |
| fhv5b-c7e2-q056 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2205b7c914fe3596142251f8f8f27521392d658dd44b0dac25dc0462ae069b6c` | answered | `2d4bdc55100fd04c9e6aef5f327e97bc3ac1bbf18155271f334b89abfe306521` | pass |
| fhv5b-c7e2-q056 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b3fb385bfc2bb9a3847d8113d53b92e0191db678940dad6caed9d990b501b9a2` | answered | `510300c4d89f20df4150403cf77af93288b7dff56194b9330ab1a64c0e810f39` | pass |
| fhv5b-c7e2-q057 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `cddef3cdd5ac94619bc1e327687af3a2db15520fe39a3764ec7ecddf4ff642e8` | answered | `3acc2986fec452618fa1fd69f8f6b3396c89fc33f570c538344daccda3c46b30` | pass |
| fhv5b-c7e2-q057 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a2aed1d69bf63df2cdd8313e3120a68041c3b499f52a779ff3d5ec820bef1540` | answered | `8caa3e5b756d8cb9786783f5d79358d8bbb7cf1e29fc126e05084dfb6cd9a02c` | pass |
| fhv5b-c7e2-q058 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e49ffbf61cffedc5c20075147311d4df73bb7f970dcaf00c7c3974a4f416f5fd` | answered | `7e48bdfe47a01a365b63d0aa674a27d8e5b8735d14209e15a6824b7beb9f1226` | pass |
| fhv5b-c7e2-q058 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `8696b59e09d41e8eb8b8b5b7665806b9cbdca3968206e3700d5b842c7a940091` | answered | `52e091728741d24a1475dfd7093823c0750f4409020b1d8c1f6a1d6dd74dc825` | pass |
| fhv5b-c7e2-q059 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `126acd95bec1401c81e05594bc44bf595ca7ff42c81b03f992a5a76039b9e8ff` | answered | `364929a86794a2f482a2596c04f351250372e7c4637a08da247e7f16a8b82ac2` | pass |
| fhv5b-c7e2-q059 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b1dbbae9bf6bafcc33db84416adf64c8a7c151202469d45ca4bd8145c91c0fd9` | answered | `5cee4d9ecc4fc481aa5526a22778d399bb25261a5ec89e8a0368d7bed2cb2757` | pass |
| fhv5b-c7e2-q060 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2bbb0748d5519c5939c0c550ff3ec4bef3d36386e27d1263a3f2a30fcea90536` | answered | `00dcbc946e9db898c5344e8dc5dfbd1cee404367a11a0ae7da0ed3e8b27f33e6` | fail |
| fhv5b-c7e2-q060 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2a1a8363468ea6427ef7dc6c18908a9f8cd76413e0f3f5b5c699a6a4b853e077` | answered | `f3d31cde4d75752faed65b74f387a12c953d73fb8ff7a56fc2f39ac526cbbd14` | fail |
| fhv5b-c7e2-q061 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `56528cdbf77e9fcc14684d93cfe538cd2d32c64252999f8b1c9c2ce754813be2` | answered | `fd416ad0121eb8ef31310d17f8acec1100cc33ba53f37513f496577e45336cf2` | pass |
| fhv5b-c7e2-q061 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a9fbd4bd6ee6d60deacabfbc7dfb51cc275c0cc1510c5b8ad407fcff3b343741` | answered | `73d450644c415ac7447e1faad83e71e517e12ad11b37d84b85ef73f4d01d2bbc` | pass |
| fhv5b-c7e2-q062 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d618dac5c1dccf90bfe99507dc1cf09c8b4e1c036a392fb7a88287da19a2767b` | answered | `abecb7afeb8abf914df65a01208f8c7c49c78b10b9f6a7c21f9dad64ce86abb6` | pass |
| fhv5b-c7e2-q062 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `40b056e0025f84ba21713137d8ef45b457f123ff729da6e5fd225a8d253bdefa` | answered | `8411e6c498aecc3eca3a32e28fb28f342eeffe0109223b5b547ec8b34d6f258d` | pass |
| fhv5b-c7e2-q063 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `35804f6f7139b34da6de8755c5dcb9ab849c2292f48150992e934d6f12defbf8` | answered | `fa5d1895b72ae18c9e6585528099b14592f92c55cbd59cf06a306dbc806c82d3` | pass |
| fhv5b-c7e2-q063 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f21ff5a5f9f69db1e7d5675210ad1c9e3c20f2d5d310daddf77a3d58d4459c00` | answered | `c6864129e46155293e75b6ba8e8f3749bdf8464dab0099985c1f66d9b72a7315` | pass |
| fhv5b-c7e2-q064 | second-primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e59ee09640672790599347e6a5182fd96e13632a7a26a61fa6200eb04e107292` | answered | `9cf3c4ebbef471a6cbe5ac08c66240a8ee9050768a778df450a239a4d8a66df9` | pass |
| fhv5b-c7e2-q064 | second-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e738071df5f48f7972c02140b22bf2d11683dec7fc80fffb405b9122a24a99b1` | answered | `308b76541be573a0d494c275bbaf63facad9adce5dff3449bc51108e86e4b442` | pass |

## Per-query outcomes

| query | stratum | outcome | reason |
|---|---|---|---|
| fhv5b-c7e2-q001 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q002 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q003 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q004 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q005 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q006 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q007 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q008 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q009 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q010 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q011 | exact_identifier | pass | both primary grades passed |
| fhv5b-c7e2-q012 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q013 | exact_path | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| fhv5b-c7e2-q014 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q015 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q016 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q017 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q018 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q019 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q020 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q021 | exact_path | pass | both primary grades passed |
| fhv5b-c7e2-q022 | exact_path | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| fhv5b-c7e2-q023 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q024 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q025 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q026 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q027 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q028 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q029 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q030 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q031 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q032 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q033 | nl_behaviour | pass | both primary grades passed |
| fhv5b-c7e2-q034 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q035 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q036 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q037 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q038 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q039 | architecture_flow | pass | both primary grades passed |
| fhv5b-c7e2-q040 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q041 | architecture_flow | pass | both primary grades passed |
| fhv5b-c7e2-q042 | architecture_flow | pass | both primary grades passed |
| fhv5b-c7e2-q043 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q044 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q045 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q046 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q047 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q048 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q049 | config_docs | pass | both primary grades passed |
| fhv5b-c7e2-q050 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q051 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q052 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q053 | config_docs | pass | both primary grades passed |
| fhv5b-c7e2-q054 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q055 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q056 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q057 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q058 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q059 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q060 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5b-c7e2-q061 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q062 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q063 | ambiguous | pass | both primary grades passed |
| fhv5b-c7e2-q064 | ambiguous | pass | both primary grades passed |

## No override

There is no flag, environment variable, configuration key or report field that lowers `k`,
waives a query, excludes a query from `N`, retries a graded response or forces a pass. A
missing, empty or refused response is a failure for that rater and a failure for its query,
and is not adjudicated, re-requested or replaced. A pass count below `k` records
`RELEASE: NO` and exits non-zero.
