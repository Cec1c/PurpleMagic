"""HTTP contract checks for the announcement CLI; uses no production service."""
import argparse
import contextlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
import urllib.parse

import gm_announcement as cli


class AnnouncementTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = {"revision": "rev-1", "ops": {}, "rows": [], "posts": 0, "lose": False, "conflict": False}
        state = self.state

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def reply(self, value, status=200, cookie=None):
                raw = json.dumps(value).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                if cookie:
                    self.send_header("Set-Cookie", cookie)
                self.end_headers()
                self.wfile.write(raw)

            def do_GET(self):
                if self.headers.get("Cookie") != "fixture=session":
                    return self.reply({}, 401)
                if self.path == "/api/v1/capabilities":
                    return self.reply({"data": {"csrfToken": "fixture-csrf"}})
                if self.path == "/api/v1/hub/state":
                    return self.reply({"newsManager": True, "newsRevision": state["revision"]})
                if state.get("readback_fail"):
                    return self.reply({}, 503)
                query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
                offset = int(query.get("offset", [0])[0])
                rows = state["rows"]
                # Force a second page, so readback cannot rely only on page one.
                page = rows[offset:offset + 1]
                return self.reply({"rows": page, "revision": state["revision"],
                                   "next": offset + 1 if offset + 1 < len(rows) else -1})

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if self.headers.get("Origin") != "http://" + self.headers["Host"]:
                    return self.reply({}, 403)
                if self.path == "/api/auth/login":
                    if state.get("redirect"):
                        self.send_response(307)
                        self.send_header("Location", "http://127.0.0.1:1/unexpected")
                        self.send_header("Content-Length", "0")
                        self.end_headers()
                        return
                    if body != {"password": "fixture-token", "remember": False}:
                        return self.reply({"success": False})
                    return self.reply({"success": True}, cookie="fixture=session; Path=/; HttpOnly")
                if self.headers.get("Cookie") != "fixture=session" or self.headers.get("X-CSRF-Token") != "fixture-csrf":
                    return self.reply({}, 403)
                if self.path == "/api/auth/logout":
                    return self.reply({"success": True})
                if self.path != "/api/v1/hub/news/entries":
                    return self.reply({}, 404)
                state["posts"] += 1
                key = self.headers.get("Idempotency-Key")
                if key in state["ops"]:
                    prior, result = state["ops"][key]
                    return self.reply(result if prior == body else {}, 200 if prior == body else 409)
                if state["conflict"] or self.headers.get("If-Match") != json.dumps(state["revision"]):
                    return self.reply({}, 409)
                entry_id = body["id"] or "new-entry"
                state["rows"] = [{"id": "older-same-title", **body["news"]}, {"id": entry_id, **body["news"]}]
                state["revision"] = "rev-2"
                result = {"ok": True, "id": entry_id, "newsRevision": state["revision"]}
                state["ops"][key] = (body, result)
                if state["lose"]:
                    state["lose"] = False
                    self.connection.shutdown(2)
                    self.connection.close()
                    return
                return self.reply(result)

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.base = f"http://127.0.0.1:{server.server_port}"
        self.body = self.root / "body.md"
        self.body.write_text("更新内容：\n\n* 修复辅助装备栏未开启的问题。\n", encoding="utf-8")
        self.request = self.root / "request.json"

    def draft(self, entry_id=None):
        with contextlib.redirect_stdout(None):
            return cli.prepare(argparse.Namespace(title="游戏更新公告", body=self.body, category="更新", date="11 / 06",
                           base_url=self.base, entry_id=entry_id, output=self.request))

    def publish(self):
        with contextlib.redirect_stdout(None):
            return cli.publish(self.request, "fixture-token")

    def test_publish_readback_and_repeat_only_reads(self):
        with patch.object(cli.Client, "request", side_effect=AssertionError("prepare must be offline")):
            self.draft()
        result = self.publish()
        self.assertTrue(result["readback"]["verified"])
        self.assertEqual(result["published"]["id"], "new-entry")
        self.publish()
        self.assertEqual(self.state["posts"], 1)
        self.assertNotIn("fixture-token", self.request.read_text(encoding="utf-8"))
        self.assertNotIn("fixture-csrf", self.request.read_text(encoding="utf-8"))

    def test_lost_response_reuses_key_and_original_revision(self):
        draft = self.draft()
        self.state["lose"] = True
        with self.assertRaises((ValueError, OSError, ConnectionError)):
            self.publish()
        saved = json.loads(self.request.read_text(encoding="utf-8"))
        self.assertEqual(saved["attempt"]["revision"], "rev-1")
        self.assertNotIn("published", saved)
        result = self.publish()
        self.assertEqual(result["idempotencyKey"], draft["idempotencyKey"])
        self.assertEqual(len(self.state["ops"]), 1)
        self.assertEqual(self.state["posts"], 2)

    def test_conflict_never_refreshes_revision_automatically(self):
        self.draft()
        self.state["conflict"] = True
        with self.assertRaisesRegex(ValueError, "409"):
            self.publish()
        self.state["revision"] = "rev-new"
        self.state["conflict"] = False
        with self.assertRaisesRegex(ValueError, "409"):
            self.publish()
        self.assertEqual(len(self.state["ops"]), 0)
        self.assertEqual(json.loads(self.request.read_text(encoding="utf-8"))["attempt"]["revision"], "rev-1")

    def test_started_request_cannot_change_content(self):
        self.draft()
        self.publish()
        value = json.loads(self.request.read_text(encoding="utf-8"))
        value["mutation"]["news"]["body"] += "changed"
        cli.save(self.request, value)
        with self.assertRaisesRegex(ValueError, "内容发生变化"):
            self.publish()
        self.assertEqual(self.state["posts"], 1)

    def test_edit_preserves_entry_id(self):
        self.draft("existing-entry")
        self.assertEqual(self.publish()["published"]["id"], "existing-entry")

    def test_readback_failure_retains_success_without_reposting(self):
        self.draft()
        self.state["readback_fail"] = True
        with self.assertRaisesRegex(ValueError, "503"):
            self.publish()
        saved = json.loads(self.request.read_text(encoding="utf-8"))
        self.assertTrue(saved["published"]["ok"])
        self.state["readback_fail"] = False
        self.assertTrue(self.publish()["readback"]["verified"])
        self.assertEqual(self.state["posts"], 1)

    def test_login_redirect_is_not_followed(self):
        self.draft()
        self.state["redirect"] = True
        with self.assertRaisesRegex(ValueError, "307"):
            self.publish()
        self.assertEqual(self.state["posts"], 0)

    def test_template_placeholder_and_utf8_limit(self):
        self.body.write_text("修复{{场景}}问题", encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "占位"):
            self.draft()
        self.body.write_text("字" * 22000, encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "64 KiB"):
            self.draft()
        self.assertFalse(self.request.exists())

    def test_origin_and_existing_draft(self):
        for invalid in ("http://gm.example.com", "https://secret@gm.example.com", "https://gm.example.com/console/", "https://gm.example.com/#gateway"):
            with self.assertRaises(ValueError):
                cli.origin(invalid)
        first = self.draft()
        with self.assertRaises(FileExistsError):
            self.draft()
        self.assertEqual(json.loads(self.request.read_text(encoding="utf-8")), first)


if __name__ == "__main__":
    unittest.main()
