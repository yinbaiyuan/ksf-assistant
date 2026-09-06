import argparse
import concurrent.futures
import hashlib
import json
import os
import pathlib
import re
import subprocess
import tempfile


def sha(data):
    return hashlib.sha256(data).hexdigest()


def classify_skill_call(tail, path_index, groups):
    segment = re.split(r'[\n`"\'|;&]', tail, maxsplit=1)[0]
    tokens = segment.split()
    if 'lark-cli' in tokens:
        tokens = tokens[:tokens.index('lark-cli')]
    invocation = ' '.join(tokens[:3]) or '(bare CLI reference)'
    def classified(kind, reason, **fields):
        return {'invocation': invocation, 'classification': kind, 'reason': reason, **fields}
    for length in [3, 2]:
        candidate = ' '.join(tokens[:length])
        if candidate in path_index:
            descriptor = path_index[candidate]
            if len(tokens) > length and tokens[length] in ['--help', '-h', '--help=true']:
                return classified('local-diagnostic', 'Explicit leaf help; exact command is present in the pinned descriptor inventory.', invocation=candidate + ' --help', diagnosticPath=candidate)
            return {'invocation': candidate, 'canonical': descriptor['path'], 'classification': descriptor['status'], 'alias': candidate != descriptor['path']}
    for length in [2, 1, 0]:
        candidate = ' '.join(tokens[:length])
        if candidate in groups and len(tokens) > length and tokens[length] in ['--help', '-h', '--help=true']:
            return classified('local-diagnostic', 'Explicit group help; exact Usage path is verified against the fixed binary.', invocation=(candidate + ' --help').strip(), diagnosticPath=candidate)
    head = ' '.join(tokens[:3])
    if re.search(r'[<>…]|\.{3}', head):
        return classified('non-executable-example/prose', 'Lexical placeholder or ellipsis in the command path; not a concrete invocation.', evidence='placeholder-or-ellipsis')
    if any('/' in token for token in tokens[:3] if not token.startswith('--')) and (not tokens or tokens[0] not in ['api']):
        return classified('non-executable-example/prose', 'Slash-separated alternatives or prose notation in the command path.', evidence='slash-alternatives')
    if tokens and (tokens[0] in ['auth', 'schema', 'skills', 'config', 'profile', 'whoami', 'completion', 'update', 'doctor', 'api', 'event'] or tokens[0].startswith('--')):
        return classified('restricted', 'Toolchain/raw API/event surface; only separately reviewed local commands, exact API adapters or fixed subscriptions are executable.')
    path = []
    for token in tokens[:3]:
        if re.fullmatch(r'\+?[a-z][a-z0-9_.-]*', token):
            path.append(token)
            if len(path) == 2 and token.startswith('+'):
                break
        else:
            break
    candidate = ' '.join(path)
    if len(path) == 2 and path[1].startswith('+') or len(path) == 3 and path[0] in groups and path[1] not in ['not', 'timed', 'was']:
        return classified('upstream-missing', 'Concrete command path absent from both registered descriptors and exact fixed-binary Usage paths.', invocation=candidate, missingPath=candidate)
    if re.search(r'[^\x00-\x7f]', head):
        return classified('non-executable-example/prose', 'Natural-language text or typographic punctuation occurs where a command component is required.', evidence='natural-language-command-position')
    if re.match(r'(not found|timed out|exited with|stdout was|returned a|themselves\b)', segment):
        return classified('non-executable-example/prose', 'Literal diagnostic message or English narrative, not CLI argument syntax.', evidence='literal-diagnostic-or-narrative')
    if candidate in groups and len(tokens) == len(path) or not tokens:
        return classified('non-executable-example/prose', 'Bare CLI/group reference without an executable leaf or help option.', evidence='bare-group-reference')
    raise ValueError('unclassified Skills occurrence requires explicit lexical review: ' + repr(tail))


def extract_skill_calls(text, path_index, groups):
    logical = re.sub(r'\\\r?\n\s*', lambda match: ' ' * len(match.group()), text)
    calls = []
    for occurrence in re.finditer(r'\blark-cli(?:\.exe)?(?=\s)', logical):
        call = classify_skill_call(logical[occurrence.end():].lstrip(' \t'), path_index, groups)
        start = text.rfind('\n', 0, occurrence.start()) + 1
        end = text.find('\n', occurrence.start())
        if end < 0:
            end = len(text)
        call['rawLine'] = text[start:end]
        call['rawInvocation'] = text[occurrence.start():end]
        call['line'] = text[:occurrence.start()].count('\n') + 1
        before = text[max(0, occurrence.start()-180):occurrence.start()]
        call['exampleContext'] = 'negative-or-warning' if re.search(r'(?i)(do not|don.t|wrong|incorrect|invalid|avoid|不要|禁止|错误|反例)', before) else 'example-or-reference'
        calls.append(call)
    return calls


def help_usage_paths(output):
    return sorted(set(match.group(1).strip() for match in re.finditer(r'^  lark-cli(?: (.*?))?(?: \[|$)', output, re.M) if match.group(1)))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', required=True)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--check', action='store_true')
    options = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[1]
    source = pathlib.Path(options.source).resolve()
    binary = pathlib.Path(options.binary).resolve()
    runtime = json.loads((root / 'runtime/lark-cli-runtime.json').read_bytes())
    skills_bytes = (root / 'runtime/lark-skills.json').read_bytes()
    skills = json.loads(skills_bytes)
    policy_source = root / 'Core/internal/feishucli/catalog.json'
    policy_catalog = {item['id']: item for item in json.loads(policy_source.read_bytes())['catalog']}
    digest = sha(binary.read_bytes())
    artifact_hashes = {platform: item['executableSha256'] for platform, item in sorted(runtime['artifacts'].items())}
    if digest not in [item['executableSha256'] for item in runtime['artifacts'].values()]:
        raise ValueError('binary does not match pinned runtime')
    archive = root / 'dist/cache/lark-cli' / skills['source']['archive']
    if sha(archive.read_bytes()) != skills['source']['sha256']:
        raise ValueError('source archive does not match pinned Skills source')
    import tarfile
    with tarfile.open(archive) as bundle:
        for member in bundle.getmembers():
            if member.isfile() and (member.name.startswith(skills['source']['root'] + '/shortcuts/') or member.name.startswith(skills['source']['root'] + '/cmd/')):
                relative = member.name.split('/', 1)[1]
                if sha(bundle.extractfile(member).read()) != sha((source / relative).read_bytes()):
                    raise ValueError('source differs from archive: ' + relative)
    ast_env = {**os.environ, 'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOTOOLCHAIN': 'local'}
    rows = json.loads(subprocess.check_output(['go', 'run', str(root / 'scripts/execution-source-ast.go'), str(source)], env=ast_env))
    sheet_defs = json.loads((source / 'shortcuts/sheets/data/flag-defs.json').read_bytes())
    for row in rows:
        if row['Service'] == 'sheets':
            row['Flags'] = [{**{key.capitalize(): value for key, value in flag.items()}, 'Required': flag.get('required') == 'required'} for flag in sheet_defs[row['Command']]['flags'] if flag['kind'] != 'system']
            for flag in row['Flags']:
                if flag['Name'] == 'spreadsheet-token':
                    flag['Aliases'] = ['token']
    with tempfile.TemporaryDirectory(prefix='ksfas-execution-schema-') as home:
        env = {'HOME': home, 'USERPROFILE': home, 'LARKSUITE_CLI_CONFIG_DIR': home + '/config', 'LARKSUITE_CLI_REMOTE_META': 'off', 'LARKSUITE_CLI_NO_UPDATE_NOTIFIER': '1', 'LARKSUITE_CLI_NO_SKILLS_NOTIFIER': '1', 'NO_COLOR': '1'}
        def offline(args):
            return subprocess.check_output([str(binary), *args], env=env, cwd=home, stderr=subprocess.PIPE, timeout=20).decode()
        schemas = json.loads(offline(['schema', '--json']))
        if len(schemas) != 250 or len(rows) != 526:
            raise ValueError('pinned source/binary inventory changed; review counts explicitly')
        commands = [schema['name'] for schema in schemas] + [row['Service'] + ' ' + row['Command'] for row in rows]
        groups = {''}
        for command in commands:
            parts = command.split()
            groups.update(' '.join(parts[:length]) for length in range(1, len(parts)))
        if 'slides' in groups:
            groups.add('slide')
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            helps = dict(zip(commands, pool.map(lambda path: offline(path.split() + ['--help']), commands)))
            group_helps = dict(zip(sorted(groups), pool.map(lambda path: offline(path.split() + ['--help']), sorted(groups))))
        for path, output in group_helps.items():
            canonical = 'slides' if path == 'slide' else path
            if path and canonical not in help_usage_paths(output):
                raise ValueError('group help did not resolve exact Usage path: ' + path)
    descriptors = []
    schema_digest = sha(json.dumps(schemas, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode())
    flag_pattern = re.compile(r'^\s+(?:-([a-zA-Z]),\s+)?--([a-z0-9][a-z0-9-]*)(?:\s+(stringArray|strings|intSlice|string|int64|int|float64|float|uint|duration))?\s{2,}(.+)$', re.M)
    for schema in schemas:
        metadata = schema['_meta']
        item = {'path': schema['name'], 'kind': 'typed', 'description': schema['description'], 'officialRisk': metadata.get('risk', ''), 'identities': metadata.get('access_tokens', []), 'source': 'fixed-binary:schema ' + schema['name'], 'sourceSha256': schema_digest, 'inputSchema': schema['inputSchema'], 'flags': []}
        descriptors.append(item)
    for row in rows:
        flags = []
        for flag in row.get('Flags') or []:
            if not flag or not flag.get('Name'):
                raise ValueError('unresolved source flag: ' + row['Command'])
            flags.append({'name': flag['Name'], 'type': flag.get('Type') or 'string', 'description': flag.get('Desc') or '', 'default': flag.get('Default') or '', 'required': bool(flag.get('Required')), 'hidden': bool(flag.get('Hidden')), 'aliases': flag.get('Aliases') or [], 'input': flag.get('Input') or [], 'enum': flag.get('Enum') or [], 'unresolvedMetadata': [key for key in ['Type', 'Required', 'Aliases', 'Enum', 'Input', 'Default'] if key in flag and flag[key] is None]})
        identities = row.get('AuthTypes')
        if 'AuthTypes' not in row:
            runner = (source / 'shortcuts/common/runner.go').read_text()
            if not re.search(r'if len\(shortcut.AuthTypes\) == 0\s*\{\s*shortcut.AuthTypes = \[\]string\{"user"\}', runner):
                raise ValueError('official identity default changed')
            identities = ['user']
        item = {'path': row['Service'] + ' ' + row['Command'], 'kind': 'shortcut', 'description': row.get('Description') or '', 'officialRisk': row.get('Risk') or '', 'identities': identities or [], 'identitySource': 'declared AuthTypes' if 'AuthTypes' in row else 'shortcuts/common/runner.go:932 explicit default', 'source': row['source'], 'sourceSha256': sha((source / row['source']).read_bytes()), 'flags': flags}
        item['scopes'] = {key: row.get(key) or [] for key in ['Scopes', 'UserScopes', 'BotScopes', 'ConditionalScopes', 'ConditionalUserScopes', 'ConditionalBotScopes']}
        if 'Flags' in row and row['Flags'] is None:
            item['unresolvedMetadata'] = ['Flags']
        if row['Service'] == 'slides':
            item['aliases'] = [item['path'].replace('slides ', 'slide ', 1)]
        descriptors.append(item)
    overlay = json.loads((root / 'Core/internal/usercommand/execution-overlay.json').read_bytes())
    for item in descriptors:
        path = item['path']
        official_flags = {flag['name']: flag for flag in item['flags']}
        help_flags = {}
        for alias, name, kind, description in flag_pattern.findall(helps[path]):
            help_flags[name] = {'name': name, 'type': {'strings': 'string_slice', 'stringArray': 'string_array', 'intSlice': 'int_array'}.get(kind, kind or 'bool'), 'description': description}
        for name, flag in help_flags.items():
            if name not in official_flags:
                official_flags[name] = flag
            elif path in ['base +record-get', 'base +record-list', 'base +record-search'] and name == 'field-id':
                official_flags[name]['type'] = flag['type']
        if item['kind'] == 'typed':
            for name in ['data', 'params']:
                if name in official_flags:
                    official_flags[name]['input'] = ['file', 'stdin']
                    official_flags[name]['role'] = 'json'
            params = item['inputSchema'].get('properties', {}).get('params', {})
            for key, value in params.get('properties', {}).items():
                name = value.get('flag', '').removeprefix('--')
                if name in official_flags:
                    official_flags[name]['required'] = key in params.get('required', [])
                    official_flags[name]['enum'] = [str(value) for value in value.get('enum', [])]
            if 'file' in official_flags:
                official_flags['file']['role'] = 'multipart'
            if 'data' in item['inputSchema'].get('required', []) and 'data' in official_flags:
                official_flags['data']['required'] = True
        for name, flag in official_flags.items():
            for field in list(flag.get('unresolvedMetadata', [])):
                rendered = help_flags.get(name, {}).get('description', '')
                if field == 'Enum':
                    choices = re.findall(r'\(([a-zA-Z0-9_.-]+(?:\|[a-zA-Z0-9_.-]+)+)\)', rendered)
                    if choices:
                        flag['enum'] = choices[-1].split('|')
                        flag['unresolvedMetadata'].remove(field)
                if field == 'Default':
                    default = re.search(r'\(default (.+)\)$', rendered)
                    if default:
                        flag['default'] = default.group(1).strip('"')
                        flag['unresolvedMetadata'].remove(field)
            if item['kind'] == 'shortcut' and re.search(r'\bJSON\b', flag.get('description', '')) and (name == 'json' or flag.get('input')):
                flag['role'] = 'json'
            if name in ['help', 'as', 'dry-run', 'yes', 'jq']:
                flag['role'] = 'control'
            elif name in ['format', 'json'] and name not in {entry['name'] for entry in item['flags']}:
                flag['role'] = 'output-format'
            if name == 'format' and flag.get('role') == 'output-format' and not flag.get('enum'):
                rendered = help_flags.get(name, {}).get('description', '')
                choices = re.match(r'output format:\s*(.*)', rendered, re.I)
                if not choices or '|' not in choices.group(1):
                    raise ValueError('cannot verify output format enum: ' + path)
                flag['enum'] = []
                for choice in choices.group(1).split('|'):
                    value = re.match(r'\s*([a-z][a-z0-9_-]*)', choice)
                    if not value:
                        raise ValueError('cannot parse output format enum: ' + path)
                    flag['enum'].append(value.group(1))
            if name in ['output', 'output-dir', 'output-path', 'out', 'out-dir']:
                flag['role'] = 'artifact'
        item['flags'] = sorted(official_flags.values(), key=lambda flag: flag['name'])
        item['risk'] = {'read': 'read', 'write': 'write', 'high-risk-write': 'high-impact-write'}.get(item['officialRisk'], 'unknown')
        item['status'] = 'supported' if item['risk'] != 'unknown' else 'restricted'
        item['capabilityIds'] = [path.replace(' +', '.shortcut.').replace('-', '.').replace(' ', '.') if item['kind'] == 'shortcut' else path.replace(' ', '.')]
        prior = policy_catalog.get(item['capabilityIds'][0])
        if prior:
            item['risk'] = prior['risk']
            item['riskSource'] = 'frozen product capability catalog: ' + prior['id']
        else:
            item['riskSource'] = 'official risk; high-risk-write means high-impact until explicit destructive review'
        if not item['identities']:
            item['status'] = 'restricted'
        item['limitations'] = []
        if item.get('unresolvedMetadata'):
            item['status'] = 'restricted'
            item['limitations'].append('Unresolved source flag constructor; fixed help is not a substitute for the missing input contract.')
        for flag in item['flags']:
            if flag.get('unresolvedMetadata'):
                flag['role'] = 'restricted'
                item['limitations'].append('--' + flag['name'] + ' restricted: source metadata unresolved: ' + ','.join(flag['unresolvedMetadata']))
                if flag.get('required'):
                    item['status'] = 'restricted'
        for rule in overlay['rules']:
            if path in rule.get('paths', []) or any(path.startswith(prefix) for prefix in rule.get('prefixes', [])):
                for key in ['status', 'risk', 'artifacts', 'artifactCompanions', 'capabilityIds']:
                    if key in rule:
                        item[key] = rule[key]
                if 'risk' in rule:
                    item['riskSource'] = 'reviewed source overlay: ' + item['source'] if path == 'im +messages-resources-download' else item.get('riskSource', 'reviewed source overlay')
                item['limitations'].extend(rule.get('limitations', []))
                for flag in item['flags']:
                    if flag['name'] in rule.get('flagRoles', {}):
                        flag['role'] = rule['flagRoles'][flag['name']]
        if item['risk'] == 'unknown':
            item['limitations'].append('Official risk unresolved: not executable.')
        item['helpSha256'] = sha(helps[path].encode())
    result = {'schemaVersion': 1, 'version': runtime['version'], 'provenance': {'binarySha256': digest, 'sourceArchiveSha256': skills['source']['sha256'], 'skillsSha256': sha(skills_bytes), 'overlaySha256': sha((root / 'Core/internal/usercommand/execution-overlay.json').read_bytes()), 'shortcutSource': 'AST of AllShortcuts registration; actual fixed-binary help cross-check; source fallback typed catalog is not used'}, 'counts': {'typed': len(schemas), 'shortcuts': len(rows)}, 'restrictions': ['No arbitrary API routes, shell/SQL/remote generation, unbounded pagination, independent event streams, implicit media URL fetches, unfrozen dependencies, caller retry overrides or self approval.', 'Input payloads are bounded to 2 MiB. Dynamic resource branches require a separate reviewed dependency freezer.'], 'skills': [], 'descriptors': sorted(descriptors, key=lambda item: item['path'])}
    result['localDiagnostics'] = [{'path': path, 'helpSha256': sha(output.encode())} for path, output in sorted(group_helps.items())]
    path_index = {path: item for item in descriptors for path in [item['path'], *item.get('aliases', [])]}
    for skill in skills['skills']:
        classified = []
        for name, expected in sorted(skill['files'].items()):
            content = (source / 'skills' / skill['name'] / name).read_bytes()
            if sha(content) != expected:
                raise ValueError('skill source changed: ' + skill['name'] + '/' + name)
            text = content.decode('utf-8', errors='replace') if name.endswith(('.md', '.json', '.yaml', '.txt', '.py', '.js')) else ''
            calls = extract_skill_calls(text, path_index, groups)
            matches = sorted({call['canonical'] for call in calls if 'canonical' in call})
            classified.append({'file': name, 'sha256': expected, 'commands': matches, 'calls': calls, 'classification': 'command-reference' if calls else 'no-cli-invocation'})
        result['skills'].append({'name': skill['name'], 'files': classified})
    missing = sorted({call['missingPath'] for skill in result['skills'] for entry in skill['files'] for call in entry['calls'] if 'missingPath' in call})
    result['upstreamMissingChecks'] = []
    with tempfile.TemporaryDirectory(prefix='ksfas-execution-missing-') as home:
        env = {'HOME': home, 'USERPROFILE': home, 'LARKSUITE_CLI_CONFIG_DIR': home + '/config', 'LARKSUITE_CLI_REMOTE_META': 'off', 'LARKSUITE_CLI_NO_UPDATE_NOTIFIER': '1', 'LARKSUITE_CLI_NO_SKILLS_NOTIFIER': '1', 'NO_COLOR': '1'}
        for path in missing:
            output = subprocess.check_output([str(binary), *path.split(), '--help'], env=env, cwd=home, stderr=subprocess.PIPE, timeout=20).decode()
            observed = help_usage_paths(output)
            if path in observed or path in path_index:
                raise ValueError('Skills concrete command exists but is absent from classification: ' + path)
            if not observed:
                raise ValueError('cannot verify missing command against exact Usage paths: ' + path)
            result['upstreamMissingChecks'].append({'path': path, 'helpSha256': sha(output.encode()), 'observedUsagePaths': observed})
    destination = root / 'Core/internal/usercommand/execution-manifest.json'
    del result['provenance']['binarySha256']
    result['provenance']['binaryArtifacts'] = artifact_hashes
    result['provenance']['schemaSha256'] = schema_digest
    result['provenance']['generatorSha256'] = sha(pathlib.Path(__file__).read_bytes())
    result['provenance']['astExtractorSha256'] = sha((root / 'scripts/execution-source-ast.go').read_bytes())
    result['provenance']['productCatalogSha256'] = sha(policy_source.read_bytes())
    result['provenance']['reviewAdapterSha256'] = sha((root / 'Core/internal/usercommand/catalog.go').read_bytes())
    engine = json.loads(subprocess.check_output(['go', 'run', str(root / 'scripts/execution-source-ast.go'), '--engine', str(root / 'Core/internal/usercommand')], env=ast_env))
    result['apiDescriptors'] = []
    for method, path, spec in engine['apiSpecifications']:
        flags = []
        for name in spec.get('values', '').split():
            flag = {'name': name, 'type': 'string', 'required': name in spec.get('required', '').split()}
            if name in ['params', 'data']:
                flag.update({'role': 'json', 'input': ['file', 'stdin']})
            if name in spec.get('files', '').split():
                flag['role'] = 'multipart'
            flags.append(flag)
        result['apiDescriptors'].append({'path': 'api ' + method + ' ' + path, 'kind': 'api', 'status': 'supported', 'risk': spec['risk'], 'capabilityIds': spec['capabilities'].split(), 'description': spec['action'], 'flags': flags, 'source': 'Core/internal/usercommand/catalog.go', 'sourceSha256': result['provenance']['reviewAdapterSha256']})
    result['legacyPolicyAliases'] = [{'path': item['path'], 'capabilityIds': item['capabilities'].split()} for item in engine['legacySpecifications']]
    closure = {}
    for folder in ['Core/internal/usercommand', 'Core/internal/capabilitypolicy']:
        for path in sorted((root / folder).glob('*.go')):
            if not path.name.endswith('_test.go'):
                closure[str(path.relative_to(root))] = sha(path.read_bytes())
    result['provenance']['engineFiles'] = closure
    result['provenance']['engineSha256'] = sha(json.dumps(closure, sort_keys=True, separators=(',', ':')).encode())
    result['parameterRestrictions'] = {'files': 'only flags carrying file/media-file/multipart roles accept local regular files; Input file/stdin comes from official metadata', 'resources': 'implicit local/remote XML/Markdown uploads and unreviewed nested file references are refused per flag role', 'output': 'staged files publish atomically per file to frozen original-cwd targets only after success; conflicts refused, partial publication returns error plus canonical partial list without destructive rollback; Close removes staging', 'controls': 'profile/config/auth-token overrides, jq, dry-run and caller user --yes refused; business json/format/token flags retain command-specific types and output formats follow official enums'}
    result['parameterRestrictions']['pagination'] = 'page-all requires an explicit page-limit 1..100 and command-level support; no silent default injection; partial results retain upstream has_more markers and are not asserted complete'
    result['skillCallCounts'] = {status: sum(call['classification'] == status for skill in result['skills'] for entry in skill['files'] for call in entry['calls']) for status in ['supported', 'restricted', 'local-diagnostic', 'non-executable-example/prose', 'upstream-missing']}
    encoded = (json.dumps(result, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode()
    if options.check:
        if destination.read_bytes() != encoded:
            raise ValueError('execution manifest differs; regenerate and review')
    else:
        destination.write_bytes(encoded)
    print(json.dumps({'typed': len(schemas), 'shortcuts': len(rows), 'supported': sum(item['status'] == 'supported' for item in descriptors), 'restricted': sum(item['status'] == 'restricted' for item in descriptors), 'manifestDigest': sha(encoded)}))


if __name__ == '__main__':
    main()
