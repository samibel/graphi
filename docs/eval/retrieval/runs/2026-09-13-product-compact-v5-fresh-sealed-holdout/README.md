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
| observed pass count (as reviewed) | 32 |
| observed incidence | 32/64 (resolution 1/64) |
| observed pass rate | 50.0% |
| exact Clopper-Pearson 95% interval | [0.372322862895, 0.627677137104] |
| lower bound clears the 3/4 floor | false |
| primary disagreements | 2/64 |
| adjudications | 2 |
| missing, empty or refused primary responses | 0 |
| **RELEASE** | **NO** |

- the reviewed pass count is 32 of 64, below the pre-registered k=56 (reviewed 32, corrected 32, and the release is decided on the smaller); there is no override, exception or waiver

## Capture binding

The rated bytes are bound to the candidate implementation and the indexed checkout this run
names: candidate `fd27bc083df69913746a3f1f10fc13145733edd2`, checkout `a0a6ae020bb3899ff0276067863e50523f897370`, both worktrees clean at capture.

## How `k` was derived, before any response was opened

`k` is the smallest integer in `[0, N]` whose two-sided exact Clopper-Pearson 95% lower bound is
at least `3/4`. It was derived by code from `N`, not written into this document.

- `N` = 64, read from the sealed dataset (`9f2289c71bbc8515bd58b0210ddcf528eb5be427896918b3e5a3d390ad10d5aa`): count of answerable holdout queries in the sealed dataset: split=holdout, stratum!=no_hit, at least one grade-3 span
- `k` = 56, whose lower bound is 0.768473694033
- `k-1` = 55, whose lower bound is 0.749763164375 — below the floor, which is what fixes `k`
- method: two-sided exact Clopper-Pearson binomial interval; the floor comparison is exact rational arithmetic on the upper tail at p = 3/4, and rendered endpoints are bisection brackets of width 2^-64
- pre-registered at 2026-09-13T12:26:20Z, naming precondition record `33db6a8f09ac65965f60b2a74c3cb744c64a67cac264a2d671ab42bdbaa13243` at commit `fd27bc083df69913746a3f1f10fc13145733edd2`

## Per-stratum counts

Counts are authoritative; each stratum states its own `1/n` resolution.

| stratum | passed/total | resolution |
|---|---|---|
| ambiguous | 6/10 | 1/10 |
| architecture_flow | 3/11 | 1/11 |
| config_docs | 2/10 | 1/10 |
| exact_identifier | 11/11 | 1/11 |
| exact_path | 6/11 | 1/11 |
| nl_behaviour | 4/11 | 1/11 |

## Participants

| role | id | provider | model | took part in this track | basis |
|---|---|---|---|---|---|
| primary | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Stable logical primary-A configuration: concrete non-alias model ID gpt-6-astra, codex-cli 0.153.4, reasoning effort high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item. Preregistered acknowledgement before dataset completion states no candidate implementation, dataset annotation or prior holdout participation. OpenAI exposes no immutable backend snapshot/build digest, so model behavior is not byte-reproducible at that layer. |
| primary | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Stable logical primary-B configuration: concrete non-alias model ID gpt-5.6-sol, codex-cli 0.153.4, reasoning effort high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item. Preregistered acknowledgement before dataset completion states no candidate implementation, dataset annotation or prior holdout participation. OpenAI exposes no immutable backend snapshot/build digest, so model behavior is not byte-reproducible at that layer. |
| grader | grader--gpt-6-astra--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-6-astra | false | Stable logical grader configuration: concrete non-alias model ID gpt-6-astra, codex-cli 0.153.4, reasoning effort high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item. Preregistered acknowledgement before dataset completion states no candidate implementation, dataset annotation or prior holdout participation. It receives only one generated grader packet. OpenAI exposes no immutable backend snapshot/build digest, so model behavior is not byte-reproducible at that layer. |
| adjudicator | adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 | OpenAI Codex via codex-cli 0.153.4 | gpt-5.6-sol | false | Stable logical adjudicator configuration: concrete non-alias model ID gpt-5.6-sol, codex-cli 0.153.4, reasoning effort high, and one fresh codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only process per item. Preregistered acknowledgement before dataset completion states no candidate implementation, dataset annotation or prior holdout participation. It receives only the original allowed prompt and no primary response or grade before its answer is sealed. OpenAI exposes no immutable backend snapshot/build digest, so model behavior is not byte-reproducible at that layer. |

## Adjudicated queries

| query | primary outcomes | adjudicator | final |
|---|---|---|---|
| fhv5-9f4c-q016 | primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | fail | fail |
| fhv5-9f4c-q059 | primary-a--gpt-6-astra--high--codex-cli-0.153.4=pass, primary-b--gpt-5.6-sol--high--codex-cli-0.153.4=fail | pass | pass |

## Frozen inputs and the end-of-run comparison

| role | path | frozen sha256 | observed sha256 | matches |
|---|---|---|---|---|
| dataset | `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-fresh-sealed-holdout/sealed-dataset.json` | `9f2289c71bbc8515bd58b0210ddcf528eb5be427896918b3e5a3d390ad10d5aa` | `9f2289c71bbc8515bd58b0210ddcf528eb5be427896918b3e5a3d390ad10d5aa` | true |
| budgets | `docs/eval/retrieval-budgets.json` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | true |
| targets | `docs/eval/retrieval-targets.json` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | `07e26ef60407bc8437e33333712c888ea09c13ec8d3c27389c24818f7832694d` | true |
| grading_rubric | `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-fresh-sealed-holdout/grading-rubric.md` | `f0420f756d3a5742ca684916965921bbb6794010af781aaf129e61ba7f07457c` | `f0420f756d3a5742ca684916965921bbb6794010af781aaf129e61ba7f07457c` | true |
| methodology | `docs/eval/retrieval/methodology.md` | `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d` | `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d` | true |

Compared at 2026-09-13T13:05:57Z. All match: true.

## Capture provenance

- capture instrument: `sw280-candidate-mcp-capture/4`
- transport: MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)
- payload boundary: `mcp_jsonrpc_response_bytes`
- candidate: `task_context/2-compact/4` at a 1200-token budget
- repository: cobra at `a0a6ae020bb3899ff0276067863e50523f897370`
- embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (model `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b`, index `147:static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b
40:e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
64:75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c
64:107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45
3:256
2:v3
0:
32:6543e477319536aa1da76c5371dad981`, generation `g-12a4691e8c247dec`, 768 persisted vectors, state ready)
- tokenizer: `tiktoken:cl100k_base:ordinary` (vocabulary `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`)

## Content addresses

| query | query text sha256 | bundle sha256 | bundle bytes |
|---|---|---|---|
| fhv5-9f4c-q001 | `4a935727d5672fe2250e9bf95e30517806aba25ff3c6fb933fd72db0db6a6239` | `e812e3eda4a63f6a29ce75f4c03e513afe184618c00ddf893de6bf5baa85c3c2` | 3405 |
| fhv5-9f4c-q002 | `7f27a03f14c9ffffeb50db74832d0b5e3d44c03c1cf9f94b9c73a807c0733f9f` | `0a8492d4f72841f916203f14b3958bbdce69c776d03ca72aba2dfa3ddc079b42` | 3228 |
| fhv5-9f4c-q003 | `b16edefccfa64e49a5a4862cdaae389f5e74acffde28e06485e9e2f0c2c29b27` | `d8875f07129275f8316b9bb2d91cf9e0c43867db849885f2c4b64a8d5c9faf54` | 3225 |
| fhv5-9f4c-q004 | `b142f4d633956619c39b0f94e9efc689a118e1ebe979f3ae2b8ab275a579954c` | `0a6f9a26ff4bc644db19cde75a69d0bc2f781f95816aae6aa7f8e7c12815dc73` | 3277 |
| fhv5-9f4c-q005 | `f722f42ba292bb6d96b08a5682b93fa62a51c5263f421c0e15cc16ad612c8765` | `f858d065422ba5fc1239af803103cbed91843336954b8ab2c5f97831b77c40fc` | 3554 |
| fhv5-9f4c-q006 | `2df29377b149e99608fa6dcb694c4835ef6866dc926e70066d962f91ade5e826` | `c91363897fda66d25408243c4536be78e6ab71afb501c740b299fbaf0848e751` | 3697 |
| fhv5-9f4c-q007 | `c868f1e7d1c2c29aaea87815883bcad83b1db615c56fb9c9e3f0c5de96db3603` | `1930a07c49e06b0de372a9444f947c2cd9d870c6979f0839626e0bf93aafb5ae` | 3044 |
| fhv5-9f4c-q008 | `f687b5b3875d5b689edae431a6b9724a43688b9614119fe32f125989d657f808` | `db4b974783db4db660750e1093555ac5c1785879e270305fdf52ee35d5d9ae4e` | 3024 |
| fhv5-9f4c-q009 | `2f25ccd547b0becf5f555bc126c95193ced8297304466329eaf874a7f0cde1e9` | `ecb91935b1c6b4222df265f3ec3c45d691f2312940fd1b6ac215896438219be3` | 3449 |
| fhv5-9f4c-q010 | `7c2f3c48366fa3b4b98488ab85ce7cf6b30df9660adb57885c466149b7462889` | `e757d121ca81cde2e61e1597ca830938910620d82c08f6b94513262e222d5a2c` | 3204 |
| fhv5-9f4c-q011 | `e5dc375104456b1767376b6f34e292037ede2f4f708591d2805a246890391044` | `8edcbc85e2e3388dec6e43da9300139e1b136f06c12f3607d4984003a8d0b06b` | 3169 |
| fhv5-9f4c-q012 | `2e02e176e3eb717965d38466df19d6e8ea8b7bbd36c115481a626db8155c2203` | `08d5b326dcb2e8735af6f3ba31d57476f261e57cd0e19a3be5c9cbd443862a56` | 2886 |
| fhv5-9f4c-q013 | `086040d39b18fcfdf975756749d1aabfb019fbe9bb37e5fd0203bb3428cb10e1` | `c455a0590306f1281e10ca462407de34c99a3b0a811b568d1460ecbdfa45f795` | 3325 |
| fhv5-9f4c-q014 | `45cb4726b41fa11c2ef4806dfa4884351f326507f65d2a6ab8030b7d2d8089a4` | `0a97858aece54ce015df6f716822f092a5f8355ca22e4485b0c0e8dc67230baa` | 2954 |
| fhv5-9f4c-q015 | `6c3d2b24f4200515bba33f161ca08fb75a636b63834fb3cd824555e5b90459e5` | `b00839cb9d73c2d4391b3efc24fc8135bf73e703204f26e7f137e1d6278c4820` | 3716 |
| fhv5-9f4c-q016 | `06145d73a68260d350abe0891b69750b8fd5e28d126c965f4becb88a09a2ffbf` | `d18ac57a59646b5a381005554f6602950d5d3e8c6f8af33e981153d4e337aa71` | 3399 |
| fhv5-9f4c-q017 | `87ba2b24f59ec403e839f31428b290207874391ca35ca2c299d75efd5e8ea88b` | `9d6f7d4bb8483ef7ba8efa368d45a11d49fa7a6a75d2b4814b73bb57548115d9` | 3173 |
| fhv5-9f4c-q018 | `08e84bd3874e1a4d9827c425360f96d2b243f252d05ddc81a316259c8fc6654a` | `f9c071dc40438df7f852fa9b786ee8a6746888473d7f6a446e88d620bfce1edd` | 3320 |
| fhv5-9f4c-q019 | `f671a80e9375909e9fddb22e90b689e21ef45cba193ac3adefbcbec34a2ebf17` | `7d900166ca750127809a9309612604b74103a01595ecb1604dad7cb8643db956` | 3455 |
| fhv5-9f4c-q020 | `f4f566cf0bf53368c030b72c57f45c7e6eb755930863ae20c30061738701cd75` | `19c60bf2066e800f3ae2fb5a44b4160bbb7c159828a9d8541ecca2348cf71c5c` | 3692 |
| fhv5-9f4c-q021 | `a1eb764960cf5b36df27652736484a969b5c0558153e9d8a43fda045aca360f1` | `30515256d856cec091c0420f4f1051e6575ecc449dadd10c9d633afbeb26bbdc` | 3929 |
| fhv5-9f4c-q022 | `528084fa84dcde6a482457f58f2e1098f49db1f7915d95e0296ea0ac25b90b0a` | `c1571b6c773e86504605384222d78d086c7e7ee6fa590b09a66b3da339f0a7e0` | 3004 |
| fhv5-9f4c-q023 | `101260eb622f876e913834a907a80474a8b9d8e6303c6b033e13f7613985c8f6` | `9c5962d28a299221e00ad95e85a29e54a21e4c9115fafdea1b7a3995a4686fdb` | 3066 |
| fhv5-9f4c-q024 | `5057cb1ae3dce34b2e3c440dc82e5596af888a2c48b8d8f5bcb447412dad9731` | `c9a7daf0a399d5edfaa7ec1eff6362d67468b42f43ba5964dd69b5d5316cb790` | 3542 |
| fhv5-9f4c-q025 | `652e9cf4d1f03cf9e8cdd81db3a11a7148a907e5817356d2569540ff5f7b80ac` | `9a27bdbaadc6cc76eebe60d58e989197bd3188e790bd2842c3a95622365fa9e9` | 3795 |
| fhv5-9f4c-q026 | `95575c75596894ab978abc1ae52bd8939d0c1f29db8bd9947558bd8e38a8b19d` | `7bf6345029eb8e66056a2ef933a74ee698b9fc970cbb5a0756f7f851afa5020c` | 3503 |
| fhv5-9f4c-q027 | `b857d9996e64bc0f9120a967b55ddd7a0a0bac04350ecf9ab1921c1f94198ab1` | `2e89c7c321591fad07f28d6a15b746b3d0c3d09acaaaf589c4b37f7f9c7fdc55` | 3240 |
| fhv5-9f4c-q028 | `290536dac13ea859491b45639208cd5d8770bd9fb18e09feb0aff4b33fa94c72` | `fbddf7455317fcc0779b904cfceb15dac6a1d7ff5eff255eba1a8fa5f8198cc4` | 3337 |
| fhv5-9f4c-q029 | `433743eea3c65715ee00595d13b88e172dfecbd326361d211e2d3a553fd6106d` | `b1470131b94f43b02870f16776657ba145e3010a7b85393d17284f7b326ee92d` | 3258 |
| fhv5-9f4c-q030 | `5a62630292daee20b6b51e495be4ec2a1726abe5e2963fd797a224787fd858f5` | `afe9c98a1dee1b8a1f6cbdec736e066c6ac72fc6d9bdb1988cc725f706bc690a` | 3430 |
| fhv5-9f4c-q031 | `fbdeeb7ee7dd065a4c0071d388055be82190f58ba70f5c2de79f63ea6569246b` | `ae5f36bcdf319ae2229b5fd37a2a85b7eaa688b43d1075f8501634eac7209e10` | 2903 |
| fhv5-9f4c-q032 | `95644c89afc4e5d57cbb7eba27a77ba111cfdfaee56a9349a2f7d325ad666a45` | `84f02166675ede7452fa1ff1f73cf0892ce2a7c1838fe07cec114a9faf09d7b2` | 3297 |
| fhv5-9f4c-q033 | `0863431988d2bdf4e625c65f351a6f99bd0a4a328f011fbdde74f98dfbb0b120` | `c07c43ecc2bfeb409e10a90b4762499d43199df35a01c387910d8b6a1e2587bd` | 3188 |
| fhv5-9f4c-q034 | `fc8e3c80544a61784f1425455d64ab021818929ddbc5b75f3a9229cb3363842a` | `2aac6603d688069e8ac6603dc3c08e698da921adce44ef3e555856c57c36490b` | 3021 |
| fhv5-9f4c-q035 | `bfb77b286ab8e71a59645ccdd2287ba76a44c3948e59812fb7e63c4eea4f6c2b` | `204d7438c4bd9dd5c713794c744e9fef0624f67e8a3d520ce49a048321f55923` | 3386 |
| fhv5-9f4c-q036 | `86af27992ca72899106b4c8b26d0e2d649473a17e03426246e5d1e8a5d5eb59e` | `686ebc9108e0353e562973b987a749d27467171173123d29d2c5d601b67a3a3d` | 3344 |
| fhv5-9f4c-q037 | `a9b09e4633e7feb2805f4e6c8d8803427cb9bbf29d1a374b6fc46a8a26144936` | `1f99daf9db7004e7b38d827a0753ab709c51af1b5007b56df2de4da3e5c75fef` | 3341 |
| fhv5-9f4c-q038 | `aa5ac9c3b6b5acf3741c241d49d9194cd5c6c40ccede664e270e8e8fdf63d573` | `c3233d185777405fb64a3daa63875b2859c8426a06462b879509f2fba4623094` | 3613 |
| fhv5-9f4c-q039 | `81dd63a62db3e8f27eeb68aafd5346690de962a2db0d03796d305ced96def3ec` | `ab301f157f721fb32561f255967b4f8cc67589682ecac098e453487ed0d59690` | 3395 |
| fhv5-9f4c-q040 | `f20dc3eabd1aade4252482640ad90810b7399baeafd89282a46844dd6b4a945a` | `e403f82a8a83e32dad3387f92b886df69ac36c675779819d870bfff056e93da1` | 3428 |
| fhv5-9f4c-q041 | `bdccb835f1cc79a78707bba8c0c9e7a466bf874c77840d228662312d5cef64b6` | `27d2c39baedf55f0d03b1f6a6bcc5a5bb64974a78e796614fc217b146e29c406` | 4027 |
| fhv5-9f4c-q042 | `5e1fe311e805c4ef0a56d7b4ee9ed6ed67b5aa3d9bb88a42c9389566922e0f64` | `b78ce0cf440f7bb988458fc4402009f8b0b25d608238bdb04f0cf72d1ce1f75a` | 3577 |
| fhv5-9f4c-q043 | `e20fd2a5b591a0750e215bf398975269e4518c80ab184cac0f0b5a4144190c64` | `2da0e9d38fff2b58c7a1a2b3b30c6882ac5b693d8df3e018ddafb081d1d11613` | 3388 |
| fhv5-9f4c-q044 | `b39f17a943b02670c0bde1ae1c0c71070f8a52b76fae21208eabdf598fb303be` | `bc3dcbc88f209b8469fe8637ae3e82751ee96c84a5510e75b0fea2ef45202e21` | 3603 |
| fhv5-9f4c-q045 | `044e524efd041088bb26b2ab957ff5bc61cc1ee04b174de37f44fb1978ae8077` | `2b6982258d1707f65f121f899088daedab831fdbb69584cb2d71c4a5b9997bcb` | 3377 |
| fhv5-9f4c-q046 | `66e474a91af6b3a987700f8e5108e772d1e6895b8ba1d8ead519a58efdcd0edd` | `9a5e125aa09220180a3c2b3731f4de2083ea4cbb0587d3a29b8c355b8b3b46e5` | 2974 |
| fhv5-9f4c-q047 | `2aeb34120ac1c15311996e798bb8c904f6338d7f1657a88c5d71d6560600c21f` | `43faec66082e8ebae92eb1f185308c98c538f48a99240a24d2eac9583f8c071b` | 3259 |
| fhv5-9f4c-q048 | `bfa52870cd3f9ee338a6dda635538cbb338250b6ac18de9f127507a07788bbe8` | `5a4465d1d248e48141fb1bc96fd17652a403be62dece792bf55e505c6cd1de28` | 3372 |
| fhv5-9f4c-q049 | `1a092759ec1bc9e3147b9d245d6011f574a2075a4d64bd388cb12e06b0a4ce01` | `fe38f76976ae018dce1d12fc1a003bbba3501703c54061aff3e3ca3d4add324a` | 3431 |
| fhv5-9f4c-q050 | `c00c96473eb3d295b2b714f1c6d4692268c69e018ade668d01ecd8e8fc9c7b7a` | `ac1c26984c72dbea59a03c3b1ad9663266a4b7c003019b3dfaebeb1eafb83a26` | 3762 |
| fhv5-9f4c-q051 | `a2f33ef73936b96799badc3878efdf92e89c672fa13b5429608d27966d3fc165` | `0c4768fa1f30081aeba1f39f64216e55ff48258cb2f1a79eda7750bb53b394bc` | 3655 |
| fhv5-9f4c-q052 | `9ee81f354804483c1564de4812b6668631eaee6379e87a2d1f92040fa74973d8` | `f507dd0d4a41cf9744d202d31d62d1af4f1300cb174137673bab976eab81092c` | 3395 |
| fhv5-9f4c-q053 | `e6331eca832d7c3d46787c09a095c07db3049cbc9fb3bb0c233de54c1e283c61` | `130ac4253a22be42c24800895fb72121ac1755a9cfa5a27beb09b3e264a39f5f` | 3186 |
| fhv5-9f4c-q054 | `f54d65c6a3a6ae2dc9bc4a6821d39a505ac8ff0c8d7e8420ced0e540cb56c9f4` | `19f948d8a8d73ca9c3905bb98b5de690595f7243f3b0aeec86885f4421884c5a` | 3421 |
| fhv5-9f4c-q055 | `58cc4543a79e99fb248149fc4abea1243da9b2cf3753219148686d3b8cc5312a` | `2fc02566744d7eb3f9b205cc2556938a91a96b4f353bb6a07b38492bdc6f2120` | 3074 |
| fhv5-9f4c-q056 | `db24dbddd6ef57c50aea7b852a10acef1741fa041def80924734748985b23f55` | `ce9583da5a450206e15b7a93260d1a8d4de6c0726f86ee0013fc562e620e62d6` | 2986 |
| fhv5-9f4c-q057 | `79535fea1fb0012261fcdfbebdd17e50a0bfa8cc3ea2345fecaa5f9ae6dae9db` | `6bab71e8182ab95263b8c54d1286d11692eb7ea812b36dd3991381794fc1a16f` | 3129 |
| fhv5-9f4c-q058 | `be0e1f71a293c7e11547c890d3264b2a36eb7526df2f75b67ba2da48941cd68c` | `d40c7c2d87bde6e9172e5904ac878e5facc5751d654aa0b1dcad732fa9ec3188` | 3386 |
| fhv5-9f4c-q059 | `315c4972e1db1ce94c4183d1399a1eadd73ed3a8a22768d93716d261ed19d423` | `793b43bbaebc7385520aa4e724dc8782cb544bc704bcf501194ae2650a74afae` | 3160 |
| fhv5-9f4c-q060 | `e23e456e449361e3d1e04b7459611e1b20cef0fd1282fdcfcb716749140051a8` | `47bcfd7fb78d868586fb48948e078141321ae06c046f7d1975b9e03fcde6f41c` | 3422 |
| fhv5-9f4c-q061 | `e45995abca84c875aad8fbf4d3cba88ace8a8d5b37cbf2bf1f48574ad4f5ed92` | `ed5c059042865d9babddc854c60c02f1d09d9edf8b3350faf53c085b590b264e` | 3079 |
| fhv5-9f4c-q062 | `b5130606913e3aea30b52fd14738a6c3b9527227880586ad5fadf17ccd57d9b0` | `49d13abb84aba19f2d8ce7f680acea0ff0831f3ea5e72e4a938c7de283f50cc1` | 3019 |
| fhv5-9f4c-q063 | `3b0f44d61dc2a8c74265b905df2853d9e29040190791072b3efffb194e9c3293` | `6b87972a294be1572cb37dced22c09195578879d87dbcea2e6e753d5a19bf775` | 3793 |
| fhv5-9f4c-q064 | `09df8b1064329e42614e577f2895624af2145ee215466fc1cc7e4221163021e5` | `310313e29730055a03077d374cb799384b0db2f778526150aca4d02f816476ed` | 3452 |

| query | rater | response sha256 | status | grade sha256 | outcome |
|---|---|---|---|---|---|
| fhv5-9f4c-q001 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3c96693191b33511bf1cbc577f348f60ea008db0950dbaf673a51dc6d723a6c7` | answered | `ed217ebc799a24083ed116dbb1217326e0bd67ecfd2ae29b0caa44a978450103` | pass |
| fhv5-9f4c-q001 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c106d38ef7186766876bc10d4655c0a4b3fa69f9c731ea9354d4b5a02eea136b` | answered | `88f74baddb523ce475992ef586a5959b390c9206502e836ea9e3a4be6d3766f0` | pass |
| fhv5-9f4c-q002 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a4af2fb5fc7678bc434ef0de8d81482b6ec72bb82f7f51880036e5292a058eed` | answered | `6d0ba182e0217da89afd788e2e145dd2b4d8b000869da8ef10fdbc2d63883c32` | pass |
| fhv5-9f4c-q002 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `25b1bebf3ef043699f2d9e8796759d986a4050088f22c999160725eaaafad8d2` | answered | `464db77177dd934c4d5e1e6277096ad18f7343079f9aec0144fcba3de4b239e2` | pass |
| fhv5-9f4c-q003 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1ff5357ea4f9ebd636bae2c8e1410703b80f955af496f844fa221e36f685e516` | answered | `e0552a2c6452b11bbcef272648f73e16c8b07163ca5beb87e278f942ea618ae2` | pass |
| fhv5-9f4c-q003 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `15e0a65cdcbb59ab879e5743ad93f966fb8fbdd11fcb0e4610feca29a9a207bb` | answered | `ee79c62f95aa2c0316b69a1d7f06f5a310010c43abc505e980bf85ae64a82ec4` | pass |
| fhv5-9f4c-q004 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a9ee9ec96ff510692d24df9ddb25c5193daed76c2812a9fcafb90282cc523ddd` | answered | `54eff0e912536bf945e7cefb7cca248f7e8d080c64a7074ebf3464b2216eb2b7` | pass |
| fhv5-9f4c-q004 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `ff9d4166676afa8cab9a42f97cf656f91397c8fda90f9605579457a24196f6b0` | answered | `3c387b1151da16886147a399e32ae7c6ab928eaececf4f21208a6bf69493462b` | pass |
| fhv5-9f4c-q005 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `69e4967e2451b7b8e7563a064473502ff1126143cc486cab20281c3156aa31e5` | answered | `ccd395520e8701b36d9b41081fa08e95379a484459080121b3eb025b1946d7bf` | pass |
| fhv5-9f4c-q005 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `495682c142d7a3f2e2e2d167e9fdf05f001c76feb2414bedabaf32bdd5f40b7f` | answered | `330d2114c17f7679e23a0fff15f9f266e117aac6f4c85f3ebc11c7f1ffb51c55` | pass |
| fhv5-9f4c-q006 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5b3ee58ced766d17305aabc1f213ec8a59f04d147d58c4b1250201541ef90c74` | answered | `c11758ca94a5c20bf972bded320fcabf6122151d200b4811a97337095a926318` | pass |
| fhv5-9f4c-q006 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `05457d1920e11e9ea1ed1399f4c4f0231867e0e0c1e1420bd3c26cb18160fcec` | answered | `2e5733d7d3b0c51ca6cf37172fa7111ec29e049ea133d96448d5907723c13335` | pass |
| fhv5-9f4c-q007 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `762b2e44a61158efdf60f7eb992faaabcfa50841a2dcd14cebbb3ee197666bdd` | answered | `6ffdb239d32e5b99c336b356bd7454fe029edb18bb55311197112a9587e2d943` | pass |
| fhv5-9f4c-q007 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `651c8b70f73fce64cace37b97573e1763ac2cf696fa13e7470ea5b6f8f9ed24e` | answered | `3eb9df3ac24f8d522eb140b1573e172187898a48809d8ca0846f8fe7a13f8f01` | pass |
| fhv5-9f4c-q008 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `fdd01507b7b9f22ac974eaa8882cc6586b146495485dce1e1284205b0cb8f470` | answered | `ff85c524f5c9d701da13dffe42aeafefa409bdc95f1bac4495c8aeb20d7e7847` | pass |
| fhv5-9f4c-q008 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `63be62fcea50ae0e2ad92db07464fc8bed82a2f514420e7c8c63cc9e8f707373` | answered | `a8cdaa9e13e94e3f893bc8dc53e95b68f13aa2b7ab763dfed574e711f0f50d25` | pass |
| fhv5-9f4c-q009 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `179ed86c151f5c8b850858a834cc2cbc24fd48aeb4b49f8ce84ac5db674bf5e7` | answered | `ae3f2b23492fd2a94adce3a1a2400b899c09c26682874a534877cf00c7b2b254` | pass |
| fhv5-9f4c-q009 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `91bca75044fe106db2fc132099d33146294c897cb91d9315dfe892d1fdaa87db` | answered | `60557a20de06634f69b0f8f2f1203bfe2057882aa8ea0d4a2209e42cf463bee2` | pass |
| fhv5-9f4c-q010 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `25cfc7cde17e849f917fbbed4270669f6789eff7d708025f2d1f8e58a5eb918b` | answered | `3ed3f94b9d119df550e018cd871200261f8fd584c964cc460f935b56d9102030` | pass |
| fhv5-9f4c-q010 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `77c1d2da795824dab4f9acb8664f9e2901bc3dc7f87f60defc7a322ce940cbe0` | answered | `ade366358a9cab55f074569b542e26fff6e6cbe94d48a0892720b439ee419b2e` | pass |
| fhv5-9f4c-q011 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `80a620f8f98c99833e7cd7c585f0d73f7774687cac9b58e01e795416146f4f2b` | answered | `6e38694da18ba842816ac93ff7c1938b42632041cf6a521703b49386cccbd73e` | pass |
| fhv5-9f4c-q011 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4a9493ffae6bbc51631f81464dbceca96426a73a9568b5f0ed84ce52ee482261` | answered | `d0c58e191151dd31a0bc9002d479ad9dca31614d3abf0e51688c80a6df845c4c` | pass |
| fhv5-9f4c-q012 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `5e80d47e2e54aed9543158df2d469428ccf28537ccda481d56054311848d0d78` | answered | `1f331488883baef2cc1d96e70452e2f29508676ea5181168dd47ab11162ba2ad` | fail |
| fhv5-9f4c-q012 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4b82cb0ff326ff1b86ede09f56fba0b042856a57f7805088d2e6381bb1856f37` | answered | `45da7e70579fda670691723eb62223f969c424e21327acb3d29bdd85d615cc26` | fail |
| fhv5-9f4c-q013 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d741bfcd8bdd7102cff44bb46a419317ac740b8be87ccdf6457b69bab904493e` | answered | `c79d54b3cd0b6a078edbde3a6d4b01580b78314c88c073bd6792ba6de6c56244` | pass |
| fhv5-9f4c-q013 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `fb5a375fb08dabe3062b3089743d006a160373fb9dae556711e47a496bbbe3c2` | answered | `8858b66c005e1562fd399d88fb0e5ee664e63e5c7782f513722f19137e98494f` | pass |
| fhv5-9f4c-q014 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f2842c22503ce15ea1e22ff8b4a676b6b659910883d3c1f8e12e947162e0a19a` | answered | `088d27a8eab18e8f2e534da4cb5a6022e1ea8794a32f6f7147254d949061653e` | pass |
| fhv5-9f4c-q014 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `62e7ddf977a1e625ce0ebbc9a97847e2aacf33236e526c84f908f29631b8c500` | answered | `ee4b6e193fcfd7c9e771519eb3245174833574cc28331314aee46908dc5f2b24` | pass |
| fhv5-9f4c-q015 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e5be269c4b6dd473d4227fd88565b70dbde18baad9f3369e927938eec7d449cd` | answered | `dd71465a79e3f65053cfa4c78dec283fa144d4edc7e93521eb4292d5dc9dbf7e` | fail |
| fhv5-9f4c-q015 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9d3480b537449001fe4667e541c478e8033c25c17bc554a623b8705313d7031e` | answered | `ade4da3da86a66fcab1bce20eb87e7b7786afa3210e0a584b143f2ce075ce55c` | fail |
| fhv5-9f4c-q016 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `e5f1143d3bd7506f837fc48f66562eccec927a5cf492060bd3131360e3cafb0e` | answered | `f052d514941b17de06fc33bfee047f76bdb53405332fab382efdb326109435b6` | pass |
| fhv5-9f4c-q016 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `52c58c1c94581f59949b71525cc0dc46700aac18d44597bcc255ea27d5f53e4e` | answered | `d0ad2797bbc7441f7382a856500e4619e9bf6975c6c293fbb7460abc9048b3a6` | fail |
| fhv5-9f4c-q016 | adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `a144216b096307b10a8ec92da6f34893bc9c28c976f6d8d70d6ac30cb3bb1acc` | answered | `676f7c360f73f01122a0618611932fa1af189b25319dc7d2a3550c3a58c49bd6` | fail |
| fhv5-9f4c-q017 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `caf46b2e6f2f2f9eb973b7803a16132b4a74ea7d93bd23f6e1456ea5464cd47a` | answered | `3a37101c61fd1204a55a02825ed7291333ac710b4a37860638bd63e6302d1495` | pass |
| fhv5-9f4c-q017 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `a679ceb4e7a175c83416b08abc827d099b4dbdef6926bf3d0d69b2e135e1effd` | answered | `ef06a89fdedc7d2c9efa539c649aa058bb730effae0a9ac75f69aafc202a59b7` | pass |
| fhv5-9f4c-q018 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ab7b9da1f3b4747145077a03f134e5e77bc758a6c050e7a71bcb2584d7bf4dce` | answered | `464c0a8888e338f4b81fb5370e9cc2690375f32ef68d03dbd605a1fcc1f275c6` | pass |
| fhv5-9f4c-q018 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1ed8781b6cfb759613c257f4e727b19393876b46a3458af7d45100919b23a5dc` | answered | `af7386acc45c03bd5ddc783d62a9d6c5fda08f314ad8f030d0d9552d531ccc11` | pass |
| fhv5-9f4c-q019 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `12250574aedaeaa1ff2a83e8944e2327571eedcf81c2351f920015898f4856db` | answered | `266e9de33f476b02a958200a279b5a6093d8d7e02fb3c46db8df3d7063979052` | pass |
| fhv5-9f4c-q019 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4230dae674c83a3b98442cf56430e531959922181188f831ab339022736e83c5` | answered | `b7edcd24473f86978a5c67e3e4f31087c3c90268de56afeddd20b1fa52761c79` | pass |
| fhv5-9f4c-q020 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `663730efd20d6e7a8d8aa14e47ca6ce5e5143d647bf9884e7c363510d231763d` | answered | `f0f8c83287ee4ffc3ecb76c38e28225017ef0ef736dcf20fc4648393c84c24a1` | pass |
| fhv5-9f4c-q020 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `04200bb841736ac0c0b0ecf4ec77594b7ab160e39fd81d56e6a8418184a13228` | answered | `fee0361ac77e2273bb555915ffcedee6d3c29b827734b1d1351977ef3cc567db` | pass |
| fhv5-9f4c-q021 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `19e3af0f027471b153c6c94e9cb17ed648727b35a366f8df91caa327f7bc5cb1` | answered | `ca8785732ebc3c097ecf3e65ee48d6d31ee4738272ca90ebc83f6a82675e3676` | fail |
| fhv5-9f4c-q021 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d4d8560845b87076a561776ecd11cc52962d00fea894ce84a08b661b529b4528` | answered | `6ae6c791afaca1208fe06e34217109937f804b681a564f456a9eccdf33856677` | fail |
| fhv5-9f4c-q022 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `91b134e91be3ce49105524c307e79f6c79b255f1ad7d3eee610569e2877130f4` | answered | `b9eb32bffd7abcdd49d3061fe4425df82db47865fb3a7b177dd83544af1b00d1` | fail |
| fhv5-9f4c-q022 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `f4686db20da126ede3f949397a9e0aac26cdbfc605d487f68f72c3ae4a6c9db4` | answered | `6ea9b9a003b06760085d4ce613b84366bea5f13123576f779529cf7c9cac04cd` | fail |
| fhv5-9f4c-q023 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `9b5aae1cf50f342a6d074de1d51bf2dcddaf5ea7a4e36c1c285b074651f72144` | answered | `c561745304e082e84dd8afd90c714cbc1229492f1b073574c0c357d96ea7e2be` | fail |
| fhv5-9f4c-q023 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d59a6e835ebf1cbd453a886414332f4181ebc34d6a495bfe33e871d74cfca1a9` | answered | `acb8e7f7e3db72c244952f2e5774e4fb39861c86fc994db4cd901cfaaf875b45` | fail |
| fhv5-9f4c-q024 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `8fcc6a286e3d911d2932005e28748ab4fe5c2ab99b57b744a8cc2bf328808b79` | answered | `55ef385e9aa93a3ea5d5126ea1f416d7b4236d88f525f015453a2c77fcfccac6` | pass |
| fhv5-9f4c-q024 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9b7c6f4cc72d947e9ee75f15bb47849141ff81ee00d2fb6ca72bb2b3da85320e` | answered | `3775b46822dc2ed229144c98563856f43e57538efb98777046a2cb7f943bc5a7` | pass |
| fhv5-9f4c-q025 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `67e53afa22a4258eb1d6a4e08435f2b6a8b41901fbcf5666c5e2643957ba408e` | answered | `d6ed6ee50c017520e18a8215aa2d074cf8764a3f8fb3d613817ebe13e24d7c0c` | fail |
| fhv5-9f4c-q025 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7f61211c376281ea14e83e98efaed34989545b2baa62ef300ad5571e397de340` | answered | `199b62dafd87eaad5391ff57cb26a7de35243b40e8363c3c863f4e8cdc1fa8ba` | fail |
| fhv5-9f4c-q026 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `84a590c8b549f3326010cff0f912bdbc61edfbe2f255b87e0fb7781c30515ccd` | answered | `c593ad0d69af12c84c8006e414b7ca91fe97263e95438a45b6ac14bd5aa61ae1` | fail |
| fhv5-9f4c-q026 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `0ee81525766a2dc9a3412ed95089c8bc03b9d9a3e2e36eea2d9ffb39fb56a77e` | answered | `54844e78275bf7bfeb0f37e5f876240d40fcac27a2d302e3e13d58aade059e6e` | fail |
| fhv5-9f4c-q027 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d804ac9b96921950b203704d24fbe92771400bf31a60c9bc41f865087d719379` | answered | `a028ffa00c6736fbd0cf13d9cc65885ff399a494015e291c4138bea68268a5ef` | fail |
| fhv5-9f4c-q027 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `df53b9faa992e0e603c6eb5e7fcfe3accacdc11e48ef32c1536e3336927dd019` | answered | `df09523460034d2312ffc7692e05075181b746404732f0d210cb18b702ef1f84` | fail |
| fhv5-9f4c-q028 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b3dd1e7901c7bab247533be132fd8e1e89fe566aa653dcb7659c22274cdc8f71` | answered | `1ccff330145a4bcf66c5ffc2c8caf75ae65d3355125e4d2027528bedc4b761b9` | fail |
| fhv5-9f4c-q028 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `48900e8c8b11b4b44d8afb75925798f9dbfb2135022a0886c1340832a37a76e0` | answered | `c66125f92fd21aa41b03a638a93792978ab94774a601023723cb5a6ef00a1e86` | fail |
| fhv5-9f4c-q029 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `c999960c4ef741c978134bc5851624de89baf909aa4804a26a0595918a798e83` | answered | `5be2672649112570ec61269013c8f9f58f48a8fab01203f512a9815df13e9c2a` | pass |
| fhv5-9f4c-q029 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1bddcca5869bfe4fc952786ca2052109b9ae8c2cbdbff964245ab8dfb4ecd846` | answered | `219b0c2f1a569689e1c1bda3c205572096b39d33ff4f0065dc29a3dd5b628aef` | pass |
| fhv5-9f4c-q030 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b22a66efac1f00578115a1b6cbb0a65bfe42a918c246922493bb74ceb80910de` | answered | `260ea355e403b2f742bef8533da180587e506f24b77f0ff7076bef30db6456c0` | fail |
| fhv5-9f4c-q030 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `81c080537f84c8a0f741192357e20441dc5275f8dbe42895fe20ffcd59a9f019` | answered | `c554197fea25b8293e2f412218ee4d0aed13748fcb2b527857eb75195d3bff33` | fail |
| fhv5-9f4c-q031 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `c273c6d852c0ea388f973b19258ae71247c0e25a6d36c4ecbd1735597d4f1a6f` | answered | `2a00c15e52131c8aac6adbe3b335e93afbf3bbd371a38c2add35ef1f972ad956` | pass |
| fhv5-9f4c-q031 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `75d97ad63d5b3578c5789e8578bfee734697b63f4b921500492893c1839b8d7d` | answered | `cfd58c933379020a1b0087e9381536a05831970312bd45396ca69a88d6fab266` | pass |
| fhv5-9f4c-q032 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `6fde69077193fe30ca651dac76a074dc521622817f88aae678c5a8aebe9729c9` | answered | `5fb562abd68ff424c0a2f6d067e0999f65b2de0cad2d196f4730e883e83374dc` | fail |
| fhv5-9f4c-q032 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `09ad7d5024564f7bbd369aeacece0c96856c5906adff2e27caa977e258898029` | answered | `6c252f4f33876796666f04e19a518adc2ee5528ecd9d2bebda86f35a38152b29` | fail |
| fhv5-9f4c-q033 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `1d1f53396c0b7bd47d12323375006078c8d119d32b3a24dcb2b57bb826bc3ae6` | answered | `1405568cf7d07e8e834ec9446fcd34efb61d2512e47b3d73797c9e2f5550bea0` | pass |
| fhv5-9f4c-q033 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `bd1746abf44b17126e49226c3c40b4bc7ef8eb28111e9ea518a83dc548369ba7` | answered | `04b3afc0a8c17a89fb8ab97ddaecc24dc42fe52baa959227ad1ede1caa75134a` | pass |
| fhv5-9f4c-q034 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `b06dc41d4d8941795bf6e692473e2fd33319ff07f0e7d454f2635e294fd677ea` | answered | `dc71eff876c10310b04f10bd47e67381dc034eca092e7ac8874520633c7d2e9c` | fail |
| fhv5-9f4c-q034 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `78a1d4cc8779a24d96aaff63ab8ea3f262062465ad944e57441f0ed949c0dc9d` | answered | `8ea7c382138db60a79477c8ecc4cf36e903d7bfa75702eae51f67a2e173a1268` | fail |
| fhv5-9f4c-q035 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `0f39259d5bacdd0d5d90b597bf6166e705dd7423ee05cd67d71ad7900df2b7eb` | answered | `ab69e589df5dcbe5fb0fccb834ff1dfaa7bf7414caf37dceb78abc50622a338c` | fail |
| fhv5-9f4c-q035 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `542242e4ccdafacbca1943163116b8b85f103f516f8a13b73dffc5352678dc5a` | answered | `f0f2199f96fee82abc12bd58e94d27f1bc8fa7a28591dd61cf5c8b8563c6c146` | fail |
| fhv5-9f4c-q036 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7b1e7150bacad2ff1d7010a03acb29931b292b75b445b546adf7a7316d01a9b0` | answered | `dba34dd96760a170811d7ee0a5bbbedcb4f6aba6c2f43a9c071448775eafb534` | pass |
| fhv5-9f4c-q036 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `e0dfaa4e5d1e324999c452a861d511862147190929f3aa3bdfd354721d0bfcc4` | answered | `87ac2bd27347ae3d8a35d954df0a0f20f8423bb8f5ffcde0a0ed73b69c974dc6` | pass |
| fhv5-9f4c-q037 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bc10ba387988705393542aa53a503e64e27723f6764ddcd5de51eedea9afae6e` | answered | `4c4f4b53ae048e06eddb5f617693cafa73e37544abb3b0912faaa57a474d41d6` | fail |
| fhv5-9f4c-q037 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `2527fc9aefbe3fc97e7f4e318017cfc537c60a67dda2255e50202fb5a55401cb` | answered | `674e9e5fe319bdc24bd8c65c12def3e7641cfd5c7514ab2ba449ad0b43edc28f` | fail |
| fhv5-9f4c-q038 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `313a36055c46b35a7604f79e1f9cc03fb7e1be9f7f107e90faa07d7021fdaae2` | answered | `77f06fbb36d62b5aa23a9f8ae1631015dc1a0809183190fad6ab44c32a8f476a` | fail |
| fhv5-9f4c-q038 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `1d41b637d46a062a8993fdda02bb91fbba737962b5a39b6dc4539372acce1e12` | answered | `93909529f571f6437e49cdf5ae065d056d320604001590d7951e07cc84a8c423` | fail |
| fhv5-9f4c-q039 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `99787c61fa103d97afd7ddf79edbd10489ca6eeac9086a7874d43f05e2865228` | answered | `28f2bca2e5bd8c629c90b72ead3a2a273f8fff1fc6e16e23f1abc5714aa8e683` | fail |
| fhv5-9f4c-q039 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `bddc26814db43144534dd0e54333d3297f452e08a2f53fec2a829adf21bd9d1c` | answered | `e229fffb9996cf76c20df2f923ea746d51a8160f3f6c11ab8faef73bf8fc051c` | fail |
| fhv5-9f4c-q040 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `197863b1517dfc62ba8f4b31b06c831f1521c69d4db10c4794b3b0f22dd9925d` | answered | `b5675c7faab2f0c1bff3cc2211e886fe34eefe6d2cf6c96337f6f790463145fa` | fail |
| fhv5-9f4c-q040 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `721e2b8c8e7c11fe1a9d4e8e7257a88e83e1c62bf14c47c87315d41edf070abc` | answered | `cc4271a2987a11f611a270139435e5e3f1d53758c69318fe898d6ef93522902c` | fail |
| fhv5-9f4c-q041 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `91d84f7dd3215f9fa7aee022e3496362fe1e93f93aabcc958a1745746ba955ce` | answered | `a4a6581ef09ea1a732d35bdb8ce9e13196bacd576441d3396e82c4992aa845af` | pass |
| fhv5-9f4c-q041 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `67ba2a0e2eace032ae306e52145c6bc04048ad32e533669bffb289f6d0bde19c` | answered | `111419e601a69f52b0afb1f84fe2aaaac27203adf0d87b807b2854b38b79f80d` | pass |
| fhv5-9f4c-q042 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `be69eb5ec89b6cb754bfc5bcac7852fb6bf228df3df4595d137d96e38d8f5c19` | answered | `967efc52e69646f64d899a922e4f322cbd3b2f812454954bf399ddff4c22eda3` | fail |
| fhv5-9f4c-q042 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `c7e5cf877acdf5d25acc6b9b5f8d354edfa2f856b1503b6b8680859563f21653` | answered | `09aff0c2f044a894627fc1321f5632b90dcbd89bf9d263f60a92dfa48e947690` | fail |
| fhv5-9f4c-q043 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3cbbceafad2018913372138eb68fa178f7fd6943fec8148c33bb25c78a68ebd6` | answered | `6d7b3041724d5e2dd94ee4ea4980c66231869954ecfc4aa99c5bf2f3feb4086c` | fail |
| fhv5-9f4c-q043 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `883245f51d0992c094a8dcba71623ca13ee3b78dab055511d924faa620833c4b` | answered | `ef4a78b7e94e674a9fcdc9f5f33c9783eec085095a8d0a6ef75b353d0477cde2` | fail |
| fhv5-9f4c-q044 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `75e2dfd0571388c0b397ea59566f13cabd999929e90eee20eb10b654212a28e7` | answered | `e6b8df3357239d11b8ca20db25fc93d4dc9d2a6afcbad0709357c7583c31c7c4` | pass |
| fhv5-9f4c-q044 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7b7004ed95c2bd57b6a2b2d77ef1d35a12f6abb10d0995f9fa9296c98e114ba5` | answered | `8b509ef24691cabca9ec454a69c433747cae1ed3b9efa6a1cafbb367f8f8cc89` | pass |
| fhv5-9f4c-q045 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `a8145a33b94d53e9ac6322319875be93abc82d3255c3abecee2ba86df6f5a340` | answered | `e4b4ec5695f60cc15fe7b4710c0a37b7119c33a438dc8c1b0ec7b5601f22036b` | fail |
| fhv5-9f4c-q045 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `3945d32edd5fa38aafbe516ffac6a540abf3bacc1278f1618140973dab35b2a2` | answered | `651b9c045ec07aaf587d110c2ee9390e497b8d6409f391c14822a94c3a2f2446` | fail |
| fhv5-9f4c-q046 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `571ac0aaaa76156eca35ec8421cc932158a068679aed0a43bab3784bbcbf294a` | answered | `8cf4834db110fed02b59611e7f0fc97734aeb04576c4f865a643cb72201d6027` | fail |
| fhv5-9f4c-q046 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7d789c7cf8a2025bf1c14ad21c0637892fbdb2c434ee5dbf50939b007ec6f4eb` | answered | `6f5cda2ce4592fe26aa794bcf5f5d166a4ed52b2a48d91432e842f14247eb3b1` | fail |
| fhv5-9f4c-q047 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `20a5720d07fc96ab73b4477d2e1965524c5ba616ac618cd2830e9c1e306d194c` | answered | `e34f45c759d332573d65a8fdc704da48dc75de7e54710a38f4645a2153866f94` | pass |
| fhv5-9f4c-q047 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `05bd2ccac51871440c0431fedd3740b0d41cb9e2d8c7ffaaea023089ad246f06` | answered | `a04d109d8fface21f276bcbd79e445434190dd5c60bfeeef79eae4de416f2b6f` | pass |
| fhv5-9f4c-q048 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d06ee21a68a688791011b8a4f31b01b7b89e80831aebbf09c636cb15d24b89f1` | answered | `36d54b7a5715996cfc1c911ee90755d7a3db2f1aa1ca43b79a3ac0cb4eb1ec2c` | fail |
| fhv5-9f4c-q048 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9586e5b86d2429670bd0f666686e690cc3a2e1a280443d5deb5a6ee35228a309` | answered | `596cef4eb71c62d73f4b26ade72a4dc9651d8beabd1216acf438b47646d0becd` | fail |
| fhv5-9f4c-q049 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7295f114a924f121badd91853ec59c83fbb79b4bbe3174f030ce57a97f820fc1` | answered | `7185db88f7ee6c3cfacc5d8f67f353f2ea4f685cad0fcf57225fb02f49365f24` | pass |
| fhv5-9f4c-q049 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `07efd506efd1463adbc4527e7b30e2a0f44867e2886a66fcb4830e2f1d0f6b5d` | answered | `0d054465efe477c86abb4110adba48064d029ff446b6e745a97b8dc391fda452` | pass |
| fhv5-9f4c-q050 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `efe409dd4402861dc6b9be26bc0d824720da175f6abf79e2953fa6efaf1d2047` | answered | `6fb4c2b1ba070eb15b9a53672dbd4f0969c4a17e29aedd6301d9a6643c594452` | fail |
| fhv5-9f4c-q050 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d2431bdc62403616066391345b02461642af992f38bc855f4938653ba9406585` | answered | `b9cc3f97c8c49174620a442f5d3f4455cf0981a5d0f2ebdd7706e933227c3e62` | fail |
| fhv5-9f4c-q051 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `3ec25e49d27f9484daa8752a7bda7885b641636626067a21da40f1078aae97e3` | answered | `334bbc3871ae692d86e02d5a6fd2f57abe851b6596b8cf861d8fe2de69581bb9` | fail |
| fhv5-9f4c-q051 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b8564a64b12b9a6ee93b52381ae43fdafd1ce6ca77911b22f9bfdbeb3be74eb3` | answered | `f451c1370ecc476b0777fd23c8fb001fa6dceba6686ac131050df47a6be2cfcd` | fail |
| fhv5-9f4c-q052 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `577df72ec309abe534596758a634cacd5301cd0dcf8d14d21752fa79ce8c0ad9` | answered | `7d56c4633ed11efad661dcff3f8f707db0674b00f7ceca862fd8f11bbf2bc06b` | fail |
| fhv5-9f4c-q052 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `4c804434e0feda9e3694f64ba1170202a4edf3304e6b517a7e8a0cf96c7d7bda` | answered | `5fea6453359d8048080cc0db18727e1f0d56f0cc4c676ff34e0c96275769505d` | fail |
| fhv5-9f4c-q053 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `f3791056957dfda9205ac0c7bfe15e73a7733858cc3f0871920d79690f3266be` | answered | `6e4889e275fe4fccded90932f1119986093fea55fb41373695125d1213a1955d` | fail |
| fhv5-9f4c-q053 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `0f67fc5dfe2424b7d1d9f3e053a0800477a6b227cc341a49326a4999da6d9957` | answered | `03485263b69feadc17468a8e2450cb2ee28366d1aed39b4d133db17b02de735c` | fail |
| fhv5-9f4c-q054 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2eae8e05064f6c5fa9d566e8a7e502b3253ae0509952485fc16f867f230e99a5` | answered | `4d00c26bb64efe91c642155b4bc153ded086c346e9db305322f817c27298a532` | fail |
| fhv5-9f4c-q054 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `35aebd0662d4eae9dc401dcf3c0106052da6010151c5d511af2fae918eafcf98` | answered | `bcb019d91df7c86a6c66b9514a877f091cc69e617d44e16d33c1a6a79d045621` | fail |
| fhv5-9f4c-q055 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `357974b4945f9462bdc2bffbfbc197fe96d752d4dfdaae17bf7d0f5d9959e9b9` | answered | `496d77a42b49eb465640bda23fb75f7190011cb7e2713c99f350c3b789864aa4` | pass |
| fhv5-9f4c-q055 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9af61ef9854a59be36c4f97c577b4cb142dd1ec362a78abfd01882fde7b34f21` | answered | `61d8258ea5ab0b1a7e6a9fab0261f88f696097bf4e766fc2563d6002fa222af2` | pass |
| fhv5-9f4c-q056 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `08d57df581f1361cb566edd4d832f685307c2e394e88cc883ba735c7377f23c0` | answered | `f29fdb9082d04b8feaa33fc593ecf0f97e8327fe66c10108e81d59493fbaa91e` | fail |
| fhv5-9f4c-q056 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d7b0be7e801f0b3d73c58d06d6dc17d37bdc0b6674206149394c9a30e1b9fa3f` | answered | `27e60c13a2cc4d14a267ac137dc438a7afa1c66ed7eb1eba57bc220ebebaefc9` | fail |
| fhv5-9f4c-q057 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `4b43c93df946556454a5bbfc434bbb7709acc924e092308a7d624387fd555333` | answered | `de8dac0d1c018bf71a66162331335ce95ddc6d3317e0ab0e8569fff9e4ca74d9` | pass |
| fhv5-9f4c-q057 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `24281c341708f30b3502137a0619a307ab0df8694196f19bee0235e4f8b7e4bd` | answered | `0fd5ce40a245b616646340c60ffabcc7b41b76b84933955e367d137e75660288` | pass |
| fhv5-9f4c-q058 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `ab7328021fa66d034314f125439d187e74286d28f12d7e86151ad2809351c0eb` | answered | `793a6f0992577b1dad933c98e7b252e8dd29ca8862fee495e5367b92ed91893e` | pass |
| fhv5-9f4c-q058 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `9fe3c2a08e46af5177fed37db7c039c9e3fa5065c24587c5c02ad8f6ae0be5d9` | answered | `ce46c937d995a19c5fabe97d361ce3eda6eed29723910d86429fef032ddb5a3c` | pass |
| fhv5-9f4c-q059 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2e976d4645d5f2f3b9540093bd73391c6261db6d5f95a0a0d67654062fc597cf` | answered | `8fb77386e14d5e7cb9b9de90ba0f526022347031c88d04bdf25eb338f8c4bcbc` | pass |
| fhv5-9f4c-q059 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `113a8cd04af292b0e2d746c6e99535235b9edb4492bdd7b44387a0879b44fcfa` | answered | `4b9d78f789ead1816b236df6dd609139aa91a301c91140bf0c984ff24c94eeb4` | fail |
| fhv5-9f4c-q059 | adjudicator--gpt-5.6-sol--high--codex-cli-0.153.4 (adjudicator) | `1f4fc8675da4d1841fca80e3249e56ba88daaf3f10465de7ac200539bfe3438a` | answered | `d9094fc055169007824c92977b28d3976b6e2c03918716b219be4877c38ddf4e` | pass |
| fhv5-9f4c-q060 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `7f04d6420de077f2b0bb3f9cd0960b8ee012da2e26222dfcea73678efa307406` | answered | `9b1dc02979adcc4dee88c3e37f1a7d17e48a1b108a6ea3cd22ca8382b26694ff` | pass |
| fhv5-9f4c-q060 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `7322512f7ffb83fac2fc981f53793760a88d6c139d7821eea6b4916d174b5225` | answered | `07032ab4027a6ef914f8a8ac56246d8d21bc1e4dcd5336897bc794456289805d` | pass |
| fhv5-9f4c-q061 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `bbfe069cf8dc21ee411c60f06780c6b272931a5eaef1611cbeb044f46077462e` | answered | `849194fab270aff9085b749f30fb8e4ed41be422add50e8a0568bc0132ef9330` | pass |
| fhv5-9f4c-q061 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `32e8bf48a1b69f8800e136a49c520ee1aba1d1bf964838ea99c3a485404909de` | answered | `24966d135d72a936da4c2e87214ebeb219e7a5de2ef2be9cf01522b706611f86` | pass |
| fhv5-9f4c-q062 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `2428025eb13a3a5b4fb37811b45c3f7f4447f8a52b17b36b2e768896bcccffd5` | answered | `99a380378ad228c2683db927a8bd6b97da183282b8fdb4b2d0aa49f58dc61e26` | fail |
| fhv5-9f4c-q062 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `b8031e9afe6105b5e2babdfc10c0f9891d1f387e21ea8e2a9c9454af9903e125` | answered | `90b417195544ec4ff80435e34d7ce5dd87d46cba68701916a7f751dff4ac4801` | fail |
| fhv5-9f4c-q063 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `d767ac8f06ca04defd2de7b5232fdab9b009683ed7511cd0cf938413527a6e92` | answered | `0839e01c16dccd44f321ca3c1e999479d87b47c4f3d4c6280b857a7e7b1f782c` | fail |
| fhv5-9f4c-q063 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `6bf4d1634cad1b9ec74abe21a3c2fc859f987e40ba7df45b6034d55814eb1d0a` | answered | `1392646b66c44b33128dbae9ed81f7ed96549dcf14d0727f14fa6097856cba13` | fail |
| fhv5-9f4c-q064 | primary-a--gpt-6-astra--high--codex-cli-0.153.4 | `fcbc04e6212f8b7203f48228f6ff2cd536051f9c3968b491bc65c6f08d6986e2` | answered | `b5afb45bdd4b45b9ef7da6a74b35d791e107bdd71ee2312024c3ea91e601dd34` | fail |
| fhv5-9f4c-q064 | primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 | `d420c6736d429aa633b243868ca40c804b9e410481463742fbfaf113efe81dc4` | answered | `6027ae4b5a1e14b53f0def29b7ec5fea575f5326bb4b239ff8a60d5c944d78e0` | fail |

## Per-query outcomes

| query | stratum | outcome | reason |
|---|---|---|---|
| fhv5-9f4c-q001 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q002 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q003 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q004 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q005 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q006 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q007 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q008 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q009 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q010 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q011 | exact_identifier | pass | both primary grades passed |
| fhv5-9f4c-q012 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q013 | exact_path | pass | both primary grades passed |
| fhv5-9f4c-q014 | exact_path | pass | both primary grades passed |
| fhv5-9f4c-q015 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q016 | exact_path | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| fhv5-9f4c-q017 | exact_path | pass | both primary grades passed |
| fhv5-9f4c-q018 | exact_path | pass | both primary grades passed |
| fhv5-9f4c-q019 | exact_path | pass | both primary grades passed |
| fhv5-9f4c-q020 | exact_path | pass | both primary grades passed |
| fhv5-9f4c-q021 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q022 | exact_path | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q023 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q024 | nl_behaviour | pass | both primary grades passed |
| fhv5-9f4c-q025 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q026 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q027 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q028 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q029 | nl_behaviour | pass | both primary grades passed |
| fhv5-9f4c-q030 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q031 | nl_behaviour | pass | both primary grades passed |
| fhv5-9f4c-q032 | nl_behaviour | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q033 | nl_behaviour | pass | both primary grades passed |
| fhv5-9f4c-q034 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q035 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q036 | architecture_flow | pass | both primary grades passed |
| fhv5-9f4c-q037 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q038 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q039 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q040 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q041 | architecture_flow | pass | both primary grades passed |
| fhv5-9f4c-q042 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q043 | architecture_flow | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q044 | architecture_flow | pass | both primary grades passed |
| fhv5-9f4c-q045 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q046 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q047 | config_docs | pass | both primary grades passed |
| fhv5-9f4c-q048 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q049 | config_docs | pass | both primary grades passed |
| fhv5-9f4c-q050 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q051 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q052 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q053 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q054 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q055 | ambiguous | pass | both primary grades passed |
| fhv5-9f4c-q056 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q057 | ambiguous | pass | both primary grades passed |
| fhv5-9f4c-q058 | ambiguous | pass | both primary grades passed |
| fhv5-9f4c-q059 | ambiguous | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| fhv5-9f4c-q060 | ambiguous | pass | both primary grades passed |
| fhv5-9f4c-q061 | ambiguous | pass | both primary grades passed |
| fhv5-9f4c-q062 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q063 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| fhv5-9f4c-q064 | ambiguous | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |

## No override

There is no flag, environment variable, configuration key or report field that lowers `k`,
waives a query, excludes a query from `N`, retries a graded response or forces a pass. A
missing, empty or refused response is a failure for that rater and a failure for its query,
and is not adjudicated, re-requested or replaced. A pass count below `k` records
`RELEASE: NO` and exits non-zero.
