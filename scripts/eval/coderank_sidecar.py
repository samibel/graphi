#!/usr/bin/env python3
"""Evaluation-only, offline CodeRank reference service. Never started by GrapHi."""

import argparse
import copy
import hashlib
import importlib.metadata
import ipaddress
import json
import math
import os
from pathlib import Path
import platform
import re
import secrets
import socket
import stat
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

PROTOCOL = "graphi-coderank/2"
QUERY_INSTRUCTION = "Represent this query for searching relevant code: "
MAX_BODY = 1024 * 1024
MAX_BATCH = 32
RUNTIME_PACKAGES = (
    "sentence-transformers", "transformers", "torch", "tokenizers",
    "huggingface-hub", "safetensors", "numpy",
)
TOKENIZER_FILES = frozenset({
    "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json",
    "added_tokens.json", "vocab.txt", "vocab.json", "merges.txt",
    "tokenizer.model", "spiece.model", "sentencepiece.bpe.model",
})


def strict_keys(value, expected):
    if not isinstance(value, dict) or set(value) != set(expected):
        raise ValueError("invalid object fields")


def strict_json(data):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("duplicate JSON field")
            result[key] = value
        return result

    def constant(_):
        raise ValueError("nonfinite JSON number")

    try:
        return json.loads(data.decode("utf-8"), object_pairs_hook=pairs, parse_constant=constant)
    except (UnicodeError, RecursionError) as exc:
        raise ValueError("invalid JSON") from exc


def text_value(value):
    if not isinstance(value, str):
        raise ValueError("expected text")
    try:
        value.encode("utf-8")
    except UnicodeError as exc:
        raise ValueError("invalid UTF-8 text") from exc
    return value


def valid_digest(value):
    return (isinstance(value, str) and len(value) == 64 and value != "0" * 64
            and all(c in "0123456789abcdef" for c in value))


def validate_manifest(m):
    strict_keys(m, ("schema_version", "protocol", "endpoint", "model", "tokenizer", "runtime",
                    "dimension", "precision", "normalization", "compute", "admission", "query"))
    if type(m["schema_version"]) is not int or m["schema_version"] != 1 or m["protocol"] != PROTOCOL:
        raise ValueError("unsupported manifest protocol")
    if (type(m["dimension"]) is not int or
            (m["dimension"], m["precision"], m["normalization"], m["compute"]) != (768, "float32", "l2", "cpu")):
        raise ValueError("unsupported embedding profile")
    for field in ("model", "tokenizer", "runtime", "admission", "query"):
        expected = {
            "model": ("id", "revision", "sha256"), "tokenizer": ("id", "revision", "sha256"),
            "runtime": ("name", "version", "sha256"),
            "admission": ("max_tokens", "reserve", "algorithm", "algorithm_version"),
            "query": ("id", "version", "instruction", "instruction_sha256"),
        }[field]
        strict_keys(m[field], expected)
        for key, value in m[field].items():
            if key in ("max_tokens", "reserve"):
                if type(value) is not int or value < (1 if key == "max_tokens" else 0):
                    raise ValueError("invalid admission limit")
            elif key.endswith("sha256"):
                if not valid_digest(value):
                    raise ValueError("invalid digest")
            elif not text_value(value).strip():
                raise ValueError("empty manifest pin")
    if m["runtime"]["name"] != "sentence-transformers":
        raise ValueError("unsupported runtime")
    if (m["admission"]["algorithm"], m["admission"]["algorithm_version"]) != ("first-n-tokens", "1"):
        raise ValueError("unsupported admission algorithm")
    if m["query"]["instruction"] != QUERY_INSTRUCTION or m["query"]["instruction_sha256"] != hashlib.sha256(QUERY_INSTRUCTION.encode()).hexdigest():
        raise ValueError("query instruction mismatch")
    endpoint = text_value(m["endpoint"])
    origin = urlsplit(endpoint)
    host = origin.hostname
    try:
        loopback = host == "localhost" or host == "::1" or (
            ipaddress.ip_address(host).version == 4 and ipaddress.ip_address(host).is_loopback)
        port = origin.port
    except (ValueError, TypeError) as exc:
        raise ValueError("invalid endpoint") from exc
    if (origin.scheme != "http" or not loopback or origin.username is not None
            or origin.password is not None or origin.path or "?" in endpoint or "#" in endpoint
            or origin.netloc.endswith(":") or (port is not None and not 1 <= port <= 65535)):
        raise ValueError("endpoint must be a loopback HTTP origin")


def framed_digest(values):
    encoded = [str(value).encode("utf-8") for value in values]
    return hashlib.sha256(b"\n".join(str(len(value)).encode("ascii") + b":" + value for value in encoded)).hexdigest()


def identity_digest(m):
    values = [m["schema_version"], m["protocol"]]
    for section, fields in (
        ("model", ("id", "revision", "sha256")),
        ("tokenizer", ("id", "revision", "sha256")),
        ("runtime", ("name", "version", "sha256")),
    ):
        values.extend(m[section][field] for field in fields)
    values.extend(m[field] for field in ("dimension", "precision", "normalization", "compute"))
    values.extend(m["admission"][field] for field in ("max_tokens", "reserve", "algorithm", "algorithm_version"))
    values.extend(m["query"][field] for field in ("id", "version", "instruction", "instruction_sha256"))
    return framed_digest(values)


def local_directory(model_dir):
    path = Path(model_dir)
    if not path.is_absolute() or not path.is_dir() or path.is_symlink():
        raise ValueError("model-dir must be an absolute local artifact directory")
    # Canonicalize OS aliases such as macOS /var; never pass an alias to the loader.
    return path.resolve(strict=True)


def artifact_files(model_dir):
    root = local_directory(model_dir)
    files = []
    for path in root.rglob("*"):
        mode = path.lstat().st_mode
        if stat.S_ISLNK(mode) or not (stat.S_ISREG(mode) or stat.S_ISDIR(mode)):
            raise ValueError("artifact tree contains symlink or special file")
        if stat.S_ISREG(mode):
            files.append(path)
    if not files:
        raise ValueError("empty artifact tree")
    return sorted(files, key=lambda path: path.relative_to(root).as_posix().encode("utf-8"))


def files_digest(root, paths):
    values = []
    for path in paths:
        with path.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
        values.extend((path.relative_to(root).as_posix(), digest))
    return framed_digest(values)


def tree_digest(model_dir):
    root = local_directory(model_dir)
    return files_digest(root, artifact_files(root))


def tokenizer_digest(model_dir):
    root = local_directory(model_dir)
    paths = [path for path in artifact_files(root)
             if path.parent == root and path.name in TOKENIZER_FILES]
    if not any(path.name in {"tokenizer.json", "vocab.txt", "vocab.json", "tokenizer.model", "spiece.model", "sentencepiece.bpe.model"} for path in paths):
        raise ValueError("missing root tokenizer artifact")
    return files_digest(root, paths)


def runtime_versions():
    versions = {"python": platform.python_version()}
    try:
        versions.update((name, importlib.metadata.version(name)) for name in RUNTIME_PACKAGES)
    except importlib.metadata.PackageNotFoundError as exc:
        raise ValueError("required local runtime package is not installed") from exc
    return versions


def runtime_digest(versions):
    return framed_digest([item for name in sorted(versions) for item in (name, versions[name])])


def verify_runtime_versions(pin):
    versions = runtime_versions()
    if pin["name"] != "sentence-transformers" or versions["sentence-transformers"] != pin["version"]:
        raise ValueError("runtime version mismatch")
    if runtime_digest(versions) != pin["sha256"]:
        raise ValueError("runtime digest mismatch")


def verify_local_custom_code(root):
    """Reject custom-code redirects before the runtime can consult a hub cache."""
    reference = re.compile(r"[A-Za-z_][A-Za-z_0-9]*(?:\.[A-Za-z_][A-Za-z_0-9]*)+")

    def check_reference(value, directory, allow_runtime=False):
        if not isinstance(value, str) or reference.fullmatch(value) is None:
            raise ValueError("custom-code target must be a local module/class reference")
        if allow_runtime and value.startswith("sentence_transformers."):
            return
        module = directory.joinpath(*value.split(".")[:-1]).with_suffix(".py")
        if not module.is_file() or not module.resolve().is_relative_to(root):
            raise ValueError("custom-code module is absent from the verified local tree")

    def check_map(mapping, directory):
        if not isinstance(mapping, dict):
            raise ValueError("invalid custom-code map")
        for target in mapping.values():
            targets = target if isinstance(target, list) else [target]
            # Tokenizer maps may contain a null fast/slow alternative.
            if not targets or all(value is None for value in targets):
                raise ValueError("empty custom-code target")
            for value in targets:
                if value is None and isinstance(target, list):
                    continue
                check_reference(value, directory)

    def check_modules(entries, directory):
        if not isinstance(entries, list):
            raise ValueError("invalid modules manifest")
        for entry in entries:
            if not isinstance(entry, dict):
                raise ValueError("invalid module entry")
            check_reference(entry.get("type"), directory, allow_runtime=True)
            relative = entry.get("path")
            if (not isinstance(relative, str) or ":" in relative or "\\" in relative
                    or "--" in relative or Path(relative).is_absolute()
                    or ".." in Path(relative).parts):
                raise ValueError("module path must stay within the verified local tree")
            module_dir = (directory / relative).resolve()
            if not module_dir.is_relative_to(root) or not module_dir.is_dir():
                raise ValueError("module path is absent from the verified local tree")

    def visit(value, directory):
        if isinstance(value, dict):
            for key, child in value.items():
                if key == "auto_map":
                    check_map(child, directory)
                else:
                    visit(child, directory)
        elif isinstance(value, list):
            for child in value:
                visit(child, directory)

    for path in artifact_files(root):
        if path.name == "modules.json":
            check_modules(strict_json(path.read_bytes()), path.parent)
        # Tokenizer vocabulary JSON can contain a literal "auto_map" token;
        # only configuration files give that key executable meaning.
        if path.suffix == ".json" and "config" in path.stem:
            visit(strict_json(path.read_bytes()), path.parent)


def verify_artifacts(manifest, model_dir):
    validate_manifest(manifest)
    root = local_directory(model_dir)
    if tree_digest(root) != manifest["model"]["sha256"]:
        raise ValueError("model tree digest mismatch")
    if tokenizer_digest(root) != manifest["tokenizer"]["sha256"]:
        raise ValueError("tokenizer digest mismatch")
    verify_local_custom_code(root)
    verify_runtime_versions(manifest["runtime"])
    return root


class LocalEncoder:
    """Use the local tokenizer and model forward path without encode's truncation."""

    def __init__(self, model):
        self.model = model.float().eval()

    def token_count(self, text):
        return len(self.model.tokenizer(text, add_special_tokens=True, truncation=False)["input_ids"])

    def encode_with_diagnostics(self, texts, max_tokens):
        import torch
        features = self.model.tokenizer(
            texts, add_special_tokens=True, padding=True, truncation=False, return_tensors="pt")
        if "input_ids" not in features or "attention_mask" not in features:
            raise ValueError("tokenizer omitted required embedding features")
        input_ids = features["input_ids"].tolist()
        attention = features["attention_mask"].tolist()
        if len(input_ids) != len(texts) or len(attention) != len(texts):
            raise ValueError("invalid tokenizer feature cardinality")
        unknown_id = self.model.tokenizer.unk_token_id
        if unknown_id is not None and type(unknown_id) is not int:
            raise ValueError("invalid tokenizer unknown id")
        token_counts = []
        unknown_counts = []
        for ids, mask in zip(input_ids, attention):
            if not isinstance(ids, list) or not isinstance(mask, list) or len(ids) != len(mask):
                raise ValueError("invalid tokenizer feature shape")
            active = []
            for token, included in zip(ids, mask):
                if type(token) is not int or type(included) is not int or included not in (0, 1):
                    raise ValueError("invalid tokenizer feature value")
                if included == 1:
                    active.append(token)
            if len(active) > max_tokens:
                raise ValueError("embedding input exceeds token limit")
            token_counts.append(len(active))
            unknown_counts.append(0 if unknown_id is None else sum(token == unknown_id for token in active))
        with torch.inference_mode():
            vectors = self.model(features)["sentence_embedding"].float().tolist()
        return vectors, token_counts, unknown_counts


def load_encoder(manifest, model_dir):
    root = verify_artifacts(manifest, model_dir)
    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    os.environ["HF_DATASETS_OFFLINE"] = "1"
    os.environ["HF_HUB_DISABLE_TELEMETRY"] = "1"
    # Avoid altering the pinned tree through Python's bytecode cache.
    sys.dont_write_bytecode = True
    from sentence_transformers import SentenceTransformer
    model = SentenceTransformer(str(root), device="cpu", trust_remote_code=True, local_files_only=True)
    return LocalEncoder(model)


class SidecarApp:
    def __init__(self, manifest, encoder):
        validate_manifest(manifest)
        self._manifest = copy.deepcopy(manifest)
        self._identity = identity_digest(self._manifest)
        self._epoch = secrets.token_hex(32)
        self.encoder = encoder
        self._lock = threading.Lock()

    @property
    def epoch(self):
        return self._epoch

    def binding(self):
        return {"protocol": PROTOCOL, "identity_digest": self._identity, "epoch": self.epoch}

    def attestation(self):
        return dict(self.binding(), dimension=self._manifest["dimension"])

    def _request(self, request, keys):
        strict_keys(request, keys)
        if request["protocol"] != PROTOCOL:
            raise ValueError("unsupported protocol")

    def _count(self, text):
        count = self.encoder.token_count(text)
        if type(count) is not int or count < 0:
            raise ValueError("invalid tokenizer count")
        return count

    def admit(self, request):
        self._request(request, ("protocol", "text"))
        text = text_value(request["text"])
        limit = self._manifest["admission"]["max_tokens"]
        with self._lock:
            # Token counts can decrease when a character completes a merged token.
            # Descending character boundaries prove maximality without assuming monotonicity.
            for end in range(len(text), -1, -1):
                prefix = text[:end]
                count = self._count(prefix)
                if count <= limit:
                    return dict(self.binding(), text=prefix, token_count=count)
        raise ValueError("empty preparation exceeds token limit")

    def embed(self, request):
        self._request(request, ("protocol", "kind", "texts"))
        if request["kind"] not in ("query", "document"):
            raise ValueError("unsupported embedding kind")
        texts = request["texts"]
        if not isinstance(texts, list) or len(texts) > MAX_BATCH:
            raise ValueError("invalid batch size")
        for text in texts:
            text_value(text)
            if request["kind"] == "query" and not text.startswith(QUERY_INSTRUCTION):
                raise ValueError("query is missing its pinned instruction")
        if not texts:
            return dict(self.binding(), vectors=[], unknown_token_counts=[])
        with self._lock:
            vectors, token_counts, unknown_counts = self.encoder.encode_with_diagnostics(
                texts, self._manifest["admission"]["max_tokens"])
        if len(vectors) != len(texts) or len(token_counts) != len(texts) or len(unknown_counts) != len(texts):
            raise ValueError("invalid embedding cardinality")
        for count, unknown in zip(token_counts, unknown_counts):
            if type(count) is not int or not 0 <= count <= self._manifest["admission"]["max_tokens"]:
                raise ValueError("invalid tokenizer count")
            if type(unknown) is not int or not 0 <= unknown <= count:
                raise ValueError("invalid unknown-token count")
        normalized = []
        for vector in vectors:
            if len(vector) != self._manifest["dimension"]:
                raise ValueError("invalid embedding dimension")
            if any(type(value) not in (int, float) or not math.isfinite(value) for value in vector):
                raise ValueError("invalid embedding component")
            norm = math.hypot(*vector)
            if not math.isfinite(norm) or norm == 0:
                raise ValueError("invalid embedding norm")
            normalized.append([value / norm for value in vector])
        return dict(self.binding(), vectors=normalized, unknown_token_counts=unknown_counts)


def make_server(app, bind, port):
    if bind not in ("127.0.0.1", "::1"):
        raise ValueError("bind must be literal 127.0.0.1 or ::1")
    if type(port) is not int or not 0 <= port <= 65535:
        raise ValueError("invalid port")

    class Handler(BaseHTTPRequestHandler):
        def setup(self):
            self.request.settimeout(10)
            super().setup()

        def log_message(self, *args):
            pass

        def send_json(self, status, payload):
            data = json.dumps(payload, allow_nan=False, separators=(",", ":")).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(data)))
            self.send_header("Connection", "close")
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(data)
            self.close_connection = True

        def send_error(self, code, message=None, explain=None):
            self.send_json(code, dict(app.binding(), error="invalid HTTP request"))

        def dispatch(self):
            try:
                if self.command == "GET" and self.path == "/v1/attestation":
                    self.send_json(200, app.attestation())
                    return
                if self.command not in ("GET", "POST"):
                    self.send_json(405, dict(app.binding(), error="method not allowed"))
                    return
                if self.command != "POST" or self.path not in ("/v1/admit", "/v1/embed"):
                    self.send_json(404, dict(app.binding(), error="unknown endpoint"))
                    return
                lengths = self.headers.get_all("Content-Length", [])
                if self.headers.get("Transfer-Encoding") is not None or len(lengths) != 1 or not lengths[0].isascii() or not lengths[0].isdigit():
                    raise ValueError("invalid body framing")
                length = int(lengths[0])
                if length > MAX_BODY:
                    self.send_json(413, dict(app.binding(), error="request body exceeds 1 MiB"))
                    return
                data = self.rfile.read(length)
                if len(data) != length:
                    raise ValueError("incomplete body")
                request = strict_json(data)
                result = app.admit(request) if self.path == "/v1/admit" else app.embed(request)
                self.send_json(200, result)
            except (ValueError, UnicodeError):
                self.send_json(400, dict(app.binding(), error="invalid request or model output"))
            except (BrokenPipeError, ConnectionResetError, TimeoutError):
                self.close_connection = True
            except Exception:
                self.send_json(500, dict(app.binding(), error="local inference failed"))

        do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = do_HEAD = do_OPTIONS = dispatch

    class Server(ThreadingHTTPServer):
        address_family = socket.AF_INET6 if bind == "::1" else socket.AF_INET
        daemon_threads = True

        def handle_error(self, request, client_address):
            # Never expose inference paths or tracebacks via server logging.
            pass

    return Server((bind, port), Handler)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("verify", "serve"))
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--model-dir", required=True, type=Path)
    parser.add_argument("--bind", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8765)
    args = parser.parse_args(argv)
    try:
        with args.manifest.open("rb") as stream:
            data = stream.read(MAX_BODY + 1)
        if len(data) > MAX_BODY:
            raise ValueError("manifest exceeds 1 MiB")
        manifest = strict_json(data)
        if args.command == "verify":
            verify_artifacts(manifest, args.model_dir)
            print(json.dumps({"protocol": PROTOCOL, "identity_digest": identity_digest(manifest), "verified": True}))
            return 0
        if args.bind not in ("127.0.0.1", "::1") or not 1 <= args.port <= 65535:
            raise ValueError("invalid loopback bind or port")
        encoder = load_encoder(manifest, args.model_dir)
        with make_server(SidecarApp(manifest, encoder), args.bind, args.port) as server:
            server.serve_forever()
    except KeyboardInterrupt:
        return 0
    except Exception:
        print("coderank-sidecar: local verification or service failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
