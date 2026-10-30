import { useEnterMotion } from "../motion";
import { useState, useEffect } from "react";
import {
  Send,
  X,
  Users,
  Package,
  Mail as MailIcon,
  Trash2,
  History,
  Coins,
} from "lucide-react";
import { useLive } from "../api/context";
import { LoadMore, TaskDetail } from "../api/controls";
import type { Attachment } from "../domain";
import { resolveRecipients } from "../domain";
import { itemExpirationLabel } from "../item-expiration";
import {
  Tabs,
  Field,
  Pill,
  Modal,
  CheckBox,
  Empty,
  toggle,
  number,
  timestamp,
  type PageProps,
} from "../ui";
import { PlayerPicker } from "./Players";
import { ItemPicker } from "../ItemPicker";
import { appendAttachments } from "../item-picker";

export function MailPage({
  state,
  character,
  selection,
  setSelection,
  request,
}: PageProps) {
  const live = useLive();
  const [taskId, setTaskId] = useState("");
  const [tab, setTab] = useState<"compose" | "inbox" | "history">("compose"),
    [picker, setPicker] = useState(false),
    [title, setTitle] = useState("冒险补给"),
    [body, setBody] = useState("祝各位勇士冒险愉快！"),
    [gold, setGold] = useState(0),
    [items, setItems] = useState<Attachment[]>([]),
    [checked, setChecked] = useState<string[]>([]),
    [viewMail, setViewMail] = useState("");
  useEffect(() => {
    if (!live || tab === "compose") return;
    void live.guard(() => tab === "inbox" ? live.mailbox() : live.tasks());
    return () => live.cancelScope(tab === "inbox" ? "mailbox" : "tasks");
  }, [live, tab, character.id]);
  const recipients = resolveRecipients(state, selection),
    mails = state.mails.filter((m) => m.characterId === character.id),
    opened = mails.find((m) => m.id === viewMail);
  useEffect(() => {
    if (live && viewMail) void live.guard(() => live.mail(viewMail));
  }, [live, viewMail]);
  const motion = useEnterMotion(tab);
  return (
    <div ref={motion} className="motion-section">
      <div className="section-tabs">
        <Tabs
          label="邮件工作区"
          items={[
            { id: "compose", label: "发送邮件" },
            { id: "inbox", label: `角色邮箱 (${mails.length})` },
            { id: "history", label: "发放记录" },
          ]}
          value={tab}
          onChange={value => { live?.navigate("mail", true); setTab(value); }}
        />
      </div>
      {tab === "compose" ? (
        <div className="compose-layout item-compose-layout">
          <form
            className="glass content-panel"
            onSubmit={(e) => {
              e.preventDefault();
              request({ kind: "mail", recipients, title, body, gold, items });
            }}
          >
            <div className="panel-heading">
              <h2>写一封邮件</h2>
              <Pill>系统邮件</Pill>
            </div>
            <div className="recipient-strip">
              <Users size={19} />
              <div>
                <strong>{recipients.length} 个收件角色</strong>
                <span>
                  {state.characters
                    .filter((c) => recipients.includes(c.id))
                    .map((c) => c.name)
                    .join("、") || "尚未选择收件角色"}
                </span>
              </div>
              <button
                type="button"
                className="secondary"
                onClick={() => setPicker(true)}
              >
                选择收件人
              </button>
            </div>
            <Field label="邮件标题">
              <input
                required
                maxLength={80}
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="输入邮件标题"
              />
            </Field>
            <Field label="邮件正文">
              <textarea
                rows={4}
                value={body}
                onChange={(e) => setBody(e.target.value)}
                placeholder="给冒险家的话"
              />
            </Field>
            <div className="form-divider" />
            <div className="panel-heading">
              <h3>附件与金币</h3>
              <span className="muted">{items.length} 种物品</span>
            </div>
            {!items.length && <p className="attachment-empty">从物品列表添加附件，可一次勾选多种物品。</p>}
            <div className="attachment-list">
              {items.map((i) => (
                <div key={i.id}>
                  <Package size={19} />
                  <span>
                    <strong>{i.name}</strong>
                    <small>ID {i.id} · {itemExpirationLabel(i.expiration)}</small>
                  </span>
                  <label>
                    数量
                    <input
                      aria-label={`${i.name} 数量`}
                      type="number"
                      min={1}
                      max={99999}
                      value={i.count}
                      onChange={(e) =>
                        setItems(
                          items.map((x) =>
                            x.id === i.id
                              ? { ...x, count: Number(e.target.value) }
                              : x,
                          ),
                        )
                      }
                    />
                  </label>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`移除 ${i.name}`}
                    onClick={() => setItems(items.filter((x) => x.id !== i.id))}
                  >
                    <X size={16} />
                  </button>
                </div>
              ))}
            </div>
            <Field label="每个角色获得的金币">
              <input
                type="number"
                min="0"
                max="2147483647"
                value={gold}
                onChange={(e) => setGold(Number(e.target.value))}
              />
            </Field>
            <div className="form-footer">
              <span>发送后在发放记录中查看结果</span>
              <button
                className="primary"
                disabled={
                  !recipients.length ||
                  (!items.length && gold === 0 && !body.trim())
                }
              >
                <Send size={16} />
                发送给 {recipients.length} 个角色
              </button>
            </div>
          </form>
          <aside className="item-picker-column">
            <ItemPicker api={state.api} multiple addedIds={items.map(i => i.id)} onChoose={(selected, count) => setItems(appendAttachments(items, selected, count))} />
            <section className="glass content-panel summary-panel">
            <h3>本次发放</h3>
            <dl className="detail-list">
              <div>
                <dt>收件角色</dt>
                <dd>{recipients.length} 位</dd>
              </div>
              <div>
                <dt>附件种类</dt>
                <dd>{items.length} 种</dd>
              </div>
              <div>
                <dt>每角色金币</dt>
                <dd>{number(gold)}</dd>
              </div>
              <div>
                <dt>金币总计</dt>
                <dd className="gold-text">
                  {number(gold * recipients.length)}
                </dd>
              </div>
            </dl>
            <div className="form-divider" />
            <h3>收件名单</h3>
            <ul className="recipient-names">
              {state.characters
                .filter((c) => recipients.includes(c.id))
                .map((c) => (
                  <li key={c.id}>
                    <span>
                      {c.name}
                      <small>
                        {state.accounts.find((a) => a.id === c.accountId)?.name}
                      </small>
                    </span>
                    <Pill>Lv.{c.level}</Pill>
                  </li>
                ))}
            </ul>
            {!recipients.length && <Empty title="选择收件人" />}
            </section>
          </aside>
        </div>
      ) : tab === "inbox" ? (
        <div className="table-panel glass">
          <div className="panel-heading">
            <h2>{character.name} 的邮箱</h2>
            <button
              className="danger-button"
              disabled={!checked.length}
              onClick={() => request({ kind: "delete-mail", ids: checked })}
            >
              <Trash2 size={15} />
              删除选中
            </button>
          </div>
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th className="check-cell">
                    <CheckBox
                      label="全选邮件"
                      checked={
                        mails.length > 0 &&
                        mails.every((m) => checked.includes(m.id))
                      }
                      onChange={() =>
                        setChecked(
                          mails.every((m) => checked.includes(m.id))
                            ? []
                            : mails.map((m) => m.id),
                        )
                      }
                    />
                  </th>
                  <th>邮件</th>
                  <th>附件 / 金币</th>
                  <th>领取状态</th>
                  <th>收到时间</th>
                </tr>
              </thead>
              <tbody>
                {mails.map((m) => (
                  <tr key={m.id}>
                    <td>
                      <CheckBox
                        label={`选择邮件 ${m.title}`}
                        checked={checked.includes(m.id)}
                        onChange={() => setChecked(toggle(checked, m.id))}
                      />
                    </td>
                    <td>
                      <button
                        className="text-button"
                        onClick={() => setViewMail(m.id)}
                      >
                        <MailIcon size={16} />
                        {m.title}
                      </button>
                      <small className="cell-meta">{m.sender}</small>
                    </td>
                    <td>
                      {live ? "查看附件" : `${m.items.length} 件`} ·{" "}
                      {number(m.gold)} 金币
                    </td>
                    <td>
                      <Pill tone={m.claimed ? "neutral" : "cyan"}>
                        {m.claimed ? "已领取" : "未领取"}
                      </Pill>
                    </td>
                    <td className="muted">{timestamp(m.createdAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!mails.length && <Empty title="邮箱暂无邮件" />}
        </div>
      ) : (
        <div className="glass content-panel">
          <h2>
            <History size={19} />
            发放记录
          </h2>
          <div className="activity-list">
            {state.receipts
              .filter((r) => r.kind === "mail" || r.kind.startsWith("benefit"))
              .map((r) => (
                <div key={r.id}>
                  <span className="activity-icon">
                    <Send size={16} />
                  </span>
                  <div>
                    {live ? (
                      <button
                        className="text-button"
                        onClick={() => setTaskId(r.id)}
                      >
                        {r.summary}
                      </button>
                    ) : (
                      <strong>{r.summary}</strong>
                    )}
                    <small>{r.target}</small>
                  </div>
                  <time>{timestamp(r.at)}</time>
                </div>
              ))}
          </div>
          {!state.receipts.some(
            (r) => r.kind === "mail" || r.kind.startsWith("benefit"),
          ) && (
            <Empty
              title="尚无发放记录"
              detail="完成一次发送后，结果会显示在这里"
            />
          )}
        </div>
      )}
      <LoadMore
        show={
          tab === "inbox"
            ? !!state.api?.mailNext
            : tab === "history"
              ? !!state.api?.taskNext
              : false
        }
        onClick={() =>
          void live?.guard(() =>
            tab === "inbox"
              ? live.mailbox(true)
              : tab === "history"
                ? live.tasks(true)
                : Promise.resolve(),
          )
        }
      />
      {picker && (
        <Modal
          title="选择收件账号与角色"
          wide
          onClose={() => setPicker(false)}
          footer={
            <button className="primary" onClick={() => setPicker(false)}>
              使用 {recipients.length} 个角色
            </button>
          }
        >
          <PlayerPicker
            state={state}
            selection={selection}
            setSelection={setSelection}
          />
        </Modal>
      )}
      {live && taskId && (
        <TaskDetail
          id={taskId}
          onClose={() => setTaskId("")}
          request={request}
        />
      )}
      {opened && (
        <Modal title={opened.title} onClose={() => setViewMail("")}>
          <p className="muted">
            {opened.sender} · {timestamp(opened.createdAt)}
          </p>
          <p className="mail-body">{opened.body}</p>
          <div className="attachment-list">
            {opened.items.map((i, n) => (
              <div key={n}>
                <Package size={18} />
                <span>{i.name}</span>
                <b>× {number(i.count)}</b>
              </div>
            ))}
            <div>
              <Coins size={18} />
              <span>金币</span>
              <b>{number(opened.gold)}</b>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
