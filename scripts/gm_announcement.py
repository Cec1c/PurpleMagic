"""Prepare Markdown announcements and publish through the existing GM session API."""
import argparse
import datetime
import getpass
import hashlib
import http.cookiejar
import ipaddress
import json
import os
from pathlib import Path
import re
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid


def origin(value):
    parsed = urllib.parse.urlsplit(value)
    local = parsed.hostname == "localhost"
    try:
        local = local or ipaddress.ip_address(parsed.hostname or "").is_loopback
    except ValueError:
        pass
    if (not parsed.hostname or parsed.username or parsed.password or parsed.query
            or parsed.fragment or parsed.path not in ("", "/")
            or not (parsed.scheme == "https" or parsed.scheme == "http" and local)):
        raise ValueError("base-url 使用 GM 站点根地址；HTTPS 或本机 HTTP，例如 https://gm.example.com")
    return value.rstrip("/")


def encoded(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


def digest(value):
    return hashlib.sha256(encoded(value)).hexdigest()


def save(path, value, new=False):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    raw = json.dumps(value, ensure_ascii=False, indent=2) + "\n"
    if new:
        with path.open("x", encoding="utf-8") as stream:
            stream.write(raw)
    else:
        temp = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
        temp.write_text(raw, encoding="utf-8")
        os.replace(temp, path)


def validate_news(news):
    if set(news) != {"title", "body", "category", "date", "format"}:
        raise ValueError("公告字段必须为 title/body/category/date/format")
    if not all(isinstance(v, str) and "\0" not in v for v in news.values()):
        raise ValueError("公告字段必须为文本")
    if not news["title"].strip() or len(news["title"]) > 160:
        raise ValueError("标题需为 1–160 字")
    if not news["body"].strip() or len(news["body"].encode("utf-8")) > 64 << 10:
        raise ValueError("正文不能为空，最多 64 KiB UTF-8")
    if len(news["date"].encode("utf-8")) > 32:
        raise ValueError("显示日期最多 32 字节")
    if news["category"] not in ("综合", "更新", "指南") or news["format"] != "markdown":
        raise ValueError("分类为综合／更新／指南，正文格式为 markdown")
    if any(re.search(r"\{\{[^{}]+\}\}", value) for value in news.values()):
        raise ValueError("请填写或删除模板中的 {{占位内容}}")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, base_url, ca_file=None):
        self.base = origin(base_url)
        self.csrf = ""
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
            urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=ca_file)),
            NoRedirect(),
        )

    def request(self, path, body=None, headers=None):
        request_headers = {"Accept": "application/json", "Origin": self.base, "Cache-Control": "no-cache"}
        if body is not None:
            request_headers.update({"Content-Type": "application/json", "X-CSRF-Token": self.csrf})
        request_headers.update(headers or {})
        req = urllib.request.Request(self.base + path, data=None if body is None else encoded(body), headers=request_headers)
        try:
            with self.opener.open(req, timeout=30) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            # Print protocol status, never raw proxy/login responses or credentials.
            message = "公告列表已变化；保留本次请求，核对已发布内容后重新准备草稿" if error.code == 409 else "检查 GM 地址、权限与服务状态"
            raise ValueError(f"HTTP {error.code}：{message}") from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise ValueError("连接失败；保留请求文件，再运行同一条 publish 命令") from None
        except (ValueError, UnicodeError):
            raise ValueError("接口未返回 JSON；检查 GM 站点地址") from None

    def login(self, token):
        result = self.request("/api/auth/login", {"password": token, "remember": False})
        if result.get("success") is not True:
            raise ValueError("GM 登录失败；检查 Token 或登录冷却状态")
        self.csrf = self.request("/api/v1/capabilities")["data"]["csrfToken"]

    def logout(self):
        try:
            self.request("/api/auth/logout", {})
        except (ValueError, KeyError):
            pass

    def readback(self, entry_id, news):
        offset, revision = 0, None
        while True:
            query = {"offset": offset, "limit": 50, "q": news["title"]}
            if revision is not None:
                query["revision"] = revision
            page = self.request("/api/v1/hub/news?" + urllib.parse.urlencode(query))
            revision = page["revision"]
            for row in page["rows"]:
                if row["id"] == entry_id:
                    if any(row.get(key) != value for key, value in news.items()):
                        raise ValueError("公告已发布，但当前正文与本次请求不同；请查看管理记录")
                    return {"id": entry_id, "revision": revision, "verified": True}
            next_offset = page["next"]
            if next_offset < 0:
                raise ValueError("发布回执已保存，列表尚未找到对应条目；重跑同一请求以继续回读")
            if next_offset <= offset:
                raise ValueError("公告分页游标无效")
            offset = next_offset


def prepare(args):
    news = {"title": args.title, "body": Path(args.body).read_text(encoding="utf-8-sig"),
            "category": args.category, "date": args.date, "format": "markdown"}
    validate_news(news)
    request = {"schema": 1, "baseUrl": origin(args.base_url), "idempotencyKey": str(uuid.uuid4()),
               "mutation": {"action": "save", "id": args.entry_id or "", "news": news}}
    save(args.output, request, new=True)
    print(f"草稿已生成：{args.output}\n标题：{news['title']}\n\n{news['body']}")
    return request


def publish(path, token, ca_file=None):
    path = Path(path)
    value = json.loads(path.read_text(encoding="utf-8-sig"))
    if value.get("schema") != 1:
        raise ValueError("请求文件版本无效")
    mutation = value["mutation"]
    if set(mutation) != {"action", "id", "news"} or mutation["action"] != "save" or not isinstance(mutation["id"], str):
        raise ValueError("脚本支持新增或编辑单篇公告")
    validate_news(mutation["news"])
    key = value["idempotencyKey"]
    if not isinstance(key, str) or not key or len(key) > 100 or any(c in key for c in "\r\n\0"):
        raise ValueError("操作编号无效")
    base = origin(value["baseUrl"])
    fingerprint = digest({"base": base, "key": key, "mutation": mutation})
    if value.get("attempt") and value["attempt"]["fingerprint"] != fingerprint:
        raise ValueError("已发起的请求内容发生变化；请使用原请求文件恢复，修改内容另建草稿")
    client = Client(base, ca_file)
    try:
        client.login(token)
        if not value.get("attempt"):
            state = client.request("/api/v1/hub/state")
            if not state.get("newsManager"):
                raise ValueError("该 GM 尚未提供逐条公告管理接口")
            value["attempt"] = {"fingerprint": fingerprint, "revision": state["newsRevision"]}
            save(path, value)
        if not value.get("published"):
            result = client.request("/api/v1/hub/news/entries", mutation,
                                    {"If-Match": json.dumps(value["attempt"]["revision"]), "Idempotency-Key": key})
            if result.get("ok") is not True or not result.get("id"):
                raise ValueError("未收到发布成功回执；重跑同一请求")
            value["published"] = result
            save(path, value)
        value["readback"] = client.readback(value["published"]["id"], mutation["news"])
        value["verifiedAt"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save(path, value)
        print(json.dumps({"published": True, "verified": True, "id": value["published"]["id"],
                          "title": mutation["news"]["title"], "receipt": str(path)}, ensure_ascii=False))
        return value
    finally:
        client.logout()


def load_token(path=None):
    if path:
        raw = Path(path).read_text(encoding="utf-8-sig").strip()
        token = json.loads(raw)["key"] if raw.startswith("{") else raw
    else:
        token = os.environ.get("PURPLEMAGIC_GM_TOKEN") or getpass.getpass("GM Token: ")
    if not isinstance(token, str) or not token:
        raise ValueError("GM Token 为空")
    return token


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    draft = commands.add_parser("prepare", help="从 Markdown 文件生成草稿；不联网")
    draft.add_argument("--base-url", required=True)
    draft.add_argument("--title", required=True)
    draft.add_argument("--body", required=True)
    draft.add_argument("--date", required=True)
    draft.add_argument("--category", choices=("综合", "更新", "指南"), default="更新")
    draft.add_argument("--entry-id", help="编辑已发布公告；新增时省略")
    draft.add_argument("--output", required=True)
    send = commands.add_parser("publish", help="发布请求文件并回读；重试沿用原文件")
    send.add_argument("--request", required=True)
    send.add_argument("--token-file", help="受保护的纯文本 Token 文件或 {key: ...} JSON 文件")
    send.add_argument("--ca-file", help="自建 CA 的 PEM 文件；默认使用系统信任")
    args = parser.parse_args()
    try:
        if args.command == "prepare":
            prepare(args)
        else:
            publish(args.request, load_token(args.token_file), args.ca_file)
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(f"未完成：{error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
