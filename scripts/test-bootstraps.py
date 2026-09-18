"""Exercise Pages entry points without network, product execution, or profile writes."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    repo = Path(__file__).resolve().parent.parent
    windows = os.name == 'nt'
    with tempfile.TemporaryDirectory(prefix='midden bootstrap ') as temporary:
        root = Path(temporary).resolve()
        downloads = root / 'downloads'; downloads.mkdir()
        scratch = root / 'scratch'; scratch.mkdir()
        marker = root / 'invoked'
        script = 'install.ps1' if windows else 'install.sh'
        (downloads / 'manifest.tsv').write_text('fixture manifest\n')
        if windows:
            body = '''param([string]$Version,[string]$Probe)
if ($Version -ne 'v0.2.0' -or $Probe -ne $env:TEST_PROBE) { throw 'arguments lost' }
if (!(Test-Path (Join-Path $PSScriptRoot 'manifest.tsv'))) { throw 'manifest missing' }
Set-Content -LiteralPath $env:TEST_MARKER -Value 'invoked'
exit ([int]$env:TEST_EXIT)
'''
        else:
            body = '''#!/usr/bin/env bash
set -eu
test "$1" = --version && test "$2" = v0.2.0
shift 2
test -f "$(dirname "$0")/manifest.tsv"
if [[ "$1" = --interactive ]]; then
  printf 'fixture prompt: '
  read -r answer
  test "$answer" = 'terminal answer'
else
  test "$1" = --yes && test "$2" = 'path with spaces'
fi
printf invoked > "$TEST_MARKER"
exit "$TEST_EXIT"
'''
        (downloads / script).write_text(body)
        sums = ''.join(hashlib.sha256((downloads / f).read_bytes()).hexdigest() + '  ' + f + '\n'
                       for f in (script, 'manifest.tsv'))
        (downloads / 'SHA256SUMS').write_text(sums)
        env = dict(os.environ, TEST_DOWNLOADS=str(downloads), TEST_MARKER=str(marker), TEST_EXIT='0')
        if windows:
            env.update(TEMP=str(scratch), TMP=str(scratch), TEST_BOOTSTRAP=str(repo / 'docs' / script), TEST_PROBE='')
            wrapper = root / 'wrapper.ps1'
            wrapper.write_text('''$ErrorActionPreference = 'Stop'
function Invoke-WebRequest {
param($Uri,$OutFile,[switch]$UseBasicParsing)
Copy-Item -LiteralPath (Join-Path $env:TEST_DOWNLOADS ($Uri.Split('/')[-1])) -Destination $OutFile
}
$text = [IO.File]::ReadAllText($env:TEST_BOOTSTRAP)
# Pipeline-to-iex execution has no script path and must still work.
if ($env:TEST_PROBE) { & $env:TEST_BOOTSTRAP -Probe $env:TEST_PROBE }
else { $text | Invoke-Expression }
''')
            command = ['pwsh', '-NoProfile', '-File', str(wrapper)]
        else:
            tools = root / 'tools'; tools.mkdir()
            curl = tools / 'curl'
            curl.write_text('''#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
case "$1" in -o) out="$2"; shift 2;; https:*) url="$1"; shift;; *) shift;; esac
done
cp "$TEST_DOWNLOADS/${url##*/}" "$out"
''')
            curl.chmod(0o755)
            env.update(PATH=str(tools) + os.pathsep + env['PATH'], TMPDIR=str(scratch))
            command = ['/bin/bash', '-s', '--', '--yes', 'path with spaces']

        def run(success):
            result = subprocess.run(command, env=env, text=True, capture_output=True,
                                    input=None if windows else (repo / 'docs' / script).read_text(), timeout=30)
            assert (result.returncode == 0) == success, result.stdout + result.stderr
            assert not list(scratch.iterdir()), 'temporary downloads leaked'

        run(True)
        assert marker.exists()
        marker.unlink()
        if windows:
            env['TEST_PROBE'] = 'path with spaces'
            run(True)
            assert marker.exists()
            marker.unlink()
        (downloads / 'manifest.tsv').write_text('corrupt')
        run(False)
        assert not marker.exists(), 'executed after checksum failure'
        (downloads / 'manifest.tsv').write_text('fixture manifest\n')
        (downloads / 'SHA256SUMS').write_text(sums + sums)
        run(False)
        assert not marker.exists(), 'executed after duplicate checksum'
        (downloads / 'SHA256SUMS').write_text(sums)
        env['TEST_EXIT'] = '7'
        run(False)
        assert marker.exists(), 'child failure was not exercised'
        marker.unlink()
        env['TEST_EXIT'] = '0'

        if not windows:
            # Real controlling terminal plus a separate script pipe: ensure the
            # child reads answers from /dev/tty, not from downloaded script bytes.
            import pty
            import select
            import time
            pid, fd = pty.fork()
            if pid == 0:
                os.execvpe('/bin/bash', ['/bin/bash', '-c',
                           'cat "$1" | /bin/bash -s -- --interactive', 'test', str(repo / 'docs' / script)], env)
            output = b''
            sent = False
            deadline = time.monotonic() + 30
            try:
                while time.monotonic() < deadline:
                    if select.select([fd], [], [], 0.2)[0]:
                        try:
                            chunk = os.read(fd, 4096)
                        except OSError:
                            break
                        if not chunk:
                            break
                        output += chunk
                        if b'fixture prompt:' in output and not sent:
                            os.write(fd, b'terminal answer\n'); sent = True
                    done, status = os.waitpid(pid, os.WNOHANG)
                    if done:
                        assert os.waitstatus_to_exitcode(status) == 0, output
                        pid = 0
                        break
                assert sent and marker.exists(), output
                assert not list(scratch.iterdir()), 'interactive cleanup failed'
            finally:
                if pid:
                    done, status = os.waitpid(pid, os.WNOHANG)
                    if not done:
                        os.kill(pid, 9); os.waitpid(pid, 0)
                    else:
                        assert os.waitstatus_to_exitcode(status) == 0, output
                os.close(fd)
        print('PASS bootstrap: piped execution, argument forwarding, checksum refusal, child failure, cleanup' +
              ('' if windows else ', interactive terminal input'))


if __name__ == '__main__':
    main()
