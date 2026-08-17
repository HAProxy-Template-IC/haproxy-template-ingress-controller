#!/usr/bin/env python3
"""The benchmark fixture puts default-ssl-cert in namespace `default`, but the
chart's defaultSSLCertificate resolves it in the release namespace
(haproxy-haptic) — the render aborts on the missing Secret. Move the fixture."""
import re
import sys

path = sys.argv[1]
s = open(path).read()
new, n = re.subn(
    r"(name: default-ssl-cert\n(\s+)namespace: )default",
    r"\1haproxy-haptic",
    s,
)
open(path, "w").write(new)
print(f"patched {n} secret fixtures")
