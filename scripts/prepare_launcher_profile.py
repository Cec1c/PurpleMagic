"""Build a player profile from an operator's connection config and approved assets."""
import argparse
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import ssl
from urllib.parse import urlsplit


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as source:
        for part in iter(lambda: source.read(1024 * 1024), b''):
            h.update(part)
    return h.hexdigest()


def validate_anticheat(policy):
    if policy is None:
        return
    def require(ok):
        if not ok:
            raise ValueError('Invalid AntiCheat policy; see docs/LAUNCHER_ANTICHEAT.md')
    def sha(value):
        return isinstance(value, str) and re.fullmatch(r'[a-fA-F0-9]{64}', value)
    require(isinstance(policy, dict))
    require(not set(policy) - {'Enabled', 'Mode', 'RequiredFiles', 'Rules', 'AllowedModuleHashes', 'CodeRegions'})
    require(type(policy.get('Enabled', False)) is bool and policy.get('Mode', 'audit') in ('audit', 'enforce'))
    for key, maximum in [('RequiredFiles', 64), ('Rules', 256), ('AllowedModuleHashes', 512), ('CodeRegions', 32)]:
        require(isinstance(policy.get(key, []), list) and len(policy.get(key, [])) <= maximum)
    names, ids = set(), set()
    for file in policy.get('RequiredFiles', []):
        require(isinstance(file, dict) and set(file) == {'Name', 'Sha256'})
        name = file['Name']
        require(isinstance(name, str) and re.fullmatch(r'[a-zA-Z0-9_-][a-zA-Z0-9_.-]{0,119}', name))
        require(name.lower().endswith('.dll') and not re.fullmatch(r'CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9]', name.split('.')[0], re.I))
        require(name.lower() not in names and sha(file['Sha256']))
        names.add(name.lower())
    for key in ('Rules', 'CodeRegions'):
        for rule in policy.get(key, []):
            fields = {'Id', 'Scope', 'Sha256'} if key == 'Rules' else {'Id', 'ExeSha256', 'Rva', 'Length', 'Sha256'}
            require(isinstance(rule, dict) and set(rule) == fields)
            identity = rule['Id']
            require(isinstance(identity, str) and re.fullmatch(r'[a-z0-9][a-z0-9-]{0,47}', identity))
            require(identity not in ids and sha(rule['Sha256']))
            ids.add(identity)
            if key == 'Rules':
                require(rule['Scope'] in ('module', 'process'))
            else:
                require(sha(rule['ExeSha256']) and type(rule['Rva']) is int and type(rule['Length']) is int)
                require(0 <= rule['Rva'] <= 0x7fffffff and 1 <= rule['Length'] <= 32768 and rule['Rva'] + rule['Length'] <= 0x80000000)
    require(all(sha(value) for value in policy.get('AllowedModuleHashes', [])))


def build(connection, client, certificate, public_key):
    allowed = {'ApiUrl', 'SettingsUrl', 'GameHost', 'BootstrapPort', 'ChannelServerId',
               'PreviousChannelServerId', 'ChannelHistory', 'MinimumLauncherVersion', 'DefaultClientDirectory', 'AntiCheat'}
    if set(connection) - allowed:
        raise ValueError('Unknown connection fields; use launcher/server.example.json')
    validate_anticheat(connection.get('AntiCheat'))
    api = urlsplit(connection['ApiUrl'])
    if api.scheme != 'https' or not api.hostname or api.username or api.path or api.query or api.fragment:
        raise ValueError('ApiUrl must be an HTTPS origin')
    ipaddress.ip_address(connection['GameHost'])
    if not 1024 <= connection['BootstrapPort'] <= 65535 or not 0 <= connection['ChannelServerId'] < 250:
        raise ValueError('Invalid bootstrap port or channel server ID')
    previous = connection.get('PreviousChannelServerId')
    if previous is not None and (not 0 <= previous < 250 or previous == connection['ChannelServerId']):
        raise ValueError('PreviousChannelServerId must be a distinct supported ID')
    history = connection.get('ChannelHistory')
    if history is not None:
        if history.get('WindowsCodePage') != 936 or history.get('Adapter') != 'dnf90-history-v1':
            raise ValueError('Use the tested channel history adapter and code page')
        section = base64.b64decode(history['AccountSectionBase64'], validate=True)
        if not section or len(section) > 256 or any(c in section for c in (b'\r', b'\n', b'\x00', b'[', b']')):
            raise ValueError('Supply only the approved account-section name, not a DNF.cfg file')
    minimum = connection.get('MinimumLauncherVersion', '0.2.18')
    if not re.fullmatch(r'\d+\.\d+\.\d+', minimum):
        raise ValueError('MinimumLauncherVersion needs a three-part version')
    pem = certificate.read_text(encoding='ascii').split('-----END CERTIFICATE-----')[0] + '-----END CERTIFICATE-----'
    der = ssl.PEM_cert_to_DER_cert(pem)
    public = public_key.read_text(encoding='ascii').strip()
    if 'PRIVATE KEY' in public or not public.startswith('-----BEGIN PUBLIC KEY-----'):
        raise ValueError('Pass an SPKI public key, never a signing private key')
    profile = dict(connection, CertificateSha256=hashlib.sha256(der).hexdigest(),
                   UpdatePublicKey=public, ExeSha256=digest(client/'DNF.exe'),
                   PvfSha256=digest(client/'Script.pvf'), MinimumLauncherVersion=minimum,
                   DefaultClientDirectory=connection.get('DefaultClientDirectory', ''),
                   SettingsUrl=connection.get('SettingsUrl', connection['ApiUrl']+'/launcher/settings.json'))
    return profile


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--connection', required=True, type=Path)
    p.add_argument('--client', required=True, type=Path)
    p.add_argument('--certificate', required=True, type=Path)
    p.add_argument('--public-key', required=True, type=Path)
    p.add_argument('--output', required=True, type=Path)
    args = p.parse_args()
    try:
        profile = build(json.loads(args.connection.read_text(encoding='utf-8-sig')),
                        args.client, args.certificate, args.public_key)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as dest:
            json.dump(profile, dest, ensure_ascii=False, indent=2)
            dest.write('\n')
    except (ValueError, KeyError, OSError) as error:
        p.exit(1, f'Profile preparation failed: {error}\n')
    print('Player profile prepared from the approved client and public trust material.')


if __name__ == '__main__':
    main()
