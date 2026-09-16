"""Offline contract tests: no inference packages or model downloads required."""

import hashlib
import builtins
import http.client
import json
import math
import os
import pathlib
import re
import tempfile
import threading
import types
import unittest
from unittest import mock

from scripts.eval import coderank_sidecar as sidecar


def valid_manifest(max_tokens=8192):
    return {
        "schema_version": 1, "protocol": sidecar.PROTOCOL,
        "endpoint": "http://127.0.0.1:8765",
        "model": {"id": "nomic-ai/CodeRankEmbed", "revision": "model-revision", "sha256": "a" * 64},
        "tokenizer": {"id": "nomic-ai/CodeRankEmbed", "revision": "tokenizer-revision", "sha256": "b" * 64},
        "runtime": {"name": "sentence-transformers", "version": "test-version", "sha256": "c" * 64},
        "dimension": 768, "precision": "float32", "normalization": "l2", "compute": "cpu",
        "admission": {"max_tokens": max_tokens, "reserve": 0, "algorithm": "first-n-tokens", "algorithm_version": "1"},
        "query": {"id": "coderank-code-search", "version": "1",
                  "instruction": "Represent this query for searching relevant code: ",
                  "instruction_sha256": hashlib.sha256(b"Represent this query for searching relevant code: ").hexdigest()},
    }


class FakeEncoder:
    def __init__(self):
        self.inputs = []
        self.token_count_calls = 0

    @staticmethod
    def _token_count(text):
        return len(re.findall(r"\s*\S+|\s+$", text))

    def token_count(self, text):
        # Spaces belong to the following token, so "alpha beta " is 3 tokens.
        self.token_count_calls += 1
        return self._token_count(text)

    def encode(self, texts):
        self.inputs.extend(texts)
        return [[3.0, 4.0] + [0.0] * 766 for _ in texts]

    def encode_with_diagnostics(self, texts, max_tokens):
        counts = [self._token_count(text) for text in texts]
        if any(count > max_tokens for count in counts):
            raise ValueError("embedding input exceeds token limit")
        self.inputs.extend(texts)
        vectors = [[3.0, 4.0] + [0.0] * 766 for _ in texts]
        return vectors, counts, [min(2, count) for count in counts]


class SidecarContractTest(unittest.TestCase):
    def setUp(self):
        self.app = sidecar.SidecarApp(valid_manifest(), FakeEncoder())

    def test_query_and_document_paths_preserve_wire_bytes(self):
        doc = self.app.embed({"protocol": sidecar.PROTOCOL, "kind": "document", "texts": [" x "]})
        query = sidecar.QUERY_INSTRUCTION + "find x"
        qry = self.app.embed({"protocol": sidecar.PROTOCOL, "kind": "query", "texts": [query]})
        self.assertEqual(self.app.encoder.inputs, [" x ", query])
        self.assertEqual(doc["epoch"], qry["epoch"])
        self.assertEqual(doc["vectors"][0][:2], [0.6, 0.8])
        self.assertEqual(doc["unknown_token_counts"], [2])
        self.assertEqual(qry["unknown_token_counts"], [2])
        self.assertEqual(self.app.encoder.token_count_calls, 0)
        self.assertAlmostEqual(sum(x*x for x in qry["vectors"][0]), 1.0)
        with self.assertRaises(ValueError):
            self.app.embed({"protocol": sidecar.PROTOCOL, "kind": "query", "texts": ["find x"]})
        repeated = sidecar.QUERY_INSTRUCTION * 2 + "x"
        self.app.embed({"protocol": sidecar.PROTOCOL, "kind": "query", "texts": [repeated]})
        self.assertEqual(self.app.encoder.inputs[-1], repeated)

    def test_admission_returns_longest_unchanged_utf8_prefix(self):
        app = sidecar.SidecarApp(valid_manifest(2), FakeEncoder())
        got = app.admit({"protocol": sidecar.PROTOCOL, "text": "alpha beta gamma"})
        self.assertEqual((got["text"], got["token_count"]), ("alpha beta", 2))
        got = app.admit({"protocol": sidecar.PROTOCOL, "text": "α 😀 z"})
        self.assertEqual((got["text"], got["token_count"]), ("α 😀", 2))

    def test_admission_does_not_assume_monotonic_token_counts(self):
        class MergingEncoder(FakeEncoder):
            def token_count(self, text):
                return {"": 0, "a": 1, "ab": 3, "abc": 2, "abcd": 4}[text]
        app = sidecar.SidecarApp(valid_manifest(2), MergingEncoder())
        self.assertEqual(app.admit({"protocol": sidecar.PROTOCOL, "text": "abcd"})["text"], "abc")

    def test_preparation_count_is_authoritative_and_reserve_not_subtracted_twice(self):
        class SpecialEncoder(FakeEncoder):
            def token_count(self, text):
                return super().token_count(text) + 2
        manifest = valid_manifest(3)
        manifest["admission"]["reserve"] = 2
        app = sidecar.SidecarApp(manifest, SpecialEncoder())
        result = app.admit({"protocol": sidecar.PROTOCOL, "text": "a b"})
        self.assertEqual((result["text"], result["token_count"]), ("a", 3))

    def test_overlimit_embedding_is_rejected_before_encoding(self):
        app = sidecar.SidecarApp(valid_manifest(1), FakeEncoder())
        with self.assertRaises(ValueError):
            app.embed({"protocol": sidecar.PROTOCOL, "kind": "document", "texts": ["a b"]})
        self.assertEqual(app.encoder.inputs, [])

    def test_strict_payloads_and_batch_limit(self):
        cases = [
            {"protocol": "wrong", "kind": "document", "texts": ["x"]},
            {"protocol": sidecar.PROTOCOL, "kind": "other", "texts": ["x"]},
            {"protocol": sidecar.PROTOCOL, "kind": "document", "texts": ["x"] * 33},
            {"protocol": sidecar.PROTOCOL, "kind": "document", "texts": [None]},
            {"protocol": sidecar.PROTOCOL, "kind": "document", "texts": ["\ud800"]},
            {"protocol": sidecar.PROTOCOL, "kind": "document", "texts": "x"},
            {"protocol": sidecar.PROTOCOL, "kind": "document", "texts": [], "extra": 1},
        ]
        for payload in cases:
            with self.subTest(payload=repr(payload)), self.assertRaises(ValueError):
                self.app.embed(payload)
        for payload in [{"protocol": sidecar.PROTOCOL}, {"protocol": sidecar.PROTOCOL, "text": None}]:
            with self.assertRaises(ValueError):
                self.app.admit(payload)

    def test_invalid_vectors_fail_closed(self):
        for vectors in [[], [[1.0]], [[None] * 768], [[math.nan] * 768], [[0.0] * 768]]:
            with self.subTest(vectors=repr(vectors)[:30]):
                encoder = FakeEncoder()
                encoder.encode_with_diagnostics = lambda texts, max_tokens: (vectors, [1] * len(texts), [0] * len(texts))
                app = sidecar.SidecarApp(valid_manifest(), encoder)
                with self.assertRaises(ValueError):
                    app.embed({"protocol": sidecar.PROTOCOL, "kind": "document", "texts": ["x"]})

    def test_local_encoder_counts_active_unknowns_from_forward_features_once(self):
        class Tensor:
            def __init__(self, value):
                self.value = value

            def tolist(self):
                return self.value

        class Embeddings(Tensor):
            def float(self):
                return self

        class Tokenizer:
            unk_token_id = 99

            def __init__(self):
                self.calls = 0
                self.features = None

            def __call__(self, texts, **kwargs):
                self.calls += 1
                self.features = {
                    "input_ids": Tensor([[1, 99, 0], [99, 2, 3]]),
                    "attention_mask": Tensor([[1, 1, 0], [1, 1, 1]]),
                }
                return self.features

        class Model:
            def __init__(self):
                self.tokenizer = Tokenizer()
                self.forward_features = None

            def float(self):
                return self

            def eval(self):
                return self

            def __call__(self, features):
                self.forward_features = features
                return {"sentence_embedding": Embeddings([[1.0] * 768, [2.0] * 768])}

        model = Model()
        encoder = sidecar.LocalEncoder(model)
        inference = mock.MagicMock()
        inference.__enter__.return_value = None
        inference.__exit__.return_value = False
        with mock.patch.dict("sys.modules", {"torch": types.SimpleNamespace(inference_mode=lambda: inference)}):
            vectors, counts, unknowns = encoder.encode_with_diagnostics(["a", "b"], 3)
        self.assertEqual((counts, unknowns), ([2, 3], [1, 1]))
        self.assertEqual(len(vectors), 2)
        self.assertEqual(model.tokenizer.calls, 1)
        self.assertIs(model.forward_features, model.tokenizer.features)

    def test_binding_cannot_drift_through_manifest_or_epoch_assignment(self):
        manifest = valid_manifest()
        app = sidecar.SidecarApp(manifest, FakeEncoder())
        before = app.attestation()
        manifest["model"]["revision"] = "changed"
        with self.assertRaises(AttributeError):
            app.epoch = "changed"
        self.assertEqual(app.attestation(), before)
        self.assertRegex(before["epoch"], r"^[a-f0-9]{64}$")
        self.assertNotEqual(before["epoch"], self.app.attestation()["epoch"])


class VerificationTest(unittest.TestCase):
    def test_external_module_type_and_escaping_path_rejected_before_import(self):
        original_import = builtins.__import__

        def guarded_import(name, *args, **kwargs):
            if name == "sentence_transformers":
                raise AssertionError("imported runtime before rejecting modules.json")
            return original_import(name, *args, **kwargs)

        entries = [
            {"type": "untrusted/repo--model.Model", "path": ""},
            {"type": "https://example.com/model.Model", "path": ""},
            {"type": "/absolute/model.Model", "path": ""},
            {"type": "../model.Model", "path": ""},
            {"type": "missing.Model", "path": ""},
            {"type": "sentence_transformers.models.Transformer", "path": "../outside"},
            {"type": "sentence_transformers.models.Transformer", "path": "/tmp"},
            {"type": "sentence_transformers.models.Transformer", "path": "https://example.com/module"},
            {"type": "sentence_transformers.models.Transformer", "path": "sub/../../outside"},
            {"type": "sentence_transformers.models.Transformer", "path": "..\\outside"},
        ]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "tokenizer.json").write_text("{}")
            for entry in entries:
                (root / "modules.json").write_text(json.dumps([dict(idx=0, name="0", **entry)]))
                manifest = valid_manifest()
                manifest["model"]["sha256"] = sidecar.tree_digest(root)
                manifest["tokenizer"]["sha256"] = sidecar.tokenizer_digest(root)
                with self.subTest(entry=entry), mock.patch.object(sidecar, "verify_runtime_versions"), mock.patch(
                    "builtins.__import__", side_effect=guarded_import
                ), self.assertRaisesRegex(ValueError, "module"):
                    sidecar.load_encoder(manifest, root)

    def test_runtime_and_local_module_classes_with_confined_paths_are_accepted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "tokenizer.json").write_text("{}")
            (root / "model.py").write_text("class Model: pass\n")
            (root / "pooling").mkdir()
            (root / "modules.json").write_text(json.dumps([
                {"idx": 0, "name": "0", "type": "sentence_transformers.models.Transformer", "path": ""},
                {"idx": 1, "name": "1", "type": "model.Model", "path": "pooling"},
            ]))
            manifest = valid_manifest()
            manifest["model"]["sha256"] = sidecar.tree_digest(root)
            manifest["tokenizer"]["sha256"] = sidecar.tokenizer_digest(root)
            with mock.patch.object(sidecar, "verify_runtime_versions"):
                self.assertEqual(sidecar.verify_artifacts(manifest, root), root.resolve())

    def test_external_custom_code_is_rejected_before_runtime_import(self):
        original_import = builtins.__import__

        def guarded_import(name, *args, **kwargs):
            if name == "sentence_transformers":
                raise AssertionError("imported inference runtime before rejecting custom code")
            return original_import(name, *args, **kwargs)

        targets = [
            "untrusted/repo--model.Model", "https://example.com/model.Model",
            "/absolute/model.Model", "../model.Model", "package..model.Model",
            "package/other.Model", "package\\other.Model", "missing.Model",
            ["model.Model", "repo--other.Model"],
        ]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "tokenizer.json").write_text("{}")
            (root / "model.py").write_text("class Model: pass\n")
            config = root / "config.json"
            for target in targets:
                config.write_text(json.dumps({"nested": {"auto_map": {"AutoModel": target}}}))
                manifest = valid_manifest()
                manifest["model"]["sha256"] = sidecar.tree_digest(root)
                manifest["tokenizer"]["sha256"] = sidecar.tokenizer_digest(root)
                with self.subTest(target=target), mock.patch.object(sidecar, "verify_runtime_versions"), mock.patch(
                    "builtins.__import__", side_effect=guarded_import
                ), self.assertRaisesRegex(ValueError, "custom-code"):
                    sidecar.load_encoder(manifest, root)

    def test_local_custom_code_configuration_is_accepted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "tokenizer.json").write_text("{}")
            (root / "model.py").write_text("class Model: pass\n")
            (root / "config.json").write_text(json.dumps({
                "auto_map": {"AutoModel": "model.Model", "AutoTokenizer": ["model.Model", None]}
            }))
            manifest = valid_manifest()
            manifest["model"]["sha256"] = sidecar.tree_digest(root)
            manifest["tokenizer"]["sha256"] = sidecar.tokenizer_digest(root)
            with mock.patch.object(sidecar, "verify_runtime_versions"):
                self.assertEqual(sidecar.verify_artifacts(manifest, root), root.resolve())

    def test_model_load_is_offline_cpu_local_only_after_verification(self):
        class Model:
            def float(self):
                return self

            def eval(self):
                return self

        loaded = []
        def constructor(path, **kwargs):
            self.assertEqual(os.environ["HF_HUB_OFFLINE"], "1")
            self.assertEqual(os.environ["TRANSFORMERS_OFFLINE"], "1")
            loaded.append((path, kwargs))
            return Model()

        with tempfile.TemporaryDirectory() as root:
            path = pathlib.Path(root)
            (path / "tokenizer.json").write_text("{}")
            manifest = valid_manifest()
            manifest["model"]["sha256"] = sidecar.tree_digest(path)
            manifest["tokenizer"]["sha256"] = sidecar.tokenizer_digest(path)
            with mock.patch.object(sidecar, "verify_runtime_versions"), mock.patch.dict(
                "sys.modules", {"sentence_transformers": types.SimpleNamespace(SentenceTransformer=constructor)}
            ), mock.patch.dict(os.environ, {}, clear=False):
                sidecar.load_encoder(manifest, path)
            self.assertEqual(loaded, [(str(path.resolve()), {"device": "cpu", "trust_remote_code": True, "local_files_only": True})])

    def test_nonlocal_artifacts_are_rejected(self):
        for path in ["nomic-ai/CodeRankEmbed", "https://example.com/model", "/does-not-exist"]:
            with self.subTest(path=path), self.assertRaises(ValueError):
                sidecar.load_encoder(valid_manifest(), pathlib.Path(path))

    def test_artifact_and_tokenizer_drift_rejected_before_import(self):
        with tempfile.TemporaryDirectory() as root:
            path = pathlib.Path(root)
            (path / "tokenizer.json").write_text("{}")
            manifest = valid_manifest()
            with self.assertRaisesRegex(ValueError, "model.*digest"):
                sidecar.load_encoder(manifest, path)
            manifest["model"]["sha256"] = sidecar.tree_digest(path)
            with self.assertRaisesRegex(ValueError, "tokenizer.*digest"):
                sidecar.load_encoder(manifest, path)
            (path / "link").symlink_to(path / "tokenizer.json")
            with self.assertRaises(ValueError):
                sidecar.tree_digest(path)

    def test_runtime_version_and_fingerprint_must_match(self):
        manifest = valid_manifest()
        versions = {"python": "3.12.0", "sentence-transformers": "9.9.9"}
        with mock.patch.object(sidecar, "runtime_versions", return_value=versions):
            with self.assertRaisesRegex(ValueError, "version"):
                sidecar.verify_runtime_versions(manifest["runtime"])
            manifest["runtime"]["version"] = "9.9.9"
            with self.assertRaisesRegex(ValueError, "digest"):
                sidecar.verify_runtime_versions(manifest["runtime"])
            manifest["runtime"]["sha256"] = sidecar.runtime_digest(versions)
            sidecar.verify_runtime_versions(manifest["runtime"])

    def test_manifest_rejects_profile_changes_and_unknown_keys(self):
        for key, value in [("dimension", 3), ("dimension", 768.0), ("precision", "float16"), ("unknown", 1), ("schema_version", True)]:
            manifest = valid_manifest()
            manifest[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                sidecar.SidecarApp(manifest, FakeEncoder())

    def test_protocol_version_is_v2(self):
        self.assertEqual(sidecar.PROTOCOL, "graphi-coderank/2")

    def test_no_runtime_import_on_module_import(self):
        import subprocess
        result = subprocess.run([
            "python3", "-c", "import sys; from scripts.eval import coderank_sidecar; "
            "assert 'sentence_transformers' not in sys.modules; assert 'torch' not in sys.modules"
        ], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)


class HTTPContractTest(unittest.TestCase):
    def setUp(self):
        self.app = sidecar.SidecarApp(valid_manifest(), FakeEncoder())
        self.server = sidecar.make_server(self.app, "127.0.0.1", 0)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def request(self, method, path, body=None, headers=None):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=5)
        try:
            conn.request(method, path, body=body, headers=headers or {})
            response = conn.getresponse()
            return response.status, json.loads(response.read())
        finally:
            conn.close()

    def test_http_operations_and_errors_keep_binding(self):
        status, binding = self.request("GET", "/v1/attestation")
        self.assertEqual(status, 200)
        cases = [
            ("POST", "/v1/admit", '{"protocol":"graphi-coderank/2","text":"x"}', 200),
            ("POST", "/v1/embed", '{"protocol":"graphi-coderank/2","kind":"document","texts":["x"]}', 200),
            ("POST", "/v1/admit", "{", 400),
            ("POST", "/v1/admit", '{"protocol":"graphi-coderank/2","text":"x","text":"y"}', 400),
            ("POST", "/v1/admit", "{} {}", 400),
            ("POST", "/v1/admit", '{"protocol":"wrong","text":"x"}', 400),
            ("GET", "/other", None, 404),
            ("PUT", "/v1/admit", "{}", 405),
        ]
        for method, path, body, expected in cases:
            with self.subTest(method=method, path=path, body=body):
                status, got = self.request(method, path, body)
                self.assertEqual(status, expected)
                for key in ("protocol", "identity_digest", "epoch"):
                    self.assertEqual(got[key], binding[key])
                self.assertNotIn("Traceback", json.dumps(got))

    def test_body_is_bounded_before_reading(self):
        status, got = self.request("POST", "/v1/admit", b"", {"Content-Length": str(1024*1024+1)})
        self.assertEqual(status, 413)
        self.assertIn("epoch", got)

    def test_encoder_exceptions_do_not_leak(self):
        def fail(texts, max_tokens):
            raise RuntimeError("secret model path")
        self.app.encoder.encode_with_diagnostics = fail
        status, got = self.request("POST", "/v1/embed", json.dumps({
            "protocol": sidecar.PROTOCOL, "kind": "document", "texts": ["x"]}))
        self.assertEqual(status, 500)
        self.assertNotIn("secret", json.dumps(got))

    def test_nonliteral_and_nonloopback_bind_rejected(self):
        for bind in ["localhost", "0.0.0.0", "127.0.0.2", "example.com", "::"]:
            with self.subTest(bind=bind), self.assertRaises(ValueError):
                sidecar.make_server(self.app, bind, 0)


if __name__ == "__main__":
    unittest.main()
