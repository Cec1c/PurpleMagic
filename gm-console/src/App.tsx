import { useEffect, useState, useMemo } from "react";
import {
  Search,
  Backpack,
  Mail,
  ScrollText,
  WandSparkles,
  Gift,
  Users,
  Settings2,
  Megaphone,
  Sun,
  Moon,
  PanelLeft,
  ChevronRight,
  CheckCircle2,
  AlertCircle,
  RotateCcw,
  Palette,
  X,
  KeyRound,
  ArrowRightLeft,
  LoaderCircle,
  ExternalLink,
} from "lucide-react";
import type { Command, Section, Selection } from "./domain";
import { applyCommand, resolveRecipients } from "./domain";
import { initialState, loadState, persistState } from "./fixtures";
import {
  Identity,
  IconButton,
  Modal,
  Field,
  CheckBox,
  number,
  type PageProps,
} from "./ui";
import { Inventory } from "./pages/Inventory";
import { QueryPage } from "./pages/Query";
import { Players, PlayerPicker } from "./pages/Players";
import { MailPage } from "./pages/Mail";
import { QuestPage } from "./pages/Quests";
import { GmPage } from "./pages/Gm";
import { BenefitsPage } from "./pages/Benefits";
import { Settings } from "./pages/Settings";
import { ConsoleAPI } from "./api/console";
import { emptyState } from "./api/model";
import { LiveContext } from "./api/context";
import { LoginPanel, LiveConfirmation } from "./api/controls";
import { Empty } from "./ui";
const demo =
  import.meta.env.VITE_CONSOLE_MODE === "demo" ||
  new URLSearchParams(location.search).get("demo") === "1";

const navigation = [
  {
    id: "query",
    label: "查询",
    icon: Search,
    title: "查询资料",
    sub: "查询账号、角色、PVF 物品和服务器活动。",
  },
  {
    id: "inventory",
    label: "背包",
    icon: Backpack,
    title: "背包与物品",
    sub: "物品、穿戴与仓库，在一个工作区中管理。",
  },
  {
    id: "mail",
    label: "邮件",
    icon: Mail,
    title: "邮件与发放",
    sub: "选择收件角色，配置奖励，一次确认完成发放。",
  },
  {
    id: "quests",
    label: "任务",
    icon: ScrollText,
    title: "角色任务",
    sub: "按章节查看任务进度，完成或重置选中任务。",
  },
  {
    id: "gm",
    label: "GM",
    icon: WandSparkles,
    title: "GM 管理",
    sub: "角色成长与资产调整。",
  },
  { id: "benefits", label: "福利规则", icon: Gift, title: "福利规则", sub: "全局奖励自动发放，查看每个账号与角色的发放情况。" },
  {
    id: "accounts",
    label: "账号与角色",
    icon: Users,
    title: "账号与角色",
    sub: "查看账号归属与角色资料，统一选择操作对象。",
  },
  {
    id: "settings",
    label: "公告与下载源",
    icon: Megaphone,
    title: "公告与下载源",
    sub: "维护公告、更新资源地址与游戏连接。",
  },
] as const;
const preferenceKey = "purplemagic-glass-appearance-v1";
function readPreferences() {
  try {
    return {
      theme: "dark",
      opacity: 66,
      blur: 20,
      background: true,
      compact: false,
      ...JSON.parse(localStorage.getItem(preferenceKey) || "{}"),
    };
  } catch {
    return {
      theme: "dark",
      opacity: 66,
      blur: 20,
      background: true,
      compact: false,
    };
  }
}
function readSection(): Section {
  const id = location.hash.replace("#", "");
  return navigation.some((n) => n.id === id) ? (id as Section) : "inventory";
}

export function App() {
  const [state, setState] = useState(() => (demo ? loadState() : emptyState())),
    [section, setSection] = useState<Section>(readSection),
    [characterId, setCharacterId] = useState(demo ? "1" : ""),
    [selection, setSelection] = useState<Selection>({
      accounts: [],
      characters: demo ? ["1"] : [],
    }),
    [preferences, setPreferences] = useState(readPreferences),
    [appearance, setAppearance] = useState(false),
    [pickCharacter, setPickCharacter] = useState(false),
    [mobileNav, setMobileNav] = useState(false),
    [reset, setReset] = useState(false);
  const [tokenPanel, setTokenPanel] = useState(false);
  const [toast, setToast] = useState<{ text: string; error: boolean } | null>(
      null,
    ),
    [pending, setPending] = useState<{
      command: Command;
      requestId: string;
      summary: string;
      target: string;
    } | null>(null);
  const live = useMemo(() => (demo ? null : new ConsoleAPI(setState)), []);
  const character = state.characters.find((c) => c.id === characterId) ||
      (demo ? state.characters[0] : undefined) || {
        id: "",
        accountId: "",
        name: "选择角色",
        job: "",
        level: 0,
        online: null,
        gold: NaN,
        sp: NaN,
        tp: NaN,
      },
    nav = navigation.find((n) => n.id === section)!;
  useEffect(() => {
    document.title = "PurpleMagic · GM 工作台";
    const onHash = () => { live?.navigate(readSection()); setSection(readSection()); };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  useEffect(() => {
    if (demo) persistState(state);
  }, [state]);
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = preferences.theme;
    root.dataset.line = "90";
    root.dataset.background = preferences.background ? "on" : "off";
    root.dataset.density = preferences.compact ? "compact" : "normal";
    root.style.setProperty(
      "--glass-opacity",
      String(preferences.opacity / 100),
    );
    root.style.setProperty("--blur-glass", `${preferences.blur}px`);
    localStorage.setItem(preferenceKey, JSON.stringify(preferences));
  }, [preferences]);
  useEffect(() => {
    if (live) void live.boot();
  }, [live]);
  useEffect(() => {
    if (live?.meta.authenticated) void live.guard(() => live.enter(section));
  }, [live, section, state.api?.authenticated, characterId, character.id]);
  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(null), 4500);
    return () => clearTimeout(timer);
  }, [toast]);
  function go(id: Section) {
    live?.navigate(id);
    setSection(id);
    location.hash = id;
    setMobileNav(false);
  }
  function selectCharacter(id: string) {
    if (live) live.navigate(live.sectionId, true);
    if (live)
      void live.guard(async () => {
        await live.select(id);
      });
    setCharacterId(id);
    if (selection.accounts.length === 0 && selection.characters.length <= 1)
      setSelection({ accounts: [], characters: [id] });
    setPickCharacter(false);
  }
  function request(command: Command) {
    if (live) {
      void live.guard(() => live.prepare(command, selection));
      return;
    }
    try {
      const requestId = crypto.randomUUID(),
        preview = applyCommand(state, command, undefined, requestId)
          .receipts[0];
      setPending({
        command,
        requestId,
        summary: preview.summary,
        target: preview.target,
      });
    } catch (e) {
      setToast({ text: (e as Error).message, error: true });
    }
  }
  function confirm() {
    if (!pending) return;
    try {
      const next = applyCommand(
        state,
        pending.command,
        undefined,
        pending.requestId,
      );
      setState(next);
      setToast({ text: next.receipts[0].summary, error: false });
      setPending(null);
    } catch (e) {
      setToast({ text: (e as Error).message, error: true });
    }
  }
  function chooseSelection(value: Selection) {
    setSelection(value);
    if (live)
      void live.guard(async () => {
        await live.resolveSelection(value);
      });
  }
  const props: PageProps = {
    state,
    character,
    selection,
    setSelection: chooseSelection,
    selectCharacter,
    go,
    request,
    notify: (text) => setToast({ text, error: false }),
  };
  const currentRecipients =
    pending?.command.kind === "mail"
      ? state.characters.filter(
          (c) =>
            pending.command.kind === "mail" &&
            pending.command.recipients.includes(c.id),
        )
      : [];
  const noIdentity = ["query", "accounts", "settings", "quests", "benefits"].includes(section);
  if (live && !state.api?.authenticated) {
    return <LiveContext.Provider value={live}>
      <div className="app-shell auth-shell">
      <main className="auth-screen">
        <div className="auth-appearance"><IconButton label={preferences.theme === "dark" ? "切换浅色主题" : "切换深色主题"} onClick={() => setPreferences({ ...preferences, theme: preferences.theme === "dark" ? "light" : "dark" })}>
          {preferences.theme === "dark" ? <Sun size={19} /> : <Moon size={19} />}
        </IconButton></div>
        {state.api?.authReady ? <LoginPanel /> : <div className="auth-restoring" role="status"><LoaderCircle size={23} className="is-spinning" /><span>正在连接工作台</span></div>}
        {state.api?.error && <p className="auth-service-error inline-error" role="alert">{state.api.error}</p>}
        <small className="auth-caption">让每一次管理更从容</small>
      </main>
      </div>
    </LiveContext.Provider>;
  }
  return (
    <LiveContext.Provider value={live}>
      <div className="app-shell">
        {mobileNav && (
          <button
            className="nav-backdrop"
            aria-label="关闭导航"
            onClick={() => setMobileNav(false)}
          />
        )}
        <aside className={`sidebar ${mobileNav ? "open" : ""}`}>
          <a
            href="#inventory"
            className="brand"
            onClick={() => go("inventory")}
          >
            <img className="brand-image" src="./brand/witch.png" alt="" width="36" height="36" />
            <div>
              <strong>
                Purple<span>Magic</span>
              </strong>
              <small>阿拉德 · GM 工作台</small>
            </div>
          </a>
          <div className="sidebar-label">玩家管理</div>
          <nav aria-label="主要功能">
            {navigation.slice(0, 6).map((n) => (
              <a
                href={`#${n.id}`}
                aria-current={section === n.id ? "page" : undefined}
                key={n.id}
                className={section === n.id ? "active" : ""}
                onClick={() => go(n.id)}
              >
                <n.icon size={18} />
                {n.label}
                {section === n.id && (
                  <ChevronRight size={14} className="nav-indicator" />
                )}
              </a>
            ))}
          </nav>
          <div className="sidebar-label">运营配置</div>
          <nav aria-label="运营配置">
            <a
              href="#settings"
              className={section === "settings" ? "active" : ""}
              onClick={() => go("settings")}
            >
              <Megaphone size={18} />
              公告与下载源
            </a>
          </nav>
          <div className="sidebar-bottom">
            <div className="source-state">
              <span className="status online">
                <i />
                {demo
                  ? "本地预览 · 演示数据"
                  : state.api?.authenticated
                    ? "管理服务已连接"
                    : "等待登录"}
              </span>
            </div>
            <button
              className="appearance-entry"
              onClick={() => setAppearance(true)}
            >
              <Palette size={17} />
              <span>外观设置</span>
              <Settings2 size={15} />
            </button>
            {live && state.api?.authenticated && (
              <button
                className="token-entry"
                aria-label="管理访问 Token"
                onClick={() => setTokenPanel(true)}
              >
                <KeyRound size={18} />
                <span><strong>Token 已连接</strong><small>访问凭证</small></span>
                <ArrowRightLeft size={15} />
              </button>
            )}
            <small className="sidebar-copyright">让每一次管理更从容</small>
          </div>
        </aside>
        <div className="app-right">
          <header className="topbar">
            <div className="topbar-left">
              <IconButton
                label="打开导航"
                onClick={() => setMobileNav(!mobileNav)}
              >
                <PanelLeft size={18} />
              </IconButton>
              <span className="topbar-root">GM 工作台</span>
              <ChevronRight size={13} />
              <strong>{nav.label}</strong>
            </div>
            <div className="topbar-actions">
              {live && state.api?.pending?.confirmed && (
                <button className="secondary" onClick={() => live.openPending()}>
                  操作回执 · {state.api.pending.steps.filter((step) => step.done).length}/{state.api.pending.steps.length}
                </button>
              )}
              <span className="topbar-selected">
                {resolveRecipients(state, selection).length} 个已选角色
              </span>
              <IconButton
                label={
                  preferences.theme === "dark" ? "切换浅色主题" : "切换深色主题"
                }
                onClick={() =>
                  setPreferences({
                    ...preferences,
                    theme: preferences.theme === "dark" ? "light" : "dark",
                  })
                }
              >
                {preferences.theme === "dark" ? (
                  <Sun size={18} />
                ) : (
                  <Moon size={18} />
                )}
              </IconButton>
              <IconButton label="外观设置" onClick={() => setAppearance(true)}>
                <Settings2 size={18} />
              </IconButton>
              <button className="primary" onClick={() => go("mail")}>
                <Mail size={15} />
                发邮件
              </button>
            </div>
          </header>
          <main id="main-content">
            <div className="page-head">
              <div>
                <p className="eyebrow">PURPLEMAGIC / {section.toUpperCase()}</p>
                <div className="page-title-row">
                  <h1>{nav.title}</h1>
                  <span
                    className="page-loading"
                    data-loading={!!(state.api?.busy || !!state.api?.reading || !!state.api?.inventory.loading)}
                    role="status"
                    aria-label={(state.api?.busy || !!state.api?.reading || !!state.api?.inventory.loading) ? "正在读取" : undefined}
                    aria-hidden={!(state.api?.busy || !!state.api?.reading || !!state.api?.inventory.loading)}
                  >
                    <LoaderCircle size={20} className={(state.api?.busy || !!state.api?.reading || !!state.api?.inventory.loading) ? "is-spinning" : undefined} aria-hidden="true" />
                  </span>
                </div>
                <p>{nav.sub}</p>
              </div>
              {section !== "benefits" && <button
                className="secondary current-target"
                onClick={() => setPickCharacter(true)}
              >
                <Users size={15} />
                {character.name}
                <ChevronRight size={14} />
              </button>}
            </div>
            {!noIdentity &&
              character.id &&
              state.api?.authenticated !== false && (
                <Identity
                  state={state}
                  character={character}
                  onChoose={() => setPickCharacter(true)}
                />
              )}
            {state.api?.error && (
              <div className="glass live-status" role="alert">
                <span className="inline-error">{state.api.error}</span>
                <button
                  className="secondary"
                  onClick={() =>
                    void live?.guard(async () => {
                      await live.retry();
                    })
                  }
                >
                  重试
                </button>
              </div>
            )}
            <div key={`${section}-${section === "benefits" ? "global" : character.id}`} className="page-content">
              {live && !state.api?.authenticated ? (
                <LoginPanel />
              ) : live &&
                !live.current &&
                !["query", "accounts", "settings", "quests", "benefits"].includes(section) ? (
                <div className="glass content-panel">
                  <Empty title="选择一位角色开始管理" />
                  <button className="primary" onClick={() => go("query")}>
                    查询账号与角色
                  </button>
                </div>
              ) : (
                <>
                  {section === "inventory" ? (
                    <Inventory {...props} />
                  ) : section === "query" ? (
                    <QueryPage {...props} />
                  ) : section === "accounts" ? (
                    <Players {...props} accountMode />
                  ) : section === "mail" ? (
                    <MailPage {...props} />
                  ) : section === "quests" ? (
                    <QuestPage {...props} />
                  ) : section === "benefits" ? (
                    <BenefitsPage {...props} />
                  ) : section === "gm" ? (
                    <GmPage {...props} />
                  ) : (
                    <Settings {...props} />
                  )}
                </>
              )}
            </div>
            <footer className="page-footer">
              <span>PurpleMagic</span>
              <span>外部 GM · 玩家管理工作台</span>
            </footer>
          </main>
        </div>
        {toast && (
          <div
            className={`toast ${toast.error ? "error" : ""}`}
            role={toast.error ? "alert" : "status"}
          >
            {toast.error ? (
              <AlertCircle size={18} />
            ) : (
              <CheckCircle2 size={18} />
            )}
            <span>{toast.text}</span>
            <button aria-label="关闭提示" onClick={() => setToast(null)}>
              <X size={15} />
            </button>
          </div>
        )}
        {pickCharacter && (
          <Modal
            title="切换当前角色"
            wide
            onClose={() => setPickCharacter(false)}
          >
            <PlayerPicker
              state={state}
              selection={selection}
              setSelection={chooseSelection}
              pickOnly
              onChoose={selectCharacter}
            />
          </Modal>
        )}
        {state.api?.pending && !state.api.pending.hidden && <LiveConfirmation pending={state.api.pending} />}
        {tokenPanel && live && <Modal title="访问 Token" onClose={() => setTokenPanel(false)} footer={<>
          <button className="secondary" onClick={() => setTokenPanel(false)}>保持连接</button>
          <button className="primary" onClick={() => void live.guard(async () => {
            await live.logout(); setTokenPanel(false); setCharacterId(""); setSelection({ accounts: [], characters: [] });
          })}><ArrowRightLeft size={15} />更换 Token</button>
        </>}><p>清除本机的连接记忆，然后输入新的 Token。</p></Modal>}
        {pending && (
          <Modal
            title={
              pending.command.kind === "mail" ? "确认发送邮件" : "确认本次修改"
            }
            onClose={() => setPending(null)}
            footer={
              <>
                <button className="secondary" onClick={() => setPending(null)}>
                  取消
                </button>
                <button className="primary" onClick={confirm}>
                  {pending.command.kind === "mail" ? "确认发送" : "确认执行"}
                </button>
              </>
            }
          >
            <p className="confirm-target">{pending.target}</p>
            <p>
              {pending.summary
                .replace(/^已/, "")
                .replace("已自动发放", "自动发放")
                .replace("已自动检查", "自动检查")}
            </p>
            {pending.command.kind === "item" && (
              <dl className="detail-list">
                {Object.entries(pending.command.patch).map(([k, v]) => (
                  <div key={k}>
                    <dt>
                      {
                        {
                          count: "数量",
                          enhance: "强化",
                          amplify: "增幅",
                          forge: "锻造",
                          bound: "绑定",
                        }[k]
                      }
                    </dt>
                    <dd>{typeof v === "boolean" ? (v ? "是" : "否") : v}</dd>
                  </div>
                ))}
              </dl>
            )}
            {pending.command.kind === "mail" && (
              <>
                <dl className="detail-list">
                  <div>
                    <dt>标题</dt>
                    <dd>{pending.command.title}</dd>
                  </div>
                  <div>
                    <dt>每角色金币</dt>
                    <dd>{number(pending.command.gold)}</dd>
                  </div>
                  {pending.command.items.map((i) => (
                    <div key={i.id}>
                      <dt>{i.name}</dt>
                      <dd>× {number(i.count)}</dd>
                    </div>
                  ))}
                </dl>
                <h3>最终收件名单 · {currentRecipients.length} 人</h3>
                <ul className="confirmation-recipients">
                  {currentRecipients.map((c) => (
                    <li key={c.id}>
                      <strong>{c.name}</strong>
                      <span>
                        Lv.{c.level} ·{" "}
                        {state.accounts.find((a) => a.id === c.accountId)?.name}
                      </span>
                    </li>
                  ))}
                </ul>
              </>
            )}
            {pending.command.kind === "growth" && (
              <p className="confirm-values">
                Lv.{pending.command.level} · {pending.command.job} · SP{" "}
                {pending.command.sp} · TP {pending.command.tp}
              </p>
            )}
            {pending.command.kind === "currency" && (
              <dl className="detail-list">
                <div>
                  <dt>金币</dt>
                  <dd>{number(pending.command.gold)}</dd>
                </div>
                <div>
                  <dt>点券</dt>
                  <dd>{number(pending.command.points)}</dd>
                </div>
                <div>
                  <dt>代币券</dt>
                  <dd>{number(pending.command.tokens)}</dd>
                </div>
              </dl>
            )}
            {pending.command.kind === "delete-items" && (
              <ul className="confirmation-recipients">
                {state.items
                  .filter(
                    (i) =>
                      pending.command.kind === "delete-items" &&
                      pending.command.keys.includes(i.key),
                  )
                  .map((i) => (
                    <li key={i.key}>
                      {i.name}
                      <span>× {number(i.count)}</span>
                    </li>
                  ))}
              </ul>
            )}
          </Modal>
        )}
        {appearance && (
          <Modal
            title="外观设置"
            onClose={() => setAppearance(false)}
            footer={
              <button className="primary" onClick={() => setAppearance(false)}>
                完成
              </button>
            }
          >
            <div className="theme-choices">
              <button
                className={preferences.theme === "dark" ? "active" : ""}
                onClick={() =>
                  setPreferences({ ...preferences, theme: "dark" })
                }
              >
                <Moon size={22} />
                <strong>兰紫夜幕</strong>
                <span>深色 · 粉紫微光</span>
              </button>
              <button
                className={preferences.theme === "light" ? "active" : ""}
                onClick={() =>
                  setPreferences({ ...preferences, theme: "light" })
                }
              >
                <Sun size={22} />
                <strong>兰紫晨光</strong>
                <span>浅色 · 淡紫通透</span>
              </button>
            </div>
            <Field label={`面板不透明度 · ${preferences.opacity}%`}>
              <input
                aria-label="面板不透明度"
                type="range"
                min="50"
                max="95"
                value={preferences.opacity}
                onChange={(e) =>
                  setPreferences({
                    ...preferences,
                    opacity: Number(e.target.value),
                  })
                }
              />
            </Field>
            <Field label={`毛玻璃模糊 · ${preferences.blur}px`}>
              <input
                aria-label="毛玻璃模糊"
                type="range"
                min="0"
                max="32"
                value={preferences.blur}
                onChange={(e) =>
                  setPreferences({
                    ...preferences,
                    blur: Number(e.target.value),
                  })
                }
              />
            </Field>
            <label className="checkline">
              <CheckBox
                label="显示背景氛围"
                checked={preferences.background}
                onChange={() =>
                  setPreferences({
                    ...preferences,
                    background: !preferences.background,
                  })
                }
              />
              显示背景氛围
            </label>
            <label className="checkline">
              <CheckBox
                label="紧凑列表"
                checked={preferences.compact}
                onChange={() =>
                  setPreferences({
                    ...preferences,
                    compact: !preferences.compact,
                  })
                }
              />
              紧凑列表
            </label>
            <div className="form-divider" />
            {demo && (
              <button
                className="text-button muted"
                onClick={() => {
                  setAppearance(false);
                  setReset(true);
                }}
              >
                <RotateCcw size={15} />
                重置演示数据
              </button>
            )}
          </Modal>
        )}
        {reset && (
          <Modal
            title="重置演示数据"
            onClose={() => setReset(false)}
            footer={
              <>
                <button className="secondary" onClick={() => setReset(false)}>
                  取消
                </button>
                <button
                  className="primary"
                  onClick={() => {
                    setState(initialState());
                    setSelection({ accounts: [], characters: ["1"] });
                    setCharacterId("1");
                    setReset(false);
                    setToast({ text: "演示数据已重置", error: false });
                  }}
                >
                  重置
                </button>
              </>
            }
          >
            <p>恢复示例账号、物品与邮件，并清空本地操作记录。</p>
          </Modal>
        )}
      </div>
    </LiveContext.Provider>
  );
}
