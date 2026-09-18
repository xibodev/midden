"""Real terminal/ConPTY tests for keyboard wizard paths; isolated synthetic hosts.

Windows requires pywinpty. Unix uses the standard-library PTY implementation.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


class Terminal:
    def __init__(self, command, env):
        self.output = ''
        self.cursor = 0
        if os.name == 'nt':
            from winpty import PtyProcess
            self.process = PtyProcess.spawn(subprocess.list2cmdline(command), env=env, dimensions=(32, 110))
        else:
            import pty
            import fcntl
            import struct
            import termios
            self.pid, self.fd = pty.fork()
            if self.pid == 0:
                os.execvpe(command[0], command, env)
            fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 32, 110, 0, 0))

    def send(self, value):
        if os.name == 'nt':
            self.process.write(value)
        else:
            os.write(self.fd, value.encode())

    def expect(self, text):
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            if text in self.output[self.cursor:]:
                self.cursor = self.output.index(text, self.cursor) + len(text)
                return
            if os.name == 'nt':
                import select
                if not select.select([self.process.fileobj], [], [], .1)[0]:
                    continue
                try:
                    self.output += self.process.read(8192)
                except EOFError:
                    break
                time.sleep(0.03)
            else:
                import select
                if select.select([self.fd], [], [], 0.1)[0]:
                    try:
                        chunk = os.read(self.fd, 8192)
                    except OSError:
                        break
                    if not chunk:
                        break
                    self.output += chunk.decode(errors='replace')
        raise AssertionError(f'Missing {text!r} in terminal:\n{self.output}')

    def close(self):
        if os.name == 'nt':
            self.process.close(force=True)
        else:
            try:
                done, _ = os.waitpid(self.pid, os.WNOHANG)
                if not done:
                    os.kill(self.pid, 9)
                    os.waitpid(self.pid, 0)
            finally:
                os.close(self.fd)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--program', default='powershell' if os.name == 'nt' else '/bin/bash')
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    windows = os.name == 'nt'
    with tempfile.TemporaryDirectory(prefix='midden-terminal-') as temporary:
        root = Path(temporary).resolve()
        home = root / 'home'; home.mkdir()
        project = root / 'project'; project.mkdir()
        tools = root / 'tools'; tools.mkdir()
        bundle = root / 'bundle'; bundle.mkdir()
        binary = 'midden.exe' if windows else 'midden'
        (bundle / binary).write_bytes(b'not an executable; installer must not invoke it')
        for relative in ('skills/session-recovery/SKILL.md', 'skills/evidence-selection/SKILL.md',
                         'skills/content-seed/SKILL.md', 'agents/midden-recovery.md'):
            target = bundle / relative; target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(repo / relative, target)
        fixture = tools / ('terminal-host.cmd' if windows else 'terminal-host')
        fixture.write_text('@echo off\necho fixture-host 1.0\n' if windows else '#!/bin/sh\necho fixture-host 1.0\n')
        fixture.chmod(0o755)
        rows = (repo / 'installer/manifest.tsv').read_text().splitlines()
        for i, row in enumerate(rows):
            if row.startswith('host\t'):
                fields = row.split('\t')
                fields[2] = 'terminal-host' if fields[1] in ('copilot-cli', 'claude-code') else 'midden-not-installed'
                rows[i] = '\t'.join(fields)
        manifest = root / 'manifest.tsv'; manifest.write_text('\n'.join(rows) + '\n')
        env = {k:v for k,v in os.environ.items() if not k.startswith('MIDDEN_')}
        env.update(PATH=str(tools) + os.pathsep + env['PATH'], TERM='xterm-256color', COLUMNS='110')
        dest = home / 'bin'; state = home / 'state'
        if windows:
            command = [args.program, '-NoProfile', '-File', str(repo / 'install.ps1'), '-Manifest', str(manifest),
                       '-BundleDir', str(bundle), '-HomeDir', str(home), '-InstallDir', str(dest),
                       '-StateDir', str(state), '-NoPath']
        else:
            command = [args.program, str(repo / 'install.sh'), '--manifest', str(manifest),
                       '--bundle-dir', str(bundle), '--home-dir', str(home), '--install-dir', str(dest),
                       '--state-dir', str(state), '--no-path']
        terminal = Terminal(command, env)
        try:
            terminal.expect('How would you like to start?')
            terminal.expect('Up/Down: move')
            terminal.send('\x1b[B'); time.sleep(.2)
            terminal.send('\x1b[A'); time.sleep(.2)
            terminal.send('\r')
            terminal.expect('Which CLI should use Midden?')
            terminal.expect('Up/Down: move')
            terminal.send('\x1b[B'); time.sleep(.2)
            terminal.send('\r')  # first individual host, not all
            terminal.expect('What would you like to create?')
            terminal.expect('Up/Down: move'); terminal.send('\r')
            terminal.expect('Install with these settings?')
            terminal.expect('Up/Down: move'); terminal.send('\r')
            terminal.expect('installed successfully')
            terminal.expect('Manage this install')
            assert (dest / binary).exists()
            roots = [home / '.copilot/skills', home / '.claude/skills']
            assert sum((p / 'midden-session-recovery/SKILL.md').exists() for p in roots) == 1
            assert '[1/3]' in terminal.output and '[2/3]' in terminal.output and '[3/3]' in terminal.output
        finally:
            terminal.close()
        lifecycle = ['-NonInteractive','-Uninstall'] if windows else ['--yes','--uninstall']
        subprocess.run(command + lifecycle, env=env, check=True, capture_output=True)

        # Exercise Custom, edit folders, choose project scope and cancel at the
        # keyboard confirmation. No install/data/project-skill writes may occur.
        terminal = Terminal(command, env)
        custom_dest = home / 'custom-bin'; custom_state = home / 'custom-state'
        try:
            terminal.expect('Up/Down: move'); terminal.send('\x1b[B'); time.sleep(.2); terminal.send('\r')
            terminal.expect('Binary installation folder'); terminal.send(str(custom_dest)+'\r')
            terminal.expect('Up/Down: move'); terminal.send('\x1b[B'); time.sleep(.2); terminal.send('\r')
            terminal.expect('Project folder'); terminal.send(str(project)+'\r')
            terminal.expect('Recovery data folder'); terminal.send(str(custom_state)+'\r')
            terminal.expect('Up/Down: move'); terminal.send('\r')  # No PATH default
            terminal.expect('Which CLI should use Midden?'); terminal.expect('Up/Down: move'); terminal.send('\r')
            terminal.expect('What would you like to create?'); terminal.expect('Up/Down: move'); terminal.send('\r')
            terminal.expect('Install with these settings?'); terminal.expect('Up/Down: move')
            terminal.send('\x1b[B'); time.sleep(.2); terminal.send('\r')
            terminal.expect('Cancelled.')
            assert not custom_dest.exists() and not custom_state.exists()
            assert not (project / '.claude').exists() and not (project / '.github').exists()
        finally:
            terminal.close()
        terminal = Terminal(command, env)
        try:
            terminal.expect('Up/Down: move'); terminal.send('q')
            terminal.expect('cancelled')
            assert not (dest / binary).exists()
        finally:
            terminal.close()
        print('PASS real terminal: arrows, Quick, individual host, Custom project/paths, cancel, Q, stages')


if __name__ == '__main__':
    main()
