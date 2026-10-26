#!/usr/bin/env python3
"""Validate source and packaged Markdown links plus maintained config contracts."""
import argparse
import collections
from pathlib import Path
import re
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[2]


def anchors(text):
    counts = collections.Counter()
    result = set()
    in_fence = False
    for line in text.splitlines():
        if line.startswith(('```', '~~~')):
            in_fence = not in_fence
        if in_fence:
            continue
        heading = re.match(r'^#{1,6}\s+(.+?)\s*#*$', line)
        if not heading:
            continue
        slug = re.sub(r'[^\w\- ]', '', heading[1].lower()).replace(' ', '-')
        result.add(slug + (f'-{counts[slug]}' if counts[slug] else ''))
        counts[slug] += 1
    return result


def check_links(root, files):
    root = root.resolve()
    errors = []
    count = 0
    for path in files:
        path = path.resolve()
        for target in re.findall(r'\[[^\]\n]+\]\(([^)\s]+)\)', path.read_text()):
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc:
                continue
            count += 1
            destination = (path.parent / unquote(parsed.path)).resolve() if parsed.path else path
            if not destination.is_relative_to(root.resolve()) or not destination.exists():
                errors.append(f'{path.relative_to(root)}: missing local target {target}')
            elif parsed.fragment and destination.suffix == '.md':
                if unquote(parsed.fragment) not in anchors(destination.read_text()):
                    errors.append(f'{path.relative_to(root)}: missing anchor {target}')
    return count, errors


def check_contracts(root):
    errors = []
    cfg = (root / 'internal/config/config.go').read_text()
    docs = (root / 'docs/configuration.md').read_text()
    # Compare the defaults in the reference table to the source declaration.
    defaults = re.search(r'return Config\{(.*?)\}', cfg).group(1)
    for field, go_name in [('listen', 'Listen'), ('model', 'Model'),
                           ('reasoning_effort', 'ReasoningEffort'),
                           ('context_window', 'ContextWindow'), ('compact_window', 'CompactWindow')]:
        value = re.search(r'\b' + go_name + r':\s*("[^"]*"|\d+)', defaults).group(1).strip('"')
        if f'| `{field}` | `{value}` |' not in docs:
            errors.append(f'configuration.md: {field} default differs from source ({value})')
    source_models = set(re.findall(r'\{"(gpt-[^"]+)",', (root / 'internal/config/models.go').read_text()))
    table_models = set(re.findall(r'^\| .*? \| `(gpt-[^`]+)` \|$', docs, re.M))
    if source_models != table_models:
        errors.append('configuration.md: model alias table differs from the local catalog')
    modules = dict(re.findall(r'^\s+([\w./-]+)\s+(v\S+)', (root / 'go.mod').read_text(), re.M))
    notices = dict(re.findall(r'^## ([\w./-]+)@(v\S+) —', (root / 'THIRD_PARTY_NOTICES.md').read_text(), re.M))
    if modules != notices:
        errors.append('THIRD_PARTY_NOTICES.md: dependency names/versions differ from go.mod')
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--package', type=Path, help='Check an extracted release instead of the checkout')
    args = parser.parse_args()
    root = (args.package or ROOT).resolve()
    files = list(root.glob('*.md')) + list((root / 'docs').rglob('*.md'))
    if not args.package:
        files += list((root / '.github').rglob('*.md'))
    count, errors = check_links(root, files)
    if not args.package:
        errors += check_contracts(root)
    elif not (root / 'SECURITY.md').is_file() or (root / 'docs/design').exists():
        errors.append('release must include SECURITY.md and exclude docs/design')
    if errors:
        print('\n'.join(errors), file=sys.stderr)
        return 1
    print(f'Validated {len(files)} Markdown files and {count} local links.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
