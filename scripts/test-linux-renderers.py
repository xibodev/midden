"""Real apt + pinned D2 integration. Run only in a disposable Debian/Ubuntu container.

Installs Pandoc with apt through the installer, downloads upstream D2, renders
PPTX/HTML/SVG, verifies repeat/uninstall ownership. No real agent or session data.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--disposable-container', action='store_true', required=True)
    parser.parse_args()
    assert Path('/.dockerenv').exists(), 'This test changes system packages; use Docker'
    assert shutil.which('pandoc') is None and shutil.which('d2') is None, 'Need a clean renderer baseline'
    repo=Path(__file__).resolve().parent.parent
    with tempfile.TemporaryDirectory(prefix='midden-both-') as temporary:
        root=Path(temporary).resolve()
        home=root/'home';home.mkdir()
        project=root/'project';project.mkdir()
        bundle=root/'bundle';bundle.mkdir()
        (bundle/'midden').write_text('not executable; setup must not invoke product')
        for relative in ('skills/session-recovery/SKILL.md','skills/evidence-selection/SKILL.md',
                         'skills/content-seed/SKILL.md','agents/midden-recovery.md'):
            target=bundle/relative;target.parent.mkdir(parents=True,exist_ok=True)
            shutil.copyfile(repo/relative,target)
        host=root/'opencode';host.write_text('#!/bin/sh\necho fixture-opencode\n');host.chmod(0o755)
        dest=home/'bin';state=home/'state'
        common=['bash',str(repo/'install.sh'),'--manifest',str(repo/'installer/manifest.tsv'),
                '--bundle-dir',str(bundle),'--home-dir',str(home),'--install-dir',str(dest),
                '--state-dir',str(state),'--hosts','opencode','--host-path',str(host),
                '--scope','project','--project',str(project),'--yes','--no-path']
        corrupt=root/'corrupt.tsv'
        rows=(repo/'installer/manifest.tsv').read_text().splitlines()
        corrupt.write_text('\n'.join('\t'.join(row.split('\t')[:2]+['0'*64])
                         if row.startswith('setting\td2_linux_') and '_sha256\t' in row else row
                         for row in rows)+'\n')
        bad_common=list(common);bad_common[bad_common.index('--manifest')+1]=str(corrupt)
        failed=subprocess.run(bad_common+['--dependencies','pandoc,d2'],text=True,capture_output=True,timeout=180)
        assert failed.returncode!=0 and 'D2 checksum verification failed' in failed.stderr
        assert not dest.exists() and shutil.which('pandoc') is None, 'checksum failure must precede apt/system changes'
        subprocess.run(common+['--dependencies','pandoc,d2'],input='Y\n',text=True,check=True,timeout=300)
        assert (dest/'d2').exists() and (dest/'d2-LICENSE.txt').exists()
        env=dict(os.environ,PATH=str(dest)+os.pathsep+os.environ['PATH'])
        source=root/'slides.md';source.write_text('# Evidence\n\nA real rendering smoke test.\n')
        for kind in ('pptx','html'):
            subprocess.run(['pandoc',str(source),'-s','-o',str(root/('slides.'+kind))],env=env,check=True)
            assert (root/('slides.'+kind)).stat().st_size>0
        diagram=root/'diagram.d2';diagram.write_text('source -> evidence\n')
        subprocess.run(['d2',str(diagram),str(root/'diagram.svg')],env=env,check=True)
        assert '<svg' in (root/'diagram.svg').read_text()
        subprocess.run(common+['--verify'],check=True)
        subprocess.run(common,check=True)  # retains ownership without dependency selection
        receipt=(dest/'install-receipt.tsv').read_text()
        assert str(dest/'d2') in receipt
        original=(dest/'d2').read_bytes()
        (dest/'d2').write_bytes(b'user edited')
        failed=subprocess.run(common+['--uninstall'],capture_output=True,text=True)
        assert failed.returncode!=0 and (dest/'d2').read_bytes()==b'user edited'
        (dest/'d2').write_bytes(original)
        subprocess.run(common+['--uninstall'],check=True)
        assert not (dest/'d2').exists() and not (dest/'midden').exists()
        assert shutil.which('pandoc'), 'System package must be preserved'
        assert not state.exists()
        print('PASS real Linux Both: apt Pandoc + pinned D2, checksum refusal before apt, PPTX/HTML/SVG, repeat, modified-file refusal, verify, uninstall')


if __name__=='__main__':
    main()
