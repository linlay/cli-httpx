#!/usr/bin/env python3
"""Deterministic release packaging. Only generated sidecar metadata is normalized."""
import argparse
import gzip
import hashlib
import json
import os
import pathlib
import stat
import tarfile
import uuid
import zipfile

EPOCH = 315532800  # 1980-01-01; valid in both tar and ZIP, independent of checkout time.


def canonical_json(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False) + '\n').encode()


def normalize_sidecar(stage, root, target):
    mappings = [(str(stage.resolve()), '/release'), (str(target.resolve()), '/target'),
                (str(root.resolve()), '/src/kbase-lance-engine'),
                (str(pathlib.Path(os.environ.get('CARGO_HOME', pathlib.Path.home() / '.cargo')).resolve()), '/cargo')]
    for path, replacement in [(stage, '/release'), (target, '/target'), (root, '/src/kbase-lance-engine')]:
        mappings.append((str(path.absolute()), replacement))
    # Most specific paths first; preserve dependency identity and graph references.
    mappings.sort(key=lambda item: len(item[0]), reverse=True)

    def walk(value):
        if isinstance(value, str):
            for source, replacement in mappings:
                value = value.replace(source, replacement).replace(source.replace('\\', '/'), replacement)
                value = value.replace(pathlib.Path(source).as_uri(), 'file://' + replacement)
            return value
        if isinstance(value, list):
            return [walk(item) for item in value]
        if isinstance(value, dict):
            return {key: walk(item) for key, item in value.items()}
        return value

    for name in ['cargo-metadata.json', 'sbom.cdx.json']:
        path = stage / name
        data = walk(json.loads(path.read_text(encoding='utf-8')))
        if name == 'sbom.cdx.json':
            data.pop('serialNumber', None)
            data.get('metadata', {}).pop('timestamp', None)  # optional CycloneDX field
            # Syft's root file reference includes the scan path. Re-key that
            # generated identity from stable content, updating graph references.
            component = data.get('metadata', {}).get('component', {})
            old_ref = component.get('bom-ref')
            if old_ref:
                identity_fields = {key: value for key, value in component.items() if key != 'bom-ref'}
                new_ref = 'urn:sha256:' + hashlib.sha256(canonical_json(identity_fields)).hexdigest()
                def replace_ref(value):
                    if isinstance(value, str):
                        return new_ref if value == old_ref else value
                    if isinstance(value, list):
                        return [replace_ref(item) for item in value]
                    if isinstance(value, dict):
                        return {key: replace_ref(item) for key, item in value.items()}
                    return value
                data = replace_ref(data)
            identity = hashlib.sha256(canonical_json(data)).hexdigest()
            data['serialNumber'] = 'urn:uuid:' + str(uuid.uuid5(uuid.NAMESPACE_URL, 'sha256:' + identity))
        path.write_bytes(canonical_json(data))


def pack(stage, output):
    output.parent.mkdir(parents=True, exist_ok=True)
    paths = sorted(stage.rglob('*'), key=lambda p: p.relative_to(stage).as_posix())
    def mode(p):
        if p.is_dir() or p.is_symlink() or p.stat().st_mode & 0o111 or p.suffix == '.exe':
            return 0o755
        return 0o644
    if output.name.endswith('.tar.gz'):
        with output.open('wb') as raw, gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode='w', format=tarfile.PAX_FORMAT) as archive:
                for p in paths:
                    info = archive.gettarinfo(str(p), p.relative_to(stage).as_posix())
                    info.uid = info.gid = 0
                    info.uname = info.gname = ''
                    info.mtime, info.mode, info.pax_headers = EPOCH, mode(p), {}
                    if info.isfile():
                        with p.open('rb') as source:
                            archive.addfile(info, source)
                    else:
                        archive.addfile(info)
    else:
        with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for p in paths:
                name = p.relative_to(stage).as_posix()
                info = zipfile.ZipInfo(name + ('/' if p.is_dir() and not p.is_symlink() else ''), (1980, 1, 1, 0, 0, 0))
                info.create_system = 3
                kind = stat.S_IFLNK if p.is_symlink() else stat.S_IFDIR if p.is_dir() else stat.S_IFREG
                info.external_attr = ((kind | mode(p)) << 16) | (0x10 if p.is_dir() else 0)
                info.compress_type = zipfile.ZIP_DEFLATED
                data = os.readlink(p).encode() if p.is_symlink() else b'' if p.is_dir() else p.read_bytes()
                archive.writestr(info, data)
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    output.with_name(output.name + '.sha256').write_text(f'{digest}  {output.name}\n', encoding='utf-8')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--stage', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--sidecar-root', type=pathlib.Path)
    parser.add_argument('--cargo-target', type=pathlib.Path)
    args = parser.parse_args()
    if args.sidecar_root:
        if not args.cargo_target:
            parser.error('--cargo-target is required for sidecar metadata')
        normalize_sidecar(args.stage, args.sidecar_root, args.cargo_target)
    pack(args.stage, args.output)


if __name__ == '__main__':
    main()
