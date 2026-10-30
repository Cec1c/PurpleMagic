"""Offline config/trust checks with temporary keys, assets and deployment state."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def module(path, name):
    spec = importlib.util.spec_from_file_location(name, ROOT/path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


profile_tool = module('scripts/prepare_launcher_profile.py', 'profile_tool')
deploy_tool = module('deploy/selfhost/prepare.py', 'deploy_tool')


class DeploymentTools(unittest.TestCase):
    def test_anticheat_is_opt_in_and_validated(self):
        profile_tool.validate_anticheat(None)
        profile_tool.validate_anticheat({'Enabled': False})
        profile_tool.validate_anticheat({'Enabled': True, 'Mode': 'audit', 'RequiredFiles': [{'Name': 'fixture.dll', 'Sha256': 'a'*64}]})
        for policy in [{'Enabled': 'false'}, {'Enable': True}, {'Mode': 'kill'}, {'Rules': None},
                       {'RequiredFiles': [{'Name': '../fixture.dll', 'Sha256': 'a'*64}]},
                       {'Rules': [{'Id': 'test', 'Scope': 'process', 'Sha256': 'file.exe'}]}]:
            with self.assertRaises(ValueError):
                profile_tool.validate_anticheat(policy)

    @classmethod
    def setUpClass(cls):
        cls.scratch = tempfile.TemporaryDirectory(prefix='purplemagic-config-')
        cls.root = Path(cls.scratch.name)
        cls.key, cls.cert, cls.public = [cls.root/n for n in ('key.pem', 'cert.pem', 'public.pem')]
        for command in [
            ['genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:2048', '-out', str(cls.key)],
            ['pkey', '-in', str(cls.key), '-pubout', '-out', str(cls.public)],
            ['req', '-new', '-x509', '-key', str(cls.key), '-out', str(cls.cert), '-days', '1',
             '-subj', '/CN=players.example.test', '-addext', 'subjectAltName=DNS:players.example.test,DNS:gm.example.test']]:
            subprocess.run(['openssl', *command], check=True, capture_output=True)
        (cls.root/'DNF.exe').write_bytes(b'isolated client fixture')
        (cls.root/'Script.pvf').write_bytes(b'isolated pvf fixture')
        cls.connection = {'ApiUrl': 'https://players.example.test:8443', 'GameHost': '192.0.2.10',
                          'BootstrapPort': 10000, 'ChannelServerId': 1,
                          'ChannelHistory': {'WindowsCodePage': 936, 'Adapter': 'dnf90-history-v1',
                                             'AccountSectionBase64': 'YWNjb3VudA=='}}
        cls.profile = profile_tool.build(cls.connection, cls.root, cls.cert, cls.public)
        (cls.root/'profile.json').write_text(json.dumps(cls.profile))

    @classmethod
    def tearDownClass(cls):
        cls.scratch.cleanup()

    def settings(self):
        return {'gmOrigin': 'https://gm.example.test', 'approvedProfile': str(self.root/'profile.json'),
                'signingPrivateKey': str(self.key), 'pvfFile': str(self.root/'Script.pvf'),
                'playerCertificate': str(self.cert), 'playerPrivateKey': str(self.key), 'playerCA': str(self.cert),
                'webCertificate': str(self.cert), 'webPrivateKey': str(self.key),
                'database': {'registrationDsn': 'fixture@tcp(127.0.0.1:3306)/registration?parseTime=true',
                             'repositoryDsn': 'fixture@tcp(127.0.0.1:3306)/game?parseTime=true',
                             'accountTable': 'dnf90_web_accounts', 'databases': ['game'], 'tablePrefix': 'dnf'},
                'game': {'upstream': 'http://127.0.0.1:7000', 'proxySecret': 'fixture-player-proxy-key-'*2,
                         'adminUrl': 'http://127.0.0.1:7000/api/game/admin', 'adminToken': 'fixture-admin-token-'*2}}

    def test_profile_derives_hashes_and_preserves_channel_contract(self):
        self.assertEqual(self.profile['PvfSha256'], profile_tool.digest(self.root/'Script.pvf'))
        self.assertEqual(self.profile['ChannelHistory'], self.connection['ChannelHistory'])
        self.assertNotIn('PRIVATE KEY', json.dumps(self.profile))
        self.assertEqual(self.profile['DefaultClientDirectory'], '')

    def test_profile_refuses_private_material_and_unknown_fields(self):
        with self.assertRaises(ValueError):
            profile_tool.build(self.connection, self.root, self.cert, self.key)
        with self.assertRaises(ValueError):
            profile_tool.build(dict(self.connection, databasePassword='fixture'), self.root, self.cert, self.public)

    def test_complete_configuration_and_no_secret_in_compose_env(self):
        output = self.root/'complete'
        deploy_tool.prepare(self.settings(), output, 1000, 1000)
        gateway = json.loads((output/'gateway.json').read_text())
        gm = json.loads((output/'service.json').read_text())
        token = (output/'admin-token.txt').read_text().strip()
        self.assertEqual(gateway['management']['serviceToken'], gm['gateway']['token'])
        self.assertEqual(gateway['management']['catalog']['token'], gm['catalogToken'])
        self.assertEqual(gateway['launcher_hub']['admin_token_sha256'], gm['loginKeySha256'])
        self.assertNotIn(token, (output/'compose.env').read_text())
        self.assertNotIn('registrationDsn', gm)
        self.assertEqual((output/'assets/Script.pvf').read_bytes(), (self.root/'Script.pvf').read_bytes())
        if shutil.which('docker'):
            subprocess.run(['docker', 'compose', '--env-file', str(output/'compose.env'), '-f',
                            str(ROOT/'deploy/selfhost/compose.yaml'), '--profile', 'setup', 'config', '--quiet'], check=True)
        with self.assertRaisesRegex(ValueError, 'already exists'):
            deploy_tool.prepare(self.settings(), output, 1000, 1000)
        self.assertEqual(token, (output/'admin-token.txt').read_text().strip())

    def test_hash_mismatch_stops_before_writing(self):
        bad = self.root/'wrong.pvf'; bad.write_bytes(b'wrong fixture')
        settings = self.settings(); settings['pvfFile'] = str(bad)
        output = self.root/'mismatch'
        with self.assertRaisesRegex(ValueError, 'PVF bytes'):
            deploy_tool.prepare(settings, output, 1000, 1000)
        self.assertFalse(output.exists())

    def test_certificate_hostname_and_placeholders_are_rejected(self):
        settings = self.settings(); settings['gmOrigin'] = 'https://wrong.example.test'
        with self.assertRaises(ValueError):
            deploy_tool.prepare(settings, self.root/'wrong-host', 1000, 1000)
        settings = self.settings(); settings['game']['proxySecret'] = 'CHANGE_ME'
        with self.assertRaisesRegex(ValueError, 'example settings'):
            deploy_tool.prepare(settings, self.root/'placeholder', 1000, 1000)


if __name__ == '__main__':
    unittest.main()
