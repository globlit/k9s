import os
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('fuzz.sh').resolve()


class FuzzRunnerTest(unittest.TestCase):
    def run_fake(self, body, *packages):
        with tempfile.TemporaryDirectory() as d:
            go = Path(d) / 'go'
            go.write_text('#!/bin/sh\n' + body)
            go.chmod(0o755)
            return subprocess.run(['bash', str(SCRIPT), *packages], env={**os.environ, 'PATH': d + ':' + os.environ['PATH']}, capture_output=True, text=True)

    def test_package_listing_failure(self):
        self.assertNotEqual(0, self.run_fake('exit 7\n').returncode)

    def test_test_listing_failure(self):
        self.assertNotEqual(0, self.run_fake('if [ "$1" = list ]; then echo example; else exit 7; fi\n').returncode)

    def test_no_targets(self):
        result = self.run_fake('if [ "$1" = list ]; then echo example; fi\n')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('No fuzz targets', result.stderr)

    def test_campaign_failure(self):
        result = self.run_fake('if [ "$1" = list ]; then echo example; elif [ "$2" = -list ]; then echo FuzzExample; else exit 9; fi\n')
        self.assertNotEqual(0, result.returncode)

    def test_campaign_success(self):
        result = self.run_fake('if [ "$1" = list ]; then echo example; elif [ "$2" = -list ]; then echo FuzzExample; fi\n')
        self.assertEqual(0, result.returncode)

    def test_selected_package(self):
        result = self.run_fake('''
if [ "$1" = list ]; then
    [ "$2" = ./internal/example ] && [ "$#" -eq 2 ] || exit 8
    echo example
elif [ "$2" = -list ]; then
    echo FuzzExample
fi
''', './internal/example')
        self.assertEqual(0, result.returncode)

    def test_continue_after_failure(self):
        result = self.run_fake('''
if [ "$1" = list ]; then
    echo example
elif [ "$2" = -list ]; then
    printf 'FuzzFirst\nFuzzSecond\n'
else
    echo "ran $5"
    [ "$5" != '^FuzzFirst$' ]
fi
''')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('ran ^FuzzFirst$', result.stdout)
        self.assertIn('ran ^FuzzSecond$', result.stdout)


class FuzzMatrixTest(unittest.TestCase):
    def discover(self, files):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            script = root / 'hack' / 'fuzz-targets.sh'
            script.parent.mkdir()
            script.write_bytes(SCRIPT.with_name('fuzz-targets.sh').read_bytes())
            for name, content in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)
            return subprocess.run(['bash', str(script)], capture_output=True, text=True)

    def test_group_package_targets(self):
        result = self.discover({
            'internal/example/a_test.go': 'func FuzzA(f *testing.F) {}\nfunc FuzzB(f *testing.F) {}\n',
            'internal/example/b_fuzz_test.go': 'func FuzzC(f *testing.F) {}\n',
            'cmd/example_test.go': 'func FuzzD(f *testing.F) {}\n',
            'internal/unused/a_test.go': 'func TestA(t *testing.T) {}\n',
        })
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual([
            {'pkg': './cmd', 'id': 'cmd'},
            {'pkg': './internal/example', 'id': 'internal-example'},
        ], json.loads(result.stdout))

    def test_empty_matrix_fails(self):
        result = self.discover({})
        self.assertNotEqual(0, result.returncode)
        self.assertIn('No fuzz targets discovered', result.stderr)
