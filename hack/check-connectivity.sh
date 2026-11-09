#!/bin/sh
# Read project .env safely; explicit process environment overrides file settings.
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec python3 - "$script_dir/../.env" <<'PY'
import json
import math
import os
import socket
import time
import sys
from pathlib import Path
import urllib.parse
import urllib.request

keys = {
    'REMOTE_EMBEDDING': 'JAR_REMOTE_EMBEDDING',
    'REMOTE_CHAT': 'JAR_REMOTE_CHAT',
    'REMOTE_DB': 'JAR_REMOTE_DB',
    'REMOTE_DB_TOKEN': 'JAR_REMOTE_DB_TOKEN',
}
for key, process_key in keys.items():
    if key in os.environ:
        os.environ.setdefault(process_key, os.environ[key])
env_file = Path(sys.argv[1])
if env_file.is_file():
    for line in env_file.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        if line.startswith('export '):
            line = line[7:].lstrip()
        key, separator, value = line.partition('=')
        key = key.strip()
        if separator and key in keys:
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in ('"', "'"):
                value = value[1:-1]
            else:
                value = value.split(' #', 1)[0].rstrip()
            os.environ.setdefault(keys[key], value)

failed = False
configured = 0

def request(base, path, payload=None, timeout=20):
    base = base if '://' in base else 'http://' + base
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError('expected HTTP(S) host:port without credentials')
    url = base.rstrip('/')
    if url.endswith('/v1'):
        url = url[:-3]
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url + path, data=data, headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        return json.load(response)

def cosine(a, b):
    denominator = math.sqrt(sum(x*x for x in a) * sum(x*x for x in b))
    if not denominator:
        raise ValueError('zero embedding vector')
    return sum(x*y for x,y in zip(a,b)) / denominator

for service, key in [('Granite', 'JAR_REMOTE_EMBEDDING'), ('Ministral', 'JAR_REMOTE_CHAT'), ('DuckDB', 'JAR_REMOTE_DB')]:
    endpoint = os.environ.get(key, '').strip()
    if not endpoint:
        print(f'{service}: skipped (not configured)')
        continue
    configured += 1
    try:
        if service == 'Granite':
            result = request(endpoint, '/v1/embeddings', {'model': 'granite-embedding', 'input': ['olá mundo', 'hello world', 'the stock market fell today']})
            entries = sorted(result['data'], key=lambda item: item['index'])
            vectors = [entry['embedding'] for entry in entries]
            if len(vectors) != 3 or not vectors[0] or len({len(v) for v in vectors}) != 1 or not all(math.isfinite(x) for v in vectors for x in v):
                raise ValueError('invalid embedding response')
            print(f'Granite: OK ({len(vectors[0])} dimensions)')
            print(f'  olá mundo ~ hello world: {cosine(vectors[0], vectors[1]):.3f}')
            print(f'  olá mundo ~ stock market: {cosine(vectors[0], vectors[2]):.3f}')
        elif service == 'Ministral':
            result = request(endpoint, '/v1/models')
            if not isinstance(result.get('data'), list) or not result['data']:
                raise ValueError('no models advertised')
            model = result['data'][0]['id']
            started = time.perf_counter()
            result = request(endpoint, '/v1/chat/completions', {
                'model': model,
                'messages': [{'role': 'user', 'content': 'Reply with only the word OK.'}],
                'max_tokens': 32,
                'temperature': 0,
                'stream': False,
            }, timeout=60)
            elapsed = time.perf_counter() - started
            content = result['choices'][0]['message']['content']
            if not isinstance(content, str) or not content.strip():
                raise ValueError('generation returned no text')
            print('Ministral: OK (model API and text generation)')
            print('  Generated: ' + json.dumps(content.strip(), ensure_ascii=False))
            print(f'  Generation request time: {elapsed:.3f} s')
            tokens = result.get('usage', {}).get('completion_tokens')
            if isinstance(tokens, int) and not isinstance(tokens, bool) and tokens > 0 and elapsed > 0:
                print(f'  Completion tokens: {tokens}; request throughput: {tokens / elapsed:.2f} tokens/s')
        else:
            address = endpoint.removeprefix('quack:')
            parsed = urllib.parse.urlsplit('//'+address)
            if not parsed.hostname or not parsed.port or parsed.path:
                raise ValueError('expected host:port or quack:host:port')
            with socket.create_connection((parsed.hostname, parsed.port), timeout=5):
                pass
            print('DuckDB: OK (TCP reachable; run make status to validate authentication)')
    except Exception as error:
        # Avoid response bodies, URLs, and credentials in diagnostic output.
        print(f'{service}: FAILED ({type(error).__name__})')
        failed = True
if not configured:
    print('No remote services configured.')
raise SystemExit(1 if failed else 0)
PY
