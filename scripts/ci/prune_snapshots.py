"""Delete main-snapshot package versions older than the retention window."""
import argparse
import datetime
import json
import re
import subprocess
import urllib.parse

PACKAGES = ("haptic", "haptic/spoa-hub", "haptic/varnish", "haptic/charts/haptic")
SNAPSHOT_TAG = re.compile(r"main-[0-9a-f]{8}(-haproxy[0-9]+\.[0-9]+)?|0\.0\.0-main\.g[0-9a-f]{8}")


def expired(versions, now, days):
    """Versions whose every tag is a snapshot tag and that are older than the window.

    A version that also carries a release tag, or no tag at all, is never selected.
    """
    cutoff = now - datetime.timedelta(days=days)
    return [v for v in versions
            if (tags := v["metadata"]["container"]["tags"])
            and all(SNAPSHOT_TAG.fullmatch(tag) for tag in tags)
            and datetime.datetime.fromisoformat(v["created_at"].replace("Z", "+00:00")) < cutoff]


def gh(*args):
    return subprocess.run(["gh", "api", *args], check=True, capture_output=True, text=True).stdout


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--org", required=True)
    parser.add_argument("--days", type=int, required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    now = datetime.datetime.now(datetime.timezone.utc)
    for package in PACKAGES:
        path = f"orgs/{args.org}/packages/container/{urllib.parse.quote(package, safe='')}/versions"
        versions = [v for page in json.loads(gh("--paginate", "--slurp", path + "?per_page=100")) for v in page]
        for version in expired(versions, now, args.days):
            tags = ",".join(version["metadata"]["container"]["tags"])
            print(f"{'would delete' if args.dry_run else 'deleting'} {package} {tags} ({version['created_at']})")
            if not args.dry_run:
                gh("--method", "DELETE", f"{path}/{version['id']}")


if __name__ == "__main__":
    main()
