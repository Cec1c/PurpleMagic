import { numberDraft, draftNumber } from "../numeric";
import { useEnterMotion } from "../motion";
import { useState, useEffect } from "react";
import { useLive } from "../api/context";
import { catalog as demoCatalog } from "../fixtures";
import { LoadMore, TaskDetail } from "../api/controls";
import {
  Save,
  Gift,
  Pause,
  Play,
  Plus,
  Clock3,
  Coins,
  TrendingUp,
  RotateCcw,
} from "lucide-react";
import {
  Tabs,
  Field,
  CheckBox,
  Pill,
  Empty,
  number,
  timestamp,
  type PageProps,
} from "../ui";

export function GmPage({ state, character, request }: PageProps) {
  const live = useLive();
  const [taskId, setTaskId] = useState("");
  const [campaignId, setCampaignId] = useState("");
  const [tab, setTab] = useState<
    "growth" | "currency" | "benefits" | "history"
  >("growth");
  useEffect(() => {
    if (!live) return;
    void live.guard(async () => {
      if (tab === "growth") await live.growth();
      else if (tab === "currency") await live.character();
      else if (tab === "benefits") await live.benefits();
      else await Promise.all([live.history(), live.tasks()]);
    });
    return () => { live.cancelScope(tab); if (tab === "history") live.cancelScope("tasks"); if (tab === "currency") live.cancelScope("character"); };
  }, [live, tab, character.id]);
  const [level, setLevel] = useState(numberDraft(character.level)),
    [job, setJob] = useState(
      live ? `${character.jobCode}:${character.growType}` : character.job,
    ),
    [sp, setSp] = useState(numberDraft(character.sp)),
    [tp, setTp] = useState(numberDraft(character.tp));
  const a = state.accounts.find((a) => a.id === character.accountId) || { points: NaN, tokens: NaN, rawPoints: "", name: character.accountId, id: character.accountId };
  const [gold, setGold] = useState(character.gold),
    [points, setPoints] = useState(a.points),
    [tokens, setTokens] = useState(a.tokens);
  const [pointsText, setPointsText] = useState(a.rawPoints || "");
  const [create, setCreate] = useState(false),
    [name, setName] = useState("冒险启程福利"),
    [benefitGold, setBenefitGold] = useState(2000000),
    [enabled, setEnabled] = useState(true),
    [future, setFuture] = useState(true);
  const [rewardKind, setRewardKind] = useState<"gold" | "cera" | "item">("gold");
  const [benefitCera, setBenefitCera] = useState(1000);
  const [grantScope, setGrantScope] = useState<"character" | "account">("character");
  const [itemId, setItemId] = useState("");
  const [itemCount, setItemCount] = useState(1);
  const [itemQuery, setItemQuery] = useState("");
  const catalog = state.api?.catalog || demoCatalog;
  useEffect(() => {
    if (!live || !create || tab !== "benefits" || rewardKind !== "item") return;
    const timer = setTimeout(() => void live.guard(() => live.catalog(itemQuery)), 250);
    return () => clearTimeout(timer);
  }, [live, create, tab, rewardKind, itemQuery]);
  useEffect(() => {
    if (live) {
      setLevel(numberDraft(character.level));
      setJob(`${character.jobCode}:${character.growType}`);
      setSp(numberDraft(character.sp));
      setTp(numberDraft(character.tp));
      setGold(character.gold);
      setPoints(a.points);
      setPointsText(a.rawPoints || "");
      setTokens(a.tokens);
    }
  }, [
    live,
    character.level,
    character.job,
    character.jobCode,
    character.growType,
    character.sp,
    character.tp,
    character.gold,
    a.points,
    a.rawPoints,
    a.tokens,
  ]);
  const skillPoints = state.api?.resources.skillPoints;
  const balance = skillPoints?.balance;
  const motion = useEnterMotion(tab);
  return (
    <div ref={motion} className="motion-section">
      <div className="section-tabs">
        <Tabs
          label="GM 管理"
          items={[
            { id: "growth", label: "成长与职业" },
            { id: "currency", label: "金币与点券" },
            { id: "benefits", label: "福利规则" },
            { id: "history", label: "操作记录" },
          ]}
          value={tab}
          onChange={value => { live?.navigate("gm", true); setTab(value); }}
        />
      </div>
      {tab === "growth" ? (
        <div className="form-layout">
          <form
            className="glass content-panel"
            onSubmit={(e) => {
              e.preventDefault();
              request({
                kind: "growth",
                characterId: character.id,
                level: draftNumber(level),
                job,
                sp: draftNumber(sp),
                tp: draftNumber(tp),
              });
            }}
          >
            <div className="panel-heading">
              <h2>
                <TrendingUp size={20} />
                角色成长
              </h2>
              <Pill>{character.name}</Pill>
            </div>
            <div className="field-grid">
              <Field label="角色等级">
                <input
                  type="number"
                  min="1"
                  max="90"
                  value={level}
                  onChange={(e) => setLevel(e.target.value)}
                />
              </Field>
              <Field label="职业">
                <select value={job} onChange={(e) => setJob(e.target.value)}>
                  {(
                    state.api?.jobs ||
                    [
                      ...new Set([
                        character.job,
                        "帝血弑天",
                        "剑神",
                        "月之女皇",
                        "炽天使",
                        "极武圣",
                        "机械元首",
                      ]),
                    ].map((label) => ({ value: label, label }))
                  ).map((j) => (
                    <option key={j.value} value={live ? j.value : j.label}>
                      {j.label}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="额外 SP">
                <input
                  type="number"
                  min="0"
                  max={state.api?.resources.skillPoints?.maximum || 65535}
                  required
                  value={sp}
                  onChange={(e) => setSp(e.target.value)}
                />
              </Field>
              <Field label="额外 TP">
                <input
                  type="number"
                  min="0"
                  max={state.api?.resources.skillPoints?.maximum || 65535}
                  required
                  value={tp}
                  onChange={(e) => setTp(e.target.value)}
                />
              </Field>
            </div>
            <div className="form-footer">
              <span>修改对象：{character.name}</span>
              <button className="primary" disabled={!!live && !balance}>
                <Save size={16} />
                保存修改
              </button>
            </div>
          </form>
          <aside className="glass content-panel">
            <h3>当前成长</h3>
            <div className="level-display">
              {character.level}
              <span>Lv.</span>
            </div>
            <p>{character.job}</p>
            {balance && <dl className="detail-list skill-balances">
              {(["Sp", "Tp"] as const).map(kind => <div key={kind}>
                <dt>当前 {kind.toUpperCase()}</dt><dd><strong>{number(balance["remaining" + kind])}</strong> / {number(balance["total" + kind])}<small className="cell-meta">已用 {number(balance["total" + kind] - balance["remaining" + kind])} · 等级默认 {number(skillPoints.definition[kind.toLowerCase()])}</small></dd>
              </div>)}
            </dl>}
            <dl className="detail-list">
              <div>
                <dt>额外 SP</dt>
                <dd>{number(character.sp)}</dd>
              </div>
              <div>
                <dt>额外 TP</dt>
                <dd>{number(character.tp)}</dd>
              </div>
              <div>
                <dt>角色状态</dt>
                <dd>
                  {character.online === null
                    ? "状态未知"
                    : character.online
                      ? "在线"
                      : "离线"}
                </dd>
              </div>
            </dl>
            {live && <button type="button" className="secondary reset-points" disabled={!balance || !!state.api?.busy} onClick={() => request({kind:"reset-skill-points", characterId:character.id})}><RotateCcw size={15} />恢复等级默认 SP / TP</button>}
          </aside>
        </div>
      ) : tab === "currency" ? (
        <div className="form-layout">
          <form
            className="glass content-panel"
            onSubmit={(e) => {
              e.preventDefault();
              request({
                kind: "currency",
                characterId: character.id,
                gold,
                points,
                ...(live ? { pointsText } : {}),
                tokens,
              });
            }}
          >
            <div className="panel-heading">
              <h2>
                <Coins size={20} />
                金币与点券
              </h2>
              <Pill>账号 {a.name}</Pill>
            </div>
            <Field label="角色金币">
              <input
                type="number"
                min={0}
                max={2147483647}
                value={Number.isFinite(gold) ? gold : ""}
                onChange={(e) => setGold(Number(e.target.value))}
              />
            </Field>
            <div className="field-grid">
              <Field label="账号点券">
                <input
                  type={live ? "text" : "number"}
                  inputMode="numeric"
                  pattern={live ? "[0-9]+" : undefined}
                  value={
                    live ? pointsText : Number.isFinite(points) ? points : ""
                  }
                  onChange={(e) =>
                    live
                      ? setPointsText(e.target.value)
                      : setPoints(Number(e.target.value))
                  }
                />
              </Field>
              <Field
                label="账号代币券"
                hint={live ? "当前服务未提供此币种管理接口" : undefined}
              >
                <input
                  type="number"
                  min={0}
                  max={2147483647}
                  disabled={!!live}
                  value={Number.isFinite(tokens) ? tokens : ""}
                  onChange={(e) => setTokens(Number(e.target.value))}
                />
              </Field>
            </div>
            <div className="form-footer">
              <span>填写修改后的目标数值</span>
              <button className="primary">
                <Save size={16} />
                保存修改
              </button>
            </div>
          </form>
          <aside className="glass content-panel">
            <h3>当前资产</h3>
            <dl className="detail-list">
              <div>
                <dt>金币</dt>
                <dd className="gold-text">{number(character.gold)}</dd>
              </div>
              <div>
                <dt>点券</dt>
                <dd>{number(a.rawPoints ?? a.points)}</dd>
              </div>
              <div>
                <dt>代币券</dt>
                <dd>{number(a.tokens)}</dd>
              </div>
            </dl>
          </aside>
        </div>
      ) : tab === "benefits" ? (
        <>
          <div className="workspace-intro">
            <div>
              <h2>福利规则</h2>
              <p>启用后自动发放，按规则保留领取记录</p>
            </div>
            <button className="primary" onClick={() => setCreate(!create)}>
              <Plus size={16} />
              {create ? "收起表单" : "创建规则"}
            </button>
          </div>
          {create && (
            <form
              className="glass content-panel benefit-create"
              onSubmit={(e) => {
                e.preventDefault();
                request({
                  kind: "benefit-create",
                  name,
                  gold: benefitGold,
                  rewardKind,
                  cera: benefitCera,
                  grantScope,
                  item: rewardKind === "item" ? {
                    id: itemId.trim(), name: catalog.find((i) => i.id === itemId.trim())?.name || itemId.trim(),
                    count: itemCount, enhance: 0,
                  } : undefined,
                  enabled,
                  scope: future ? "future" : "existing",
                });
              }}
            >
              <div className="field-grid">
                <Field label="规则名称">
                  <input
                    required
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </Field>
                <Field label="奖励类型">
                  <select aria-label="奖励类型" value={rewardKind} onChange={(e) => setRewardKind(e.target.value as typeof rewardKind)}>
                    <option value="gold">金币</option>
                    <option value="cera">点券</option>
                    <option value="item">指定道具</option>
                  </select>
                </Field>
                <Field label="领取次数">
                  <select aria-label="领取次数" value={rewardKind === "cera" ? "account" : grantScope}
                    disabled={rewardKind === "cera"} onChange={(e) => setGrantScope(e.target.value as typeof grantScope)}>
                    <option value="character">每角色一次</option>
                    <option value="account">每账号一次</option>
                  </select>
                </Field>
                {rewardKind !== "item" && <Field label={rewardKind === "cera" ? "每个账号获得点券" : "每次发放金币"}>
                  <input
                    type="number"
                    required
                    min="1"
                    max="2147483647"
                    step="1"
                    value={rewardKind === "cera" ? benefitCera : benefitGold}
                    onChange={(e) => (rewardKind === "cera" ? setBenefitCera : setBenefitGold)(Number(e.target.value))}
                  />
                </Field>}
                {rewardKind === "item" && <>
                  <Field label="查找道具">
                    <input placeholder="输入物品名称或 ID" value={itemQuery} onChange={(e) => setItemQuery(e.target.value)} />
                  </Field>
                  <Field label="搜索结果">
                    <select aria-label="搜索结果" value={catalog.some((i) => i.id === itemId) ? itemId : ""} onChange={(e) => setItemId(e.target.value)}>
                      <option value="">选择道具</option>
                      {catalog.filter((i) => live || !itemQuery || i.name.includes(itemQuery) || i.id.includes(itemQuery)).map((i) =>
                        <option key={i.id} value={i.id}>{i.name} · {i.id}</option>)}
                    </select>
                  </Field>
                  <Field label="物品 ID">
                    <input required inputMode="numeric" pattern="[1-9][0-9]*" value={itemId} onChange={(e) => setItemId(e.target.value)} />
                  </Field>
                  <Field label="每次发放数量">
                    <input required type="number" min="1" max="2147483647" step="1" value={itemCount} onChange={(e) => setItemCount(Number(e.target.value))} />
                  </Field>
                </>}
              </div>
              {rewardKind === "cera" && <p className="muted benefit-reward-hint">点券直接存入账号，所有角色共享；收件角色收到到账通知。每账号仅发放一次。</p>}
              {rewardKind === "item" && <p className="muted benefit-reward-hint">道具通过邮件发放，装备每封数量为 1。每条规则发放一种道具。</p>}
              <div className="check-options">
                <label className="checkline">
                  <CheckBox
                    label="包含未来新建角色"
                    checked={future}
                    onChange={() => setFuture(!future)}
                  />
                  包含未来新建角色
                </label>
                <label className="checkline">
                  <CheckBox
                    label="创建后立即启用"
                    checked={enabled}
                    onChange={() => setEnabled(!enabled)}
                  />
                  创建后立即启用
                </label>
              </div>
              <div className="form-footer">
                <span>发放范围：全部现有角色{future ? "及未来角色" : ""}</span>
                <button className="primary">
                  <Gift size={16} />
                  创建{enabled ? "并启用" : ""}
                </button>
              </div>
            </form>
          )}
          <div className="table-panel glass">
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>规则</th>
                    <th>发放内容</th>
                    <th>已发放</th>
                    <th>状态</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {state.benefits.map((b) => (
                    <tr key={b.id}>
                      <td>
                        <strong>{b.name}</strong>
                        <small className="cell-meta">
                          {b.scope === "future"
                            ? "现有及未来角色"
                            : "全部现有角色"}{" "}
                          · {b.scopeLabel || "每角色一次"}
                        </small>
                      </td>
                      <td className="gold-text">{b.rewardSummary || `金币 ${number(b.gold)}`}</td>
                      <td>
                        {live ? (
                          <button
                            className="text-button"
                            onClick={() => {
                              setCampaignId(b.id);
                              setTaskId(b.id);
                            }}
                          >
                            {b.sent === undefined
                              ? "查看发放情况"
                              : `${b.sent} ${b.grantScope === "account" ? "个账号" : "位角色"} · 查看`}
                          </button>
                        ) : (
                          `${b.recipients.length} ${b.grantScope === "account" ? "个账号" : "位角色"}`
                        )}
                      </td>
                      <td>
                        <Pill tone={b.enabled ? "green" : "neutral"}>
                          {b.enabled ? "已启用" : "已暂停"}
                        </Pill>
                      </td>
                      <td>
                        <button
                          className="secondary"
                          onClick={() =>
                            request({ kind: "benefit-toggle", id: b.id })
                          }
                        >
                          {b.enabled ? <Pause size={14} /> : <Play size={14} />}{" "}
                          {b.enabled ? "暂停" : "启用"}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </>
      ) : (
        <div className="glass content-panel">
          <h2>
            <Clock3 size={20} />
            操作记录
          </h2>
          <div className="activity-list">
            {state.receipts.map((r) => (
              <div key={r.id}>
                <span className="activity-icon">
                  <Clock3 size={16} />
                </span>
                <div>
                  <strong>{r.summary}</strong>
                  <small>
                    {r.target} · {r.id.slice(0, 8)}
                  </small>
                </div>
                <time>{timestamp(r.at)}</time>
              </div>
            ))}
          </div>
          {!state.receipts.length && <Empty title="暂无操作记录" />}
        </div>
      )}
      <LoadMore
        show={
          tab === "benefits"
            ? !!state.api?.benefitNext
            : tab === "history"
              ? Object.keys(state.api?.historyNext || {}).length > 0
              : false
        }
        onClick={() =>
          void live?.guard(() =>
            tab === "benefits" ? live.benefits(true) : live.history(true),
          )
        }
      />
      {live && taskId && (
        <TaskDetail
          id={taskId}
          campaignId={campaignId}
          onClose={() => setTaskId("")}
          request={request}
        />
      )}
    </div>
  );
}
