import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('fuzz.sh').resolve()


class FuzzRunnerTest(unittest.TestCase):
    def run_fake(self, body):
        with tempfile.TemporaryDirectory() as d:
            go = Path(d) / 'go'
            go.write_text('#!/bin/sh\n' + body)
            go.chmod(0o755)
            return subprocess.run(['bash', str(SCRIPT)], env={**os.environ, 'PATH': d + ':' + os.environ['PATH']}, capture_output=True, text=True)

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
