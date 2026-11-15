"""Run locally: python3 -m unittest discover -s deployments/platform/scripts -p 'test_*.py'."""
import contextlib
import io
import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import smoke


class SmokeTests(unittest.TestCase):
    def test_redirect_does_not_forward_bearer(self):
        seen = []

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                seen.append(self.path)
                self.send_response(302)
                self.send_header("Location", "/credential-trap")
                self.end_headers()

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            client = smoke.Client(f"http://127.0.0.1:{server.server_port}", "test-only-token")
            with self.assertRaisesRegex(RuntimeError, "HTTP 302"):
                client.call("/original")
            self.assertEqual(["/original"], seen)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_configmap_cleanup_checks_uid(self):
        class Replaced:
            def __init__(self):
                self.reads = 0
                self.deleted = False

            def call(self, path, body):
                if path.endswith("/get"):
                    self.reads += 1
                    return {"uid": "original" if self.reads < 3 else "replacement",
                            "dataKeys": ["platform-smoke", "verified"]}
                if path.endswith("/delete"):
                    self.deleted = True
                return {}

        client = Replaced()
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "was replaced"):
                smoke.check_writes(client, "demo")
        self.assertFalse(client.deleted)

    def test_unknown_apply_is_not_retried_or_deleted(self):
        class Unknown:
            def __init__(self):
                self.paths = []

            def call(self, path, body):
                self.paths.append(path)
                if path.endswith("/apply"):
                    raise RuntimeError("execution unknown")
                return {}

        client = Unknown()
        errors = io.StringIO()
        with contextlib.redirect_stderr(errors):
            with self.assertRaisesRegex(RuntimeError, "execution unknown"):
                smoke.check_writes(client, "demo")
        self.assertEqual(["/api/v1/cluster/kubectl/validate", "/api/v1/cluster/kubectl/apply"], client.paths)
        self.assertIn("CHECK MANUALLY", errors.getvalue())

    def test_success_only_mutates_the_unique_test_configmap(self):
        class Success:
            def __init__(self):
                self.name = None
                self.updated = False
                self.deleted = False

            def call(self, path, body):
                if path.endswith("/validate") or path.endswith("/apply"):
                    obj = json.loads(body["manifest"])
                    self.name = obj["metadata"]["name"]
                    assert obj["kind"] == "ConfigMap" and obj["metadata"]["namespace"] == "demo"
                    return {}
                assert body["name"] == self.name and body["namespace"] == "demo"
                if path.endswith("/get"):
                    return {"uid": "unique", "dataKeys": ["platform-smoke"] + (["verified"] if self.updated else [])}
                if path.endswith("/update"):
                    self.updated = True
                    return {"accepted": True}
                if path.endswith("/delete"):
                    self.deleted = True
                    return {"accepted": True}
                raise AssertionError(path)

        client = Success()
        with contextlib.redirect_stdout(io.StringIO()):
            smoke.check_writes(client, "demo")
        self.assertTrue(client.updated and client.deleted)
        self.assertTrue(client.name.startswith("platform-smoke-"))


if __name__ == "__main__":
    unittest.main()
