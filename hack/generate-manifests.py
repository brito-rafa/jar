#!/usr/bin/env python3
"""Fill the committed Kustomize templates and build a private application bundle.

config/manifests/kustomize/ holds the public templates with ${NAME} placeholders.
The filled bundle is written to a separate, Git-ignored directory (default
config/manifests/generated/), never back into the templates. Only allowlisted
settings are read. The env file is parsed as data, never as shell code.
Kustomize runs locally; this script never applies resources to a cluster.
"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import posixpath
import re
import shlex
import stat
import subprocess
import sys
import tempfile
from string import Template
from urllib.parse import urlsplit


DEFAULTS = {
    "DB_DISABLE_SSL": "false",
    "CHAT_API_MODEL": "",
    "EMBEDDING_API_MODEL": "",
    "EMBEDDING_MODEL": "Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M",
    "DIMENSIONS": "4096",
    "FORCE_INDEX": "false",
    "DOCUMENT_WORKERS": "20",
    "EMBEDDING_WORKERS": "1",
    "EMBEDDING_BATCH_SIZE": "8",
    "EMBEDDING_BUFFER_BATCHES": "16",
    "INSERT_BATCH_SIZE": "128",
    "CHAT_PORT": "18080",
    "CHAT_ALLOW_ANY_HOST": "false",
    "CHAT_PUBLIC_URL": "",
    "MCP_PORT": "18090",
    "NAMESPACE": "jar",
    "REGISTRY": "ghcr.io/brito-rafa",
    "IMAGE_TAG": "latest",
    "K8S_INDEX_IMAGE": "",
    "K8S_CHAT_IMAGE": "",
    "K8S_MCP_IMAGE": "",
    "INDEX_NODE_NAME": "",
    "IMAGE_PULL_SECRET": "",
    "PLATFORM": "linux/amd64",
}
REQUIRED = ("DB_URL", "DB_TOKEN", "CHAT_URL", "EMBEDDING_URL", "DOCS")
RUNTIME_KEYS = (
    "DB_URL", "DB_DISABLE_SSL", "CHAT_URL", "EMBEDDING_URL", "CHAT_API_MODEL",
    "EMBEDDING_API_MODEL", "DOCS", "EMBEDDING_MODEL", "DIMENSIONS",
    "FORCE_INDEX", "DOCUMENT_WORKERS", "EMBEDDING_WORKERS",
    "EMBEDDING_BATCH_SIZE", "EMBEDDING_BUFFER_BATCHES", "INSERT_BATCH_SIZE",
    "CHAT_PORT", "CHAT_ALLOW_ANY_HOST", "CHAT_PUBLIC_URL", "MCP_PORT",
)
KNOWN_KEYS = set(DEFAULTS) | set(REQUIRED)
FILES = (
    "kustomization.yaml", "namespace.yaml", "job.yaml",
    "deployment.yaml", "service.yaml", "configmap.yaml",
    "secret.yaml",
)
LEGACY_FILES = ("runtime.env", "secret.env", "rendered.yaml")
# deployment.yaml and service.yaml each hold jar-chat and jar-mcp.
EXPECTED_KINDS = sorted([b"Namespace", b"ConfigMap", b"Secret", b"Job",
                         b"Deployment", b"Deployment", b"Service", b"Service"])
TEMPLATE_DIR = Path(__file__).resolve().parents[1] / "config/manifests/kustomize"
DEFAULT_OUTPUT = Path("config/manifests/generated")


class ManifestTemplate(Template):
    """Only ${NAME} is a placeholder; Kubernetes $(VAR) references stay literal.

    A "${" that does not form a complete placeholder is rejected as invalid.
    """
    pattern = r"""
    \$(?:
      (?P<escaped>(?!))
      | \{(?P<braced>[A-Za-z_][A-Za-z0-9_]*)\}
      | (?P<named>(?!))
      | (?P<invalid>\{)
    )
    """


class GenerationError(Exception):
    """An actionable diagnostic that contains setting names, never their values."""


def env_value(raw, line_number):
    """Support whole-value quotes and comments, with no variable expansion."""
    value = raw.strip()
    if not value or value[0] not in "\"'":
        return re.split(r"\s+#", value, maxsplit=1)[0].rstrip()
    quote, result, i = value[0], [], 1
    while i < len(value):
        char = value[i]
        if char == quote:
            rest = value[i + 1:].strip()
            if rest and not rest.startswith("#"):
                break
            return "".join(result)
        if quote == '"' and char == "\\" and i + 1 < len(value):
            following = value[i + 1]
            if following in '\\"$`':
                result.append(following)
                i += 2
                continue
        result.append(char)
        i += 1
    raise GenerationError(f"Invalid quoted value in env file at line {line_number}")


def read_settings(env_file, environ):
    settings = dict(DEFAULTS)
    try:
        lines = env_file.read_text(encoding="utf-8-sig").splitlines()
    except FileNotFoundError:
        lines = []  # Exported settings also support CI without a local env file.
    except (OSError, UnicodeError):
        raise GenerationError("Could not read the env file") from None
    for number, line in enumerate(lines, 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export ") or line.startswith("export\t"):
            line = line[6:].lstrip()
        match = re.fullmatch(r"([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)", line)
        if not match:
            raise GenerationError(f"Invalid assignment in env file at line {number}")
        key, raw = match.groups()
        if key in KNOWN_KEYS:
            settings[key] = env_value(raw, number)
    for key in KNOWN_KEYS:
        if key in environ:
            settings[key] = environ[key]
    for key, value in settings.items():
        if any(char in value for char in ("\0", "\r", "\n")):
            raise GenerationError(f"{key} must contain a single line")
    missing = [key for key in REQUIRED if not settings.get(key, "").strip()]
    if missing:
        raise GenerationError("Required settings are missing: " + ", ".join(missing))
    return settings


def boolean(settings, key):
    value = settings[key].lower()
    if value not in ("true", "false"):
        raise GenerationError(f"{key} must be true or false")
    settings[key] = value


def integer(settings, key, minimum, maximum):
    try:
        number = int(settings[key])
    except ValueError:
        raise GenerationError(f"{key} must be an integer") from None
    if not minimum <= number <= maximum:
        raise GenerationError(f"{key} is outside its supported range")
    settings[key] = str(number)


def dns_name(value, key, maximum=253):
    label = r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?"
    if len(value) > maximum or not re.fullmatch(label + r"(?:\." + label + r")*", value):
        raise GenerationError(f"{key} must be a valid Kubernetes DNS name")
    if any(len(part) > 63 for part in value.split(".")):
        raise GenerationError(f"{key} must be a valid Kubernetes DNS name")


def validate(settings):
    docs, seen = [], set()
    for raw in settings["DOCS"].split(";"):
        path = raw.strip()
        if not path or not path.startswith("/"):
            raise GenerationError("DOCS must contain nonempty absolute directories separated by semicolons")
        # Linux host paths are normalized without checking the client filesystem.
        path = "/" + posixpath.normpath(path).lstrip("/")
        if path not in seen:
            seen.add(path)
            docs.append(path)
    settings["DOCS"] = ";".join(docs)
    if not settings["DB_URL"].startswith("quack:") or not settings["DB_URL"][6:].strip():
        raise GenerationError("DB_URL must use the quack: scheme")
    if len(settings["DB_TOKEN"]) < 4:
        raise GenerationError("DB_TOKEN must contain at least four characters")
    for key in ("CHAT_URL", "EMBEDDING_URL"):
        value = settings[key]
        try:
            url = urlsplit(value if "://" in value else "http://" + value)
            valid = (url.scheme in ("http", "https") and url.hostname and not url.username
                     and not url.password and not url.query and not url.fragment)
            _ = url.port
        except ValueError:
            valid = False
        if not valid:
            raise GenerationError(f"{key} must be an OpenAI-compatible HTTP(S) endpoint")
    for key in ("DB_DISABLE_SSL", "FORCE_INDEX", "CHAT_ALLOW_ANY_HOST"):
        boolean(settings, key)
    for key, minimum, maximum in (
        ("DIMENSIONS", 1, 2147483647), ("DOCUMENT_WORKERS", 1, 64),
        ("EMBEDDING_WORKERS", 1, 8), ("EMBEDDING_BATCH_SIZE", 1, 128),
        ("EMBEDDING_BUFFER_BATCHES", 1, 1024), ("INSERT_BATCH_SIZE", 1, 512),
        ("CHAT_PORT", 1, 65535), ("MCP_PORT", 1, 65535),
    ):
        integer(settings, key, minimum, maximum)
    if not settings["EMBEDDING_MODEL"].strip():
        raise GenerationError("EMBEDDING_MODEL must not be empty")
    namespace = settings["NAMESPACE"]
    dns_name(namespace, "NAMESPACE", 63)
    if "." in namespace:
        raise GenerationError("NAMESPACE must be a Kubernetes DNS label")
    for key in ("INDEX_NODE_NAME", "IMAGE_PULL_SECRET"):
        if settings[key]:
            dns_name(settings[key], key)
    if settings["PLATFORM"] not in ("linux/amd64", "linux/arm64"):
        raise GenerationError("PLATFORM must be linux/amd64 or linux/arm64")
    settings["REGISTRY"] = settings["REGISTRY"].rstrip("/")
    for key in ("REGISTRY", "IMAGE_TAG"):
        if not settings[key] or re.search(r"\s", settings[key]):
            raise GenerationError(f"{key} must be a nonempty image setting without whitespace")
    for key, component in (("K8S_INDEX_IMAGE", "jar-index"), ("K8S_CHAT_IMAGE", "jar-chat"),
                           ("K8S_MCP_IMAGE", "jar-mcp")):
        if not settings[key]:
            settings[key] = f'{settings["REGISTRY"]}/{component}:{settings["IMAGE_TAG"]}'
        if re.search(r"\s", settings[key]):
            raise GenerationError(f"{key} must be an image reference without whitespace")
    if not settings["CHAT_PUBLIC_URL"]:
        settings["CHAT_PUBLIC_URL"] = "http://jarchat"
    try:
        url = urlsplit(settings["CHAT_PUBLIC_URL"])
        valid = (url.scheme in ("http", "https") and url.hostname and not url.username
                 and not url.password and not url.query and not url.fragment and url.path in ("", "/")
                 and "*" not in url.netloc
                 and not any(char.isspace() for char in settings["CHAT_PUBLIC_URL"])
                 and (url.port is None or 1 <= url.port <= 65535))
    except ValueError:
        valid = False
    if not valid:
        raise GenerationError("CHAT_PUBLIC_URL must be an HTTP(S) origin")
    return docs


def scalar(value):
    return json.dumps(value, ensure_ascii=False)


def fill(template_dir, name, values):
    """Insert JSON scalars or JSON flow sequences, which are valid YAML, without shell expansion."""
    try:
        source = (template_dir / name).read_text(encoding="utf-8")
    except (OSError, UnicodeError):
        raise GenerationError("Could not read manifest template: " + name) from None
    try:
        return ManifestTemplate(source).substitute({key: scalar(value) for key, value in values.items()})
    except KeyError as error:
        raise GenerationError("Unknown placeholder in " + name + ": " + str(error.args[0])) from None
    except ValueError:
        raise GenerationError("Invalid placeholder in manifest template: " + name) from None


def render_templates(settings, template_dir=None):
    """Fill the ConfigMap and Secret templates; DB_TOKEN is accepted only by secret.yaml."""
    template_dir = TEMPLATE_DIR if template_dir is None else template_dir
    missing = [key for key in (*RUNTIME_KEYS, "NAMESPACE", "DB_TOKEN") if key not in settings]
    if missing:
        raise GenerationError("Template settings are missing: " + ", ".join(missing))
    maps = {
        "configmap.yaml": {key: settings[key] for key in (*RUNTIME_KEYS, "NAMESPACE")},
        "secret.yaml": {"NAMESPACE": settings["NAMESPACE"],
                        "DB_TOKEN": base64.b64encode(settings["DB_TOKEN"].encode("utf-8")).decode("ascii")},
    }
    return {name: fill(template_dir, name, values) for name, values in maps.items()}


def resources(settings, docs, template_dir=None):
    """Fill every committed template; each file accepts only its own placeholders."""
    template_dir = TEMPLATE_DIR if template_dir is None else template_dir
    rendered = render_templates(settings, template_dir)
    pods = {
        "CONFIGMAP_CHECKSUM": hashlib.sha256(rendered["configmap.yaml"].encode("utf-8")).hexdigest(),
        "SECRET_CHECKSUM": hashlib.sha256(rendered["secret.yaml"].encode("utf-8")).hexdigest(),
        "PLATFORM_ARCH": settings["PLATFORM"].split("/")[1],
        "IMAGE_PULL_SECRETS": [{"name": settings["IMAGE_PULL_SECRET"]}] if settings["IMAGE_PULL_SECRET"] else [],
    }
    volumes, mounts = [], []
    for number, path in enumerate(docs):
        name = f"docs-{number}"
        volumes.append({"name": name, "hostPath": {"path": path, "type": "Directory"}})
        mounts.append({"name": name, "mountPath": path, "readOnly": True})
    namespace = {"NAMESPACE": settings["NAMESPACE"]}
    maps = {
        "kustomization.yaml": namespace,
        "namespace.yaml": namespace,
        "job.yaml": {**pods, "K8S_INDEX_IMAGE": settings["K8S_INDEX_IMAGE"],
                     "INDEX_NODE_NAME": settings["INDEX_NODE_NAME"],
                     "DOCS_VOLUMES": volumes, "DOCS_VOLUME_MOUNTS": mounts},
        "deployment.yaml": {**pods, "K8S_CHAT_IMAGE": settings["K8S_CHAT_IMAGE"],
                            "K8S_MCP_IMAGE": settings["K8S_MCP_IMAGE"],
                            "CHAT_PORT": int(settings["CHAT_PORT"]),
                            "MCP_PORT": int(settings["MCP_PORT"])},
        "service.yaml": {},
    }
    for name, values in maps.items():
        rendered[name] = fill(template_dir, name, values)
    return {name: rendered[name] for name in FILES}


def separate_from_templates(output, template_dir):
    """Never write filled values over, or inside, the committed templates."""
    templates = template_dir.resolve()
    target = output.resolve()
    if target == templates or templates in target.parents:
        raise GenerationError("The output directory must not be the committed template directory "
                              "config/manifests/kustomize; use the Git-ignored config/manifests/generated")


def write_private(path, data):
    path.write_bytes(data if isinstance(data, bytes) else data.encode("utf-8"))
    path.chmod(0o600)


def install_bundle(stage, output):
    """Replace only owned files, rolling back if a filesystem operation fails."""
    if output.is_symlink() or (output.exists() and not output.is_dir()):
        raise GenerationError("The output directory must be a regular directory")
    old = {}
    for name in (*FILES, *LEGACY_FILES):
        target = output / name
        if target.is_symlink() or (target.exists() and not target.is_file()):
            raise GenerationError("A generated output path is not a regular file")
        old[name] = (target.read_bytes(), stat.S_IMODE(target.stat().st_mode)) if target.exists() else None
    existed = output.exists()
    old_mode = stat.S_IMODE(output.stat().st_mode) if existed else None
    changed = []
    try:
        output.mkdir(mode=0o700, exist_ok=True)
        output.chmod(0o700)
        for name in FILES:
            os.replace(stage / name, output / name)
            changed.append(name)
        # Remove only these obsolete generated files, after a validated build.
        for name in LEGACY_FILES:
            if old[name] is not None:
                (output / name).unlink()
                changed.append(name)
    except OSError:
        for name in reversed(changed):
            if old[name] is None:
                (output / name).unlink()
            else:
                content, mode = old[name]
                write_private(stage / name, content)
                (stage / name).chmod(mode)
                os.replace(stage / name, output / name)
        if existed:
            output.chmod(old_mode)
        else:
            output.rmdir()
        raise GenerationError("Could not replace generated files; the previous bundle was preserved") from None


def generate(env_file, output, kustomize, environ=None, template_dir=None):
    settings = read_settings(env_file, os.environ if environ is None else environ)
    docs = validate(settings)  # Do not create any output until configuration is valid.
    template_dir = TEMPLATE_DIR if template_dir is None else template_dir
    separate_from_templates(output, template_dir)
    try:
        command = shlex.split(kustomize)
    except ValueError:
        raise GenerationError("Invalid Kustomize command") from None
    if not command:
        raise GenerationError("A Kustomize build command is required")
    bundle = resources(settings, docs, template_dir)
    output = output.absolute()
    if output.is_symlink() or (output.exists() and not output.is_dir()):
        raise GenerationError("The output directory must be a regular directory")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".jar-manifests-", dir=output.parent) as temporary:
        stage = Path(temporary)
        for name, content in bundle.items():
            write_private(stage / name, content)
        try:
            result = subprocess.run(command + [str(stage)], capture_output=True, timeout=60, check=False)
        except (OSError, subprocess.TimeoutExpired):
            raise GenerationError("Kustomize build could not complete; previous output was preserved") from None
        # Never forward build diagnostics: they may include a Secret or its source.
        if result.returncode or not result.stdout.strip():
            raise GenerationError("Kustomize build failed; previous output was preserved")
        kinds = re.findall(rb"^kind: ([A-Za-z]+)\s*$", result.stdout, flags=re.MULTILINE)
        if sorted(kinds) != EXPECTED_KINDS:
            raise GenerationError("Kustomize build returned an unexpected bundle; previous output was preserved")
        install_bundle(stage, output)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, default=Path(".env"))
    parser.add_argument("--output-dir", type=Path, default=DEFAULT_OUTPUT,
                        help="Private, Git-ignored output; must differ from the template directory")
    parser.add_argument("--kustomize", default="kubectl kustomize", help="Local build command, e.g. 'kubectl kustomize' or 'kustomize build'")
    parser.add_argument("--env-overrides", default=None,
                        help="Internal Make integration: space-separated setting names explicitly overridden by the caller")
    args = parser.parse_args()
    try:
        environ = None if args.env_overrides is None else {
            key: os.environ[key] for key in args.env_overrides.split()
            if key in KNOWN_KEYS and key in os.environ
        }
        output = generate(args.env_file, args.output_dir, args.kustomize, environ=environ)
    except (GenerationError, OSError):
        error = sys.exc_info()[1]
        message = str(error) if isinstance(error, GenerationError) else "Could not write the generated bundle"
        print("ERROR: " + message, file=sys.stderr)
        return 2
    print(f"Generated and validated Kustomize bundle: {output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
