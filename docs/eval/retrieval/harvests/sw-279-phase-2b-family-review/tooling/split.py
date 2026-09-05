import json, hashlib, re, collections
import _paths

H = _paths.harvest_arg("assign family ids and apply the frozen section 7 split")
DS = _paths.DATASET_V1

def oid(q): return 'q-'+hashlib.sha256(('sw279-blind-v1\n'+q).encode()).hexdigest()[:10]
prov={}
for l in H.open(encoding='utf-8'):
    r=json.loads(l)
    if r['verdict']=='candidate': prov[oid(r['Q'])]=('github:spf13/cobra#%d'%r['issue_number'], None)
for q in json.load(DS.open(encoding='utf-8'))['queries']:
    prov[oid(q['query'])]=('dataset:cobra-v1:%s'%q['id'], q.get('split'))

ids=[l.split('\t')[0] for l in _paths.BLIND_QUERIES.open(encoding='utf-8') if l.strip()]
idset=set(ids)
def parse(p):
    s=set()
    for line in p.open(encoding='utf-8'):
        m=re.match(r'\s*(q-[0-9a-f]{10})\s*(?:->|→|:)\s*(.*)$', line)
        if not m or m.group(1) not in idset: continue
        if re.search(r'\bNONE\b', m.group(2), re.I): continue
        for b in re.findall(r'q-[0-9a-f]{10}', m.group(2)):
            if b in idset and b!=m.group(1): s.add(frozenset((m.group(1),b)))
    return s
union=parse(_paths.REVIEWER_A)|parse(_paths.REVIEWER_B)
parent={i:i for i in ids}
def find(x):
    while parent[x]!=x: parent[x]=parent[parent[x]]; x=parent[x]
    return x
for p in union:
    a,b=tuple(p); ra,rb=find(a),find(b)
    if ra!=rb: parent[ra]=rb
comp=collections.defaultdict(list)
for i in ids: comp[find(i)].append(i)

conflicts=[]; inherited={'dev':0,'holdout':0}; newonly=[]
for root,mem in comp.items():
    keys=sorted(prov[m][0] for m in mem)
    fid='cobra-family-'+hashlib.sha256('\n'.join(keys).encode()).hexdigest()[:16]
    splits={prov[m][1] for m in mem if prov[m][1]}
    if len(splits)>1: conflicts.append((fid,[prov[m][0] for m in mem],splits))
    elif len(splits)==1: inherited[splits.pop()]+=1
    else: newonly.append(fid)
print("families:", len(comp))
print("  inherit an existing split:", inherited, " -> total", sum(inherited.values()))
print("  new-only families:", len(newonly))
print("\n*** RULE-STOP CHECK: families containing existing queries from BOTH splits ***")
print("  conflicts:", len(conflicts))
for c in conflicts[:5]: print("   ", c)
# deterministic split of new-only families per section 7
order=sorted(newonly, key=lambda f:(hashlib.sha256(('sw-279-family-split-v1\n'+f).encode()).hexdigest(), f))
dev=[f for i,f in enumerate(order,1) if (i-1)%8==0]
hold=[f for f in order if f not in set(dev)]
print(f"\nnew-only split (positions 1,9,17,... -> dev): dev={len(dev)}  holdout={len(hold)}")
