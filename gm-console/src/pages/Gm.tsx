import { numberDraft, draftNumber } from "../numeric";
import { useEnterMotion } from "../motion";
import { useState, useEffect } from "react";
import { useLive } from "../api/context";
import { LoadMore } from "../api/controls";
import {
  Save,
  Clock3,
  Coins,
  TrendingUp,
  RotateCcw,
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
    "growth" | "currency" | "history"
  >("growth");
  useEffect(() => {
    if (!live) return;
    void live.guard(async () => {
      if (tab === "growth") await live.growth();
      else if (tab === "currency") await live.character();
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
