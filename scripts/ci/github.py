"""Read GitHub workflow evidence and artifacts."""

import json
import urllib.error
import urllib.parse
import urllib.request


# GitHub answers 502 for 100-job pages of a rerun attempt (run 38581317265).
PAGE_SIZE = 50

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class GitHub:
    def __init__(self, token, api_url="https://api.github.com"):
        self.api_url = api_url.rstrip("/")
        self.headers = {"Accept": "application/vnd.github+json",
                        "X-GitHub-Api-Version": "2022-11-28"}
        if token:
            self.headers["Authorization"] = "Bearer " + token

    def get(self, path, **params):
        url = self.api_url + "/" + path
        if params:
            url += "?" + urllib.parse.urlencode(params)
        request = urllib.request.Request(url, headers=self.headers)
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=30) as response:
            return json.load(response)

    def all(self, path, field, **params):
        page = 1
        while True:
            result = self.get(path, per_page=PAGE_SIZE, page=page, **params)
            values = result[field] if field is not None else result
            yield from values
            if len(values) < PAGE_SIZE:
                return
            page += 1

    def post(self, path, body):
        return self.request("POST", path, body)

    def request(self, method, path, body):
        request = urllib.request.Request(self.api_url + "/" + path, method=method,
                                         data=json.dumps(body).encode(),
                                         headers={**self.headers, "Content-Type": "application/json"})
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=30) as response:
            content = response.read()
        return json.loads(content) if content else None

    def artifact(self, repository, artifact_id, limit):
        request = urllib.request.Request(
            f"{self.api_url}/repos/{repository}/actions/artifacts/{artifact_id}/zip", headers=self.headers)
        try:
            response = urllib.request.build_opener(NoRedirect()).open(request, timeout=30)
        except urllib.error.HTTPError as error:
            if error.code != 302:
                error.close()
                raise
            location = error.headers["Location"]
            error.close()
            parsed = urllib.parse.urlsplit(location)
            if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
                raise ValueError("Artifact download requires an HTTPS storage URL") from error
            response = urllib.request.urlopen(location, timeout=30)
        with response:
            content = response.read(limit + 1)
        if len(content) > limit:
            raise ValueError("Workflow metrics artifact exceeds its size limit")
        return content


def jobs_for_run(api, repository, run):
    jobs = list(api.all(f"repos/{repository}/actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs", "jobs"))
    names = set()
    for job in jobs:
        if (job["run_id"], job["run_attempt"], job["head_sha"]) != (run["id"], run["run_attempt"], run["head_sha"]):
            raise ValueError("Job evidence does not match the workflow revision and attempt")
        if job["name"] in names:
            raise ValueError("Workflow job names are ambiguous")
        names.add(job["name"])
    return jobs
