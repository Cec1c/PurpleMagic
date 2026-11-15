import { useEffect, useLayoutEffect, useState, useRef, type ReactNode } from "react";
import { X, Search, Inbox, ChevronDown, Check, Mail, Users } from "lucide-react";
import type { State, Character, Command, Selection, Section } from "./domain";
import { useLive } from "./api/context";

export type PageProps = {
  state: State;
  character: Character;
  selection: Selection;
  setSelection: (s: Selection) => void;
  selectCharacter: (id: string) => void;
  request: (command: Command) => void;
  notify: (message: string) => void;
  go: (section: Section) => void;
  openAccount?: (accountId: string, accountName?: string) => void;
  openCharacterPicker?: () => void;
  mailComposeTick?: number;
};
export const number = (value: number | string) =>
  typeof value === "string" && /^-?\d+$/.test(value)
    ? BigInt(value).toLocaleString("zh-CN")
    : typeof value === "number" && Number.isFinite(value)
      ? value.toLocaleString("zh-CN")
      : "—";
export const timestamp = (value: string) =>
  new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
export function IconButton({
  label,
  children,
  onClick,
}: {
  label: string;
  children: ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      className="icon-button"
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      {children}
    </button>
  );
}
export function Pill({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: string;
}) {
  return <span className={`pill ${tone}`}>{children}</span>;
}
export function SearchField({
  value,
  onChange,
  placeholder = "搜索",
  label,
}: {
  value: string;
  onChange: (s: string) => void;
  placeholder?: string;
  label?: string;
}) {
  return (
    <label className="search-field">
      <Search size={16} />
      <input
        aria-label={label || placeholder}
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      {value && (
        <button aria-label="清空搜索" onClick={() => onChange("")}>
          <X size={14} />
        </button>
      )}
    </label>
  );
}
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  label,
}: {
  items: { id: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
  label: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [indicator, setIndicator] = useState({ left: 0, width: 0 });
  const labels = items.map((item) => item.label).join("|");
  useLayoutEffect(() => {
    const root = ref.current!;
    const update = () => {
      const active = root.querySelector<HTMLButtonElement>('[aria-selected="true"]');
      if (active) setIndicator({ left: active.offsetLeft, width: active.offsetWidth });
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(root);
    root.querySelectorAll("button").forEach((button) => observer.observe(button));
    return () => observer.disconnect();
  }, [value, labels]);
  return (
    <div ref={ref} className="tabs" role="tablist" aria-label={label}>
      <span className="tabs-indicator" aria-hidden="true" style={{ transform: `translateX(${indicator.left}px)`, width: indicator.width, visibility: indicator.width ? "visible" : "hidden" }} />
      {items.map((i) => (
        <button
          type="button"
          role="tab"
          aria-selected={i.id === value}
          className={i.id === value ? "active" : ""}
          key={i.id}
          onClick={() => onChange(i.id)}
        >
          {i.label}
        </button>
      ))}
    </div>
  );
}
export function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {hint && <small>{hint}</small>}
    </label>
  );
}
export function Empty({ title, detail }: { title: string; detail?: string }) {
  return (
    <div className="empty">
      <Inbox size={30} />
      <strong>{title}</strong>
      {detail && <p>{detail}</p>}
    </div>
  );
}
export function CheckBox({
  checked,
  onChange,
  label,
  disabled = false,
}: {
  disabled?: boolean;
  checked: boolean;
  onChange: () => void;
  label: string;
}) {
  return (
    <input
      aria-label={label}
      className="checkbox"
      type="checkbox"
      checked={checked}
      disabled={disabled}
      onChange={onChange}
    />
  );
}
export function toggle(list: string[], id: string) {
  return list.includes(id) ? list.filter((x) => x !== id) : [...list, id];
}
export function Modal({
  title,
  children,
  onClose,
  footer,
  wide = false,
  dismissOnBackdrop = true,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  footer?: ReactNode;
  wide?: boolean;
  dismissOnBackdrop?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const el = ref.current!;
    const previous = document.activeElement as HTMLElement;
    el.showModal();
    return () => {
      el.close();
      previous?.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={`modal ${wide ? "wide" : ""}`}
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (dismissOnBackdrop && e.target === ref.current) onClose();
      }}
    >
      <header>
        <h2>{title}</h2>
        <IconButton label="关闭" onClick={onClose}>
          <X size={19} />
        </IconButton>
      </header>
      <div className="modal-body">{children}</div>
      {footer && <footer>{footer}</footer>}
    </dialog>
  );
}
export function Identity({
  state,
  character,
  onChoose,
  onSelectCharacter,
  onQuickMail,
  onManageAccount,
}: {
  state: State;
  character: Character;
  onChoose: () => void;
  onSelectCharacter?: (id: string) => void;
  onQuickMail?: () => void;
  onManageAccount?: (accountId: string, accountName?: string) => void;
}) {
  const live = useLive();
  const a = state.accounts.find((x) => x.id === character.accountId) || {
    id: character.accountId,
    name: character.accountId,
    points: 0,
    tokens: 0,
    status: "正常" as const,
    rawPoints: undefined,
  };
  const [siblingsOpen, setSiblingsOpen] = useState(false);
  const [siblings, setSiblings] = useState<Character[]>([]);
  const [loadingSiblings, setLoadingSiblings] = useState(false);
  const popoverRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setSiblingsOpen(false);
  }, [character.id]);

  useEffect(() => {
    if (!siblingsOpen) return;
    if (!live) {
      setSiblings(state.characters.filter((c) => c.accountId === character.accountId));
      return;
    }
    const ctrl = new AbortController();
    setLoadingSiblings(true);
    void live
      .accountCharacters(character.accountId, ctrl.signal)
      .then((list) => {
        if (!ctrl.signal.aborted) setSiblings(list);
      })
      .catch(() => {})
      .finally(() => {
        if (!ctrl.signal.aborted) setLoadingSiblings(false);
      });
    return () => ctrl.abort();
  }, [siblingsOpen, live, character.accountId, state.characters]);

  useEffect(() => {
    if (!siblingsOpen) return;
    const onDown = (e: MouseEvent) => {
      if (popoverRef.current && !popoverRef.current.contains(e.target as Node)) {
        setSiblingsOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setSiblingsOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [siblingsOpen]);

  const knownSiblings = siblings.length
    ? siblings
    : state.characters.filter((c) => c.accountId === character.accountId);

  return (
    <div className="identity glass">
      <button className="identity-person" onClick={onChoose} title="切换当前角色 (Ctrl+K)">
        <span className="identity-mark">
          <Check size={18} />
        </span>
        <div>
          <strong>{character.name}</strong>
          <span>
            Lv.{character.level} · {character.job}
          </span>
        </div>
        <ChevronDown size={16} />
      </button>
      <span className={`status ${character.online ? "online" : ""}`}>
        <i />
        {character.online === null
          ? "状态未知"
          : character.online
            ? "在线"
            : "离线"}
      </span>
      <div className="identity-data">
        <span className="identity-account-item">
          账号{" "}
          {onManageAccount ? (
            <button
              type="button"
              className="identity-account-btn"
              title="打开账号管理面板"
              onClick={() => onManageAccount(a.id, a.name)}
            >
              <b>{a.name}</b>
            </button>
          ) : (
            <b>{a.name}</b>
          )}
          {a.status === "限制登录" && <Pill tone="orange">限制登录</Pill>}
        </span>
        {onSelectCharacter && character.accountId && (
          <div className="identity-siblings-wrap" ref={popoverRef}>
            <button
              type="button"
              className="identity-siblings-trigger"
              aria-expanded={siblingsOpen}
              onClick={() => setSiblingsOpen((v) => !v)}
            >
              <Users size={13} />
              同账号角色
              <ChevronDown size={13} />
            </button>
            {siblingsOpen && (
              <div className="identity-siblings-popover glass" role="menu" aria-label="同账号角色">
                <div className="identity-siblings-head">
                  <span>账号 {a.name} 下的角色</span>
                  {onManageAccount && (
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => {
                        setSiblingsOpen(false);
                        onManageAccount(a.id, a.name);
                      }}
                    >
                      管理账号
                    </button>
                  )}
                </div>
                {loadingSiblings && !knownSiblings.length ? (
                  <p className="muted identity-siblings-empty">正在读取角色列表…</p>
                ) : knownSiblings.length === 0 ? (
                  <p className="muted identity-siblings-empty">暂无角色</p>
                ) : (
                  <div className="identity-siblings-list">
                    {knownSiblings.map((c) => {
                      const isCurrent = c.id === character.id;
                      return (
                        <button
                          key={c.id}
                          type="button"
                          role="menuitem"
                          className={`identity-sibling-row ${isCurrent ? "current" : ""}`}
                          onClick={() => {
                            setSiblingsOpen(false);
                            if (!isCurrent) onSelectCharacter(c.id);
                          }}
                        >
                          <i className={`sibling-dot ${c.online ? "online" : ""}`} />
                          <div className="sibling-main">
                            <strong>{c.name}</strong>
                            <small>
                              Lv.{c.level} · {c.job}
                            </small>
                          </div>
                          {isCurrent && <Check size={14} className="sibling-check" />}
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
        <span>
          角色 ID <b>{character.id}</b>
        </span>
        <span>
          金币 <b>{number(character.gold)}</b>
        </span>
        <span>
          点券 <b>{number(a.rawPoints ?? a.points)}</b>
        </span>
      </div>
      <div className="identity-actions">
        {onQuickMail && (
          <button
            type="button"
            className="secondary identity-mail-btn"
            title="仅以当前角色为收件人发送邮件"
            onClick={onQuickMail}
          >
            <Mail size={14} />
            发邮件
          </button>
        )}
        <button type="button" className="subtle" onClick={onChoose}>
          切换角色
        </button>
      </div>
    </div>
  );
}
