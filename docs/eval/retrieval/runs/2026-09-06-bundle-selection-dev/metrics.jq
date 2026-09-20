# Run with jq -n -f this-file <one or more preserved bundle JSON files>.
def mean: if length == 0 then null else add / length end;
def metrics:
  map(select(.grade3_spans > 0)) |
  {population:length,
   any_source_overlap: (map(select(.snippet_overlap_spans > 0))|length),
   at_least_one_complete_span: (map(select(.snippet_contained_spans > 0))|length),
   all_spans_complete: (map(select(.snippet_contained_spans == .grade3_spans))|length),
   complete_spans: (map(.snippet_contained_spans)|add),
   grade3_spans: (map(.grade3_spans)|add),
   mean_snippet_whitespace_tokens: (map(.snippet_whitespace_tokens)|mean),
   mean_complete_response_cl100k_tokens:
     ([.[].capture.payload.token_counts[] | select(.tokenizer_id == "tiktoken:cl100k_base:ordinary") | .tokens]|mean)};
[inputs | {artifact:input_filename, queries, identical_payloads,
  overall:(.independent_builds[0]|metrics),
  strata:(.independent_builds[0]|group_by(.stratum)|map({stratum:.[0].stratum,metrics:metrics})),
  source_misses:[.independent_builds[0][]|select(.grade3_spans>0 and .snippet_overlap_spans==0)|.query_id]}]
