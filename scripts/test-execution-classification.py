import importlib.util
import pathlib
import unittest


spec = importlib.util.spec_from_file_location('execution_generator', pathlib.Path(__file__).with_name('generate-execution-manifest.py'))
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)


class ClassificationTests(unittest.TestCase):
    def setUp(self):
        self.groups = {'', 'approval', 'apps', 'base', 'calendar', 'mail', 'mail user_mailbox.messages', 'slides', 'slide', 'drive', 'drive files'}
        descriptor = {'path': 'slides +create', 'status': 'supported'}
        self.commands = {'slides +create': descriptor, 'slide +create': descriptor, 'apps +db-sync-enable': {'path': 'apps +db-sync-enable', 'status': 'restricted'}}

    def test_actual_lexical_examples(self):
        cases = {
            'approval --help"': 'local-diagnostic',
            'apps --help lark-cli apps +<cmd> --help"': 'local-diagnostic',
            'mail user_mailbox.messages -h': 'local-diagnostic',
            '设置协作者，并引导其在妙搭后台的权限设置中操作。': 'non-executable-example/prose',
            '自身鉴权用，如 auth status': 'non-executable-example/prose',
            'apps +<cmd> --help': 'non-executable-example/prose',
            'calendar <resource> <method>': 'non-executable-example/prose',
            'base +field-create/+field-update --json': 'non-executable-example/prose',
            'not found", cmd=cmd)': 'non-executable-example/prose',
            'drive files upload_prepare --data \'{}\'': 'upstream-missing',
            'base +app-list 。需要列出': 'upstream-missing',
            'slides +create --title <title>': 'supported',
            'slide +create --title fixture': 'supported',
            'apps +db-sync-enable --help': 'local-diagnostic',
            'apps +db-sync-enable --task-id fixture': 'restricted',
        }
        for text, expected in cases.items():
            with self.subTest(text=text):
                call = generator.classify_skill_call(text, self.commands, self.groups)
                self.assertEqual(call['classification'], expected)
                self.assertNotIn('may be', call.get('reason', ''))
        self.assertTrue(generator.classify_skill_call('slide +create', self.commands, self.groups)['alias'])

    def test_every_occurrence_keeps_original_line(self):
        text = 'header\ncommand: "lark-cli apps --help lark-cli apps +<cmd> --help"\n\nlark-cli \\\n  slides +create --title fixture\nlark-cli calendar <resource> <method>\n'
        calls = generator.extract_skill_calls(text, self.commands, self.groups)
        self.assertEqual(len(calls), 4)
        self.assertEqual([call['line'] for call in calls], [2, 2, 4, 6])
        self.assertEqual([call['classification'] for call in calls], ['local-diagnostic', 'non-executable-example/prose', 'supported', 'non-executable-example/prose'])
        for call in calls:
            self.assertEqual(call['rawLine'], text.splitlines()[call['line'] - 1])
            self.assertIn(call['rawInvocation'], call['rawLine'])

    def test_fallback_help_is_not_proof_of_command_existence(self):
        paths = generator.help_usage_paths('Usage:\n  lark-cli drive files [flags]\n  lark-cli drive files [command]\n')
        self.assertEqual(paths, ['drive files'])
        self.assertNotIn('drive files upload_prepare', paths)

    def test_unclassified_english_requires_review(self):
        with self.assertRaises(ValueError):
            generator.classify_skill_call('unexplained English sentence', self.commands, self.groups)


if __name__ == '__main__':
    unittest.main()
