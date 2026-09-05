import hashlib, json
import _paths

H = _paths.harvest_arg("build the blinded query list for the phase 2b family review")
rows=[json.loads(l) for l in H.open(encoding='utf-8') if l.strip()]
new=[r['Q'] for r in rows if r['verdict']=='candidate']
old=[q['query'] for q in json.load(_paths.DATASET_V1.open(encoding='utf-8'))['queries']]
items=[]
for q in new+old:
    # opaque, deterministic id from the text alone; provenance is NOT encoded
    oid='q-'+hashlib.sha256(('sw279-blind-v1\n'+q).encode()).hexdigest()[:10]
    items.append((oid,q))
# order by the opaque id, so neither issue number nor dataset order leaks
items.sort(key=lambda t:t[0])
assert len({i for i,_ in items})==len(items), "id collision"
out=_paths.BLIND_QUERIES
with out.open('w',encoding='utf-8') as f:
    for oid,q in items: f.write(f"{oid}\t{q}\n")
print("new candidates:", len(new), " existing:", len(old), " total:", len(items))
print("blind list written:", out)
print("sha256:", hashlib.sha256(out.read_bytes()).hexdigest()[:16])
print("\nfirst 4 lines (note: nothing identifies source or split):")
for oid,q in items[:4]: print(" ", oid, q[:74])
