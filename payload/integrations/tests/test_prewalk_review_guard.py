import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / 'prewalk' / 'review_guard.py'
spec = importlib.util.spec_from_file_location('review_guard', SCRIPT)
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class ReviewGuardTests(unittest.TestCase):
    def event(self, tool='Bash', **args):
        return dict(hook_event_name='PreToolUse', tool_name=tool, tool_input=args, cwd='/project', agent_type='engineering_reviewer')

    def test_actual_cli_event_dispatch(self):
        event = self.event('collaborationspawn_agent', agent_type='engineering_reviewer', fork_turns='all')
        event.pop('agent_type')
        self.assertIsNotNone(guard.guard(event))
        event = self.event(command='cat /user/.codex/sessions/a.jsonl')
        self.assertIsNotNone(guard.guard(event))
        event.pop('agent_type')
        self.assertIsNone(guard.guard(event), 'primary must not be restricted by reviewer guard')

    def test_malformed_events_deny_without_crashing(self):
        for event in ([], None, {'hook_event_name': 'PreToolUse'},
                      {'hook_event_name': 'PreToolUse', 'agent_type': 'primary', 'tool_name': None}):
            self.assertEqual(guard.guard(event)['hookSpecificOutput']['permissionDecision'], 'deny')

    def test_context_must_be_explicitly_fresh(self):
        for fork in ({}, {'fork_turns': 'all'}, {'fork_turns': '2'}, {'fork_context': True}):
            self.assertIsNotNone(guard.guard(self.event('spawn_agent', agent_type='engineering_reviewer', **fork), 'spawn'))
        for fork in ({'fork_turns': 'none'}, {'fork_context': False}):
            self.assertIsNone(guard.guard(self.event('spawn_agent', agent_type='engineering_reviewer', **fork), 'spawn'))
        self.assertIsNone(guard.guard(self.event('spawn_agent', agent_type='prewalk_executor'), 'spawn'))
        # A display/task name is not a native role identifier.
        self.assertIsNone(guard.guard(self.event('spawn_agent', task_name='engineering_reviewer', fork_turns='all'), 'spawn'))
        self.assertIsNone(guard.guard(self.event('spawn_agent', task_name='engineering_reviewer', agent_type='prewalk_executor', fork_turns='all'), 'spawn'))

    def test_blocks_history_and_tools(self):
        for cmd in ('cat /user/.codex/sessions/a.jsonl', 'rg foo /user/.codex/archived_sessions',
                    'cat $CODEX_HOME/history.jsonl', 'cat /private/custom/state_5.sqlite',
                    'rtk read /private/custom/sessions/a', 'rg secret /private', 'codex agents',
                    'python3 -c "open(\'/private/custom/sessions/a\').read()"'):
            self.assertIsNotNone(guard.guard(self.event(command=cmd), codex_home='/private/custom'), cmd)
        for tool in ('mcp__codex_app__read_thread', 'read_thread', 'mcp__x__search_conversations'):
            self.assertIsNotNone(guard.guard(self.event(tool, threadId='abc')))

    def test_allows_code_diff_rules_and_normal_project_sessions(self):
        for cmd in ('git diff BASE -- src', 'cat AGENTS.md', 'rg TODO src', 'cat src/sessions.py',
                    'cat src/sessions/handler.py', 'python3 -B -m unittest'):
            self.assertIsNone(guard.guard(self.event(command=cmd)), cmd)

    def test_custom_home_relative_paths_and_symlink(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / 'custom home'
            (home / 'sessions').mkdir(parents=True)
            (root / 'shortcut').symlink_to(home / 'sessions', target_is_directory=True)
            event = self.event(command='cat shortcut/conversation.jsonl')
            event['cwd'] = str(root)
            self.assertIsNotNone(guard.guard(event, codex_home=home))
            event['tool_input'] = {'path': str(home / 'sessions' / 'a.jsonl')}
            self.assertIsNotNone(guard.guard(event, codex_home=home))
            event['tool_input'] = {'command': 'cat ./custom\\ home/history.jsonl'}
            self.assertIsNotNone(guard.guard(event, codex_home=home))

    def test_resolution_failure_returns_deny(self):
        # Python versions differ on symlink-loop resolution; exercise the
        # RuntimeError path used by supported older versions deterministically.
        event = self.event(command='cat loop')
        stdin = io.TextIOWrapper(io.BytesIO(json.dumps(event).encode()))
        stdout = io.StringIO()
        with patch.object(guard.sys, 'stdin', stdin), patch.object(guard.sys, 'stdout', stdout), \
                patch.object(guard.sys, 'argv', ['review_guard.py']), \
                patch.object(Path, 'resolve', side_effect=RuntimeError('Symlink loop')):
            guard.main()
        self.assertEqual(json.loads(stdout.getvalue())['hookSpecificOutput']['permissionDecision'], 'deny')


if __name__ == '__main__':
    unittest.main()
