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

## Result

| quantity | value |
|---|---|
| answerable holdout population `N` | 64 |
| pre-registered minimum passing count `k` | 56 |
| observed pass count (as reviewed) | 31 |
| **corrected pass count** (disclosed concerns subtracted) | **30** |
| observed incidence | 31/64 (resolution 1/64) |
| observed pass rate | 48.4% |
| exact Clopper-Pearson 95% interval | [0.357501996719, 0.612741191779] |
| lower bound clears the 3/4 floor | false |
| primary disagreements | 8/64 |
| adjudications | 8 |
| missing, empty or refused primary responses | 0 |
| **RELEASE** | **NO** |

- 31 of 64 queries passed, below the pre-registered k=56; there is no override, exception or waiver
- 1 counted pass(es) are disclosed as unsupported by the bundle-only rule; the reviewed count is 31 of 64 and the corrected count is 30 of 64, and the release is decided on the smaller of the two
- the capture is not bound: the capture recorded no candidate binding: neither the candidate implementation nor the indexed checkout is bound to the commit this run names, so a worktree modified without committing — one that makes retrieval return the expected answers — would leave this report saying the frozen candidate produced these bytes

## Disclosed grading concerns — counted passes that the bundle-only rule does not support

These are **not** re-grades. A silent re-grade is the retry loop this evaluation exists to
exclude, so nothing below changes a grade, a response or a query outcome. Each entry subtracts
one from the corrected count, and the release is decided on whichever count is smaller.

### ci-1086 — counted_pass_not_supported

ci-1086 is counted as a pass, and the bundle-only rule does not support it. The reviewed grade-3 operation is pflag's Changed (internal/eval/retrieval/testdata/datasets/cobra-v2.json), and the captured task_context/2 bundle for ci-1086 does not contain it. Both primaries were nevertheless graded pass. The query is NOT re-graded here: a grade that is already sealed is never re-sealed, and quietly correcting one is exactly the retry loop the append-only seal exists to exclude. It is disclosed instead, and it subtracts one from the corrected count.

- primary rater 2 states in its own response that the bundle does not show `.Changed` or `Flags().Changed`, and answers around the absence
- primary rater 1 supplies `Changed` by analogy rather than from the bundle, and grade 5b1bb46b's own rationale acknowledges that the operation was reached by inference
- the grade-3 answer span for ci-1086 names pflag.Changed, which does not appear anywhere in the preserved bundle bytes
- raised by the SW-280 round-1 review (Codex, blocking finding 5); independently reproduced against the committed response, grade and bundle artifacts

- grades concerned: `5b1bb46b4589a195403c17e5176074a28d151b8090efd05d9fd33bf62056108a`, `345e75760fc58917f5ebef1034779a0dd04d5ddfe486608282857822b3f3b0df`
- raised by SW-280 round-1 independent review (Codex), reproduced by the round-1 fix pass at 2026-09-05T16:43:27Z

## Capture binding

**The rated bytes are NOT bound to the commits this run names, and that alone forces
`RELEASE: NO`.** Recording a commit is not binding to it:

- the capture recorded no candidate binding: neither the candidate implementation nor the indexed checkout is bound to the commit this run names, so a worktree modified without committing — one that makes retrieval return the expected answers — would leave this report saying the frozen candidate produced these bytes

## How `k` was derived, before any response was opened

`k` is the smallest integer in `[0, N]` whose two-sided exact Clopper-Pearson 95% lower bound is
at least `3/4`. It was derived by code from `N`, not written into this document.

- `N` = 64, read from the sealed dataset (`7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc`): count of answerable holdout queries in the sealed dataset: split=holdout, stratum!=no_hit, at least one grade-3 span
- `k` = 56, whose lower bound is 0.768473694033
- `k-1` = 55, whose lower bound is 0.749763164375 — below the floor, which is what fixes `k`
- method: two-sided exact Clopper-Pearson binomial interval; the floor comparison is exact rational arithmetic on the upper tail at p = 3/4, and rendered endpoints are bisection brackets of width 2^-64
- pre-registered at 2026-09-05T13:39:51Z, naming precondition record `c175fbf70391bee6c9bc2ef5fdec18814b3f1ef7730af94fb8c83bf3a08c4012` at commit `e48a1ba397392b30a1cecbbdfb4076a9bab61eff`
- **`precondition_record_commit` above is not the commit that contains the precondition record.**
  This run was captured by `sw280-candidate-mcp-capture/1`, which copied the record's own `freeze_commit` — the candidate
  commit at freeze time — into a field whose name promises the containing commit. An auditor
  following it will not find the record there. Recover the true commit with
  `git log --diff-filter=A --format=%H -- <run dir>/precondition-record.json`. The field cannot be
  corrected in place: the pre-registration is content-addressed and all 128 responses name that
  address, so rewriting it would destroy the ordering evidence it exists to provide. `sw280-candidate-mcp-capture/2`
  resolves the containing commit from git and refuses to pre-register an uncommitted record.

## Per-stratum counts

Counts are authoritative; each stratum states its own `1/n` resolution.

| stratum | passed/total | resolution |
|---|---|---|
| ambiguous | 1/1 | 1/1 |
| architecture_flow | 1/1 | 1/1 |
| config_docs | 24/57 | 1/57 |
| exact_identifier | 1/1 | 1/1 |
| exact_path | 1/1 | 1/1 |
| nl_behaviour | 3/3 | 1/3 |

## Participants

| role | id | provider | model | took part in this track | basis |
|---|---|---|---|---|---|
| primary | primary-rater-1 | anthropic, via a fresh Claude Code subagent (subagent_type general-purpose) | model alias "haiku" as resolved by this Claude Code harness; the exact model id behind the alias is chosen by the harness and is NOT independently verified in this record | false | Spawned as a fresh subagent with no conversation history from this track. It did not write any SW-266 slice, and did not harvest, annotate or review the cobra-v2 dataset. Its only input is the question text, the preserved bundle bytes and the frozen answer instructions, delivered as one file. Its model alias differs from the model that implemented this slice and from the model that annotated the dataset in SW-279 (Claude Opus 5). |
| primary | primary-rater-2 | anthropic, via a fresh Claude Code subagent (subagent_type general-purpose) | model alias "sonnet" as resolved by this Claude Code harness; the exact model id behind the alias is chosen by the harness and is NOT independently verified in this record | false | Spawned as a fresh subagent with no conversation history from this track. It did not write any SW-266 slice and did not harvest, annotate or review the cobra-v2 dataset. Its model alias differs from primary-rater-1, from the implementer and from the SW-279 annotators. It shares a PROVIDER with both, which is a real and stated limit of this panel's independence. |
| grader | grader-1 | anthropic, via a fresh Claude Code subagent (subagent_type general-purpose) | model alias "opus" as resolved by this Claude Code harness; the exact model id behind the alias is chosen by the harness and is NOT independently verified in this record | false | Spawned as a fresh subagent with no conversation history from this track. It rated nothing: it receives the question, the exact bundle, one already content-addressed response and the reviewed grade-3 answer spans, and applies the frozen rubric. |
| adjudicator | adjudicator-1 | anthropic, via a fresh Claude Code subagent (subagent_type general-purpose) | model alias "fable" as resolved by this Claude Code harness; the exact model id behind the alias is chosen by the harness and is NOT independently verified in this record | false | Spawned as a fresh subagent with no conversation history from this track and no primary rating role. It answers from the same question text and the same bundle bytes as the primaries, and its response is frozen and content-addressed before any primary response or grade enters the same decision. |

## Adjudicated queries

| query | primary outcomes | adjudicator | final |
|---|---|---|---|
| cb-17 | primary-rater-1=fail, primary-rater-2=pass | pass | pass |
| ci-829 | primary-rater-1=pass, primary-rater-2=fail | fail | fail |
| ci-1461 | primary-rater-1=fail, primary-rater-2=pass | pass | pass |
| ci-1631 | primary-rater-1=fail, primary-rater-2=pass | fail | fail |
| ci-1758 | primary-rater-1=fail, primary-rater-2=pass | pass | pass |
| ci-2138 | primary-rater-1=pass, primary-rater-2=fail | pass | pass |
| ci-2252 | primary-rater-1=fail, primary-rater-2=pass | fail | fail |
| ci-2291 | primary-rater-1=fail, primary-rater-2=pass | pass | pass |

## Frozen inputs and the end-of-run comparison

| role | path | frozen sha256 | observed sha256 | matches |
|---|---|---|---|---|
| dataset | `internal/eval/retrieval/testdata/datasets/cobra-v2.json` | `7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc` | `7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc` | true |
| budgets | `docs/eval/retrieval-budgets.json` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | `2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb` | true |
| targets | `docs/eval/retrieval-targets.json` | `26a5ea05657d18b687f42f53fbebde2876e016de90b0d22b21f0441f236a0592` | `26a5ea05657d18b687f42f53fbebde2876e016de90b0d22b21f0441f236a0592` | true |
| grading_rubric | `docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/grading-rubric.md` | `cd4d853ba57bd714ba1644be10c922cd84c4730b37b9bb66440feb6374ec08f2` | `cd4d853ba57bd714ba1644be10c922cd84c4730b37b9bb66440feb6374ec08f2` | true |
| methodology | `docs/eval/retrieval/methodology.md` | `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d` | `f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d` | true |

Compared at 2026-09-05T16:43:35Z. All match: true.

## Capture provenance

- capture instrument: `sw280-candidate-mcp-capture/1`
- transport: MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)
- payload boundary: `mcp_jsonrpc_response_bytes`
- candidate: `task_context/2` at a 1200-token budget
- repository: cobra at `a0a6ae020bb3899ff0276067863e50523f897370`
- embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (model `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b`, index `147:static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b
40:e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
64:75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c
64:107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45
3:256
2:v3
0:
32:46fd864cb5fa65938d2250cc64523cc2`, generation `g-8ed43963159b4ef9`, 768 persisted vectors, state ready)
- tokenizer: `tiktoken:cl100k_base:ordinary` (vocabulary `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`)

## Content addresses

| query | query text sha256 | bundle sha256 | bundle bytes |
|---|---|---|---|
| cb-06 | `ec7908152ae3725b160dc4a6176375d30bc0fa9f191fc9480efa26f617755999` | `8656800793c3cf3ae91424a2722c5ffa6c34d57b4b2d9cd05915018ee40eee28` | 15818 |
| cb-10 | `502559784be28f4f1ac3ff6b6567bbc145c1f2bced2c841fb2c6639db3124ae4` | `3499de7f16945a6da0e13df164580e400f212f6e515a8a7ed583ce02d8ef05ea` | 15373 |
| cb-17 | `3342c4e5cd007dc83c626ef428803bea347914743644161b4ed8657948c52407` | `4a6ec724144565e924264ee6ccf73c5c938df471b46b8323ea5c0d09c8d7dec0` | 24638 |
| cb-18 | `fc716d347e651ae9c9e3bff656afbd00521e1086ee478799d04ce217de3a9395` | `5ee1c1e52f888e43b0e4e0a1f0985eccb1cf6f421e25864c2b065483d1efab0a` | 21145 |
| cb-23 | `1addfa2e7da02d6a191809243a4cc8a9eebcdfcbbac135eb7615316c365e7cf4` | `2718a4ffff1b78ff09f5ab7688d84200de245fd8f9aabe789062f21012670484` | 39290 |
| cb-29 | `e01254597f989d7d7013d79a12557aeae1bff4a6d3b1a0403b1b815955a98c91` | `81ef4d0c110f46b50b634922a466655cae454fc42f7885381796215d9ab8b83e` | 30186 |
| cb-34 | `8d59829c1e15afe1a7fae93e8e5e32d8511bec5fd598a09f4fea6033b31e8a66` | `36fdfc2287d0ec947dd76fd1deb928a92e99cc54ee9644d94bc3192b3e10046f` | 17065 |
| ci-2 | `81a407d0ace84c8bfb2e82b5c40cde6a98ef714d196778cad1e7a84f944d5f9b` | `54e533f6a9f350f3507f42ba6e4654c054f86d1cbfc85393cdc58b0fa46b5f25` | 42120 |
| ci-43 | `cda3f0a7b94dc4e6c878ad20bbceac74035f6e45faa7f4a103d513b52a8cd182` | `86ede99dc49c3ccae932eb8a8fbef1fca0b06824a4994996159c144300216d6b` | 22129 |
| ci-298 | `15614063ef1772d6d4c3aff0a69017f5d7a539d3c4114d876d695f11d0315b2d` | `e317b5a070369ddef0046cdccd725d705b969bf397bc946e145080c4c542a728` | 15725 |
| ci-316 | `725d47e1ea7ed6708bce905a3fd7cb6d1b41ee1dea163c386b5f5ff77e7552dd` | `4ed6ec147843a94305cff8f7857885a89f5e2863e24fb511e9d58dda7acc53d4` | 37991 |
| ci-346 | `2893e84e8f7095d0af86f5fcda07860debf395229ce69d73087f1b695801a4b1` | `d894fbb948929ab7cf81135d53ceb1ebcfc875607aa29d962a84978ba8400505` | 29309 |
| ci-434 | `f206e96cdd00ef8c958118747ab58588f9081e97624a8cfb757bbcbcdf8b2ab5` | `8afcbb51c1b0ec5ff91fcfc41ed583ec8b22083ce6cefbef9d812dc8b4948ac5` | 37569 |
| ci-470 | `5338d7484494d64ae31ba559d0827d5ee620499598e4d600dd2c0c9b0182e596` | `6576e59e7f28ba55303b6948b844fc7dc54fc66cce5f17decfa915f632da2c78` | 22644 |
| ci-471 | `8a35f380d06b5cfd4a6e5b6d6445cef34222bf334a549021a108854fdaab0da2` | `bed3079b4a511681f0881006da888e91718941e9974fea680dc90c4eec9a77f5` | 20965 |
| ci-533 | `95bdc88c437e2a3c9ee0e18c58999265b4c11b894414ddaa0d824d987ae2be4a` | `9971019330b44233c263fd523a81dbad73ddc4a9e2403d0a743de20e48d4a0a4` | 20023 |
| ci-563 | `b2de880fd7a44b856e59bc92881c315e1418809d1cbde6b09056d23729187c29` | `6119eabfe039da90ed422c9c143eec7e2bb953674ce9a16018e2196a9bd42081` | 20530 |
| ci-674 | `a99ae0596e3c3fb12a0c211a4fc8f2cb9255fa7f1a7eb8441fdba5c495634b73` | `713e7dbbed04a5e1c232394e9d5e4277bbfdbbc18a203f1cbe8a9c770cf6f85a` | 20474 |
| ci-710 | `ba44ea13b5f29a121bbb86df0ba84da1ba3dd4cee84ead355c26aac53fa7a8bd` | `c900a88414c16a786c15507669ae7b8d2e0088aebd8f2bc47625bbea0a9b630a` | 37388 |
| ci-724 | `1b0415777e1187d7d8c969cca0c53bdd1b205093c21d71b4e5d7a6dde71b38cc` | `3eaead757842dc0a0ac6a5a6bbd882bd6e76ad76eebe0a83a5290ec79573351e` | 15774 |
| ci-725 | `a1a6bc05f2a5060552a84cc9767127dbba6d901b98e75775ccaa940b1b81d61e` | `b93ed4d4d632762742b3a162f5c10c102ff379e60b543d3b9f8a4e54bba7755f` | 20410 |
| ci-821 | `63ffe360865b84cf102fead755603bee8403104cea63565dfdb79df27bd532ee` | `fe84dcfe7149956c73de05941e49da2a2201f7247ef3b1b0afa851929de6b342` | 24782 |
| ci-827 | `4be1cc40191d4a5ef2196afe07f4d82958c1ba0b816d29b771600983c9c6452d` | `8a9ac2761800808e164753644e33627e8af0becca2b3f34546f05a9b9e592a0f` | 32927 |
| ci-829 | `c49fe02f14b0019e07ae187a5089e45cae814f4f56633a9dac3eb7c523577796` | `0f9cc3bacb03fd167088077843b2cd54e874b94794520dc87b03cfeafd2ddc20` | 22453 |
| ci-852 | `3ad7f4beccc891643f191938faf997e9c71425fe39f63f527940c3415bb7b98c` | `38e792c6da68d211f04455afa21a029b44c8798c05890db4449ee6f214bfdefb` | 28373 |
| ci-920 | `75a4bcee426bb68dddd71dddfb4496a148ddfc9f5b15feaab1501548468bb8f0` | `ef8783b105a9ab0dd343e1577180a866f04e2df6f87658cc5ebc3f089ebc2d73` | 21676 |
| ci-1025 | `d30f9274f45e9b39d7aa183b9eedf0d44a88356b65121b5c546faa1e15d29467` | `f26f71a76b2b4050c5e196d1f739be62f2ddb5c8b9e9de148ce28513c67dd9f6` | 24384 |
| ci-1060 | `4ea276c2c7e0e683f1bc824ff8028517289d8d714f75a34f70e75d48a1ef0cff` | `592bc94ac250bc8009122cb15d7aeaf50e32327d0ab920693c15cc002aa9b231` | 27721 |
| ci-1086 | `b9bf826129abec1dbbcd727823aa454876356e57146504ee6ad47f7a8a972eef` | `38a2c853f6aa7ae3844c6a9c17045a5df3ecf1baf7aef5da97335faa4de46ae0` | 20772 |
| ci-1098 | `a98ff40516eac33308f8bf1ee1248da09835c666c1eded124ed3856f40827c67` | `facb06a81d237ae9b2ae46ccbd84b4764f1f631ec391b89d8848af84af3b0317` | 26330 |
| ci-1111 | `675b6a84029da3aab14f51cd58cae327396fd667be07fa37d0cfb7f5f4c612c0` | `1961644f2d27aa36b1be15016098b9255b90083e86a6cc5091fadec03a7694c4` | 14524 |
| ci-1148 | `61ea09e2d4a8b65b4da3f3ae4c8033d337b60e48033f1e0e5374922e9db7902c` | `e52c04b89ab18b33e910b621b6cf7ebbfa3ff27cfbeb830fa8df1ddebf18cdbb` | 24288 |
| ci-1151 | `66b73c55d39c7cb65f38837b62f3ba5edfd1a85511e79695b4b4e7aaa94cda8a` | `2dc4ab1ddbae8683ca121ac03d1268b3c92cdfbfeb9f6fb53dcb687331768a4c` | 32927 |
| ci-1168 | `63a26ca706d86bb17e89408c2192992f84d039bd9cd5fb61dae93a22d99a6b13` | `4ef5071868ccc0c0a238dadafd5aa7f6d1e31c6f6cc5035599c02a180f8091a8` | 17138 |
| ci-1202 | `acc172befdc6cd85a259cb321a17a9c828211de50c27ec2403fcf11ca2714fcd` | `58bd78f014a8516b0dbdcf2c83d91c14a9e3d2c3bf98591a301fe6181aab6d3a` | 37076 |
| ci-1221 | `93de10aecfc68920b1a0652dac9e6b3b0b02954675c77203a00356e62f5919f9` | `e14e7438c11489be162bd4d9dac0f081f8ed904ddc0e1e2fa8b24130199c00ed` | 19136 |
| ci-1289 | `5175869aec07cd3a31cf5cfe4a905227ef2c44193509d643c1d44b266634abe4` | `b980b607ca44e81f94d6fe1348b447426f57d8d22fc7cddfae161b5359a90829` | 14378 |
| ci-1300 | `107b8e26f5790a9e06f4ef4a9f0ce1fe6fc2e1a51b02396f59752d530fa489b3` | `969930e6137f098e8bbc0cc6e62958e85bc79172e8bc90d768a7c617efabf6e8` | 34686 |
| ci-1343 | `c3d7dfb312e73f3e3022a1ab5f787cca3da6893674ffe8ed0035a214620e7b56` | `75debb24b17be49b3f237569e3bb1241c8c1d4f4a7e111ca84e583c30ff74d22` | 18279 |
| ci-1368 | `dd0a28f308614f8bdc46962c95c7c61f044d0171a3ff612710c73a286f7033ff` | `707ce613428ba339c8282b5e4484044a1374c330528d70550e0bfd64aec0b946` | 22638 |
| ci-1416 | `12bcba6132ed36666a58b1a54309120bb2e1dde97c68ca221f006500dded932d` | `7dda67e6c5861722478f7f8a4fe77dd217b21a56dfa71978f140e79891c63003` | 27099 |
| ci-1461 | `937ea02600712c12a30373bec14a02b06dcf7d9d8497c5f4505e16757f787859` | `18463cc4e2559eae12ba8cca53646f556fe33b3441a2ad40cdda08f72c13c4b2` | 26664 |
| ci-1499 | `7af27b154b8a077f896333b21fc0bde760288c78c867e43ab929f2bd95f2ab4a` | `c1828fe28853af88d49990b5558af69599b478cc6b709c6de27eacb29f40941b` | 24278 |
| ci-1505 | `96ab4f93ae7fef5c2718a8016cc91052051032472841cf62090392c1df46369c` | `3733be512a3536c36bd23dda0c1a86f1c67121fc6aaf8a208899389e6dca2bb5` | 22669 |
| ci-1564 | `80a1164c08e42d80f288e48632a886299aec0b0591c56e1472486a3b45426632` | `fa6824af19f16887110f75530575e533b4d96464c5e824d915fbb5e499ecb405` | 21046 |
| ci-1592 | `c8d4c0934cbcea98a5eefb7ec1d569fd5d5618eea10a609cb019eb2da2397bd9` | `710d82ca838fe66292d34f2efe289b0b25268aaad9edcbc4d3fdae0bea5c333d` | 28351 |
| ci-1631 | `4f29f07a352c3b1191fc637556bdbf8be72048cc71bd2a8e205b5ab4d31f7e55` | `96ffff8fd4e7175e4d2cd7ab17cf461d21b1e2278a52e2ceca76debdd578d1f4` | 23404 |
| ci-1758 | `6c99a923e785c7abb5a1178fa7ee9fb3b236202fbdc4fcfae82e5460bd5f33e4` | `ce2026adc7aec41b685e07af9c9a0068543ab9d8a1b10b643b661892ee0d8f11` | 21717 |
| ci-1790 | `02c3e537e8ff89fbdb8ac6a898a8a36d6dd6ae62baaf8b69b983b6122871ac32` | `3e94203f74747b7c34c207ff06c402dfe3a6184d5a23e34b1c15939d4ce6a900` | 11487 |
| ci-1861 | `e0030e004db845a74dfa173469990e16fdf8ea23e84f3efdb3a27e49c57922ac` | `5be78440ef030b1f5c6b26f468dbab19aa14b29ce148b0b5eb9ae5b70ae2c41a` | 23836 |
| ci-1894 | `0a4fe5a95fa1bff830073ba1f2c9173a26ab343d6a156e3404299ac94613b120` | `0e12675809014d3921513b62598d0a53e59ad460f8ef11f3cd55185814036600` | 14047 |
| ci-1923 | `39c35efacdacc2ea5ee6a5fb8defff190cafeac2490a815d9381a2f4c4a4d3e7` | `b77acee55c9645878dc2400535fca230116170d4fb54b345db4cad4d4697a6f6` | 12108 |
| ci-2007 | `e62cf047aa2d21fe06375b164125282d5cdc2182d03406e7187342b4cdf64722` | `5979f40d797806e9898f9cff9d72a21341380a687c6754c9e89425001557670f` | 34356 |
| ci-2014 | `19cd79e2d3254d7c79faa6e661455413ff66cd27577f1cad18de491d2df8f694` | `e118dde7ebda11ba844cbbe960461fa58abadb404cbe7c4682b4761fbb7b931b` | 33043 |
| ci-2138 | `cd50880edf52091bab02583410346988e102c272a1c8dd33b8ac4e9ca411a58a` | `990808473de839c9d1661f5c1d0cf4cba0cdb94e246655b11d29c5a1a9ba4b18` | 33308 |
| ci-2150 | `d3824c250ee8631cd47a119b9567ad61dbb8a472469ecb13b9f0dfeed7050664` | `051a47861b52b3b44ca55d6f0b3b5775bc68f097d8c95d287bd61e45617ee917` | 25961 |
| ci-2160 | `21dc654ef074a8582e90dd1f9dd16fc2236b34bbfd409b1fb48ce156dfefe309` | `87807d194240248e124f6d8a61209743abe40e0897f76ce4149c8b9b3aee13b5` | 22797 |
| ci-2161 | `402e5b75e56118abd88c34d6a2e1f15b5daa5368b2b12041c653ea5791f08c01` | `f22d1df5bf47bec20fe082d81feb6da008f38c5c105a8a815c0286c9aae18cea` | 20484 |
| ci-2176 | `a4b453f3b891ef2fd7508ba2ad1f394389c0141d3e29a37d050b14ffc3fa23ec` | `c2ba2790a9820865c7e730ca024e3bc84b53916d45111e77c880bd4c8d9fd3ec` | 33321 |
| ci-2177 | `8daee77d7a6a1ccf1ee5f8f8c98aafe562d9a1bd8262bd8e55caf92c67fea998` | `33fa1d97f04f64f10d7fa54a78c771c5a85568a7df4a94546be2cb3b5dd1b585` | 22655 |
| ci-2250 | `30e8f362babf4df70103551190959327a631eddcc1e1d775ba7a793a7a3e37e3` | `cbf62c620204313354c1d77dd31c871e7c0bb00157a8ca31e1ce6c2db384f3dc` | 19326 |
| ci-2252 | `107b99acf4ccab2840ee697d53f0f9108e79d3f8d9eae02466e5650a188f8fb4` | `da859ff7fa299399cc75e29d7698f3af074eea44c4ca217333b05b341a662cee` | 17846 |
| ci-2254 | `21809cbe703aa87c520469be8814b5819bcfcdb3ab6937d2678a448053fd5671` | `517944768067e72bb339da74d3f2aef965f8ba1886eececf76d8aa96bdedc8ce` | 16101 |
| ci-2291 | `83dba85b9dbd18ceef1a527cbea30056677cfde67e28a48babaa9ee8c735b290` | `3ec733a682befb9cf728a09920341f6c64e4a77e45e1378713edc3dde0750756` | 38158 |

| query | rater | response sha256 | status | grade sha256 | outcome |
|---|---|---|---|---|---|
| cb-06 | primary-rater-1 | `8d80c50958ed9046252f5cf91eada83129c86822912ed787ca0aaf21503a74f7` | answered | `30c84935caa7a917179e1bd7f1399ea1f8bd5f075d0715131d8ba02069c82b79` | pass |
| cb-06 | primary-rater-2 | `b94b5459819a9e8cd32a491a461d80c6770cfd9f8530248ea1f5c0db31c4b4c1` | answered | `21e8f651eaf5b45ea949464b77c6f8167e0baa36928320e59dca6dc4036d0007` | pass |
| cb-10 | primary-rater-1 | `e2346fbdb1d98b636ee07bbf651d6d110bd5176d1b6ef38ceb472696ca9df876` | answered | `8de2bdc4ed4b5a82e01f398a3ed456d22d78831902b17880270da0827d72eb1e` | pass |
| cb-10 | primary-rater-2 | `7ee4221ac7e43c923a179e1d90f3d6a1d3b7d52873c2961749076097ecaee7c9` | answered | `408c1d408caa19ef136b626f0eae2760aaefed1bc98cfceda6dc03a749f260a8` | pass |
| cb-17 | primary-rater-1 | `e5c00644d379b9d69ea7676a22cf917726db0fa5a8c9c4970708135481153fa3` | answered | `3f97a2ded23b428572a331494f83de4ae89b36782aa84a41fe0caef83e7964d0` | fail |
| cb-17 | primary-rater-2 | `815a2550c3af632eeb34a0994b2d1975d9e1b42b8d9c6a4b4452913bc81904e1` | answered | `c9cea7fda89ec5fa9aab0b76bf00c7c080b478003ab99b341bdfccdcb9720c63` | pass |
| cb-17 | adjudicator-1 (adjudicator) | `7eb96ae673ff1d0b75063951a60adda57fe9d339fd8b239857ff39c4e938e990` | answered | `0067b5c56cedf565793c01faa35b693b0f9ba05840d1a5856cf7b503d276981d` | pass |
| cb-18 | primary-rater-1 | `3dd0235b8662286d38773932f074455a936604e784b81f719d90458fc53f8809` | answered | `cdac440e585e84c7dded434ac66dacc5876e1ee48102dea3697f61aa086bc58f` | pass |
| cb-18 | primary-rater-2 | `52308adc0740ec0ee929fa3faf0e36b6c04b03e0e79696c1544e045914a8f15e` | answered | `9f709547579f15092a362ccc278f68e2eeb9c1bcfa55047e0810530d3c1710a8` | pass |
| cb-23 | primary-rater-1 | `f99c906956a9cad2df49c52854e494ae79f595e5225f7e53cb17916a9ca5479b` | answered | `24c582307f5bbc16f97ba9929876f6503e251d23dafdea2a44561f121105bc7d` | pass |
| cb-23 | primary-rater-2 | `668e422446360f876a275f870b680410ac0001f736085d806b4b3f6e7e0effc7` | answered | `2ed995a3dc3f218b95fcbcce29285e8fb0ef8dc2effdaf23d85663cffa68598a` | pass |
| cb-29 | primary-rater-1 | `e71241ed21ac36235e9f8a33d221849b11f2856cbbf5740b49278d0ace5e2c95` | answered | `21d3ce9b5f058a2fa27af02366ceebcfc8a0c95c4eb8fe46e75d6787d3095b65` | pass |
| cb-29 | primary-rater-2 | `8870db0494423980a6782ab1678ecd5e87bc2be0f223ff9588383a267b95c0fa` | answered | `d6a934310cfdfa75dac9c4104e5224bc532bf6d9512ac9505d64918246dea793` | pass |
| cb-34 | primary-rater-1 | `7758b3c482775387c2c77fe06246c522b061e0f278ec47a69db8a79e6f2f6c2e` | answered | `01e68040a0b12ac130668f09740f8b9dd6b56dde4efcb1a9214db19cb4dcd250` | pass |
| cb-34 | primary-rater-2 | `306bd6758983a4710e73c38dcd0d6dcddabed582a9d530f2dfe314b7ed91183d` | answered | `75c97a0b7be3203e66e242447d52303db90f5499bfd8bcb269a44732b5dc498a` | pass |
| ci-2 | primary-rater-1 | `37e03e0cb7ebab9c12ba7b199db60fd903e49b8546cc3a248ac8acc1d3c6d8b8` | answered | `bc2e31dc25e3aa63d16200a9825f07dada3c30e54547dc3f4b6247362560f8b9` | fail |
| ci-2 | primary-rater-2 | `ba2dbfd387ff2730c81f78a86c547f23bbb7b229bbb409cf1e2925c77ff6ddde` | answered | `510e45d807bb711489403e01fb183286064f7d731897f319007c43628560d6f3` | fail |
| ci-43 | primary-rater-1 | `4653831767df7d1f48211168234ba6e3406a07454430849e00fb15c8d65667a1` | answered | `f702b20028423676edcd893a3391dec95e721aac5d72ab05c34e32212a65723f` | pass |
| ci-43 | primary-rater-2 | `7c83fe06f5974a638b18420c16d46fcb9e50e69deada044131b2a9a33dbbe4ec` | answered | `26ec23de7d2513900b6feced209d2077a1919630a5625a04480e8f9330171591` | pass |
| ci-298 | primary-rater-1 | `74886e76a9fb13e3b97203319f22bc37d032f3d9da5d70a2002aeee0e32708b8` | answered | `60edd3162f984f64edb95f872b448ed412cdff13d574844fbb2de14ac9eab558` | fail |
| ci-298 | primary-rater-2 | `4329bea2601701b4ec6c86f691fdfb0b8825691ef0dc11b9c3b18adf9e51825a` | answered | `2fe986026cbce7d54513681f7ad5455f9784d1cd4d1dee064074a72dff070162` | fail |
| ci-316 | primary-rater-1 | `ad7723f93a88bef593b3eb4faa4f1aad6940be2741eea24ea8538d57c59f8ac1` | answered | `3fe1d4f69acb829782121d272ba34d9dd2f134c27885dc0d18767e3a4b899968` | pass |
| ci-316 | primary-rater-2 | `59afe5f29a3a6b73544eaecadadf6cd1c80cd88c434b9bb69620decb023a7971` | answered | `282470ead6c7c133c8c3aa586f5fd10e340c6fa926500a6abe0ff4a540848d10` | pass |
| ci-346 | primary-rater-1 | `b73a2364f6eb341e15b2e326700831d7ca87006f1d197ccd5238ea7eb0a30dd1` | answered | `1b7ac76e8d9cf5c4f1227fc5c68a9a3d8e0e49f1e32430f47a08e7eccfd344c7` | pass |
| ci-346 | primary-rater-2 | `51560620183fe7760c76847f7adf649fb4c21c6f3cba62985c0066d7a347fa60` | answered | `51f57ac43b88ba28dafeff1d1b6a3e251179ad2984134de539c47675b396efcc` | pass |
| ci-434 | primary-rater-1 | `f54bdbccdd80f4c63525e2a8bf7866fe10d616bdc902e6ba6748f17d09707cb9` | answered | `ebd298f9ecf16d93cdb331fda45cd89e60c9fc28ac21e43b9334e227f929050c` | fail |
| ci-434 | primary-rater-2 | `c79ead9bc7096c1c27ba4a72513d1b0ba49ee6bbf92412a5baecf501daeeae88` | answered | `e4148a08796e06b9b6590dae896cff60637be7531266f67e58538e3e69355516` | fail |
| ci-470 | primary-rater-1 | `bc55c5d71774138922b4d0862ecb41de986e5dd7330b4b48501a14277b5c9c91` | answered | `88c268f143df32c4c617ca423fbe14e49e7b4413ecb691d3cd9e458e38e6204f` | pass |
| ci-470 | primary-rater-2 | `66d30ca246740dfa8f564cce074d100d948721dc7bb1e34b5e5200d23d08a803` | answered | `82bc6a89b89e03a776eb3866c18e94ed5ff779ee3e4ac9901fe147b83371e7d9` | pass |
| ci-471 | primary-rater-1 | `f03575a82abfb6b6a4f564d492cba0a86362d55b2d955f7f2dea8683f481e624` | answered | `8f1bdadec62a3a522faf96f39464e54fa8a48e38b49560b60fd0775ba57a24b7` | pass |
| ci-471 | primary-rater-2 | `57278e853eadb0104a2bd00f5f76e4ae3ee4b615a6937a224e8709c9890e1e68` | answered | `5f2b051de3f877096151b8a1ac4b330fee79e2de60d5ecd1e65aa07b9ee4581b` | pass |
| ci-533 | primary-rater-1 | `8e60276ae45788e54cd34bf6e390c00aa57c9dce1bed79e693617ff178f86ea4` | answered | `440392b3d70fe8fa39eebff80eed81d3f486d4c953ccb087033525028502117b` | fail |
| ci-533 | primary-rater-2 | `a7dbab1c8266096266fc0100aae020c83226f102f66e4821aae5a31c9a619a71` | answered | `fa0e23f79b5f0a6c50abe390fd9dfdf76071fad88f738ecea86f4d30ef56dac4` | fail |
| ci-563 | primary-rater-1 | `c178a19869e5b7b0f1a5c4b8fa60e1e99ac7cc6afb46e270ef49a3797859ae32` | answered | `b36103ca4687590a78c878a98a61be9cd57f285543ec6d77fc1fba16d342013e` | pass |
| ci-563 | primary-rater-2 | `147ea59b5af37fc6a4454bdffefe522d9d224cac8fbed9ef3de78cad2458ee61` | answered | `71bfabcdb555a46f339b21515ee79d73c7cbb5f8154bdaf04a02c3503d5224f5` | pass |
| ci-674 | primary-rater-1 | `c987650b19f2a9f0234768f71629f9ff1e39693c0db1ebeec9aff6df77defbf9` | answered | `cf196673532b4acc152d72e47b5ce9362dcdb6168ff3d81e060d5837330941cf` | fail |
| ci-674 | primary-rater-2 | `41ffc64563e06ecae1aec35117c3c10c00938a47c0bd6632027b2ad5d01bd879` | answered | `cdd13e144176576783d1dd39899c6204f9c768c3cd5b59635443d37fb3251862` | fail |
| ci-710 | primary-rater-1 | `f722eabe134083649982c060e3f7a4edf84d5b25539cb02f52b290b945fb5a60` | answered | `faf2946ba94a9ff0a2374e72e0ff764da20e423097543964ed38d15269f73728` | fail |
| ci-710 | primary-rater-2 | `2058559fefc60c681d15882f7ea0da13230e814c707f48a18bf482712278d5fc` | answered | `2483037583033b4d96f97ebecadb09fb459306d2363fa5b6d964b3d1a9d458d7` | fail |
| ci-724 | primary-rater-1 | `267aad028f67354d53737866e0a84247909d47323cc6f3724b6ecf6afeb614ee` | answered | `a5b3d3555036ebd0c1d3277a07f0834fb8a0db6f9eb3f66b52b4e7a4993022c5` | fail |
| ci-724 | primary-rater-2 | `6a4e7c35f19991e2570e8b9c14ca94b9e355c3e26105ebeb013bd125424abdd0` | answered | `b50fb7c2ecefd50ec64bf22ee289ecf315925cd0f460af256ec92e3a25e977f8` | fail |
| ci-725 | primary-rater-1 | `99fc1ae30ef501ac992fdf1ffc40f3e4fb653e7e57f8bb488519f0809a5c592f` | answered | `3ea2c78b2d8fc3fb66a50b1f21b7313d4f9075c1aff906c4fecf4a0a6c6d7c2d` | fail |
| ci-725 | primary-rater-2 | `4e674c4bcd56d392e5f66f00ccacfae30c349f7702db4ecce58c7486258aa2a0` | answered | `e2393bf8b39bff17695550437e7d0dc1e7de24fb0521a58658018fa0156ab67c` | fail |
| ci-821 | primary-rater-1 | `8432b89cb2456bdc1632f0b3f8cf4bb88205942b43d8a7f312b2adb30aa66fdb` | answered | `7924d4645edca21df63e7861054c311c044a449a3b0c67c63ad21af4a9a419c7` | fail |
| ci-821 | primary-rater-2 | `a49d197706ddf44f39201403bcede7b4e1b945534ddd55f09458f9216c1a9780` | answered | `65c271bd52dc74aba5bdc68ab98cd5c9b5ea6a17e1b40b1840224f9b00615c41` | fail |
| ci-827 | primary-rater-1 | `55c951868eabd6e619f6d352fbbedf284f2a060de0ce6a1054c317dbcebdecc2` | answered | `31b15a30e6a6fbc35ec08ffd6fdffa59fe272c33a801adcdc41202ea9d7a3a50` | pass |
| ci-827 | primary-rater-2 | `d930cf32b7df6aa0dd70f8e8eba7a71cb38a7cd7259fd7ebe733c4a51660f867` | answered | `2d05bed0f78113730028d1deaa56fb40809437545c22bd943c4e2716db020c5c` | pass |
| ci-829 | primary-rater-1 | `e2c52ccf4d204df5d0284eac75573583132919d5d3f7d7e8fb57906823e3d7ab` | answered | `af1a2c277d984f2214ed94df2ef828fff75efccae101aa27e4b442d22db7ea33` | pass |
| ci-829 | primary-rater-2 | `083a23eef9fcf343c3e23d7570fe90e0e1f1e2f515fd0d1699a523c7f07014a5` | answered | `0c2096e0d6b6a48f55816d07d06e2e4d0b4be21b2011d34fc70b8c82134c5520` | fail |
| ci-829 | adjudicator-1 (adjudicator) | `6044c6d54d40d04d3227a35e4fe16661cd84696caf0aefb09b13c1d4c6f2892d` | answered | `2742bfae3a71b232582cceaed09835a30664fd45e972cdd30284b705107572f9` | fail |
| ci-852 | primary-rater-1 | `c7c41b42573514b16c154d53e4b51a9a52dbba4a9b60b85c70b9169316edb363` | answered | `0b5d6b84ec20d0877f4e74ac3c0914dace5163978e4563238a366f24c06ee955` | fail |
| ci-852 | primary-rater-2 | `1236daa0604c7e675fdf41aa5dd255dfdbcf399d0369aca776b2135e4b930eca` | answered | `b0b73f933d96e546ef4be38e39372e2fdc2a6862e664e5f2c02469f99440bb6c` | fail |
| ci-920 | primary-rater-1 | `34430bfb8bc732a1eca32a36cb1d2e8ffd574467a6185948529f86bd84407a81` | answered | `a13e0af14141e48ea0de37f0e10fb5d62ce8ea16ffba5abbe2dc1a5d493a7e56` | fail |
| ci-920 | primary-rater-2 | `5cfa25a68b27c2fee5ff5c15779a919c387358222f15682c7d8260883739f1df` | answered | `7df48a2ea3ddebd0d731751dd98cecf8a89a3019373d740edbdcfe5923c1ae79` | fail |
| ci-1025 | primary-rater-1 | `c6206bc44f6cd2f0e6eda96c8f71721bcefb5678b5e4e454c2bc7aafedcc4ae2` | answered | `800921483fcc54a8ba362e0016484a3ed685bf2adc259b695d1cfe104f025f17` | fail |
| ci-1025 | primary-rater-2 | `23d996e1a54247ee352cb3c51690330d15430d5df116c27557f6fee1b9d297a6` | answered | `015b514aff476e376880546b9c94b1f8b83fdb55b497179aaca25694e7985b79` | fail |
| ci-1060 | primary-rater-1 | `185a5157e3b92fa19a144bd321c1df72c14bcb4265b3ae4886b48534d707010e` | answered | `c4206d980cc05b1f4a35c777fa8d9bed32d946ddb8e6de36433f17d217a4e6d4` | fail |
| ci-1060 | primary-rater-2 | `eb5bb8790c727b16b73615fb0d2c5d486268d7a8958bc13166309a2f02b9b57f` | answered | `7eeffba60a567728c08b5f26c493ea9ed2f3d2dafc49890964c87aa40d849625` | fail |
| ci-1086 | primary-rater-1 | `fe11a50fd83d351a6bfd965d81f6fe2f8ce00b34d214ccd325e8671030dbb693` | answered | `5b1bb46b4589a195403c17e5176074a28d151b8090efd05d9fd33bf62056108a` | pass |
| ci-1086 | primary-rater-2 | `f9222936bd093fdb36c4dfc95416e3fc3b892e64258a65cb3afa3d5bbe8ccf45` | answered | `345e75760fc58917f5ebef1034779a0dd04d5ddfe486608282857822b3f3b0df` | pass |
| ci-1098 | primary-rater-1 | `7d40f6e2f1b527b404e58dbec58f4a42dfd8efd0738032222f47f710734698f9` | answered | `2ba34724beb22e38d998b886b373dba8de4c20af01fc3d2e2b4d94e9190002d8` | fail |
| ci-1098 | primary-rater-2 | `3ad32d3101c63b523a85b6790b380dcdb1810e56692dca39a1ef36a2c5abba5e` | answered | `df62fc279a3f82425f445491abe17074c49b0421ac8d0a390118b82988ed2131` | fail |
| ci-1111 | primary-rater-1 | `cedd32c43058d6611b77b39f6c267a024bbd7b1943ee5291dc15a6ccd30bcdb4` | answered | `579f84575ceacac43f24eb6dc8efb24d2cceed4a6a73050102ee7fdbcaed876e` | fail |
| ci-1111 | primary-rater-2 | `3a3f30b0a3a674492525e311bd49c2c449a4101353645984b5f68b10f06e893d` | answered | `0ab1653d20f85d0a5c858e7e4e549861f90028e24a32f7ac7e6fdc520d3453b5` | fail |
| ci-1148 | primary-rater-1 | `c285577ac58969ff97d08aa97e5932b7242283beb8b3e796846a031f85362fcf` | answered | `77b63cbc5076441e68ee6a166e99f6afbaa36ee05eda26ad3583bb0fe08c69c4` | fail |
| ci-1148 | primary-rater-2 | `5b8543e26de84918a6e00467e638a97315cafd721f977af7d6d7a4ac273a4e2a` | answered | `6583fe3de1d632e985c87639307cd61a0fb9f403ac40ea24e3f21b5031b90762` | fail |
| ci-1151 | primary-rater-1 | `695a4aa3095fa5b275d9aca178dbae7e772ce43fe4e825588af5ec02b15f4473` | answered | `6eb4d41643d2efcb17be90a34cc5af4e9dac376006e1dc501bb56a729b5696b5` | fail |
| ci-1151 | primary-rater-2 | `ea3769cca8691bc7ddd7cdf9970fed40842568b32cc991a5e6c8d845324fd855` | answered | `888ec0c5f1b742efe4cdbf426b215983d8fe218c6375b399b56f6e5e79e44274` | fail |
| ci-1168 | primary-rater-1 | `11878c3a28564059326322e2431a9ef8be2fbb02306741679ff76ad921a7de83` | answered | `2f58380f5ce49f59e58b086f0b267e7eea228f02cf87cd0b5aa8380c134d1477` | fail |
| ci-1168 | primary-rater-2 | `2a05eafbac20650dbb50a46dc5a7f34d886af2ff0ca5054dfd4285a9d8132622` | answered | `0a9235af405617c70cbccb93a7fb135bf659b66fe41c408475f334fd87dcbd24` | fail |
| ci-1202 | primary-rater-1 | `6f46831471b7bb2c3a65ca25bd8b9ef5048bb1bf72bc1ddbab29c92ddd38a33c` | answered | `248e69387fb74ccdf57b19917ff183d6edf5aa89be4f89e39e000679bd172b59` | pass |
| ci-1202 | primary-rater-2 | `e4913a217653ebedf04505eb37365444c077e5de8174ee74e9694754197e29ef` | answered | `f1885f830b746205fe8ece4bcf9060c3cbf9a5c2a065959352f58bba21598b04` | pass |
| ci-1221 | primary-rater-1 | `7f167f8940fd5b8f71b8e5eac2bb1e8fbfe3c4bbbe0962cbb0af7ada2c24617d` | answered | `491f2832f05f3bfc52dad3c0fd06e7ecbbae33a69ff0a3770ba61b546227058b` | pass |
| ci-1221 | primary-rater-2 | `d7dbbc498d7444dda80f99b6aad7dbd32b521281157fa2b85f962a652d8b31d4` | answered | `d1b1a9663cf09cea14687e43a1114beaa6a61f65beff6f3902c613b2c19178ae` | pass |
| ci-1289 | primary-rater-1 | `6948059ccca7cc22056b1ac1d97be73adca15bd0cd5e7c413a2183bd0bd8eee1` | answered | `89232242b88d06518fa3a2c6f23eca44092e45f4d1587e84f645dbaf72ef63d1` | fail |
| ci-1289 | primary-rater-2 | `bad14c2b8db6a9f05a26dd7d9ab1e0f22e23e5ae81b0cd8bb0cbe6e3906a8097` | answered | `50f1845533dbea24513d5b15e38d2e0b09219ea2d69f358e932da7c15dffab5a` | fail |
| ci-1300 | primary-rater-1 | `867199dd68582b8bf2a20469de1fe7ac43ca67ef7a3c4d2f8916e85576ef60e7` | answered | `aa8f2eed343d41f1ef4b7478097ab2b9bbd2f3f44f7b48a532bdb6fd7ae1b388` | pass |
| ci-1300 | primary-rater-2 | `6f7d0ac8654406ad52a0ddf99369ac7822dc27487aaec29265a4773470fa24cc` | answered | `c1aba488aa1d47c3f5830cd96f1340b6a6fefdee03781ed792d7ee83113ed7dd` | pass |
| ci-1343 | primary-rater-1 | `baab29ddace85cac3d8ff25f9d145e72afd897aa8f76ded609b6c172f389acdf` | answered | `3235fe8d28ea48e613972f45ee0199e19ec141df4e04e828010023721a058a8f` | fail |
| ci-1343 | primary-rater-2 | `15e3e8f6449e79c95e42660b831f96d78c249450cb87e8c14e2e5e049ff9b187` | answered | `2d94312f6079e86eb5c819cdc5200c31f0d296df40c48316772716501214c927` | fail |
| ci-1368 | primary-rater-1 | `4b31e736230c50d4993c3ed31aeadc49c6aa45cdfad47126f75b064f66a9b323` | answered | `799f5ff4874f213e7f80a9b0a3006f16e481891549622ad678bf99ae2db0e4d8` | pass |
| ci-1368 | primary-rater-2 | `97689d5a188b4c64a7363cc36b25911fb9c719e6efabc7e594de8154801b0ca4` | answered | `4fc51af20dca5581ef17884b07c67398cfab45b0c6bfb7ebc22c8f687b629415` | pass |
| ci-1416 | primary-rater-1 | `1e5866e02e260b4ff1e8c8e0ce39cfc985860e7898c444d183c8d26944d38638` | answered | `1077f1ed0ef3135ddc4b135b802b9e9d42eba120c8dbc0ef0cd29c2e2e2d627c` | fail |
| ci-1416 | primary-rater-2 | `082dea5b717749cc9032b3e0b30b23bcb60d05d034975c5f7249af2efa6d5d42` | answered | `b8f73e2585bb6d241bf5dba696907bc105b06f0b609fb33bdf73f8aae7341e9a` | fail |
| ci-1461 | primary-rater-1 | `17961dddc04fc41987b1aebeb86d72811d62a79c9bcf8c9b1c81747316470572` | answered | `6e1b867c5d77661ba623b8934f4bd3422c49e918fb1296ac20544e5bc9169025` | fail |
| ci-1461 | primary-rater-2 | `d5a948c64c511248068bf568797cfe40e3786811914a2e6a4f9136d129979da0` | answered | `16c777db7df581e331c0756e6f2e5a7a153d89f0ecd24686c0128db96a8d6705` | pass |
| ci-1461 | adjudicator-1 (adjudicator) | `8cb6a821771097d2fb615349c73ef94972bc772aa437996b4a7e2d034f873f05` | answered | `158a3aff92b8153edd60d9b035bbefbd63bf4c9b69ca8dc0e40c606050936c3a` | pass |
| ci-1499 | primary-rater-1 | `8dbbd76dbc99c51014010bab1fea69d77e68601f1da69bdec30bb2373f04393d` | answered | `9ed3b781be96a05588ab25eaf756f1e56ab8f8989de7a92464a909f2192df1df` | fail |
| ci-1499 | primary-rater-2 | `a9aeb17a28e777fbc8ad8e287b53daf3c29991d31722fcf97b00151bedc59133` | answered | `6cf5fc418bad5fe525e679ec5b907f31292d25a6b87b6c6cb27069c596c4d467` | fail |
| ci-1505 | primary-rater-1 | `2d5f13ea8c7713a960a8216d3d626746c7022dbd9f66caaaed4a5702d9a80077` | answered | `5d3fbf2f26bb300b66f3c24d421b2505e699f42b67eb00e23bec4283504bfc31` | pass |
| ci-1505 | primary-rater-2 | `b99f43f52d4d64999fa0a2d6442490568d597a67c7d6a2de72039229ee8834f6` | answered | `39819ef949e256a62d6c732776f40420cc928a8adeac10392222faa6bd319a5d` | pass |
| ci-1564 | primary-rater-1 | `29fc7c4e4929478144d55f031737f904994b0f619e8ead43854018fb79f110d7` | answered | `cac837165dd5825a7f4b4e581007a6a113e71371f637487cf7d486dbc87fe536` | pass |
| ci-1564 | primary-rater-2 | `158661b393b52a5e13dbd154191fc325f8c40fe432d44b7385582be8854a23f5` | answered | `6db420ffbfaeee24e3c80b1163a5cd4d5a3946afc884fb0fcb88646646029d34` | pass |
| ci-1592 | primary-rater-1 | `24834e9ed8d107bc98faa8811b9a633b7c1f2289fcb5ac5519aa6e854e587bae` | answered | `8c2ffd7cb9abe9371a27b4150667d18f92647a4d664dd5913af349b424d2c28b` | pass |
| ci-1592 | primary-rater-2 | `2504e62f7edc7bd3d6392742d3f32cd1bb6dfbf8ff1db5068567e4702ab5ee79` | answered | `14053d7cf741efb9ec2a8827d55df0ba3e6898c88bcf91d57fa1912521f26658` | pass |
| ci-1631 | primary-rater-1 | `ee0035a4f8ac00f36f9bf28bc55a91b3d0986a4fa5ada757ae8d52594e43af29` | answered | `be4d4c0477501aa801d3997d5fb4588a158631d002420ef99d7818b0d8d6e617` | fail |
| ci-1631 | primary-rater-2 | `6f1cffff65e6a2ab16bb0f5013646f7e8a07bf776c87e1d2be0f3e509a190d4c` | answered | `95242f036e5123a07ab829be8bfd704f63a17895396d970c0a138fa3c4a6f62f` | pass |
| ci-1631 | adjudicator-1 (adjudicator) | `54a544e17b7a6f1e03984c3c6a3ace0dbcc2fba7c330fb352d458874df9e303d` | answered | `84cfa21df93bdf340f868601a37bfce3e5996611c8d128a090486a49a052b1ac` | fail |
| ci-1758 | primary-rater-1 | `aeb7c2ca6b154b6f1f2b05e96318f9ebad44dc1158de186ed386d4598f1ccb87` | answered | `c84b2ca1b33bbe7f5efbb43943569bc26e58b4be77495ea07e64a4a45cb9bdb6` | fail |
| ci-1758 | primary-rater-2 | `6e9746eeb0a755445b435850aaa270982ceab1c85cf366b0341525413bfe2228` | answered | `73736be523eb0519c74d49ede526358e758ee9925d93a4fd0f56f6e40c7ce53d` | pass |
| ci-1758 | adjudicator-1 (adjudicator) | `a697c3aab3e2eaec8d28bf13b5e6f0aa0421fd1d56e49bf3979805c60d48b32e` | answered | `79ea66e811dc68e4e26db8cb90cd6a61e87ed81137989075109425fc8c942631` | pass |
| ci-1790 | primary-rater-1 | `64407ece508dd552f0914b6f612e07255da1742a02268728b8510c383c891f58` | answered | `e6c836b8f0058a886c401d40302acab739d2a973acb32ee9177081e525155ced` | fail |
| ci-1790 | primary-rater-2 | `d008ed317b0fd27511341ca94e17ed6ebdfba259e3c4dfe6af461b7005e96e4e` | answered | `0380b49c9efb57d3fdade3323427e689b02cf8af77ccdec9f99fc44d7d810635` | fail |
| ci-1861 | primary-rater-1 | `86663c1aa7cd86084979c5d78ec18f01284c153b7fa95d9e73adbd93a0d49481` | answered | `d8f957c6782b983ec213d45380a7b12ce52bce7797b0f7abac0731fd4efeeaf3` | pass |
| ci-1861 | primary-rater-2 | `bb45cf27d6f7c9a98143b1281b9eb3c58377d48a2e78997b85778fd509c51f97` | answered | `f1bf649211e9aa49c2612c6f2ea8421415134e3c46ad6ba59745c47824d0da2c` | pass |
| ci-1894 | primary-rater-1 | `4433b37e1b48e36dd1899cf16956b5bf261141fb1b3016af5173c918f35048f0` | answered | `b4624f87ad1a6fb4a6a479d96a2a6cf9f4f3d240cbef93d2c23be190e3e765b6` | pass |
| ci-1894 | primary-rater-2 | `ef924f68b1349bc74e614a490c97eba8e5e7c973266d3c3da2e531a833b8a3ec` | answered | `a27a686a68e716757d97c8854b46d79979223fa47a9ac4c6684f3b0e0ee6acc1` | pass |
| ci-1923 | primary-rater-1 | `716a62c5ac7b2913b5e61cf5d99a73710ff3095ee1d81a66b971846b32496844` | answered | `1bc247f44bf75ccb24bc6187d9263af948659bda2454c949a97b537ee7d66e36` | fail |
| ci-1923 | primary-rater-2 | `f63ba75ad3a59fb60ed51cfe281e1884dafe4393bfb42179f2598f3d1ba339c5` | answered | `2c91275f901423c22690d5ffa4e96a944ed36d642835c37c0e2d6f170220f253` | fail |
| ci-2007 | primary-rater-1 | `7e97631319686e4b43b41dbba77a2a123f12505cdd5a52917bf6fc12e881eb4b` | answered | `8edcd6df13a1f2b2583cd08c7d0f7d9ef6c235cb1df5ff2c66c0b1a45505d346` | fail |
| ci-2007 | primary-rater-2 | `48acd1b112f5bed0386b83f9aa4f3c6a5de672d51cc568bf0a88fe5f8b1104e3` | answered | `70e474a9530132b2a5a4442d17d2a250a6960a564f0d4e76c973227aace31570` | fail |
| ci-2014 | primary-rater-1 | `04302908108e0e303dde44b0ad1734c54ef21904719e0538760c57d9f6a3d724` | answered | `9f33469a7bccef66c084032ab9cc41e6b0a419b12eb7759e692ac772ef320421` | pass |
| ci-2014 | primary-rater-2 | `00055744c8959feb40a2a222e13256d887172e20d7e27f1ce23aa6ea9a38b1c0` | answered | `84765115486cb59715b475131bdea60aa683f494aa6381951480930951bfa595` | pass |
| ci-2138 | primary-rater-1 | `6ca292ee6430d0e98687b5077e2cab2f20a5213b23fa408c4631c6d0da49bb59` | answered | `ea383859b16237200a2cc5b901e054701fccf0f95308ddb6ead3518d77338b2a` | pass |
| ci-2138 | primary-rater-2 | `46f841f795bc3a042d14385603f651652d3c4c2d2c9c5066090bfce1079dfe03` | answered | `d9d61586ed99f9a399101c92d65ff634935f91554a10d7ef08f9f0f5443fae3b` | fail |
| ci-2138 | adjudicator-1 (adjudicator) | `d78bf89445928987f5beca68c66f1e855b9c98073bdce11fe10509eba257b846` | answered | `59d45c1d9b5b22207f67cd96d206a6053c5c7e00b90b52f7baade77a135baa29` | pass |
| ci-2150 | primary-rater-1 | `aba3bc3e42440b6e85b3f5525bb8fc2c4578bd8df86a64d7c8422bd9a8e40a3a` | answered | `05a977dba2d98155d514c68b1011de9748548ea582279018a46f16c29efaed8f` | fail |
| ci-2150 | primary-rater-2 | `c8851ee711e8cf0cf7eaa0a8a28cc17cacb76c1b8a5083cd0f55ee339a6f0299` | answered | `50977613e3574649d97ccf14b94588d255bd4d7bf03ef54845045616337fdf6d` | fail |
| ci-2160 | primary-rater-1 | `a497dd95b5e7655cbc08b6db9d1e638d0431927ca686f3274b4751230d21c23d` | answered | `1d59e552ffb939f27709c79a1726379d65f5946e9a8db820b1cbd6042021dfb6` | pass |
| ci-2160 | primary-rater-2 | `e67403db106d609d72f954edfd1b7f7318d22899a081121a31f2060299ff962a` | answered | `6f4801c0535a3f4c16281db3c3a7c6fa27d7350eb9a48e432665232c41f00250` | pass |
| ci-2161 | primary-rater-1 | `1130d13b1b6da106fa359317a4276a6edd1915439486e3e42fb4ffc359e052bc` | answered | `6ba7d774406c5967be0d2788840e1d68e6c4636cca975d543ddd7dfac2e3907e` | fail |
| ci-2161 | primary-rater-2 | `3f9e890a463bb37ad3198d0a11c46f35c19b0f3d54b3ca3c852b13a736a12456` | answered | `bbb8ae7370245ac0cdfe7a239f9f70416a65ffcc9e2cd32e4188599c85742f99` | fail |
| ci-2176 | primary-rater-1 | `e3ffde86e56c820643b14e873eca87aff4746a2a579878cab11e99d0a693b8ef` | answered | `217bdb2606893fabc0f614099aa8cae00f01dccc28d1b6d708cfa6582b415a17` | fail |
| ci-2176 | primary-rater-2 | `bfc1f6000a11a81761333ae65ef2bb223e455ed20f224125eba1ef5a8615e31b` | answered | `fc22655b5ef8a0827ba0f73adb3290490549004f2d2aa5823ddccbb9840a4008` | fail |
| ci-2177 | primary-rater-1 | `0f518dc2be0628a45a2521d72f8fefbd543ccbccc37ab73af407d07346783e35` | answered | `67bb4ff286593b356443a275d9ba93d588dec0648dcdb0bf803f29a6ff9ea2f6` | fail |
| ci-2177 | primary-rater-2 | `227870bb96f995759323b1ca039e042caf0251d882d69571214091cf0dfa2553` | answered | `f74a40191951426b3f297e619374ba7278f0d9953debc6c28863a5be6763b39f` | fail |
| ci-2250 | primary-rater-1 | `964a3c0dcf9c042cda1abfff1fbc565bd82802ba4862f1e38290d09250bb6323` | answered | `cee52e42b2b2c77c4bc04da82ddc83af78fc7a9b4206c6a7a723d75795a7eece` | pass |
| ci-2250 | primary-rater-2 | `a581afa6ef32fdbbdb328b33091aafcf555aa47d6c56f115d3ee1520caa4caf9` | answered | `7b1dc4ad84ffb451f27ee9f855d8206a549a26bbb4e8c150a0d42cd7589e4677` | pass |
| ci-2252 | primary-rater-1 | `0eb2a4ceb08e1b8741c268f87da9096d2cc1e106387473a2302932ff09812a8a` | answered | `e76f9e4f6e086edad30f0d342450eb57c020ca7e2849fbf9cea95d8da22901e8` | fail |
| ci-2252 | primary-rater-2 | `8169809869f6a94774a78eac9a803dbe4f60a5704ae263f51bfee07ac579f2c8` | answered | `b1e67005d91470e3083f4fcb170965f67e8430c6dfee7996a13a1f0981fa1fff` | pass |
| ci-2252 | adjudicator-1 (adjudicator) | `fbeab5ce41dbed27ba1baa2d98654d3e46e021374219791e58831dc428c38eb6` | answered | `d8939f860896313f75ca06712af88ade791cec8e9adc91d2436cb21a25ce9cf2` | fail |
| ci-2254 | primary-rater-1 | `ae73cb2328d40155e6c7d6b668f71bbfedb34e4c70c4b873e34af8af50c9e0fc` | answered | `fcd552546c745a17d2e1cb10b6e4c880ef21806947fd4a4d9a488f985e9554b1` | fail |
| ci-2254 | primary-rater-2 | `2f2b4003bf8c5c6917cf820af3a8f79ac05d3f731b6674e69dd2cf4678dc80d3` | answered | `1ef2a5c90656312e9972a25575455eae815680248439afaf9329cb63613d5d71` | fail |
| ci-2291 | primary-rater-1 | `a9027079217270b21196b4de2c7557a060005d1a74066a3bf7b67ae99d16d720` | answered | `e43152f30a8b3718cfa445ea5667d1a9ecdd8968435a911fc4180ea1bd252b8b` | fail |
| ci-2291 | primary-rater-2 | `f0ff8704e3f2fad38cd45cd6e5007b8a1679412397ab1c563f1d02d571ac3671` | answered | `63b2e6a061fc3912a9655e691c46f4582027f4dece3fea0a77174398895ee6c8` | pass |
| ci-2291 | adjudicator-1 (adjudicator) | `3e9a35e85a8ee078400256a0cc116f28564b5360ce6677a5540847cf48caa55f` | answered | `a15fe81aee3184a2893dc9ea9e9b087316ddad07ef22bc1c4f4cfd19298d7454` | pass |

## Per-query outcomes

| query | stratum | outcome | reason |
|---|---|---|---|
| cb-06 | exact_identifier | pass | both primary grades passed |
| cb-10 | exact_path | pass | both primary grades passed |
| cb-17 | nl_behaviour | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| cb-18 | nl_behaviour | pass | both primary grades passed |
| cb-23 | architecture_flow | pass | both primary grades passed |
| cb-29 | config_docs | pass | both primary grades passed |
| cb-34 | ambiguous | pass | both primary grades passed |
| ci-2 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-43 | nl_behaviour | pass | both primary grades passed |
| ci-298 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-316 | config_docs | pass | both primary grades passed |
| ci-346 | config_docs | pass | both primary grades passed |
| ci-434 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-470 | config_docs | pass | both primary grades passed |
| ci-471 | config_docs | pass | both primary grades passed |
| ci-533 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-563 | config_docs | pass | both primary grades passed |
| ci-674 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-710 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-724 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-725 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-821 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-827 | config_docs | pass | both primary grades passed |
| ci-829 | config_docs | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| ci-852 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-920 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1025 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1060 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1086 | config_docs | pass | both primary grades passed |
| ci-1098 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1111 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1148 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1151 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1168 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1202 | config_docs | pass | both primary grades passed |
| ci-1221 | config_docs | pass | both primary grades passed |
| ci-1289 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1300 | config_docs | pass | both primary grades passed |
| ci-1343 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1368 | config_docs | pass | both primary grades passed |
| ci-1416 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1461 | config_docs | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| ci-1499 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1505 | config_docs | pass | both primary grades passed |
| ci-1564 | config_docs | pass | both primary grades passed |
| ci-1592 | config_docs | pass | both primary grades passed |
| ci-1631 | config_docs | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| ci-1758 | config_docs | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| ci-1790 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-1861 | config_docs | pass | both primary grades passed |
| ci-1894 | config_docs | pass | both primary grades passed |
| ci-1923 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2007 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2014 | config_docs | pass | both primary grades passed |
| ci-2138 | config_docs | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |
| ci-2150 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2160 | config_docs | pass | both primary grades passed |
| ci-2161 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2176 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2177 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2250 | config_docs | pass | both primary grades passed |
| ci-2252 | config_docs | fail | primary raters disagreed; majority of the three graded outcomes is 1 pass / 2 fail |
| ci-2254 | config_docs | fail | both primary grades failed; two primary failures are a query failure and are not adjudicated |
| ci-2291 | config_docs | pass | primary raters disagreed; majority of the three graded outcomes is 2 pass / 1 fail |

## No override

There is no flag, environment variable, configuration key or report field that lowers `k`,
waives a query, excludes a query from `N`, retries a graded response or forces a pass. A
missing, empty or refused response is a failure for that rater and a failure for its query,
and is not adjudicated, re-requested or replaced. A pass count below `k` records
`RELEASE: NO` and exits non-zero.
