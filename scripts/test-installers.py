"""Offline script-level installer tests. Never touches real CLI profiles.

python scripts/test-installers.py --shell powershell --program pwsh
python scripts/test-installers.py --shell bash --program /bin/bash
"""
import argparse
import hashlib
import json
import os
import platform
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--shell', choices=['powershell', 'bash'], required=True)
    parser.add_argument('--program', required=True)
    parser.add_argument('--release-bundle', type=Path)
    parser.add_argument('--release-version')
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    with tempfile.TemporaryDirectory(prefix='midden script tests ') as temp:
        # macOS /var is a symlink; the installers intentionally reject linked paths.
        root = Path(temp).resolve()
        home = root / 'home'; home.mkdir()
        project = root / 'project'; project.mkdir()
        bundle = root / 'bundle'; bundle.mkdir()
        dest = home / 'install with spaces' / 'bin'
        state = home / 'recovery-state'
        binary = 'midden.exe' if args.shell == 'powershell' else 'midden'
        (bundle / binary).write_bytes(b'installer never executes this product binary\n')
        for relative in ['skills/session-recovery/SKILL.md', 'skills/evidence-selection/SKILL.md',
                         'skills/content-seed/SKILL.md', 'agents/midden-recovery.md']:
            target = bundle / relative; target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((repo / relative).read_bytes())
        manifest = root / 'manifest.tsv'
        manifest.write_bytes((repo / 'installer/manifest.tsv').read_bytes())
        env = dict(os.environ)
        if args.shell == 'powershell':
            # A harmless real executable acts as a version-check fixture.
            host = Path(shutil.which('pwsh') or args.program).resolve()
            prefix = [args.program, '-NoProfile', '-File', str(repo / 'install.ps1')]
            common = ['-Manifest', str(manifest), '-BundleDir', str(bundle), '-HomeDir', str(home),
                      '-InstallDir', str(dest), '-StateDir', str(state), '-NonInteractive']
            install = ['-Hosts', 'claude-code', '-HostPath', str(host), '-Scope', 'project', '-ProjectDir', str(project)]
            preview, verify, uninstall, upgrade = '-DryRun', '-Verify', '-Uninstall', '-Upgrade'
        else:
            tools = root / 'tools'; tools.mkdir()
            host = tools / 'claude'
            host.write_bytes(b'#!/bin/sh\necho "fixture-host 1.0"\n'); host.chmod(0o755)
            prefix = [args.program, str(repo / 'install.sh')]
            common = ['--manifest', str(manifest), '--bundle-dir', str(bundle), '--home-dir', str(home),
                      '--install-dir', str(dest), '--state-dir', str(state), '--yes']
            install = ['--hosts', 'claude-code', '--host-path', str(host), '--scope', 'project', '--project', str(project)]
            preview, verify, uninstall, upgrade = '--dry-run', '--verify', '--uninstall', '--upgrade'
        def call(extra, succeeds=True):
            result = subprocess.run(prefix + common + extra, env=env, capture_output=True, text=True, timeout=90)
            assert (result.returncode == 0) == succeeds, result.stdout + result.stderr
            return result
        call(install + [preview])
        assert not dest.exists() and not state.exists() and not (project / '.claude').exists()
        interactive = list(common)
        interactive.remove('-NonInteractive' if args.shell == 'powershell' else '--yes')
        # Explicit host/scope: only capability choice and one final confirmation.
        answers = 'invalid\n1\ninvalid\n1\ninvalid\nno\n'
        result = subprocess.run(prefix + interactive + install, env=env, input=answers, capture_output=True, text=True, timeout=90)
        assert result.returncode == 0, result.stdout + result.stderr
        assert not dest.exists() and not state.exists() and not (project / '.claude').exists(), 'cancel wrote files'
        assert 'Existing pandoc' not in result.stdout and 'Binary installation directory [' not in result.stdout
        # Default detection chooses the installed host, not hardcoded Copilot.
        detection = root / 'detection'; detection.mkdir()
        isolated_manifest = root / 'detect.tsv'
        rows = manifest.read_text().splitlines()
        if args.shell == 'powershell':
            detected_host = detection / 'midden-fixture-host.cmd'
            detected_host.write_text('@echo off\necho fixture-host 1.0\n')
            for tool in ('pandoc', 'd2'):
                (detection / (tool + '.cmd')).write_text('@echo off\nexit /b 77\n')
        else:
            detected_host = detection / 'midden-fixture-host'
            detected_host.write_text('#!/bin/sh\necho fixture-host 1.0\n'); detected_host.chmod(0o755)
            for tool in ('pandoc', 'd2'):
                broken = detection / tool
                broken.write_text('#!/bin/sh\nexit 77\n'); broken.chmod(0o755)
        for i, row in enumerate(rows):
            if row.startswith('host\t'):
                fields = row.split('\t')
                fields[2] = 'midden-fixture-host' if fields[1] == 'claude-code' else 'midden-missing-' + fields[1]
                rows[i] = '\t'.join(fields)
        isolated_manifest.write_text('\n'.join(rows) + '\n')
        detect_common = list(interactive)
        detect_common[detect_common.index('-Manifest' if args.shell == 'powershell' else '--manifest') + 1] = str(isolated_manifest)
        detect_env = dict(env, PATH=str(detection) + os.pathsep + env['PATH'])
        detect_args = ['-Scope', 'project', '-ProjectDir', str(project), '-NoPath'] if args.shell == 'powershell' else ['--scope', 'project', '--project', str(project), '--no-path']
        result = subprocess.run(prefix + detect_common + detect_args, env=detect_env, input='1\n1\nyes\n', capture_output=True, text=True, timeout=90)
        assert result.returncode == 0, result.stdout + result.stderr
        assert 'Claude Code ready' in result.stdout
        assert 'Choose CLI numbers' not in result.stdout, 'single detected host should be automatic'
        assert 'Get-FileHash' not in result.stderr
        assert (project / '.claude/skills/midden-session-recovery/SKILL.md').exists()
        call([uninstall])
        print(f'PASS {args.shell}: single-host detection, minimal interactive install, broken unselected renderers ignored')
        # Multiple detected hosts use a numbered choice and reprompt on errors.
        for i, row in enumerate(rows):
            if row.startswith('host\tcopilot-cli\t'):
                fields=row.split('\t'); fields[2]='midden-fixture-host'; rows[i]='\t'.join(fields)
        isolated_manifest.write_text('\n'.join(rows) + '\n')
        result = subprocess.run(prefix + detect_common + detect_args, env=detect_env,
                                input='1\n99\n1\n1\nyes\n', capture_output=True, text=True, timeout=90)
        assert result.returncode == 0, result.stdout + result.stderr
        assert 'Choose a listed number' in result.stdout + result.stderr
        assert (project / '.github/skills/midden-session-recovery/SKILL.md').exists()
        assert (project / '.claude/skills/midden-session-recovery/SKILL.md').exists()
        call([uninstall])
        print(f'PASS {args.shell}: multiple-host selection and invalid-choice recovery')
        # Custom setup is reachable in plain mode, with explicit scope/paths and
        # no PATH mutation. A cancellation must still leave every target absent.
        custom_dest=home / 'custom bin'
        custom_state=home / 'custom state'
        custom_project=root / 'custom project'; custom_project.mkdir()
        custom_input=f'2\n{custom_dest}\n2\n{custom_project}\n{custom_state}\n2\n1\nno\n'
        result=subprocess.run(prefix+interactive+install,env=env,input=custom_input,capture_output=True,text=True,timeout=90)
        assert result.returncode==0,result.stdout+result.stderr
        assert 'Custom setup' in result.stdout+result.stderr
        assert not custom_dest.exists() and not custom_state.exists() and not (custom_project / '.claude').exists()
        result=subprocess.run(prefix+interactive+install,env=env,input='',capture_output=True,text=True,timeout=90)
        assert result.returncode!=0, 'EOF must not accept defaults and install'
        assert not (dest / binary).exists()
        # Selected renderer failure blocks installation; success requires a real
        # nonempty artifact, not just an exit code or a version string.
        if args.shell == 'powershell':
            renderer = detection / 'render.cmd'
            bad_body = '@echo off\nexit /b 12\n'
            good_body = '@echo off\n:next\nif "%~1"=="" exit /b 2\nif "%~1"=="-o" goto output\nshift\ngoto next\n:output\nshift\necho fixture-render> "%~1"\nexit /b 0\n'
            render_args = ['-Dependencies', 'pandoc', '-PandocPath', str(renderer)]
        else:
            renderer = detection / 'render'
            bad_body = '#!/bin/sh\nexit 12\n'
            good_body = '#!/bin/sh\nwhile [ "$#" -gt 0 ]; do if [ "$1" = -o ]; then printf fixture-render > "$2"; exit 0; fi; shift; done\nexit 2\n'
            render_args = ['--dependencies','pandoc','--pandoc-path',str(renderer)]
        renderer.write_text(bad_body); renderer.chmod(0o755)
        call(install + render_args, succeeds=False)
        assert not (dest / binary).exists(), 'failed selected renderer installed Midden'
        renderer.write_text(good_body); renderer.chmod(0o755)
        call(install + render_args)
        call([verify])
        call([uninstall])
        print(f'PASS {args.shell}: selected renderer functional success/failure')
        call(install)
        skill = project / '.claude/skills/midden-session-recovery/SKILL.md'
        assert skill.exists()
        assert 'Installation binding' in skill.read_text(encoding='utf-8')
        assert 'recipes.compose' in skill.read_text(encoding='utf-8')
        assert (dest / binary).read_bytes() == (bundle / binary).read_bytes()
        before = hashlib.sha256(skill.read_bytes()).hexdigest()
        call(install)
        assert hashlib.sha256(skill.read_bytes()).hexdigest() == before
        call([verify])
        (bundle / binary).write_bytes(b'upgraded binary\n')
        call(install, succeeds=False)
        call(install + [upgrade])
        call([verify])
        original = skill.read_bytes()
        skill.write_bytes(b'user edited\n')
        call([uninstall], succeeds=False)
        assert skill.read_bytes() == b'user edited\n'
        skill.write_bytes(original)
        state.mkdir(); (state / 'keep.txt').write_text('recovered work')
        foreign = skill.parent / 'user-notes.txt'; foreign.write_text('preserve')
        call([uninstall, preview])
        assert skill.exists()
        call([uninstall])
        assert not skill.exists() and not (dest / binary).exists()
        assert foreign.read_text() == 'preserve' and (state / 'keep.txt').read_text() == 'recovered work'
        print(f'PASS {args.shell}: preview, install, repeat, verify, upgrade, modified-file refusal, uninstall, data preservation')

        # Public-download path with local HTTP-command fixtures. The product
        # executable is deliberately invalid: neither installer may execute it.
        native_os = 'darwin' if platform.system() == 'Darwin' else 'linux'
        native_arch = 'arm64' if platform.machine().lower() in ('arm64', 'aarch64') else 'amd64'
        asset = 'midden_9.8.7_' + ('windows_amd64_headless.zip' if args.shell == 'powershell' else f'{native_os}_{native_arch}_headless.tar.gz')
        archive = root / asset
        if args.shell == 'powershell':
            with zipfile.ZipFile(archive, 'w') as output:
                for file in bundle.rglob('*'):
                    if file.is_file():
                        output.write(file, file.relative_to(bundle).as_posix())
        else:
            with tarfile.open(archive, 'w:gz') as output:
                for file in bundle.rglob('*'):
                    if file.is_file():
                        output.add(file, arcname=file.relative_to(bundle).as_posix())
        checksums = root / 'SHA256SUMS'
        checksums.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + asset + '\n')
        download_common = list(common)
        flag = '-BundleDir' if args.shell == 'powershell' else '--bundle-dir'
        pos = download_common.index(flag)
        del download_common[pos:pos + 2]
        download_common += ['-Version' if args.shell == 'powershell' else '--version', 'v9.8.7']
        env['TEST_ARCHIVE'] = str(archive)
        env['TEST_CHECKSUMS'] = str(checksums)
        if args.shell == 'powershell':
            wrapper = root / 'download-test.ps1'
            wrapper.write_text('''function Invoke-WebRequest {
param([string]$Uri,[string]$OutFile,[switch]$UseBasicParsing,[int]$TimeoutSec)
if ($env:TEST_RETRY -and !(Test-Path $env:TEST_RETRY)) { Set-Content $env:TEST_RETRY 'attempt'; throw 'fixture transient network failure' }
if ($Uri.EndsWith('/SHA256SUMS')) { Copy-Item -LiteralPath $env:TEST_CHECKSUMS -Destination $OutFile }
elseif ($Uri.EndsWith('.zip')) { Copy-Item -LiteralPath $env:TEST_ARCHIVE -Destination $OutFile }
else { throw "Unexpected download: $Uri" }
}
& $env:TEST_SCRIPT @args
''', encoding='utf-8')
            env['TEST_SCRIPT'] = str(repo / 'install.ps1')
            env['TEST_RETRY'] = str(root / 'retried')
            download_prefix = [args.program, '-NoProfile', '-File', str(wrapper)]
        else:
            curl = tools / 'curl'
            curl.write_text('''#!/bin/sh
url= out=
while [ "$#" -gt 0 ]; do
case "$1" in -o) out="$2"; shift 2;; http*) url="$1"; shift;; *) shift;; esac
done
case "$url" in */SHA256SUMS) cp "$TEST_CHECKSUMS" "$out";; *.tar.gz) cp "$TEST_ARCHIVE" "$out";; *) exit 1;; esac
''')
            curl.chmod(0o755)
            env['PATH'] = str(tools) + os.pathsep + env['PATH']
            download_prefix = prefix
        result = subprocess.run(download_prefix + download_common + install, env=env, capture_output=True, text=True, timeout=90)
        assert result.returncode == 0, result.stdout + result.stderr
        assert (dest / binary).read_bytes() == (bundle / binary).read_bytes()
        call([uninstall])
        checksums.write_text('0' * 64 + '  ' + asset + '\n')
        result = subprocess.run(download_prefix + download_common + install, env=env, capture_output=True, text=True, timeout=90)
        assert result.returncode != 0, 'corrupted release was installed'
        assert not (dest / binary).exists(), 'checksum failure wrote executable'
        print(f'PASS {args.shell}: prebuilt archive installation and checksum failure before install; no product installer invoked')

        # Cancellation and pre-existing files must remain independent of the
        # product binary, regardless of its contents.
        (dest / binary).parent.mkdir(parents=True, exist_ok=True)
        (dest / binary).write_bytes(b'not installer owned')
        call(install, succeeds=False)
        assert (dest / binary).read_bytes() == b'not installer owned'
        (dest / binary).unlink()
        if args.shell == 'bash':
            # PATH checks run only in an isolated POSIX home, never Windows HKCU.
            env['SHELL'] = '/bin/sh'
            profile = home / '.profile'
            profile.write_text('export KEEP_ME=yes\n')
            call(install + ['--add-path'])
            assert '# midden-cli:' in profile.read_text()
            call([uninstall])
            assert 'export KEEP_ME=yes' in profile.read_text() and '# midden-cli:' not in profile.read_text()
            for shell, relative in [('/bin/zsh','.zshrc'),('/bin/fish','.config/fish/conf.d/midden.fish'),
                                    ('/bin/bash','.bash_profile' if platform.system() == 'Darwin' else '.bashrc')]:
                env['SHELL']=shell
                profile=home / relative
                call(install + ['--add-path'])
                assert '# midden-cli:' in profile.read_text()
                call([uninstall])
                assert '# midden-cli:' not in profile.read_text()
        print(f'PASS {args.shell}: unowned file preservation')

        if args.shell == 'bash':
            # Exercise macOS selection deterministically on Linux. This is not
            # a claim of execution on a native macOS machine.
            uname = tools / 'uname'
            uname.write_text('#!/bin/sh\ncase "$1" in -s) echo Darwin;; -m) echo arm64;; esac\n')
            uname.chmod(0o755)
            asset = 'midden_9.8.7_darwin_arm64_headless.tar.gz'
            checksums.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + asset + '\n')
            result = subprocess.run(download_prefix + download_common + install, env=env, capture_output=True, text=True, timeout=90)
            assert result.returncode == 0, result.stdout + result.stderr
            call([uninstall])
            print('PASS mocked Darwin/arm64 selection (native macOS execution not performed)')

        if args.release_bundle:
            # Native CI: install actual packaged bytes, then exercise the installed
            # executable separately. Installation itself still never executes it.
            for file in args.release_bundle.rglob('*'):
                if file.is_file():
                    target = bundle / file.relative_to(args.release_bundle)
                    target.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(file, target)
            call(install)
            call([verify])
            result = subprocess.check_output([str(dest / binary), 'version'], env=env, text=True)
            assert args.release_version in result
            call([uninstall])
            print('PASS native release bundle: install, version, verify, uninstall')


if __name__ == '__main__':
    main()
