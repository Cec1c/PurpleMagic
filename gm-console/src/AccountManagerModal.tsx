import { useEffect, useState } from "react";
import {
  Coins,
  KeyRound,
  PencilLine,
  Plus,
  Minus,
  Equal,
  RefreshCw,
  Save,
  Sparkles,
  UserRound,
  Users,
} from "lucide-react";
import { useLive } from "./api/context";
import { computeCeraAdjustment, type CeraMode, type Character, type Command, type Section, type State } from "./domain";
import { ResetPassword } from "./pages/ResetPassword";
import { Field, Modal, Pill, number, timestamp } from "./ui";

const QUICK_CERA = [1000, 5000, 10000, 50000] as const;

export function CeraCalculator({
  accountId,
  accountName,
  currentRaw,
  busy = false,
  disabled = false,
  compact = false,
  onSubmit,
}: {
  accountId: string;
  accountName: string;
  currentRaw: string;
  busy?: boolean;
  disabled?: boolean;
  compact?: boolean;
  onSubmit: (mode: CeraMode, amountText: string) => void;
}) {
  const [mode, setMode] = useState<CeraMode>("add");
  const [input, setInput] = useState("");

  useEffect(() => {
    setInput(mode === "set" ? currentRaw : "");
  }, [accountId, currentRaw]);

  const switchMode = (next: CeraMode) => {
    setMode(next);
    if (next === "set") {
      setInput(currentRaw || "0");
    } else if (input === currentRaw) {
      setInput("");
    }
  };

  const applyQuick = (delta: number) => {
    const step = BigInt(delta);
    const trimmed = input.trim();
    if (mode === "set") {
      const base = /^\d+$/.test(trimmed)
        ? BigInt(trimmed)
        : /^\d+$/.test(currentRaw)
          ? BigInt(currentRaw)
          : 0n;
      setInput(String(base + step));
      return;
    }
    const base = /^\d+$/.test(trimmed) ? BigInt(trimmed) : 0n;
    setInput(String(base + step));
  };

  const calc = computeCeraAdjustment(currentRaw, mode, input);
  const hasInput = input.trim().length > 0;
  const deltaSign = calc.delta > 0n ? "+" : calc.delta < 0n ? "-" : "";
  const deltaAbs = calc.delta < 0n ? -calc.delta : calc.delta;

  return (
    <form
      className={`cera-calculator ${compact ? "compact" : "glass content-panel"}`}
      onSubmit={(e) => {
        e.preventDefault();
        if (!calc.valid || busy || disabled) return;
        onSubmit(mode, input.trim());
      }}
    >
      <div className="panel-heading">
        {compact ? (
          <h3>
            <Coins size={17} />
            账号点券
          </h3>
        ) : (
          <h2>
            <Coins size={20} />
            账号点券
          </h2>
        )}
        <Pill>账号 {accountName}</Pill>
      </div>

      <div className="cera-mode-tabs" role="group" aria-label="点券调整方式">
        <button
          type="button"
          className={mode === "add" ? "active" : ""}
          onClick={() => switchMode("add")}
        >
          <Plus size={14} />
          增加 (+)
        </button>
        <button
          type="button"
          className={mode === "sub" ? "active" : ""}
          onClick={() => switchMode("sub")}
        >
          <Minus size={14} />
          扣除 (-)
        </button>
        <button
          type="button"
          className={mode === "set" ? "active" : ""}
          onClick={() => switchMode("set")}
        >
          <Equal size={14} />
          设为 (=)
        </button>
      </div>

      <Field
        label={
          mode === "add"
            ? "增加点券数额"
            : mode === "sub"
              ? "扣除点券数额"
              : "目标点券余额"
        }
      >
        <input
          type="text"
          inputMode="numeric"
          pattern="[0-9]+"
          aria-label="点券数额"
          disabled={disabled}
          placeholder={mode === "set" ? "输入目标点券余额" : "输入或点击下方快捷面额累加"}
          value={input}
          onChange={(e) => setInput(e.target.value.replace(/[^\d]/g, ""))}
        />
      </Field>

      <div className="cera-quick-chips">
        {QUICK_CERA.map((amt) => (
          <button
            type="button"
            key={amt}
            disabled={disabled}
            onClick={() => applyQuick(amt)}
          >
            {mode === "sub" ? `-${amt.toLocaleString()}` : `+${amt.toLocaleString()}`}
          </button>
        ))}
        {hasInput && (
          <button
            type="button"
            className="chip-clear"
            disabled={disabled}
            onClick={() => setInput(mode === "set" ? currentRaw : "")}
          >
            重置输入
          </button>
        )}
      </div>

      <div className="cera-preview-bar" role="status">
        <div>
          <span className="muted">当前余额</span>
          <strong>{number(String(calc.current))}</strong>
        </div>
        <span className="cera-arrow">→</span>
        <div>
          <span className="muted">变更后余额</span>
          <strong className={calc.valid ? "accent-value" : ""}>
            {hasInput && calc.valid ? number(String(calc.after)) : number(String(calc.current))}
          </strong>
        </div>
        {hasInput && calc.valid && (
          <Pill tone={calc.delta > 0n ? "cyan" : "orange"}>
            {deltaSign}
            {number(String(deltaAbs))}
          </Pill>
        )}
      </div>
      {hasInput && !calc.valid && calc.error && (
        <p className="cera-calc-hint muted">{calc.error}</p>
      )}

      <div className="form-footer">
        <span>同账号全部角色共享点券</span>
        <button
          type="submit"
          className="primary"
          disabled={busy || disabled || !calc.valid}
        >
          <Save size={15} />
          {mode === "add" ? "确认增加点券" : mode === "sub" ? "确认扣除点券" : "保存点券余额"}
        </button>
      </div>
    </form>
  );
}

export function AccountManagerModal({
  accountId,
  accountName,
  state,
  request,
  onClose,
  onSelectCharacter,
  go,
}: {
  accountId: string;
  accountName?: string;
  state: State;
  request: (command: Command) => void;
  onClose: () => void;
  onSelectCharacter?: (id: string) => void;
  go?: (section: Section) => void;
}) {
  const live = useLive();
  const [siblings, setSiblings] = useState<Character[]>([]);
  const [premiumDays, setPremiumDays] = useState("30");
  const [newAdvName, setNewAdvName] = useState("");
  const [resetOpen, setResetOpen] = useState(false);

  useEffect(() => {
    if (!live) {
      setSiblings(state.characters.filter((c) => c.accountId === accountId));
      return;
    }
    const ctrl = new AbortController();
    void live.guard(() => live.openAccountPanel(accountId));
    void live
      .accountCharacters(accountId, ctrl.signal)
      .then((list) => {
        if (!ctrl.signal.aborted) setSiblings(list);
      })
      .catch(() => {});
    return () => {
      ctrl.abort();
    };
  }, [live, accountId]);

  const panel = state.api?.accountPanel?.accountId === accountId ? state.api.accountPanel : undefined;
  const fallbackAcc = state.accounts.find((x) => x.id === accountId);
  const acc = panel?.account || fallbackAcc;
  const displayName = acc?.name || accountName || accountId;
  const status = acc?.status || "正常";
  const currentRaw = live
    ? String(panel?.currency?.balance ?? acc?.rawPoints ?? (Number.isFinite(acc?.points) ? acc!.points : 0))
    : String(acc?.rawPoints ?? acc?.points ?? 0);

  const premium = live ? panel?.premium : undefined;
  const advResource = live
    ? panel?.adventureName
    : fallbackAcc
      ? { accountId, name: fallbackAcc.adventureName || "" }
      : undefined;
  const advName = advResource?.accountId === accountId ? advResource.name : undefined;

  useEffect(() => {
    setNewAdvName("");
  }, [accountId, advName]);

  const busy = !!state.api?.busy || !!panel?.loading;
  const canCera = !live || !!state.api?.features["currency.write"];
  const canPremium = !live || !!state.api?.features["premium.write"];
  const showAdventure = !live || !!state.api?.features["adventure.rename"];
  const showPasswordReset = !!live && !!state.api?.features["accounts.password-reset"];

  const handleClose = () => {
    live?.closeAccountPanel();
    onClose();
  };

  return (
    <>
      <Modal
        title={`账号管理 · ${displayName}`}
        wide
        onClose={handleClose}
      >
        <div className="account-drawer-head glass">
          <div className="account-drawer-meta">
            <div className="account-drawer-title">
              <Users size={18} />
              <strong>{displayName}</strong>
              <span className="mono muted">ID {accountId}</span>
              <Pill tone={status === "限制登录" ? "orange" : "cyan"}>{status}</Pill>
            </div>
            <div className="account-drawer-stats">
              <span>
                点券 <b>{number(currentRaw)}</b>
              </span>
              <span>
                黑钻{" "}
                <b>
                  {live
                    ? premium
                      ? premium.active
                        ? "已开通"
                        : "未开通 / 已过期"
                      : "读取中"
                    : "演示模式"}
                </b>
              </span>
              {advName !== undefined && (
                <span>
                  冒险团 <b>{advName || "未创建"}</b>
                </span>
              )}
            </div>
          </div>
          {live && (
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => void live.guard(() => live.openAccountPanel(accountId))}
            >
              <RefreshCw size={14} className={panel?.loading ? "is-spinning" : ""} />
              刷新账号
            </button>
          )}
        </div>

        {panel?.error && (
          <p className="inline-error" role="alert">
            {panel.error}
          </p>
        )}

        <div className="account-siblings-strip">
          <span className="muted">账号下角色（{siblings.length}）：</span>
          {siblings.length === 0 ? (
            <span className="muted">暂无角色 · 空账号仍可直接管理点券、黑钻、冒险团与密码</span>
          ) : (
            <div className="account-sibling-chips">
              {siblings.map((c) => (
                <button
                  key={c.id}
                  type="button"
                  className="sibling-chip"
                  onClick={() => {
                    onSelectCharacter?.(c.id);
                    if (go) go("inventory");
                    handleClose();
                  }}
                >
                  <i className={c.online ? "online" : ""} />
                  <UserRound size={13} />
                  <strong>{c.name}</strong>
                  <small>Lv.{c.level} · {c.job}</small>
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="account-drawer-grid">
          <CeraCalculator
            accountId={accountId}
            accountName={displayName}
            currentRaw={currentRaw}
            busy={busy}
            disabled={!canCera || (!!live && !panel?.currency?.revision)}
            compact
            onSubmit={(mode, amountText) =>
              request({
                kind: "account-currency",
                accountId,
                mode,
                amountText,
                standalone: true,
              })
            }
          />

          <div className="account-drawer-side">
            <form
              className="account-subcard"
              onSubmit={(e) => {
                e.preventDefault();
                request({
                  kind: "premium-grant",
                  accountId,
                  days: Number(premiumDays),
                  standalone: true,
                });
              }}
            >
              <div className="panel-heading">
                <h3>
                  <Sparkles size={16} />
                  黑钻续期
                </h3>
                {premium?.expireAt ? (
                  <small className="muted">
                    到期 {timestamp(new Date(premium.expireAt * 1000).toISOString())}
                  </small>
                ) : (
                  <Pill>{premium?.active ? "已开通" : "账号共享"}</Pill>
                )}
              </div>
              <Field label="开通 / 续期天数">
                <input
                  type="number"
                  min={1}
                  max={3650}
                  step={1}
                  required
                  value={premiumDays}
                  onChange={(e) => setPremiumDays(e.target.value)}
                />
              </Field>
              <div className="cera-quick-chips">
                {[7, 30, 90, 365].map((d) => (
                  <button
                    type="button"
                    key={d}
                    onClick={() => setPremiumDays(String(d))}
                  >
                    {d} 天
                  </button>
                ))}
              </div>
              <div className="form-footer">
                <span>已开通叠加时长，过期从当前起算</span>
                <button
                  type="submit"
                  className="primary"
                  disabled={busy || !canPremium}
                >
                  <Save size={15} />
                  开通 / 续期
                </button>
              </div>
            </form>

            {showAdventure && (
              <form
                className="account-subcard"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!advName) return;
                  request({
                    kind: "rename-adventure",
                    accountId,
                    expectedName: advName,
                    newName: newAdvName.trim(),
                    standalone: true,
                  });
                }}
              >
                <div className="panel-heading">
                  <h3>
                    <PencilLine size={16} />
                    冒险团改名
                  </h3>
                  <small className="muted">{advName ? `当前：${advName}` : "尚未创建冒险团"}</small>
                </div>
                <Field label="新冒险团名">
                  <input
                    value={newAdvName}
                    onChange={(e) => setNewAdvName(e.target.value)}
                    maxLength={16}
                    disabled={!advName}
                    required
                    placeholder={advName ? "最多 8 个汉字或 16 个字母" : "该账号尚未创建冒险团"}
                    autoComplete="off"
                  />
                </Field>
                <div className="form-footer">
                  <span>同账号所有角色共用</span>
                  <button
                    type="submit"
                    className="primary"
                    disabled={busy || !advName || !newAdvName.trim() || newAdvName.trim() === advName}
                  >
                    <PencilLine size={15} />
                    修改冒险团名
                  </button>
                </div>
              </form>
            )}

            {showPasswordReset && (
              <div className="account-subcard account-security-card">
                <div>
                  <h3>
                    <KeyRound size={16} />
                    登录密码重置
                  </h3>
                  <p className="muted">生成 20 位随机新密码，重置后旧密码与登录会话立即失效。</p>
                </div>
                <button
                  type="button"
                  className="secondary"
                  disabled={busy}
                  onClick={() => setResetOpen(true)}
                >
                  <KeyRound size={15} />
                  重置密码
                </button>
              </div>
            )}
          </div>
        </div>
      </Modal>
      {resetOpen && (
        <ResetPassword
          account={{ id: accountId, name: displayName }}
          onClose={() => setResetOpen(false)}
        />
      )}
    </>
  );
}
