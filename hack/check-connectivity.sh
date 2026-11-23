#!/bin/sh
# Read project .env without evaluating shell code. Process environment takes precedence.
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec python3 - "$script_dir/../.env" "$@" <<'PY'
import json
import math
import os
import socket
import sys
import time
from pathlib import Path
import urllib.parse
import urllib.request

keys = {'EMBEDDING_URL', 'CHAT_URL', 'DB_URL', 'CHAT_API_MODEL', 'EMBEDDING_API_MODEL'}
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
            os.environ.setdefault(key, value)

services = sys.argv[2:] or ['embedding', 'chat', 'db']
if any(service not in ('embedding', 'chat', 'db') for service in services):
    print('Usage: check-connectivity.sh [embedding|chat|db ...]', file=sys.stderr)
    raise SystemExit(2)

def request(base, path, payload=None, timeout=20):
    base = base if '://' in base else 'http://' + base
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError('expected HTTP(S) endpoint without credentials, query, or fragment')
    url = base.rstrip('/')
    if url.endswith('/v1'):
        url = url[:-3]
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url + path, data=data, headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        return json.load(response)

def model_name(endpoint, setting):
    name = os.environ.get(setting, '').strip()
    if name:
        return name
    models = request(endpoint, '/v1/models').get('data')
    if not isinstance(models, list) or len(models) != 1 or not models[0].get('id'):
        raise ValueError('set an API model ID when the endpoint does not advertise exactly one model')
    return models[0]['id']

failed = False
configured = 0
for service in services:
    key = {'embedding': 'EMBEDDING_URL', 'chat': 'CHAT_URL', 'db': 'DB_URL'}[service]
    endpoint = os.environ.get(key, '').strip()
    label = {'embedding': 'Embeddings', 'chat': 'Chat', 'db': 'DuckDB'}[service]
    if not endpoint:
        print(f'{label}: skipped ({key} not configured)')
        continue
    configured += 1
    try:
        if service == 'embedding':
            model = model_name(endpoint, 'EMBEDDING_API_MODEL')
            result = request(endpoint, '/v1/embeddings', {'model': model, 'input': ['hello world']})
            entries = result['data']
            if len(entries) != 1 or entries[0]['index'] != 0:
                raise ValueError('invalid embedding response')
            vector = entries[0]['embedding']
            if not vector or not all(math.isfinite(x) for x in vector):
                raise ValueError('invalid embedding vector')
            print(f'Embeddings: OK ({len(vector)} dimensions)')
        elif service == 'chat':
            model = model_name(endpoint, 'CHAT_API_MODEL')
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
            print(f'Chat: OK (model API and text generation; {elapsed:.3f} s)')
            tokens = result.get('usage', {}).get('completion_tokens')
            if isinstance(tokens, int) and not isinstance(tokens, bool) and tokens > 0 and elapsed > 0:
                print(f'  Completion tokens: {tokens}; request throughput: {tokens / elapsed:.2f} tokens/s')
        else:
            if not endpoint.startswith('quack:'):
                raise ValueError('DB_URL must use quack:host:port')
            parsed = urllib.parse.urlsplit('//' + endpoint[len('quack:'):])
            if not parsed.hostname or not parsed.port or parsed.path or parsed.username or parsed.password or parsed.query or parsed.fragment:
                raise ValueError('expected quack:host:port without credentials')
            with socket.create_connection((parsed.hostname, parsed.port), timeout=5):
                pass
            print('DuckDB: OK (TCP reachable; run make status to validate authentication)')
    except Exception as error:
        # Avoid response bodies, URLs, model text, and credentials in diagnostics.
        print(f'{label}: FAILED ({type(error).__name__})')
        failed = True
if not configured:
    print('No services configured.')
raise SystemExit(1 if failed else 0)
PY
