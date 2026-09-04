import re, collections
import _paths

BQ = _paths.BLIND_QUERIES
ids=[l.split('\t')[0] for l in BQ.open(encoding='utf-8') if l.strip()]
idset=set(ids)
text={l.split('\t')[0]: l.split('\t',1)[1].strip() for l in BQ.open(encoding='utf-8') if l.strip()}

def parse(path):
    pairs=set(); seen=set()
    for line in path.open(encoding='utf-8'):
        m=re.match(r'\s*(q-[0-9a-f]{10})\s*(?:->|→|:)\s*(.*)$', line)
        if not m: continue
        a=m.group(1); rest=m.group(2)
        if a not in idset: continue
        seen.add(a)
        if re.search(r'\bNONE\b', rest, re.I): continue
        for b in re.findall(r'q-[0-9a-f]{10}', rest):
            if b in idset and b!=a: pairs.add(frozenset((a,b)))
    return pairs, seen

pa, sa = parse(_paths.REVIEWER_A)
pb, sb = parse(_paths.REVIEWER_B)
n=len(ids)
print(f"reviewer A (pi)   : {len(sa)}/{n} ids answered, {len(pa)} same-task pairs")
print(f"reviewer B (codex): {len(sb)}/{n} ids answered, {len(pb)} same-task pairs")
print(f"agreed pairs: {len(pa&pb)}   only-A: {len(pa-pb)}   only-B: {len(pb-pa)}")

union = pa | pb                      # the rule: joined if EITHER marks same-task
parent={i:i for i in ids}
def find(x):
    while parent[x]!=x: parent[x]=parent[parent[x]]; x=parent[x]
    return x
def union_(a,b):
    ra,rb=find(a),find(b)
    if ra!=rb: parent[ra]=rb
for p in union:
    a,b=tuple(p); union_(a,b)
comp=collections.defaultdict(list)
for i in ids: comp[find(i)].append(i)
fams=sorted(comp.values(), key=len, reverse=True)
print(f"\nfamilies after union + transitive closure: {len(fams)}")
print("size distribution:", dict(collections.Counter(len(f) for f in fams)))
print("\nlargest families:")
for f in fams[:4]:
    if len(f)<2: break
    print(f"  [{len(f)}]")
    for i in sorted(f): print("     ", text[i][:78])
