#!/usr/bin/env python3
"""Merge only an unambiguous release-please branch with successful required CI."""
import json
import os
import subprocess

repo = os.environ['GITHUB_REPOSITORY']

def gh(*args):
    return subprocess.check_output(['gh', *args, '--repo', repo], text=True)

prs = json.loads(gh('pr', 'list', '--base', 'main', '--label', 'autorelease: pending', '--state', 'open', '--json', 'number,headRefName,isDraft,author'))
app = os.environ.get('RELEASE_APP_SLUG', '')
candidates = [p for p in prs if p['headRefName'] == 'release-please--branches--main' and not p['isDraft'] and p['author']['is_bot'] and app and p['author']['login'].removeprefix('app/').removesuffix('[bot]') == app]
if len(candidates) > 1:
    raise SystemExit('Multiple release PRs found; refusing to select one.')
if not candidates:
    print('No pending release-please PR.')
    raise SystemExit(0)
pr = candidates[0]
view = json.loads(gh('pr', 'view', str(pr['number']), '--json', 'headRefOid,statusCheckRollup,mergeStateStatus'))
checks = view['statusCheckRollup']
# An empty set or a pending CI must never be treated as a successful release check.
required = [c for c in checks if c.get('name') == 'CI required']
if not required or any(c.get('status') != 'COMPLETED' or c.get('conclusion') != 'SUCCESS' for c in required):
    print('CI required has not succeeded; leaving PR open.')
    raise SystemExit(0)
if any(c.get('status') == 'IN_PROGRESS' or c.get('status') == 'QUEUED' or c.get('conclusion') not in (None, 'SUCCESS', 'NEUTRAL', 'SKIPPED') or c.get('state') in ('FAILURE', 'ERROR', 'PENDING') for c in checks):
    print('Other checks are pending or failed; leaving PR open.')
    raise SystemExit(0)
if view['mergeStateStatus'] != 'CLEAN':
    print('Merge is not ready; leaving PR open.')
    raise SystemExit(0)
# Pin the reviewed head so a newer untested commit cannot slip through.
gh('pr', 'merge', str(pr['number']), '--squash', '--match-head-commit', view['headRefOid'])
