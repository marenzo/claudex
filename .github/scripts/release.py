#!/usr/bin/env python3
"""Plan a stable release. This script never mutates Git refs or publishes."""
import os
from pathlib import Path
import re
import subprocess


PATTERN = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")


def version(tag):
    match = PATTERN.fullmatch(tag)
    if not match:
        raise ValueError(f"invalid stable version: {tag}")
    return tuple(map(int, match.groups()))


def choose(floor, tags, at_head):
    minimum = version(floor)
    stable = sorted((t for t in tags if PATTERN.fullmatch(t)), key=version)
    existing = [t for t in stable if t in at_head]
    if existing:
        target = existing[-1]  # Resume a partially completed release of this commit.
        if version(target) < minimum:
            raise ValueError("tag at HEAD is older than the requested release floor")
    elif stable:
        major, minor, patch = version(stable[-1])
        target = "v%d.%d.%d" % max(minimum, (major, minor, patch + 1))
    else:
        target = floor
    previous = [t for t in stable if version(t) < version(target)]
    return target, previous[-1] if previous else ""


def changelog_section(text, tag):
    match = re.search(r"^## " + re.escape(tag) + r"(?:\s|$)[^\n]*\n(.*?)(?=^## |\Z)", text, re.M | re.S)
    return match.group(1).strip() if match else ""


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def main():
    head = git("rev-parse", "HEAD")
    if head != os.environ["GITHUB_SHA"]:
        raise RuntimeError("release checkout does not match the triggering commit")
    tags = git("tag", "--list").splitlines()
    at_head = [t for t in tags if PATTERN.fullmatch(t) and git("rev-list", "-n", "1", t) == head]
    target, previous = choose(Path("VERSION").read_text().strip(), tags, at_head)
    notes = changelog_section(Path("CHANGELOG.md").read_text(), target)
    if not notes:
        if previous:
            subprocess.run(["git", "merge-base", "--is-ancestor", previous, head], check=True)
        notes = git("log", "--no-merges", "--format=- %s (%h)", f"{previous}..{head}" if previous else head)
    notes += f"\n\nBuilt from `{head}`.\n"
    Path("release").mkdir(exist_ok=True)
    Path("release/NOTES.md").write_text(notes)
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"version={target}\n")


if __name__ == "__main__":
    main()
