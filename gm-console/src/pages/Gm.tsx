import { numberDraft, draftNumber } from "../numeric";
import { useEnterMotion } from "../motion";
import { refreshDraftValue } from "../draft-refresh";
import { useState, useEffect, useRef } from "react";
import { useLive } from "../api/context";
import { professionGroups, demoProfessionGroups } from "../professions";
import { LoadMore } from "../api/controls";
import {
  Save,
  Clock3,
  Coins,
  TrendingUp,
  RotateCcw,
  PencilLine,
} from "lucide-react";
import {
  Tabs,
  Field,
  Pill,
  Empty,
  number,
  timestamp,
  type PageProps,
} from "../ui";

export function GmPage({ state, character, request }: PageProps) {
  const live = useLive();
  const [tab, setTab] = useState<
    "growth" | "currency" | "premium" | "history"
  >("growth");
  useEffect(() => {
    if (!live) return;
    void live.guard(async () => {
      if (tab === "growth") await live.growth();
      else if (tab === "currency") await live.balances();
      else if (tab === "premium") await live.premium();
      else await Promise.all([live.history(), live.tasks()]);
    });
    return () => { live.cancelScope(tab); if (tab === "history") live.cancelScope("tasks"); if (tab === "currency") live.cancelScope("balances"); };
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
  const [newName, setNewName] = useState("");
  const [premiumDays, setPremiumDays] = useState("30");
  const premium = state.api?.resources.premium;
  const currentPremium = premium?.accountId === character.accountId ? premium : undefined;
  useEffect(() => { setNewName(""); }, [character.id]);
  useEffect(() => { setNewName(v => v === character.name ? "" : v); }, [character.name]);
  const adventure = live ? state.api?.resources.adventureName : { accountId:a.id, name:"adventureName" in a ? a.adventureName : "" };
  const adventureName = adventure?.accountId === character.accountId ? adventure.name : undefined;
  const [newAdventureName, setNewAdventureName] = useState("");
  useEffect(() => { setNewAdventureName(""); }, [character.accountId]);
  useEffect(() => { setNewAdventureName(v => v === adventureName ? "" : v); }, [adventureName]);
  const carbonMode = !!state.api?.systemSettings?.carbonMode;
  const previous = useRef({ owner: character.id, level: numberDraft(character.level),
    job: `${character.jobCode}:${character.growType}`, sp: numberDraft(character.sp),
    tp: numberDraft(character.tp), gold: character.gold, points: a.points,
    pointsText: a.rawPoints || "", tokens: a.tokens });
  useEffect(() => {
    const next = { owner: character.id, level: numberDraft(character.level),
      job: `${character.jobCode}:${character.growType}`, sp: numberDraft(character.sp),
      tp: numberDraft(character.tp), gold: character.gold, points: a.points,
      pointsText: a.rawPoints || "", tokens: a.tokens };
    const old = previous.current, changed = old.owner !== next.owner;
    if (live) {
      setLevel(v => refreshDraftValue(v, old.level, next.level, changed));
      setJob(v => refreshDraftValue(v, old.job, next.job, changed));
      setSp(v => refreshDraftValue(v, old.sp, next.sp, changed));
      setTp(v => refreshDraftValue(v, old.tp, next.tp, changed));
      setGold(v => refreshDraftValue(v, old.gold, next.gold, changed));
      setPoints(v => refreshDraftValue(v, old.points, next.points, changed));
      setPointsText(v => refreshDraftValue(v, old.pointsText, next.pointsText, changed));
      setTokens(v => refreshDraftValue(v, old.tokens, next.tokens, changed));
    }
    previous.current = next;
  }, [live, character.id, character.level, character.jobCode, character.growType,
    character.sp, character.tp, character.gold, a.points, a.rawPoints, a.tokens]);
  const skillPoints = state.api?.resources.skillPoints;
  const balance = skillPoints?.balance;
  const groups = live ? professionGroups(state.api?.jobsRaw || []) : demoProfessionGroups;
  const optionValue = (v: { value: string; label: string }) => live ? v.value : v.label;
  const root = groups.find(g => g.choices.some(v => optionValue(v) === job));
  const motion = useEnterMotion(tab);
  return (
    <div ref={motion} className="motion-section">
      <div className="section-tabs">
        <Tabs
          label="GM 管理"
          items={[
            { id: "growth", label: "成长与职业" },
            { id: "currency", label: "金币与点券" },
            { id: "premium", label: "黑钻贵族" },
            { id: "history", label: "操作记录" },
          ]}
          value={tab}
          onChange={value => { live?.navigate("gm", true); setTab(value); }}
        />
      </div>
      {tab === "growth" ? (
        <div className="form-layout">
          <div style={{ display: "grid", gap: 20, minWidth: 0 }}>
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
              <Field label="根职业">
                <select value={root?.value || ""} disabled={!groups.length} onChange={(e) => {
                  const group = groups.find(g => g.value === e.target.value);
                  if (group) setJob(optionValue(group.choices[0]));
                }}>
                  {!root && <option value="">选择根职业</option>}
                  {groups.map(g => <option key={g.value} value={g.value}>{g.label}</option>)}
                </select>
              </Field>
              <Field label="转职 / 觉醒">
                <select value={job} onChange={(e) => setJob(e.target.value)}>
                  {!root && <option value={job}>{character.job}</option>}
                  {(root?.choices || []).map((j) => (
                    <option key={j.value} value={optionValue(j)}>
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
          {(!live || state.api?.features["characters.rename"]) && <form className="glass content-panel" onSubmit={e => {
            e.preventDefault();
            request({kind:"rename-character", characterId:character.id, expectedName:character.name, newName:newName.trim()});
          }}>
            <div className="panel-heading"><h2><PencilLine size={20} />角色改名</h2><Pill>ID {character.id}</Pill></div>
            <div className="field-grid">
              <Field label="当前角色名"><input value={character.name} readOnly /></Field>
              <Field label="新角色名"><input value={newName} onChange={e => setNewName(e.target.value)} maxLength={12} required placeholder="最多 6 个汉字或 12 个英文字母" autoComplete="off" /></Field>
            </div>
            <div className="form-footer"><span>{carbonMode ? "碳铀模式 · 允许在线改名" : "在线修改可到系统设置开启碳铀模式"}</span><button className="primary" disabled={!!state.api?.busy || !newName.trim() || newName.trim() === character.name}><PencilLine size={16} />修改角色名</button></div>
          </form>}
          {(!live || state.api?.features["adventure.rename"]) && <form className="glass content-panel" onSubmit={e => {
            e.preventDefault();
            if (adventureName) request({kind:"rename-adventure", accountId:character.accountId, expectedName:adventureName, newName:newAdventureName.trim()});
          }}>
            <div className="panel-heading"><h2><PencilLine size={20} />冒险团改名</h2><Pill>账号 {a.name}</Pill></div>
            <div className="field-grid">
              <Field label="当前冒险团名"><input value={adventureName || ""} readOnly placeholder={adventureName === undefined ? "" : "尚未创建冒险团"} /></Field>
              <Field label="新冒险团名"><input value={newAdventureName} onChange={e => setNewAdventureName(e.target.value)} maxLength={16} required placeholder="最多 8 个常用汉字或 16 个英文字母" autoComplete="off" /></Field>
            </div>
            <div className="form-footer"><span>所有角色共用此名称；在线修改可开启碳铀模式</span><button className="primary" disabled={!!state.api?.busy || !adventureName || !newAdventureName.trim() || newAdventureName.trim() === adventureName}><PencilLine size={16} />修改冒险团名</button></div>
          </form>}
          </div>
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
      ) : tab === "premium" ? (
        <div className="form-layout">
          <form className="glass content-panel" onSubmit={e => {e.preventDefault();request({kind:"premium-grant",accountId:character.accountId,days:Number(premiumDays)});}}>
            <h2><TrendingUp size={20} />黑钻贵族</h2>
            <p className="muted">账号内所有角色共享。已开通时，在现有到期时间上续期；已过期时，从现在开始计算。</p>
            <Field label="目标账号"><input value={`${a.name} · ${character.accountId}`} readOnly /></Field>
            <Field label="开通 / 续期天数"><input type="number" min="1" max="3650" step="1" required value={premiumDays} onChange={e => setPremiumDays(e.target.value)} /></Field>
            <div className="button-row">{[7,30,90,365].map(days => <button type="button" key={days} onClick={() => setPremiumDays(String(days))}>{days} 天</button>)}</div>
            <div className="form-footer"><span>修改对象：账号 {a.name}</span><button type="submit" className="primary" disabled={!!live && !state.api?.features["premium.write"]}><Save size={16} />开通 / 续期</button></div>
          </form>
          <aside className="glass content-panel"><h2>当前状态</h2><dl className="detail-list">
            <div><dt>黑钻贵族</dt><dd>{currentPremium ? currentPremium.active ? "已开通" : "未开通 / 已过期" : "—"}</dd></div>
            <div><dt>到期时间</dt><dd>{currentPremium?.expireAt ? timestamp(new Date(currentPremium.expireAt * 1000).toISOString()) : "—"}</dd></div>
          </dl><p className="muted">重新登录游戏后更新黑钻状态。</p></aside>
        </div>
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
      <LoadMore show={tab === "history" && Object.keys(state.api?.historyNext || {}).length > 0} onClick={() => void live?.guard(() => live.history(true))} />
    </div>
  );
}
