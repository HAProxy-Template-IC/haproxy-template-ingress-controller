"""Resolve the checked-in job definitions used by the verification manifest."""

import itertools
from pathlib import Path

import yaml


class Reference(list):
    pass


class Loader(yaml.SafeLoader):
    pass


Loader.add_constructor("!reference", lambda loader, node: Reference(loader.construct_sequence(node)))


def load(path=".gitlab-ci.yml"):
    return yaml.load(Path(path).read_text(), Loader=Loader)


def expand(value, config):
    if isinstance(value, Reference):
        target = config
        for key in value:
            target = target[key]
        return expand(target, config)
    if isinstance(value, list):
        result = []
        for item in value:
            resolved = expand(item, config)
            result.extend(resolved if isinstance(resolved, list) else [resolved])
        return result
    return value


def merge(left, right):
    result = dict(left)
    for key, value in right.items():
        result[key] = merge(result[key], value) if isinstance(result.get(key), dict) and isinstance(value, dict) else value
    return result


def job(config, name, visiting=()):
    if name in visiting:
        raise ValueError(f"Cyclic CI inheritance: {name}")
    definition = config[name]
    parents = definition.get("extends", [])
    if isinstance(parents, str):
        parents = [parents]
    inherited = {}
    for parent in parents:
        inherited = merge(inherited, job(config, parent, (*visiting, name)))
    return merge(inherited, definition)


def instances(config, name):
    parallel = job(config, name).get("parallel")
    if not parallel:
        return [name]
    if isinstance(parallel, int):
        return [f"{name} {index}/{parallel}" for index in range(1, parallel + 1)]
    result = []
    for entry in parallel["matrix"]:
        values = [value if isinstance(value, list) else [value] for value in entry.values()]
        result.extend(f"{name}: [{', '.join(map(str, combination))}]" for combination in itertools.product(*values))
    return result
