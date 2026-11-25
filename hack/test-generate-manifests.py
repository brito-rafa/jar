#!/usr/bin/env python3
"""Focused regression tests; no cluster, network, models, or user env file."""

import base64
import hashlib
import json
import os
import re
import shutil
from pathlib import Path
import runpy
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

GEN = runpy.run_path(str(Path(__file__).with_name("generate-manifests.py")), run_name="jar_generator_test")
ERROR = GEN["GenerationError"]
ROOT = Path(__file__).resolve().parents[1]
KINDS = (b"Namespace", b"ConfigMap", b"Secret", b"Job", b"Deployment", b"Deployment", b"Service", b"Service")
BUILT = b"\n---\n".join(b"kind: " + kind for kind in KINDS) + b"\n"


def flow(text, key):
    """Parse one rendered JSON scalar or flow value, such as `volumes: [...]`."""
    return json.loads(re.search(r"^\s*" + re.escape(key) + r": (.+)$", text, re.MULTILINE).group(1))


def document(text, name):
    """Return the YAML document in a multi-document file whose metadata name matches."""
    matches = [part for part in re.split(r"(?m)^---$", text) if re.search(r"(?m)^  name: " + re.escape(name) + r"$", part)]
    if len(matches) != 1:
        raise AssertionError(f"expected one document named {name}, found {len(matches)}")
    return matches[0]


class GeneratorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="jar-manifest-tests-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env_file = self.root / "fixture.env"
        self.env_file.write_text(
            "DB_URL=quack:database.example:9494\nDB_TOKEN='synthetic#=literal$(do-not-execute)'\n"
            "CHAT_URL=https://chat.example\nEMBEDDING_URL=https://embedding.example\n"
            'DOCS="/not-a-client-directory/docs one;/not-a-client-directory/docs one/;/node/archive"\n'
        )
        self.output = self.root / "output"
        self.templates = self.root / "templates"
        shutil.copytree(GEN["TEMPLATE_DIR"], self.templates)

    def settings(self, overrides=None):
        return GEN["read_settings"](self.env_file, overrides or {})

    def build(self):
        return mock.patch.object(GEN["subprocess"], "run", return_value=subprocess.CompletedProcess([], 0, BUILT, b""))

    def generate(self, overrides=None):
        return GEN["generate"](self.env_file, self.output, "fixture-kustomize build", overrides or {}, template_dir=self.templates)

    def test_env_is_data_and_explicit_environment_wins(self):
        self.env_file.write_text(self.env_file.read_text() + "\nexport CHAT_API_MODEL='model # literal' # comment\nUNKNOWN_SECRET=do-not-copy\n")
        settings = self.settings({"CHAT_URL": "https://override.example"})
        self.assertEqual(settings["DB_TOKEN"], "synthetic#=literal$(do-not-execute)")
        self.assertEqual(settings["CHAT_API_MODEL"], "model # literal")
        self.assertEqual(settings["CHAT_URL"], "https://override.example")
        self.assertNotIn("UNKNOWN_SECRET", settings)
        with self.assertRaisesRegex(ERROR, "CHAT_URL"):
            GEN["validate"](self.settings({"CHAT_URL": ""}))

    def test_public_origin_validation_matches_application_policy(self):
        for value in ("http://jarchat:0", "https://jarchat:65536", "http://*.example.test",
                      "https://jar chat.example.test", "https://jar\tchat.example.test"):
            with self.subTest(value=value), self.assertRaisesRegex(ERROR, "CHAT_PUBLIC_URL"):
                GEN["validate"](self.settings({"CHAT_PUBLIC_URL": value}))
        for value in ("https://chat.example.test", "https://chat.example.test/", "http://jarchat:65535"):
            GEN["validate"](self.settings({"CHAT_PUBLIC_URL": value}))
        self.assertEqual(self.settings()["CHAT_PORT"], "18080")

    def test_cli_filters_exported_defaults_when_make_supplies_override_names(self):
        main = GEN["main"]
        fake_environment = {"CHAT_PORT": "3333", "CHAT_URL": "https://explicit.example", "UNKNOWN": "ignore"}
        for names, expected in ((None, None), ("", {}), ("CHAT_URL UNKNOWN", {"CHAT_URL": "https://explicit.example"})):
            with self.subTest(names=names):
                argv = ["generate-manifests.py"]
                if names is not None:
                    argv.extend(["--env-overrides", names])
                with mock.patch.dict(os.environ, fake_environment, clear=True), \
                        mock.patch.object(GEN["sys"], "argv", argv), \
                        mock.patch.dict(main.__globals__, {"generate": mock.Mock(return_value=self.output)}) as globals_dict, \
                        mock.patch("builtins.print"):
                    self.assertEqual(main(), 0)
                    self.assertEqual(globals_dict["generate"].call_args.kwargs["environ"], expected)

    def test_docs_normalize_deduplicate_without_client_directory_checks(self):
        settings = self.settings()
        docs = GEN["validate"](settings)
        self.assertEqual(docs, ["/not-a-client-directory/docs one", "/node/archive"])
        self.assertEqual(settings["DOCS"], ";".join(docs))
        bundle = GEN["resources"](settings, docs)
        volumes, mounts = flow(bundle["job.yaml"], "volumes"), flow(bundle["job.yaml"], "volumeMounts")
        self.assertEqual([volume["hostPath"]["path"] for volume in volumes], docs)
        self.assertEqual([mount["mountPath"] for mount in mounts], docs)
        self.assertTrue(all(mount["readOnly"] for mount in mounts))
        self.assertNotIn("DB_TOKEN", bundle["configmap.yaml"])
        self.assertNotIn(settings["DB_TOKEN"], bundle["job.yaml"])

    def test_yaml_templates_fill_literals_namespace_and_base64_secret(self):
        self.env_file.write_text(self.env_file.read_text() + "\nCHAT_API_MODEL='literal \"quote\" # $(shell never-run)'\nNAMESPACE=research\n")
        sources = {p.name: p.read_bytes() for p in self.templates.iterdir()}
        with self.build():
            self.generate()
        configmap = (self.output / "configmap.yaml").read_text()
        secret = (self.output / "secret.yaml").read_text()
        self.assertIn('namespace: "research"', configmap)
        self.assertIn('namespace: "research"', secret)
        value = re.search(r"^  CHAT_API_MODEL: (.+)$", configmap, re.MULTILINE).group(1)
        self.assertEqual(json.loads(value), 'literal "quote" # $(shell never-run)')
        encoded = json.loads(re.search(r"^  DB_TOKEN: (.+)$", secret, re.MULTILINE).group(1))
        self.assertEqual(base64.b64decode(encoded).decode(), self.settings()["DB_TOKEN"])
        self.assertNotIn("DB_TOKEN", configmap)
        self.assertNotIn(self.settings()["DB_TOKEN"], secret)
        self.assertEqual(sources, {p.name: p.read_bytes() for p in self.templates.iterdir()})
        kustomization = (self.output / "kustomization.yaml").read_text()
        self.assertIn('namespace: "research"', kustomization)
        self.assertIn("- configmap.yaml", kustomization)
        self.assertIn("- secret.yaml", kustomization)
        self.assertNotIn("Generator", kustomization)
        self.assertNotIn("runtime.env", kustomization)
        self.assertNotIn("secret.env", kustomization)
        self.assertIn("name: jar-runtime", (self.output / "deployment.yaml").read_text())
        self.assertIn("name: jar-secret", (self.output / "deployment.yaml").read_text())

    def test_committed_templates_hold_only_placeholders(self):
        templates = GEN["TEMPLATE_DIR"]
        self.assertEqual(templates, ROOT / "config/manifests/kustomize")
        text = {p.name: p.read_text() for p in templates.iterdir()}
        self.assertEqual(sorted(text), sorted(GEN["FILES"]))
        for name, content in text.items():
            with self.subTest(name=name):
                self.assertNotRegex(content, r"/(Users|home)/")
                self.assertNotRegex(content, r"[0-9a-f]{64}")  # no rendered checksums
        for name, placeholders in (
            ("secret.yaml", ("${DB_TOKEN}",)),
            ("configmap.yaml", ("${DOCS}", "${DB_URL}", "${CHAT_URL}", "${EMBEDDING_URL}", "${CHAT_PUBLIC_URL}", "${MCP_PORT}")),
            ("job.yaml", ("${DOCS_VOLUMES}", "${DOCS_VOLUME_MOUNTS}", "${K8S_INDEX_IMAGE}", "${INDEX_NODE_NAME}")),
            ("deployment.yaml", ("${K8S_CHAT_IMAGE}", "${K8S_MCP_IMAGE}", "${CHAT_PORT}", "${MCP_PORT}")),
            ("kustomization.yaml", ("${NAMESPACE}",)),
            ("namespace.yaml", ("${NAMESPACE}",)),
        ):
            for placeholder in placeholders:
                self.assertIn(placeholder, text[name])

    def test_generated_bundle_fills_every_placeholder(self):
        overrides = {"INDEX_NODE_NAME": "worker-1", "IMAGE_PULL_SECRET": "registry-pull", "PLATFORM": "linux/arm64",
                     "CHAT_PORT": "9000", "K8S_INDEX_IMAGE": "registry.example/jar-index:test"}
        with self.build():
            self.generate(overrides)
        for path in self.output.iterdir():
            self.assertNotIn("${", path.read_text(), path.name)
        job, deployment = (self.output / "job.yaml").read_text(), (self.output / "deployment.yaml").read_text()
        self.assertEqual(flow(job, "nodeName"), "worker-1")
        self.assertEqual(flow(job, "image"), "registry.example/jar-index:test")
        self.assertEqual(flow(deployment, "containerPort"), 9000)
        for text in (job, deployment):
            self.assertEqual(flow(text, "imagePullSecrets"), [{"name": "registry-pull"}])
            self.assertEqual(flow(text, "kubernetes.io/arch"), "arm64")
        self.assertIn('"$(DOCS)"', job)  # Kubernetes variable references stay literal.
        with self.build():
            self.generate()
        job = (self.output / "job.yaml").read_text()
        self.assertEqual(flow(job, "nodeName"), "")
        self.assertEqual(flow(job, "imagePullSecrets"), [])

    def test_mcp_deployment_uses_its_own_image_and_both_services_join_the_tailnet(self):
        overrides = {"MCP_PORT": "9100", "CHAT_PORT": "9000", "K8S_CHAT_IMAGE": "registry.example/jar-chat:test",
                     "K8S_MCP_IMAGE": "registry.example/jar-mcp:test",
                     "IMAGE_PULL_SECRET": "registry-pull", "PLATFORM": "linux/arm64"}
        with self.build():
            self.generate(overrides)
        deployment = (self.output / "deployment.yaml").read_text()
        chat, mcp = document(deployment, "jar-chat"), document(deployment, "jar-mcp")
        self.assertEqual(flow(chat, "containerPort"), 9000)
        self.assertEqual(flow(mcp, "containerPort"), 9100)
        self.assertEqual(flow(chat, "image"), "registry.example/jar-chat:test")
        self.assertEqual(flow(mcp, "image"), "registry.example/jar-mcp:test")
        self.assertNotIn("command:", mcp)  # The jar-mcp image's entrypoint runs the server.
        self.assertIn('- "$(MCP_PORT)"', mcp)  # Kubernetes expands it from the ConfigMap.
        self.assertIn("name: jar-runtime", mcp)
        self.assertIn("name: jar-secret", mcp)
        self.assertEqual(flow(mcp, "imagePullSecrets"), [{"name": "registry-pull"}])
        self.assertEqual(flow(mcp, "kubernetes.io/arch"), "arm64")
        self.assertIn("runAsNonRoot: true", mcp)
        self.assertIn("allowPrivilegeEscalation: false", mcp)
        self.assertNotIn("allow-any-host", mcp)
        self.assertEqual(json.loads(re.search(r"^  MCP_PORT: (.+)$", (self.output / "configmap.yaml").read_text(),
                                              re.MULTILINE).group(1)), "9100")
        services = (self.output / "service.yaml").read_text()
        chat_service = document(services, "jar-chat")
        self.assertIn('tailscale.com/expose: "true"', chat_service)
        self.assertIn("tailscale.com/hostname: jarchat", chat_service)
        service = document(services, "jar-mcp")
        self.assertIn('tailscale.com/expose: "true"', service)
        self.assertIn("tailscale.com/hostname: jarmcp", service)
        self.assertIn("type: ClusterIP", service)
        self.assertIn("app.kubernetes.io/name: jar-mcp", service)
        self.assertRegex(service, r"(?m)^      port: 80$")
        self.assertRegex(service, r"(?m)^      targetPort: mcp$")

    def test_images_default_to_registry_and_tag_per_component(self):
        with self.build():
            self.generate({"REGISTRY": "registry.example/team/", "IMAGE_TAG": "v2"})
        deployment = (self.output / "deployment.yaml").read_text()
        self.assertEqual(flow(document(deployment, "jar-chat"), "image"), "registry.example/team/jar-chat:v2")
        self.assertEqual(flow(document(deployment, "jar-mcp"), "image"), "registry.example/team/jar-mcp:v2")
        self.assertEqual(flow((self.output / "job.yaml").read_text(), "image"), "registry.example/team/jar-index:v2")

    def test_make_builds_and_pushes_all_three_images(self):
        makefile = (ROOT / "Makefile").read_text()
        self.assertRegex(makefile, r"(?m)^build: build-index build-chat build-mcp\b")
        self.assertRegex(makefile, r"(?m)^push: push-index push-chat push-mcp\b")
        self.assertIn('-f Dockerfile --target jar-chat -t "$(CHAT_IMAGE)"', makefile)
        self.assertIn('-f Dockerfile --target jar-mcp -t "$(MCP_IMAGE)"', makefile)
        self.assertIn('/jar-mcp:$(IMAGE_TAG)"', makefile)
        dockerfile = (ROOT / "Dockerfile").read_text()
        targets = re.findall(r"(?m)^FROM \S+ AS (\S+)$", dockerfile)
        self.assertIn("jar-mcp", targets)
        self.assertEqual(targets[-1], "jar-chat")  # Default target when --target is omitted.

    def test_bundle_without_mcp_resources_is_rejected(self):
        legacy = b"\n---\n".join(b"kind: " + kind for kind in (b"Namespace", b"ConfigMap", b"Secret", b"Job", b"Deployment", b"Service"))
        with mock.patch.object(GEN["subprocess"], "run", return_value=subprocess.CompletedProcess([], 0, legacy, b"")):
            with self.assertRaisesRegex(ERROR, "unexpected bundle"):
                self.generate()
        self.assertFalse(self.output.exists())

    def test_output_never_overwrites_committed_templates(self):
        sources = {p.name: p.read_bytes() for p in self.templates.iterdir()}
        for output in (self.templates, self.templates / "nested"):
            with self.subTest(output=output.name), self.build():
                with self.assertRaisesRegex(ERROR, "template directory"):
                    GEN["generate"](self.env_file, output, "fixture-kustomize build", {}, template_dir=self.templates)
            self.assertEqual(sources, {p.name: p.read_bytes() for p in self.templates.iterdir()})

    def test_make_deploy_always_regenerates_the_ignored_bundle(self):
        makefile = (ROOT / "Makefile").read_text()
        ignored = (ROOT / ".gitignore").read_text().splitlines()
        self.assertRegex(makefile, r"(?m)^MANIFEST_DIR \?= config/manifests/generated$")
        self.assertRegex(makefile, r"(?m)^deploy: generate-manifests\b")
        self.assertRegex(makefile, r'(?m)^\t\$\(KUBECTL\) apply -k "\$\(MANIFEST_DIR\)"$')
        self.assertIn("/config/manifests/generated/", ignored)
        self.assertNotIn("/config/manifests/kustomize/", ignored)
        self.assertEqual(GEN["DEFAULT_OUTPUT"], Path("config/manifests/generated"))

    def test_checksums_follow_rendered_template_content_for_both_pods(self):
        settings = self.settings()
        docs = GEN["validate"](settings)

        def bundle():
            rendered = GEN["resources"](settings, docs, template_dir=self.templates)
            sums = {name: hashlib.sha256(rendered[name].encode()).hexdigest()
                    for name in ("configmap.yaml", "secret.yaml")}
            for pod, count in (("job.yaml", 1), ("deployment.yaml", 2)):  # jar-chat and jar-mcp
                self.assertEqual(rendered[pod].count('checksum/configmap: "' + sums["configmap.yaml"] + '"'), count)
                self.assertEqual(rendered[pod].count('checksum/secret: "' + sums["secret.yaml"] + '"'), count)
            return sums

        original = bundle()
        template = self.templates / "configmap.yaml"
        template.write_text(template.read_text() + "# User-edited template comment\n")
        changed = bundle()
        self.assertNotEqual(original["configmap.yaml"], changed["configmap.yaml"])
        self.assertEqual(original["secret.yaml"], changed["secret.yaml"])
        settings["DB_TOKEN"] = "changed-synthetic-secret"
        token_changed = bundle()
        self.assertEqual(changed["configmap.yaml"], token_changed["configmap.yaml"])
        self.assertNotEqual(changed["secret.yaml"], token_changed["secret.yaml"])

    def test_missing_or_unknown_template_placeholders_preserve_output(self):
        with self.build():
            self.generate()
        before = {p.name: p.read_bytes() for p in self.output.iterdir()}
        template = self.templates / "configmap.yaml"
        source = template.read_text()
        for invalid, message in (("  FORBIDDEN: ${DB_TOKEN}\n", "DB_TOKEN"),
                                 ("  UNKNOWN: ${UNKNOWN_SETTING}\n", "UNKNOWN_SETTING"),
                                 ("  INVALID: ${\n", "configmap.yaml")):
            template.write_text(source + invalid)
            with self.assertRaises(ERROR) as caught:
                self.generate()
            self.assertIn(message, str(caught.exception))
            self.assertNotIn(self.settings()["DB_TOKEN"], str(caught.exception))
            self.assertEqual(before, {p.name: p.read_bytes() for p in self.output.iterdir()})
        template.write_text(source)
        (self.templates / "secret.yaml").unlink()
        with self.assertRaisesRegex(ERROR, "secret.yaml"):
            self.generate()
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.output.iterdir()})
        settings = self.settings()
        del settings["DOCS"]
        with self.assertRaisesRegex(ERROR, "DOCS"):
            GEN["render_templates"](settings, self.templates)

    def test_success_removes_only_known_legacy_files(self):
        self.output.mkdir()
        for name in GEN["LEGACY_FILES"]:
            (self.output / name).write_text("obsolete synthetic setting")
        sentinel = self.output / "unrelated.env"
        sentinel.write_text("retain unrelated file")
        with self.build():
            self.generate()
        self.assertEqual(sentinel.read_text(), "retain unrelated file")
        for name in GEN["LEGACY_FILES"]:
            self.assertFalse((self.output / name).exists())
        for name in GEN["FILES"]:
            self.assertEqual(stat.S_IMODE((self.output / name).stat().st_mode), 0o600)

    def test_legacy_cleanup_failure_rolls_back_new_and_legacy_files(self):
        with self.build():
            self.generate()
        for name in GEN["LEGACY_FILES"]:
            (self.output / name).write_text("preserve synthetic legacy input")
            (self.output / name).chmod(0o600)
        before = {p.name: (p.read_bytes(), stat.S_IMODE(p.stat().st_mode)) for p in self.output.iterdir()}
        unlink = Path.unlink

        def fail_rendered_cleanup(path, *args, **kwargs):
            if path == self.output / "rendered.yaml":
                raise OSError("synthetic legacy cleanup failure")
            return unlink(path, *args, **kwargs)

        with self.build(), mock.patch.object(Path, "unlink", autospec=True, side_effect=fail_rendered_cleanup):
            with self.assertRaisesRegex(ERROR, "previous bundle was preserved"):
                self.generate({"NAMESPACE": "changed"})
        self.assertEqual(before, {p.name: (p.read_bytes(), stat.S_IMODE(p.stat().st_mode)) for p in self.output.iterdir()})

    def test_invalid_configuration_does_not_create_output(self):
        for key, value in (("DOCS", "relative/path"), ("DOCS", "/one;;/two"), ("DOCS", "/one;"),
                           ("CHAT_PORT", "65536"), ("MCP_PORT", "0"), ("FORCE_INDEX", "maybe"), ("PLATFORM", "windows/amd64"),
                           ("NAMESPACE", "not a namespace"), ("DB_TOKEN", "bad\nvalue")):
            with self.subTest(key=key):
                with self.assertRaises(ERROR) as caught:
                    self.generate({key: value})
                self.assertIn(key, str(caught.exception))
                self.assertFalse(self.output.exists())
        self.output = self.root / "not-created-parent" / "output"
        with self.assertRaisesRegex(ERROR, "Required settings are missing"):
            GEN["generate"](self.root / "missing.env", self.output, "fixture-build", {})
        self.assertFalse(self.output.parent.exists())

    def test_failed_build_preserves_previous_bundle_and_hides_stderr(self):
        with self.build():
            self.generate()
        sentinel = self.output / "unrelated.txt"
        sentinel.write_text("preserve this file")
        for name in GEN["LEGACY_FILES"]:
            (self.output / name).write_text("legacy synthetic content")
        before = {p.name: p.read_bytes() for p in self.output.iterdir()}
        secret = "second-synthetic-secret"
        failed = subprocess.CompletedProcess([], 1, secret.encode(), secret.encode())
        with mock.patch.object(GEN["subprocess"], "run", return_value=failed):
            with self.assertRaises(ERROR) as caught:
                self.generate({"DB_TOKEN": secret})
        self.assertNotIn(secret, str(caught.exception))
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.output.iterdir()})
        with self.build():
            self.generate({"DB_TOKEN": secret})
        self.assertEqual(sentinel.read_text(), "preserve this file")
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o700)
        for name in GEN["FILES"]:
            self.assertEqual(stat.S_IMODE((self.output / name).stat().st_mode), 0o600)

    def test_commit_failure_rolls_back_owned_files(self):
        with self.build():
            self.generate()
        before = {p.name: p.read_bytes() for p in self.output.iterdir()}
        replace = os.replace
        calls = 0

        def fail_once(source, target):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise OSError("synthetic write failure")
            return replace(source, target)

        with self.build(), mock.patch.object(GEN["os"], "replace", side_effect=fail_once):
            with self.assertRaisesRegex(ERROR, "previous bundle was preserved"):
                self.generate({"CHAT_PORT": "18080"})
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.output.iterdir()})


if __name__ == "__main__":
    unittest.main()
