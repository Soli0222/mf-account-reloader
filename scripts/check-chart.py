#!/usr/bin/env python3
"""Validate rendered Kubernetes/YAML values, including packaged appVersion."""
import pathlib
import subprocess
import tempfile
import yaml

CHART = 'charts/mf-account-reloader'

def run(*args, success=True):
    p = subprocess.run(args, text=True, capture_output=True)
    if (p.returncode == 0) != success:
        raise AssertionError(f'{args}: {p.stdout}\n{p.stderr}')
    return p.stdout

def render(path=CHART, values=None, name='test'):
    args = ['helm', 'template', name, path]
    for k, v in (values or {}).items():
        args += ['--set-string' if isinstance(v, str) else '--set', f'{k}={str(v).lower() if isinstance(v, bool) else v}']
    docs = [d for d in yaml.safe_load_all(run(*args)) if d]
    cron = next(d for d in docs if d['kind'] == 'CronJob')
    return docs, cron

def validate(docs, cron):
    spec = cron['spec']
    assert len(cron['metadata']['name']) <= 52
    assert spec['schedule'] == '0 11 * * *' and spec['timeZone'] == 'Asia/Tokyo'
    job = spec['jobTemplate']['spec']
    assert job['backoffLimit'] == 0 and job['activeDeadlineSeconds'] == 900
    pod = job['template']['spec']
    assert pod['restartPolicy'] == 'Never' and not pod['automountServiceAccountToken']
    assert pod['securityContext']['fsGroup'] == 10001
    container = pod['containers'][0]
    assert container['securityContext']['readOnlyRootFilesystem']
    assert container['securityContext']['capabilities']['drop'] == ['ALL']
    volumes = {v['name']: v for v in pod['volumes']}
    assert all(m['name'] in volumes for m in container['volumeMounts'])
    assert volumes['shm']['emptyDir']['medium'] == 'Memory'
    config = yaml.safe_load(next(d for d in docs if d['kind'] == 'ConfigMap')['data']['config.yaml'])
    assert config['auth']['stateFile'] == '/state/auth-state.json'
    return pod, volumes, config

run('helm', 'lint', '--strict', CHART)
for values in [{}, {'persistence.enabled': False}, {'persistence.existingClaim': 'existing'}, {'auth.stateSecret.name': 'seed', 'auth.existingSecret': 'credentials', 'auth.keys.email': 'mail', 'auth.keys.password': 'pass', 'auth.keys.totpSecret': 'totp'}, {'image.tag': '2.3.4'}]:
    docs, cron = render(values=values)
    pod, volumes, config = validate(docs, cron)
    if values.get('persistence.enabled') is False:
        assert 'emptyDir' in volumes['state'] and not any(d['kind'] == 'PersistentVolumeClaim' for d in docs)
    if values.get('persistence.existingClaim'):
        assert volumes['state']['persistentVolumeClaim']['claimName'] == 'existing'
        assert not any(d['kind'] == 'PersistentVolumeClaim' for d in docs)
    if values.get('auth.stateSecret.name'):
        assert volumes['seed']['secret']['secretName'] == 'seed'
        assert config['auth']['seedFile'] == '/seed/auth-state.json'
        assert volumes['credentials']['secret']['items'] == [{'key':'mail','path':'email'},{'key':'pass','path':'password'},{'key':'totp','path':'totp-secret'}]
    if values.get('image.tag'):
        assert pod['containers'][0]['image'].endswith(':2.3.4')
validate(*render(name='long-' + 'a' * 48))
for field, value in [('suspend', 'invalid'), ('activeDeadlineSeconds', '0'), ('config.unknown', 'bad'), ('image.pullPolicy', 'bad'), ('config.auth.password', 'secret'), ('config.timeouts.run','0s'), ('config.reload.pollInterval','invalid')]:
    run('helm', 'template', 'test', CHART, '--set', f'{field}={value}', success=False)
with tempfile.TemporaryDirectory() as tmp:
    run('helm', 'package', CHART, '--app-version', '9.9.9', '--destination', tmp)
    package = str(next(pathlib.Path(tmp).glob('*.tgz')))
    docs, cron = render(package)
    pod, _, _ = validate(docs, cron)
    assert pod['containers'][0]['image'] == 'ghcr.io/soli0222/mf-account-reloader:9.9.9'
print('Chart lint, render variants, schema, packaged appVersion: OK')
