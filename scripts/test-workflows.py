#!/usr/bin/env python3
"""Exercise release guards with fake GitHub responses and disposable Git history."""
import json
import os
import pathlib
import runpy
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parent.parent

class Weekly(unittest.TestCase):
    def check(self, prs, checks, merges):
        called = []
        def gh(args, **kwargs):
            called.append(args)
            if args[1:3] == ['pr', 'list']:
                return json.dumps(prs)
            if args[1:3] == ['pr', 'view']:
                return json.dumps({'headRefOid': 'reviewed-sha', 'statusCheckRollup': checks, 'mergeStateStatus': 'CLEAN'})
            if args[1:3] == ['pr', 'merge']:
                self.assertIn('--match-head-commit', args)
                self.assertIn('reviewed-sha', args)
                return ''
            self.fail(args)
        with patch.dict(os.environ, {'GITHUB_REPOSITORY':'Soli0222/mf-account-reloader', 'RELEASE_APP_SLUG':'release-app'}), patch('subprocess.check_output', gh):
            try:
                runpy.run_path(str(ROOT / 'scripts/weekly-release.py'))
            except SystemExit as e:
                self.assertEqual(e.code, 0)
        self.assertEqual(sum(a[1:3] == ['pr', 'merge'] for a in called), merges)

    def test_merge_gate(self):
        pr={'number':1,'headRefName':'release-please--branches--main','isDraft':False,'author':{'is_bot':True,'login':'release-app[bot]'}}
        success={'name':'CI required','status':'COMPLETED','conclusion':'SUCCESS'}
        self.check([],[],0)
        self.check([pr],[],0)
        self.check([pr],[dict(success, status='QUEUED')],0)
        self.check([pr],[dict(success, conclusion='FAILURE')],0)
        self.check([dict(pr,headRefName='unrelated')],[success],0)
        self.check([dict(pr,author={'is_bot':False,'login':'user'})],[success],0)
        self.check([pr],[success],1)

class Triage(unittest.TestCase):
    def test_values_not_formatting(self):
        with tempfile.TemporaryDirectory() as directory:
            cwd=pathlib.Path(directory)
            def git(*args):
                return subprocess.check_output(['git','-C',directory,*args],text=True).strip()
            git('init','-b','main')
            chart=cwd/'charts/demo/Chart.yaml';chart.parent.mkdir(parents=True)
            def commit(content):
                chart.write_text(content)
                git('add','.')
                git('-c','commit.gpgsign=false','-c','user.name=Validation','-c','user.email=validation@example.invalid','commit','-qm','change')
                return git('rev-parse','HEAD')
            before=commit('name: demo\ndescription: some words\nversion: 0.1.0\nappVersion: "0.1.0"\n')
            formatted=commit("name: demo\ndescription: some words\nversion: 0.1.0\nappVersion: '0.1.0'\n")
            after=commit('name: demo\ndescription: >-\n  some words\nversion: 0.1.0\nappVersion: "0.2.0"\n')
            output=cwd/'output'
            def triage(before,after):
                env=dict(os.environ,EVENT='push',BEFORE=before,AFTER=after,GITHUB_OUTPUT=str(output))
                return subprocess.check_output(['python3',str(ROOT/'scripts/chart-triage.py')],cwd=directory,env=env,text=True).strip()
            self.assertEqual(triage(before,formatted),'run=false')
            self.assertEqual(triage(before,after),'run=false')
            newer=commit('name: demo\ndescription: changed words\nversion: 0.2.0\nappVersion: "0.2.0"\n')
            self.assertEqual(triage(after,newer),'run=true')
            self.assertEqual(triage('0'*40,newer),'run=true')

if __name__ == '__main__':
    unittest.main()
