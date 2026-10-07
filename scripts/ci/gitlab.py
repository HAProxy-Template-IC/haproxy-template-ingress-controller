"""Read GitLab records through the CLI or the public API."""

import json
import subprocess
import urllib.parse
import urllib.request


class GitLab:
    def __init__(self, public_url=None):
        self.public_url = public_url

    def get(self, path, **params):
        query = urllib.parse.urlencode(params)
        endpoint = path + ("?" + query if query else "")
        if self.public_url:
            request = urllib.request.Request(self.public_url.rstrip("/") + "/" + endpoint)
            with urllib.request.urlopen(request, timeout=60) as response:
                return json.load(response)
        result = subprocess.run(["glab", "api", endpoint], check=True, capture_output=True, text=True)
        return json.loads(result.stdout)

    def all(self, path, **params):
        page = 1
        while True:
            batch = self.get(path, **params, page=page, per_page=100)
            if not isinstance(batch, list):
                raise ValueError(f"GitLab returned a non-list for {path}")
            yield from batch
            if len(batch) < 100:
                return
            page += 1


def encoded(value):
    return urllib.parse.quote(str(value), safe="")
