# jq -s -f this-file before-bundles.json after-bundles.json
if length != 2 then error("expected exactly two captures") else . end |
.[0].independent_builds[0] as $before |
.[1].independent_builds[0] as $after |
if ($before|map(.query_id)) != ($after|map(.query_id)) then error("population/order mismatch") else . end |
[range(0; $before|length) | . as $i |
 {query_id:$after[$i].query_id, stratum:$after[$i].stratum,
  source_overlap:[$before[$i].snippet_overlap_spans,$after[$i].snippet_overlap_spans],
  complete_spans:[$before[$i].snippet_contained_spans,$after[$i].snippet_contained_spans],
  cl100k_tokens:([$before[$i],$after[$i]]|map(.capture.payload.token_counts[]|select(.tokenizer_id=="tiktoken:cl100k_base:ordinary")|.tokens))}]
