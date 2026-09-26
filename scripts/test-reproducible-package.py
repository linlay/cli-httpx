#!/usr/bin/env python3
import importlib.util
import json
import os
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('packaging', pathlib.Path(__file__).with_name('reproducible-package.py'))
packaging = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packaging)


class PackagingTests(unittest.TestCase):
    def test_different_paths_and_mtimes_have_identical_archives(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            for ext in ['tar.gz', 'zip']:
                outputs = []
                for i in [1, 2]:
                    stage = root / f'{ext}-{i}'
                    stage.mkdir()
                    (stage / 'executable').write_bytes(b'payload')
                    (stage / 'executable').chmod(0o755)
                    os.utime(stage / 'executable', (i * 1000, i * 1000))
                    (stage / 'empty').mkdir()
                    if os.name != 'nt':
                        (stage / 'link').symlink_to('executable')
                    output = root / f'output-{i}.{ext}'
                    packaging.pack(stage, output)
                    outputs.append(output.read_bytes())
                self.assertEqual(*outputs)

    def test_sidecar_retains_dependency_facts_and_normalizes_generated_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            results = []
            for i in [1, 2]:
                source = root / str(i)
                stage = source / 'stage'
                stage.mkdir(parents=True)
                (stage / 'cargo-metadata.json').write_text(json.dumps({'workspace_root': str(source), 'packages': [{'name':'lancedb','version':'0.30.0','license':'Apache-2.0','manifest_path': str(source / 'Cargo.toml')}]}))
                (stage / 'sbom.cdx.json').write_text(json.dumps({'serialNumber':str(i),'metadata':{'timestamp':str(i),'component':{'bom-ref':str(i),'name':'engine','version':'sha256:same'}},'dependencies':[{'ref':str(i),'dependsOn':['dependency']}],'components':[{'name':'dependency','license':'MIT'}]}))
                packaging.normalize_sidecar(stage, source, source / 'target')
                results.append([(stage / n).read_bytes() for n in ['cargo-metadata.json','sbom.cdx.json']])
            self.assertEqual(*results)
            self.assertIn(b'Apache-2.0', results[0][0])
            self.assertIn(b'dependency', results[0][1])
            self.assertIn(b'urn:uuid:', results[0][1])
            sbom = json.loads(results[0][1])
            self.assertEqual(sbom['metadata']['component']['bom-ref'], sbom['dependencies'][0]['ref'])


if __name__ == '__main__':
    unittest.main()
