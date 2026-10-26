#!/usr/bin/env python3
"""Plan a stable release. This script never mutates Git refs or publishes."""
import argparse
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
    match = re.search(r"^## " + re.escape(tag) + r"(?:[ \t]+[^\n]*)?\n(.*?)(?=^## |\Z)", text, re.M | re.S)
    return match.group(1).strip() if match else ""


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def release_notes(text, tag):
    notes = changelog_section(text, tag)
    if not notes or not re.search(r"^[-*] \S", notes, re.M):
        raise ValueError(f"CHANGELOG.md needs nonempty release notes under ## {tag}")
    return notes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Validate planned notes without writing files')
    args = parser.parse_args()
    head = git("rev-parse", "HEAD")
    if not args.check and head != os.environ["GITHUB_SHA"]:
        raise RuntimeError("release checkout does not match the triggering commit")
    tags = git("tag", "--list").splitlines()
    at_head = [t for t in tags if PATTERN.fullmatch(t) and git("rev-list", "-n", "1", t) == head]
    target, previous = choose(Path("VERSION").read_text().strip(), tags, at_head)
    notes = release_notes(Path("CHANGELOG.md").read_text(), target)
    if args.check:
        print(f"Validated release notes for {target}.")
        return
    notes += f"\n\nBuilt from `{head}`.\n"
    Path("release").mkdir(exist_ok=True)
    Path("release/NOTES.md").write_text(notes)
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"version={target}\n")


if __name__ == "__main__":
    main()
