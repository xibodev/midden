"""Build portable release archives from a clean checkout (Python 3 + Go).

Usage: python scripts/build-release.py --out <absolute-directory> [--targets windows/amd64 linux/amd64]
Archives contain the executable, license, readme and descriptor-declared content.
No state, credentials, source transcripts or local examples are packaged.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--targets', nargs='+', default=['windows/amd64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'])
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    if run('git', 'status', '--porcelain', cwd=root):
        raise SystemExit('Release builds require a clean checkout')
    if not args.out.is_absolute():
        raise SystemExit('--out must be absolute')
    args.out.mkdir(parents=True, exist_ok=True)
    commit = run('git', 'rev-parse', 'HEAD', cwd=root)
    with tempfile.TemporaryDirectory(prefix='midden-release-') as temp:
        temp = Path(temp)
        native = temp / ('describe.exe' if os.name == 'nt' else 'describe')
        subprocess.run(['go', 'build', '-trimpath', '-o', str(native), './cmd/midden'], cwd=root, check=True)
        descriptor = json.loads(run(str(native), 'module', 'describe', '--json', cwd=temp))['result']
        version = descriptor['version']
        content = [(root / 'LICENSE', 'LICENSE'), (root / 'README.md', 'README.md')]
        for entry in descriptor['agent_overlays'] + descriptor['skills']:
            relative = Path(entry['path'])
            if relative.is_absolute() or '..' in relative.parts:
                raise SystemExit('Unsafe descriptor content path')
            source = root / relative
            digest = 'sha256:' + hashlib.sha256(source.read_bytes()).hexdigest()
            if digest != entry['digest']:
                raise SystemExit(f'Descriptor content mismatch: {relative}')
            content.append((source, relative.as_posix()))
        assets = []
        for target in args.targets:
            goos, goarch = target.split('/')
            for variant in ('standalone', 'headless'):
                name = f'midden_{version}_{goos}_{goarch}_{variant}'
                stage = temp / name
                stage.mkdir()
                binary = stage / ('midden.exe' if goos == 'windows' else 'midden')
                env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0')
                command = ['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(binary)]
                if variant == 'headless':
                    command += ['-tags=headless']
                command += ['./cmd/midden']
                subprocess.run(command, cwd=root, env=env, check=True)
                binary.chmod(0o755)
                files = [(binary, binary.name)] + content
                if goos == 'windows':
                    archive = args.out / (name + '.zip')
                    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
                        for source, relative in files:
                            output.write(source, relative)
                else:
                    archive = args.out / (name + '.tar.gz')
                    with tarfile.open(archive, 'w:gz') as output:
                        for source, relative in files:
                            output.add(source, arcname=relative, recursive=False)
                assets.append(archive)
                print(f'Built {archive.name}', flush=True)
        manifest = args.out / 'build-manifest.json'
        manifest.write_text(json.dumps({'version': version, 'commit': commit, 'go': run('go', 'version'),
                                       'kernel': 'github.com/xibodev/facet-studio@v1.0.0',
                                       'targets': args.targets, 'variants': ['standalone', 'headless']}, indent=2) + '\n')
        assets.append(manifest)
        (args.out / 'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n' for p in sorted(assets)))


if __name__ == '__main__':
    main()
