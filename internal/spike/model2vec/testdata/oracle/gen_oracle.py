"""Generate SW-259 oracle fixtures from the official model2vec reference implementation.

Output: $OUT/oracle.json — token ids (exact) and normalized vectors (float32) per case,
plus the model/tokenizer pins and the reference library versions.
"""
import hashlib, json, os, sys, unicodedata

import numpy as np
import model2vec
import tokenizers
from model2vec import StaticModel
from tokenizers import Tokenizer

MODEL_DIR = os.environ["MODEL_DIR"]
OUT = os.environ["OUT"]
REV = os.environ["REV"]


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


model = StaticModel.from_pretrained(MODEL_DIR)
tok = Tokenizer.from_file(os.path.join(MODEL_DIR, "tokenizer.json"))
cfg = json.load(open(os.path.join(MODEL_DIR, "config.json")))

go_block = """// ValidateToken checks the bearer token signature and expiry.
func ValidateToken(ctx context.Context, raw string) (*Claims, error) {
\tparts := strings.Split(raw, ".")
\tif len(parts) != 3 {
\t\treturn nil, ErrMalformedToken
\t}
\treturn parseClaims(parts[1])
}
"""
long_text = " ".join(f"identifier_{i} := compute(value{i})" for i in range(600))

cases = [
    ("empty", ""),
    ("whitespace_only", "   \n\t "),
    ("ascii_identifier", "ValidateToken"),
    ("qualified_name", "func engine/auth.ValidateToken"),
    ("camel_case", "parseAuthorizationHeader"),
    ("snake_case", "parse_authorization_header"),
    ("path_segments", "engine/agenttools/hybridsearch/hybridsearch.go"),
    ("unicode_nfc", unicodedata.normalize("NFC", "Größe Ångström café")),
    ("unicode_nfd", unicodedata.normalize("NFD", "Größe Ångström café")),
    ("cjk", "認証トークンを検証する関数"),
    ("emoji", "token 🔐 validated ✅"),
    ("go_code_block", go_block),
    ("nl_query", "where is the auth token validated?"),
    ("oov_gibberish", "xq7zv qplmzz vvvvqqq zzzxqp"),
    ("long_text_exceeds_max", long_text),
]

batch_texts = [t for _, t in cases if t.strip()][:8]


def encode_ids(text):
    return tok.encode(text).ids


out_cases = []
for name, text in cases:
    ids = encode_ids(text)
    vec = model.encode([text])[0]
    out_cases.append({
        "name": name,
        "text": text,
        "token_ids": [int(i) for i in ids],
        "vector": [float(x) for x in np.asarray(vec, dtype=np.float32)],
        "norm": float(np.linalg.norm(np.asarray(vec, dtype=np.float32))),
    })

batch = model.encode(batch_texts)
out_batch = {
    "texts": batch_texts,
    "vectors": [[float(x) for x in np.asarray(v, dtype=np.float32)] for v in batch],
}

# Also emit a token-embedding row sample so the Go loader can check table decoding directly.
emb = model.embedding
sample_rows = {str(i): [float(x) for x in np.asarray(emb[i], dtype=np.float32)] for i in (0, 1, 2, 100, 1000, emb.shape[0] - 1)}

doc = {
    "schema": "graphi.model2vec-oracle.v1",
    "model": "minishlab/potion-code-16M-v2",
    "revision": REV,
    "files": {f: sha256(os.path.join(MODEL_DIR, f)) for f in ("config.json", "tokenizer.json", "model.safetensors", "modules.json")},
    "config": cfg,
    "embedding_shape": list(emb.shape),
    "embedding_dtype_in_memory": str(emb.dtype),
    "normalize": bool(getattr(model, "normalize", cfg.get("normalize", True))),
    # Every library whose version can move a token id or a vector component is recorded
    # here, so the fixture states its own provenance instead of relying on PINNED.md prose.
    "reference": {
        "model2vec": model2vec.__version__,
        "numpy": np.__version__,
        "tokenizers": tokenizers.__version__,
        "python": sys.version.split()[0],
    },
    "epsilon_note": "vectors compared after L2 normalisation; recommended |delta| <= 1e-5 per component",
    "cases": out_cases,
    "batch": out_batch,
    "embedding_row_samples": sample_rows,
}
os.makedirs(OUT, exist_ok=True)
with open(os.path.join(OUT, "oracle.json"), "w") as f:
    json.dump(doc, f, indent=1, ensure_ascii=False)
print("cases", len(out_cases), "dim", emb.shape[1], "vocab", emb.shape[0], "dtype", emb.dtype, "normalize", doc["normalize"])
for c in out_cases[:5]:
    print(c["name"], "ids", c["token_ids"][:8], "norm", round(c["norm"], 4))
