#!/usr/bin/env python3
"""Compare YAML values so release-please formatting cannot start a competing publish."""
import os
import subprocess
import yaml

before, after = os.environ.get('BEFORE', ''), os.environ.get('AFTER', 'HEAD')

def git(*args):
    return subprocess.run(['git', *args], text=True, capture_output=True, check=True).stdout

reconcile = True
if os.environ.get('EVENT') == 'push':
    try:
        git('cat-file', '-e', before + '^{commit}')
        changed = git('diff', '--name-only', before, after, '--', 'charts').splitlines()
        if len(changed) == 1 and changed[0].endswith('/Chart.yaml'):
            base = yaml.safe_load(git('show', f'{before}:{changed[0]}'))
            head = yaml.safe_load(git('show', f'{after}:{changed[0]}'))
            keys = {k for k in set(base) | set(head) if base.get(k) != head.get(k)}
            if keys <= {'appVersion'}:
                reconcile = False
    except subprocess.CalledProcessError:
        pass  # First push or unavailable before SHA: reconcile.
print('run=' + str(reconcile).lower())
with open(os.environ['GITHUB_OUTPUT'], 'a') as f:
    f.write('run=' + str(reconcile).lower() + '\n')
