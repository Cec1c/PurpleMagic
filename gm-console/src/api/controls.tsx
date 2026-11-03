import { getLocale } from "../i18n";
import { t } from "../i18n";
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
      aria-label={t("Token 登录")}
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
        <p>{t("阿拉德 · GM 工作台")}</p>
      </div>
      <div className="token-field">
        <label htmlFor="access-token">{t("访问 Token")}</label>
        <div className="token-input">
          <KeyRound size={17} />
        <input
          id="access-token"
          type={visible ? "text" : "password"}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          placeholder={t("粘贴访问 Token")}
          required
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
          <button type="button" className="icon-button" aria-label={visible ? t("隐藏 Token") : t("显示 Token")} onClick={() => setVisible(!visible)}>
            {visible ? <EyeOff size={17} /> : <Eye size={17} />}
          </button>
        </div>
      </div>
      <div className="token-options">
        <label className="checkline">
          <input className="checkbox" type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          {t("在此设备记忆 30 天")}
        </label>
      <label className="token-import" title={t("导入 Token 文件")}>
        <Upload size={14} /> {t("导入文件")}
        <input
          aria-label={t("导入 Token 文件")}
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
          {t(error)}
        </p>
      )}
      <button className="primary auth-submit" disabled={busy || !key.trim()}>
        {busy ? t("正在登录…") : t("进入工作台")}
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
      title={p.confirmed ? t("操作进度与回执") : p.title}
      onClose={() => api.dismissPending()}
      dismissOnBackdrop={false}
      footer={
        <>
          <button
            className="secondary"
            disabled={busy && !p.confirmed}
            onClick={() => api.dismissPending()}
          >
            {p.completed ? t("完成") : p.rejected ? t("结束此操作") : p.confirmed ? t("收起") : t("取消")}
          </button>
          {!p.completed && !p.rejected && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => void api.guard(() => api.confirm(p.id))}
            >
              {busy ? t("正在执行…") : p.confirmed ? t("继续并查询回执") : t("确认执行")}
            </button>
          )}
          {api.canRepreviewQuests() && (
            <button className="primary" disabled={busy} onClick={() => void api.guard(() => api.repreviewQuests(p.id))}>
              {t("重新预览未完成项")}
            </button>
          )}
        </>
      }
    >
      <p className="confirm-target">{p.target}</p>
      <p>{t(p.summary)}</p>
      {p.completed && p.steps.some(s => s.result?.schedule === "active") && (
        <p className="muted">{t("已提交后台发放，可关闭页面，进度见发放记录。")}</p>
      )}
      {p.confirmed && !p.completed && !p.rejected && (
        <p className="muted">{t("收起后可继续其他操作，从顶部“操作回执”查看或重试本次操作。")}</p>
      )}
      {p.activityChanges && <ul className="confirmation-recipients">
        {p.activityChanges.map((v) => <li key={v.id}>{v.name}<span>#{v.id} · {v.before ? t("开启") : t("关闭")} → {v.enabled ? t("开启") : t("关闭")}</span></li>)}
      </ul>}
      {p.recipients && (
        <ul className="confirmation-recipients">
          {p.recipients.map((c) => (
            <li key={c.id}>
              {c.name}
              <span>
                {c.accountId} {t("· 角色")} {c.id}
              </span>
            </li>
          ))}
        </ul>
      )}
      <ol className="operation-steps">
        {p.steps.map((s) => (
          <li key={s.id}>
            {s.done && <CheckCircle2 size={14} />}
            <strong>{t(s.label)}</strong>
            {s.result?.equipmentSlots && <span>{questSlotSummary(s.result.equipmentSlots, s.done)}</span>}
            {s.done && <span>{s.kind === "activities" ? `${t("配置版本")} ${s.result?.revision} · ${t(s.result?.restartRequired ? "等待游戏服务重启" : "已生效")}` : s.path.endsWith("/redelivery") ? t("补发任务已保存，可在发放详情查看进度") : t("已取得回执")}</span>}
            {s.kind === "task" && s.result && (
              <span>
                {s.result.schedule === "active" ? t("后台发放中 · ") : ""}
                {t("已发")} {s.result.sent} / {s.result.total} {t("· 失败")}{" "}
                {s.result.failed || 0}
              </span>
            )}
          </li>
        ))}
      </ol>
      {p.error && (
        <p className="inline-error" role="alert">
          {t(p.error)}
        </p>
      )}
      {p.confirmed && <p className="muted">{t("请求编号：")}{p.id}</p>}
    </Modal>
  );
}
export function OperationHistory({ onClose }: { onClose: () => void }) {
  const api = useLive()!;
  const receipts = api.operationReceipts();
  return <Modal title={t("操作回执")} wide onClose={onClose}>
    <p className="muted">{t("当前标签页的操作记录，刷新后继续保留。重试沿用原请求编号。")}</p>
    <ul className="operation-history">
      {receipts.map(p => <li key={p.id}>
        <div>
          <strong>{p.target} · {t(p.title)}</strong>
          <p>{t(p.summary)}</p>
          <small>{p.createdAt ? new Date(p.createdAt).toLocaleString(getLocale(), { timeZone: "Asia/Shanghai" }) + " · " : ""}
            {p.completed ? t("已取得回执") : p.rejected ? t("未通过") : api.meta.busy && api.meta.pending?.id === p.id ? t("执行中") : t("待确认结果")}
            {` · ${p.steps.filter(s => s.done).length}/${p.steps.length} ${t("步")}`}
          </small>
          {p.error && <p className="inline-error">{t(p.error)}</p>}
        </div>
        <button className="secondary" disabled={api.meta.busy && api.meta.pending?.id !== p.id}
          onClick={() => { api.openPending(p.id); onClose(); }}>{t("查看")}</button>
      </li>)}
    </ul>
  </Modal>;
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
      title={task?.reward?.title || t("发放详情")}
      wide
      onClose={onClose}
      footer={
        <>
          <button
            className="secondary"
            disabled={busy}
            onClick={() => void read()}
          >
            {t("刷新进度")}
          </button>
          {campaignId && <button className="primary"
            disabled={busy || redelivery?.status === "active" || !api.meta.features.backgroundBenefits || !api.state.benefits.find(b => b.id === campaignId)?.enabled}
            onClick={() => { onClose(); request({ kind: "benefit-redeliver", id: campaignId }); }}>
            {redelivery?.status === "active" ? t("补发处理中…") : t("补发未收到")}
          </button>}
          {task?.schedule === "active" && (
            <button
              className="secondary"
              disabled={busy}
              onClick={() => act("pause")}
            >
              {t("暂停发放")}
            </button>
          )}
          {task?.pending > 0 && task?.schedule !== "active" && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => act("resume")}
            >
              {t("继续发放")}
            </button>
          )}
          {task?.failed > 0 && task?.schedule !== "active" && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => act("retry")}
            >
              {t("重试失败名单")}
            </button>
          )}
        </>
      }
    >
      {task && (
        <p>
          {t("已发")} {task.sent} / {task.total} {t("· 待发")} {task.pending} {t("· 失败")}{" "}
          {task.failed}
        </p>
      )}
      {campaignId && <BenefitRecipients rows={rows} busy={busy} complete={complete} />}
      {redelivery && <div className="benefit-redelivery-status" role="status">
        <strong>{({active: "正在后台补发", completed: "本轮补发完成", failed: "本轮补发结束，存在失败记录", conflict: "补发已停止"} as Record<string, string>)[redelivery.status]}</strong>
        <p>{t("已检查")} {redelivery.checked} {t("· 确认已收到")} {redelivery.received} {t("· 跳过")} {redelivery.skipped} {t("· 失败")} {redelivery.failed}</p>
        <small>{t("确认已收到包含此前已发放的对象；失败后可再次补发，已收到的会自动跳过。")}</small>
        {redelivery.error && <p role="alert">{t(redelivery.error)}</p>}
        {redelivery.failures?.length > 0 && <ul>{redelivery.failures.map((f: DTO) => <li key={`${f.accountId}/${f.characterId}`}>{t("账号")} {f.accountId} {t("· 角色")} {f.characterId}：{f.message}</li>)}</ul>}
        {redelivery.failed > (redelivery.failures?.length || 0) && <small>{t("仅显示前 10 个失败对象。")}</small>}
      </div>}
      {redeliveryError && <p role="alert" className="inline-error">{t("补发进度：")}{redeliveryError}</p>}
      {!campaignId && busy && <p role="status">{t("正在读取发放记录…")}</p>}
      {!campaignId && !busy && !error && !rows.length && <p>{t("暂无收件角色")}</p>}
      {!campaignId && <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>{t("收件角色")}</th>
              <th>{t("账号")}</th>
              <th>{t("状态")}</th>
              <th>{t("结果")}</th>
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
                    ? t("已发送")
                    : r.state === "failed"
                      ? t("失败")
                      : r.state === "ineligible" ? t("不在发放范围")
                      : t("待发送")}
                </td>
                <td>
                  {r.failure?.message || (r.delivery?.reward?.cera
                    ? `点券 +${Number(r.delivery.reward.cera).toLocaleString(getLocale())} · 余额 ${r.delivery.before} → ${r.delivery.after}`
                    : r.mailId ? `邮件 ${r.mailId}` : "—")}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>}
      {!campaignId && <LoadMore show={!!next && !busy} onClick={() => void read(true)} />}
      {campaignId && error && !busy && <button className="secondary" onClick={() => void read(!!next)}>{t("继续查询")}</button>}
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
        {t("加载更多")}
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
      <h2>{t("公告与下载源管理")}</h2>
      <Field label={t("运营管理 Token")}>
        <input
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          required
          autoComplete="off"
        />
      </Field>
      {api.meta.hubError && <p className="inline-error">{api.meta.hubError}</p>}
      <button className="primary">{t("登录配置管理")}</button>
    </form>
  );
}
