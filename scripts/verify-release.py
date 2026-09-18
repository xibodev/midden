"""Verify the complete release contract; optionally smoke-test native artifacts."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile
import zipfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--smoke', action='store_true')
    args = parser.parse_args()
    root = args.directory
    manifest = json.loads((root / 'build-manifest.json').read_text())
    assert 'v' + manifest['version'] == args.tag, 'tag/version mismatch'
    assert manifest['commit'] == args.commit, 'source revision mismatch'
    targets = {'windows/amd64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'}
    assert set(manifest['targets']) == targets
    variants = {'headless', 'standalone'}
    assert set(manifest['variants']) == variants
    expected = {'install.ps1', 'install.sh', 'manifest.tsv', 'build-manifest.json'}
    contents = {'LICENSE', 'README.md', 'agents/midden-recovery.md',
                'skills/session-recovery/SKILL.md', 'skills/evidence-selection/SKILL.md',
                'skills/content-seed/SKILL.md'}
    for target in targets:
        system, arch = target.split('/')
        for variant in variants:
            name = f'midden_{manifest["version"]}_{system}_{arch}_{variant}'
            name += '.zip' if system == 'windows' else '.tar.gz'
            expected.add(name)
            binary = 'midden.exe' if system == 'windows' else 'midden'
            if system == 'windows':
                with zipfile.ZipFile(root / name) as archive:
                    entries = archive.infolist()
                    assert len(entries) == len(contents) + 1
                    assert {e.filename for e in entries} == contents | {binary}
                    assert all(not e.is_dir() and e.file_size > 0 for e in entries)
            else:
                with tarfile.open(root / name) as archive:
                    entries = archive.getmembers()
                    assert len(entries) == len(contents) + 1
                    assert {e.name for e in entries} == contents | {binary}
                    assert all(e.isfile() and e.size > 0 for e in entries)
    checksums = {}
    for line in (root / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split()
        assert name not in checksums
        checksums[name] = digest
    assert set(checksums) == expected
    assert {p.name for p in root.iterdir()} == expected | {'SHA256SUMS'}
    for name, digest in checksums.items():
        assert hashlib.sha256((root / name).read_bytes()).hexdigest() == digest, name
    print('PASS release: source, version, target matrix, checksums, archive allowlist')
    if not args.smoke:
        return
    system = {'Windows': 'windows', 'Darwin': 'darwin', 'Linux': 'linux'}[platform.system()]
    arch = 'arm64' if platform.machine().lower() in ('arm64', 'aarch64') else 'amd64'
    with tempfile.TemporaryDirectory(prefix='midden-release-smoke-') as temporary:
        home = Path(temporary).resolve()
        env = dict(os.environ, MIDDEN_HOME=str(home / 'state'))
        for variant in sorted(variants):
            bundle = home / variant
            bundle.mkdir()
            name = f'midden_{manifest["version"]}_{system}_{arch}_{variant}'
            if system == 'windows':
                with zipfile.ZipFile(root / (name + '.zip')) as archive:
                    archive.extractall(bundle)
                binary = bundle / 'midden.exe'
            else:
                with tarfile.open(root / (name + '.tar.gz')) as archive:
                    archive.extractall(bundle, filter='data')
                binary = bundle / 'midden'
                binary.chmod(0o755)
            version = subprocess.check_output([str(binary), 'version'], env=env, text=True)
            assert manifest['version'] in version
            descriptor = json.loads(subprocess.check_output(
                [str(binary), 'module', 'describe', '--json'], env=env, text=True))['result']
            assert descriptor['version'] == manifest['version']
            assert not (home / 'state').exists(), 'passive checks wrote state'
        repo = Path(__file__).resolve().parent.parent
        subprocess.run(['python', str(repo / 'scripts/test-installers.py'),
                        '--shell', 'powershell' if system == 'windows' else 'bash',
                        '--program', 'pwsh' if system == 'windows' else '/bin/bash',
                        '--release-bundle', str(home / 'headless'),
                        '--release-version', manifest['version']], check=True)
    print(f'PASS native {system}/{arch}: both binaries and installed headless bundle')


if __name__ == '__main__':
    main()
