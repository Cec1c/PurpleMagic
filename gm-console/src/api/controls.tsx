import { useEffect, useState } from "react";
import { KeyRound, ArrowRight, RefreshCw, CheckCircle2, Eye, EyeOff, Upload } from "lucide-react";
import { useLive } from "./context";
import type { Pending } from "./model";
import { Modal, Field } from "../ui";
import { query, segment } from "./http";
import type { DTO } from "./model";
import type { Command } from "../domain";

export function LoginPanel() {
  const api = useLive()!;
  const [key, setKey] = useState(""),
    [remember, setRemember] = useState(true),
    [visible, setVisible] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  return (
    <form
      className="glass login-panel auth-card"
      aria-label="Token 登录"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        try {
          await api.login(key.trim(), remember);
          setKey("");
        } catch (e) {
          setError((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <div className="auth-brand">
        <span className="auth-mark"><img src="./brand/witch.png" alt="" width="56" height="56" /></span>
        <h1>Purple<span>Magic</span></h1>
        <p>阿拉德 · GM 工作台</p>
      </div>
      <div className="token-field">
        <label htmlFor="access-token">访问 Token</label>
        <div className="token-input">
          <KeyRound size={17} />
        <input
          id="access-token"
          type={visible ? "text" : "password"}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          placeholder="粘贴访问 Token"
          required
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
          <button type="button" className="icon-button" aria-label={visible ? "隐藏 Token" : "显示 Token"} onClick={() => setVisible(!visible)}>
            {visible ? <EyeOff size={17} /> : <Eye size={17} />}
          </button>
        </div>
      </div>
      <div className="token-options">
        <label className="checkline">
          <input className="checkbox" type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          在此设备记忆 30 天
        </label>
      <label className="token-import" title="导入 Token 文件">
        <Upload size={14} /> 导入文件
        <input
          aria-label="导入 Token 文件"
          type="file"
          accept=".json,.txt"
          onChange={async (e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            if (file.size > 16384) {
              setError("Token 文件超过 16KB");
              return;
            }
            try {
              const raw = (await file.text()).trim();
              const value = raw.startsWith("{") ? (JSON.parse(raw).key ?? JSON.parse(raw).token) : raw;
              if (typeof value !== "string" || !value.trim()) throw new Error();
              setKey(value.trim());
              setError("");
            } catch {
              setError("Token 文件格式无效");
            }
          }}
        />
      </label>
      </div>
      {error && (
        <p role="alert" className="inline-error">
          {error}
        </p>
      )}
      <button className="primary auth-submit" disabled={busy || !key.trim()}>
        {busy ? "正在登录…" : "进入工作台"}
        {busy ? <RefreshCw className="is-spinning" size={16} /> : <ArrowRight size={17} />}
      </button>
    </form>
  );
}
export function LiveConfirmation({ pending: p }: { pending: Pending }) {
  const api = useLive()!;
  const busy = api.meta.busy;
  return (
    <Modal
      title={p.confirmed ? "操作进度与回执" : p.title}
      onClose={() => {
        if ((!p.confirmed || p.completed || p.rejected) && !busy) api.cancel();
      }}
      footer={
        <>
          {(!p.confirmed || p.completed || p.rejected) && (
            <button
              className="secondary"
              disabled={busy}
              onClick={() => api.cancel()}
            >
              {p.completed ? "完成" : p.rejected ? "结束此操作" : "取消"}
            </button>
          )}
          {!p.completed && !p.rejected && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => void api.guard(() => api.confirm())}
            >
              {busy ? "正在执行…" : p.confirmed ? "继续并查询回执" : "确认执行"}
            </button>
          )}
        </>
      }
    >
      <p className="confirm-target">{p.target}</p>
      <p>{p.summary}</p>
      {p.recipients && (
        <ul className="confirmation-recipients">
          {p.recipients.map((c) => (
            <li key={c.id}>
              {c.name}
              <span>
                {c.accountId} · 角色 {c.id}
              </span>
            </li>
          ))}
        </ul>
      )}
      <ol className="operation-steps">
        {p.steps.map((s) => (
          <li key={s.id}>
            {s.done && <CheckCircle2 size={14} />}
            <strong>{s.label}</strong>
            {s.done && <span>已取得回执</span>}
            {s.kind === "task" && s.result && (
              <span>
                已发 {s.result.sent} / {s.result.total} · 失败{" "}
                {s.result.failed || 0}
              </span>
            )}
          </li>
        ))}
      </ol>
      {p.error && (
        <p className="inline-error" role="alert">
          {p.error}
        </p>
      )}
    </Modal>
  );
}
export function TaskDetail({
  id,
  campaignId,
  onClose,
  request,
}: {
  id: string;
  campaignId?: string;
  onClose: () => void;
  request: (c: Command) => void;
}) {
  const api = useLive()!;
  const [task, setTask] = useState<DTO>();
  const [rows, setRows] = useState<DTO[]>([]),
    [next, setNext] = useState(""),
    [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function read(more = false) {
    setBusy(true);
    setError("");
    try {
      const taskId =
        campaignId && !more
          ? await api.benefitProgress(campaignId)
          : task?.id || id;
      const root = `/api/v1/tasks/${segment(taskId)}`;
      const t = await api.http.get(root);
      const r = await api.http.get<DTO[]>(
        query(root + "/recipients", {
          limit: 100,
          cursor: more ? next : undefined,
        }),
      );
      setTask(t.data);
      setRows(more ? [...rows, ...r.data] : r.data);
      setNext(r.page?.nextCursor || "");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void read();
  }, [id]);
  function act(action: "pause" | "retry" | "resume") {
    onClose();
    request({ kind: "task-control", id: task?.id || id, action });
  }
  return (
    <Modal
      title={task?.reward?.title || "发放详情"}
      wide
      onClose={onClose}
      footer={
        <>
          <button
            className="secondary"
            disabled={busy}
            onClick={() => void read()}
          >
            刷新进度
          </button>
          {task?.schedule === "active" && (
            <button
              className="secondary"
              disabled={busy}
              onClick={() => act("pause")}
            >
              暂停发放
            </button>
          )}
          {task?.pending > 0 && task?.schedule !== "active" && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => act("resume")}
            >
              继续发放
            </button>
          )}
          {task?.failed > 0 && task?.schedule !== "active" && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => act("retry")}
            >
              重试失败名单
            </button>
          )}
        </>
      }
    >
      {task && (
        <p>
          已发 {task.sent} / {task.total} · 待发 {task.pending} · 失败{" "}
          {task.failed}
        </p>
      )}
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>收件角色</th>
              <th>账号</th>
              <th>状态</th>
              <th>结果</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={`${r.accountId}/${r.characterId}`}>
                <td>
                  {r.name} · {r.characterId}
                </td>
                <td>{r.accountId}</td>
                <td>
                  {r.state === "sent"
                    ? "已发送"
                    : r.state === "failed"
                      ? "失败"
                      : "待发送"}
                </td>
                <td>
                  {r.failure?.message || (r.delivery?.reward?.cera
                    ? `点券 +${Number(r.delivery.reward.cera).toLocaleString()} · 余额 ${r.delivery.before} → ${r.delivery.after}`
                    : r.mailId ? `邮件 ${r.mailId}` : "—")}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <LoadMore show={!!next} onClick={() => void read(true)} />
      {error && (
        <p role="alert" className="inline-error">
          {error}
        </p>
      )}
    </Modal>
  );
}
export function LoadMore({
  show,
  onClick,
}: {
  show: boolean;
  onClick: () => void;
}) {
  if (!show) return null;
  return (
    <div className="load-more">
      <button className="secondary" onClick={onClick}>
        <RefreshCw size={14} />
        加载更多
      </button>
    </div>
  );
}
export function HubLogin() {
  const api = useLive()!;
  const [token, setToken] = useState("");
  return (
    <form
      className="glass content-panel"
      onSubmit={(e) => {
        e.preventDefault();
        void api.hub(token).then(() => setToken(""));
      }}
    >
      <h2>公告与下载源管理</h2>
      <Field label="运营管理 Token">
        <input
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          required
          autoComplete="off"
        />
      </Field>
      {api.meta.hubError && <p className="inline-error">{api.meta.hubError}</p>}
      <button className="primary">登录配置管理</button>
    </form>
  );
}
