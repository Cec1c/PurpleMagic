import { useState } from "react";
import { Copy, KeyRound } from "lucide-react";
import { useLive } from "../api/context";
import { createPasswordReset } from "../api/password-reset";
import { Field, Modal } from "../ui";

export function ResetPassword({ account, onClose }: {
  account: { id: string; name: string };
  onClose: () => void;
}) {
  const live = useLive();
  const [operation] = useState(() => createPasswordReset(account.id, account.name,
    (path, body, key) => live!.http.post(path, body, key)));
  const [busy, setBusy] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  async function confirm() {
    if (busy || password || !live) return;
    setBusy(true);
    setError("");
    try { setPassword(await operation.submit()); }
    catch (e) { setError((e as Error).message || "重置失败，请重试"); }
    finally { setBusy(false); }
  }
  async function copy() {
    try { await navigator.clipboard.writeText(password); setCopied(true); setError(""); }
    catch { setError("复制失败，请选中新密码手动复制"); }
  }
  return <Modal title={password ? "密码已重置" : "重置账号密码"}
    onClose={() => { if (!busy) onClose(); }} dismissOnBackdrop={false}
    footer={<>
      <button className="secondary" disabled={busy} onClick={onClose}>{password ? "关闭" : "取消"}</button>
      {password
        ? <button className="primary" onClick={() => void copy()}><Copy size={16} />{copied ? "已复制" : "复制新密码"}</button>
        : <button className="primary" disabled={busy || !live} onClick={() => void confirm()}><KeyRound size={16} />{busy ? "正在重置…" : error ? "重试本次重置" : "确认重置"}</button>}
    </>}>
    <p>账号 <strong>{account.name}</strong> <span className="muted">（{account.id}）</span></p>
    {password ? <>
      <Field label="新密码" hint="请复制并交给账号本人。关闭窗口后不再显示。">
        <input readOnly value={password} autoComplete="off" spellCheck={false}
          aria-label="新密码" onFocus={e => e.currentTarget.select()} />
      </Field>
      <p className="muted">旧密码与登录凭据已失效，请使用新密码登录。</p>
    </> : <p>生成 20 位随机密码。重置后该账号退出登录，旧密码和绑定码立即失效。</p>}
    {error && <p role="alert">{error}</p>}
  </Modal>;
}
