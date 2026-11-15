#!/usr/bin/env python3
"""Platform HTTP acceptance; read-only unless --write-test is explicitly supplied."""
import argparse
import json
import sys
import uuid
from pathlib import Path
from urllib import error, parse, request


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Never forward a Bearer credential to another address.


class Client:
    def __init__(self, base, token):
        self.base = base.rstrip("/")
        self.token = token
        self.opener = request.build_opener(NoRedirect())

    def call(self, path, body=None, token=None, expected=200):
        headers = {"X-Request-Id": "platform-smoke-" + uuid.uuid4().hex}
        credential = self.token if token is None else token
        if credential:
            headers["Authorization"] = "Bearer " + credential
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        req = request.Request(self.base + path, data=data, headers=headers)
        try:
            response = self.opener.open(req, timeout=150)
        except error.HTTPError as exc:
            response = exc
        with response:
            status = response.status
            raw = response.read(2 * 1024 * 1024 + 1)
        if len(raw) > 2 * 1024 * 1024:
            raise RuntimeError(f"{path}: smoke response exceeded 2 MiB")
        if status != expected:
            raise RuntimeError(f"{path}: HTTP {status}, expected {expected}")
        try:
            envelope = json.loads(raw)
        except (ValueError, UnicodeDecodeError) as exc:
            raise RuntimeError(f"{path}: invalid JSON response") from exc
        if expected == 200 and envelope.get("code") != 0:
            raise RuntimeError(f"{path}: nonzero business code")
        return envelope.get("data")


def check_writes(client, namespace):
    # No namespace deletion, image pulls, Helm installation or existing-object writes.
    name = "platform-smoke-" + uuid.uuid4().hex[:20]
    target = {"namespace": namespace, "kind": "ConfigMap", "name": name}
    manifest = {"apiVersion": "v1", "kind": "ConfigMap",
                "metadata": {"name": name, "namespace": namespace},
                "data": {"platform-smoke": name}}
    uid = None
    apply_started = False
    try:
        client.call("/api/v1/cluster/kubectl/validate",
                    {"namespace": namespace, "manifest": json.dumps(manifest)})
        apply_started = True
        client.call("/api/v1/cluster/kubectl/apply",
                    {"namespace": namespace, "manifest": json.dumps(manifest)})
        current = client.call("/api/v1/cluster/resources/get", target)
        uid = current.get("uid")
        if not uid or "platform-smoke" not in current.get("dataKeys", []):
            raise RuntimeError("ConfigMap read-back did not match the created test object")
        client.call("/api/v1/cluster/configmaps/update",
                    {"namespace": namespace, "name": name, "data": {"verified": "true"}})
        current = client.call("/api/v1/cluster/resources/get", target)
        if current.get("uid") != uid or "verified" not in current.get("dataKeys", []):
            raise RuntimeError("ConfigMap update read-back failed")
        print("PASS isolated ConfigMap create/read/update")
    finally:
        if uid:
            current = client.call("/api/v1/cluster/resources/get", target)
            if current.get("uid") != uid:
                raise RuntimeError(f"Test object was replaced; not deleting {namespace}/{name}")
            result = client.call("/api/v1/cluster/resources/delete", target)
            if result.get("accepted") is not True:
                raise RuntimeError("ConfigMap deletion was not accepted")
            print("PASS isolated ConfigMap deletion accepted (asynchronous)")
        elif apply_started:
            print(f"CHECK MANUALLY: {namespace}/{name}; apply outcome may be unknown; no retry or deletion", file=sys.stderr)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="Controller URL, optionally ending in /k8sctl")
    parser.add_argument("--token-file", required=True, type=Path, help="Local short-lived platform JWT file")
    parser.add_argument("--cluster-id", default="poc")
    parser.add_argument("--namespace", default="developer-demo")
    parser.add_argument("--write-test", action="store_true", help="Create/update/delete a unique isolated ConfigMap")
    args = parser.parse_args()
    url = parse.urlsplit(args.base_url)
    if url.scheme not in ("http", "https") or not url.hostname or url.username or url.password or url.query or url.fragment:
        parser.error("base-url must be an HTTP(S) URL without credentials, query or fragment")
    token = args.token_file.read_text().strip()
    if not token or any(c.isspace() for c in token):
        parser.error("token-file must contain one nonempty JWT")
    client = Client(args.base_url, token)
    client.call("/api/v1/pub/health", token="")
    client.call("/api/v1/pub/ready", token="")
    client.call("/api/v1/operator/capabilities", token="", expected=401)
    client.call("/api/v1/operator/capabilities", token="invalid-jwt", expected=401)
    cap = client.call("/api/v1/operator/capabilities")
    if cap.get("clusterId") != args.cluster_id or not cap.get("runtimeId") or cap.get("http") is not True:
        raise RuntimeError("Capabilities returned an unexpected cluster/runtime/HTTP entry")
    print("PASS health/readiness; missing/invalid JWT rejected; authenticated capabilities")
    print(json.dumps(cap, ensure_ascii=False, indent=2))
    client.call("/api/v1/cluster/namespaces")
    client.call("/api/v1/cluster/resources/list", {"namespace": args.namespace, "kind": "ConfigMap", "limit": 1})
    print("PASS authenticated namespace and scoped resource queries")
    if args.write_test:
        check_writes(client, args.namespace)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError) as exc:
        # Never include request headers, JWTs or response bodies in error output.
        print(f"FAIL {exc}", file=sys.stderr)
        sys.exit(1)
