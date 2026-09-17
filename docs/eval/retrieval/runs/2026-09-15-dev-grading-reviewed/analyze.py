"""Development grading analysis: joins overlap, rater answers and grades per
question, on the reviewed development split (answer key readable)."""
import json
import os
import sys
from collections import defaultdict

R = sys.argv[1]
questions = {q['id']: q for q in json.load(open(os.path.join(R, 'questions.json')))}
PA = 'third-primary-a--gpt-6-astra--high--codex-cli-0.153.4'
PB = 'third-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4'


def spans(bundle):
    out = []
    payload = json.loads(bundle['payload']['bytes_text']) if 'bytes_text' in bundle['payload'] else None
    return out


def sources_of(bundle):
    import base64
    raw = base64.b64decode(bundle['payload']['bytes'])
    env = json.loads(raw)
    srcs = env['result']['structuredContent']['sources']
    out = [(s['path'], s['start_line'], s['end_line']) for s in srcs]
    if bundle.get('followup_read'):
        fr = json.loads(base64.b64decode(bundle['followup_read']['bytes']))
        out.append((fr['path'], fr['start_line'], fr['end_line']))
    return out


rows = []
per_stratum = defaultdict(lambda: defaultdict(int))
for qid, q in sorted(questions.items()):
    bundle = json.load(open(os.path.join(R, 'bundles', qid + '.json')))
    j = [x for x in q['judgements'] if x['grade'] == 3][0]
    srcs = sources_of(bundle)
    overlapped = any(p == j['path'] and s <= j['end_line'] and e >= j['start_line'] for p, s, e in srcs)
    complete = any(p == j['path'] and s <= j['start_line'] and e >= j['end_line'] for p, s, e in srcs)
    grades = {}
    answers = {}
    for rater in (PA, PB):
        gp = os.path.join(R, 'grades-raw', f'{qid}--{rater}.txt')
        rp = os.path.join(R, 'responses-raw', f'{qid}--{rater}.txt')
        if os.path.exists(gp):
            grades[rater] = open(gp).read().strip()[:4]
        if os.path.exists(rp):
            answers[rater] = open(rp).read().strip()
    ga, gb = grades.get(PA, '?'), grades.get(PB, '?')
    insufficient = sum(1 for a in answers.values() if a.upper().startswith('INSUFFICIENT'))
    query_pass = (ga == 'PASS' and gb == 'PASS')
    disagreement = ga != gb
    rows.append((qid, q['stratum'], overlapped, complete, ga, gb, insufficient, len(srcs)))
    st = per_stratum[q['stratum']]
    st['n'] += 1
    st['overlapped'] += overlapped
    st['complete'] += complete
    st['both_pass'] += query_pass
    st['disagree'] += disagreement
    st['any_pass'] += (ga == 'PASS' or gb == 'PASS')
    st['overlap_but_both_fail'] += (overlapped and ga == 'FAIL' and gb == 'FAIL')
    st['no_overlap_but_pass'] += ((not overlapped) and query_pass)
    st['insufficient_responses'] += insufficient

print('stratum            n overl compl bothP anyP disagr overl&bothFAIL noOverl&pass INSUFF')
tot = defaultdict(int)
for s, st in sorted(per_stratum.items()):
    print(f"{s:18} {st['n']:2} {st['overlapped']:5} {st['complete']:5} {st['both_pass']:5} {st['any_pass']:4} {st['disagree']:6} {st['overlap_but_both_fail']:14} {st['no_overlap_but_pass']:12} {st['insufficient_responses']:6}")
    for k, v in st.items():
        tot[k] += v
print(f"{'all':18} {tot['n']:2} {tot['overlapped']:5} {tot['complete']:5} {tot['both_pass']:5} {tot['any_pass']:4} {tot['disagree']:6} {tot['overlap_but_both_fail']:14} {tot['no_overlap_but_pass']:12} {tot['insufficient_responses']:6}")
print()
print('id     stratum            overl compl  A    B   insuff sources')
for r in rows:
    print(f"{r[0]:6} {r[1]:18} {str(r[2]):5} {str(r[3]):5} {r[4]:4} {r[5]:4} {r[6]:6} {r[7]}")
