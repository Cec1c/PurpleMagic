import { useEffect, useState, useRef } from "react";
import { KeyRound, ArrowRight, RefreshCw, CheckCircle2, Eye, EyeOff, Upload } from "lucide-react";
import { useLive } from "./context";
import type { Pending } from "./model";
import { Modal, Field } from "../ui";
import { query, segment } from "./http";
import type { DTO } from "./model";
import type { Command } from "../domain";
import { collectRecipients, type RecipientRow } from "./benefit-recipients";
import { BenefitRecipients } from "./BenefitRecipients";
import { questSlotSummary } from "../quest-slots";

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
      onClose={() => api.dismissPending()}
      dismissOnBackdrop={false}
      footer={
        <>
          <button
            className="secondary"
            disabled={busy && !p.confirmed}
            onClick={() => api.dismissPending()}
          >
            {p.completed ? "完成" : p.rejected ? "结束此操作" : p.confirmed ? "收起" : "取消"}
          </button>
          {!p.completed && !p.rejected && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => void api.guard(() => api.confirm())}
            >
              {busy ? "正在执行…" : p.confirmed ? "继续并查询回执" : "确认执行"}
            </button>
          )}
          {api.canRepreviewQuests() && (
            <button className="primary" disabled={busy} onClick={() => void api.guard(() => api.repreviewQuests())}>
              重新预览未完成项
            </button>
          )}
        </>
      }
    >
      <p className="confirm-target">{p.target}</p>
      <p>{p.summary}</p>
      {p.confirmed && !p.completed && !p.rejected && (
        <p className="muted">可收起或刷新页面，从顶部“操作回执”继续。已完成的步骤会保留。</p>
      )}
      {p.activityChanges && <ul className="confirmation-recipients">
        {p.activityChanges.map((v) => <li key={v.id}>{v.name}<span>#{v.id} · {v.before ? "开启" : "关闭"} → {v.enabled ? "开启" : "关闭"}</span></li>)}
      </ul>}
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
            {s.result?.equipmentSlots && <span>{questSlotSummary(s.result.equipmentSlots, s.done)}</span>}
            {s.done && <span>{s.kind === "activities" ? `配置版本 ${s.result?.revision} · ${s.result?.restartRequired ? "等待游戏服务重启" : "已生效"}` : s.path.endsWith("/redelivery") ? "补发任务已保存，可在发放详情查看进度" : "已取得回执"}</span>}
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
  const [redelivery, setRedelivery] = useState<DTO | null>(null);
  const [redeliveryError, setRedeliveryError] = useState("");
  const [rows, setRows] = useState<RecipientRow[]>([]),
    [next, setNext] = useState(""),
    [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [complete, setComplete] = useState(false);
  const readController = useRef<AbortController | undefined>(undefined);
  const priorRedelivery = useRef<DTO | null>(null);
  async function read(more = false) {
    readController.current?.abort();
    const controller = new AbortController();
    readController.current = controller;
    setBusy(true);
    setError("");
    setComplete(false);
    if (campaignId && !more) { setRows([]); setNext(""); }
    try {
      const root = campaignId
        ? `/api/v1/benefits/${segment(campaignId)}`
        : `/api/v1/tasks/${segment(id)}`;
      if (campaignId) {
        setTask(undefined);
        await collectRecipients(
          (cursor, signal) => api.http.get<RecipientRow[]>(query(root + "/recipients", { limit: 100, cursor: cursor || undefined }), signal),
          controller.signal,
          (loaded, cursor) => { setRows(loaded); setNext(cursor); setComplete(!cursor); },
          more ? rows : [], more ? next : "",
        );
        return;
      }
      const t = campaignId ? null : await api.http.get(root, controller.signal);
      const r = await api.http.get<RecipientRow[]>(
        query(root + "/recipients", {
          limit: 100,
          cursor: more ? next : undefined,
        }),
        controller.signal,
      );
      if (controller.signal.aborted) return;
      setTask(t?.data);
      setRows(more ? [...rows, ...r.data] : r.data);
      setNext(r.page?.nextCursor || "");
    } catch (e) {
      if (controller.signal.aborted) return;
      setError((e as Error).message);
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  }
  useEffect(() => {
    setRedelivery(null);
    priorRedelivery.current = null;
    void read();
    return () => readController.current?.abort();
  }, [id, campaignId]);
  useEffect(() => {
    if (!campaignId || !api.meta.features.backgroundBenefits) return;
    const controller = new AbortController();
    let polling = false;
    const poll = async () => {
      if (polling) return;
      polling = true;
      try {
        const r = await api.http.get(`/api/v1/benefits/${segment(campaignId)}/redelivery`, controller.signal);
        if (!controller.signal.aborted) { setRedelivery(r.data); setRedeliveryError(""); }
      } catch (e) { if (!controller.signal.aborted) setRedeliveryError((e as Error).message); }
      finally { polling = false; }
    };
    void poll();
    const timer = setInterval(() => void poll(), 2000);
    return () => { clearInterval(timer); controller.abort(); };
  }, [campaignId]);
  useEffect(() => {
    if (priorRedelivery.current?.status === "active" && redelivery && redelivery.status !== "active") void read();
    priorRedelivery.current = redelivery;
  }, [redelivery?.id, redelivery?.status]);
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
          {campaignId && <button className="primary"
            disabled={busy || redelivery?.status === "active" || !api.meta.features.backgroundBenefits || !api.state.benefits.find(b => b.id === campaignId)?.enabled}
            onClick={() => { onClose(); request({ kind: "benefit-redeliver", id: campaignId }); }}>
            {redelivery?.status === "active" ? "补发处理中…" : "补发未收到"}
          </button>}
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
      {campaignId && <BenefitRecipients rows={rows} busy={busy} complete={complete} />}
      {redelivery && <div className="benefit-redelivery-status" role="status">
        <strong>{({active: "正在后台补发", completed: "本轮补发完成", failed: "本轮补发结束，存在失败记录", conflict: "补发已停止"} as Record<string, string>)[redelivery.status]}</strong>
        <p>已检查 {redelivery.checked} · 确认已收到 {redelivery.received} · 跳过 {redelivery.skipped} · 失败 {redelivery.failed}</p>
        <small>确认已收到包含此前已发放的对象；失败后可再次补发，已收到的会自动跳过。</small>
        {redelivery.error && <p role="alert">{redelivery.error}</p>}
        {redelivery.failures?.length > 0 && <ul>{redelivery.failures.map((f: DTO) => <li key={`${f.accountId}/${f.characterId}`}>账号 {f.accountId} · 角色 {f.characterId}：{f.message}</li>)}</ul>}
        {redelivery.failed > (redelivery.failures?.length || 0) && <small>仅显示前 10 个失败对象。</small>}
      </div>}
      {redeliveryError && <p role="alert" className="inline-error">补发进度：{redeliveryError}</p>}
      {!campaignId && busy && <p role="status">正在读取发放记录…</p>}
      {!campaignId && !busy && !error && !rows.length && <p>暂无收件角色</p>}
      {!campaignId && <div className="table-scroll">
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
                      : r.state === "ineligible" ? "不在发放范围"
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
      </div>}
      {!campaignId && <LoadMore show={!!next && !busy} onClick={() => void read(true)} />}
      {campaignId && error && !busy && <button className="secondary" onClick={() => void read(!!next)}>继续查询</button>}
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
