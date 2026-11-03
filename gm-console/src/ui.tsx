import { getLocale } from "./i18n";
import { t } from "./i18n";
import { useEffect, useLayoutEffect, useState, useRef, type ReactNode } from "react";
import { X, Search, Inbox, ChevronDown, Check } from "lucide-react";
import type { State, Character, Command, Selection, Section } from "./domain";

export type PageProps = {
  state: State;
  character: Character;
  selection: Selection;
  setSelection: (s: Selection) => void;
  selectCharacter: (id: string) => void;
  request: (command: Command) => void;
  notify: (message: string) => void;
  go: (section: Section) => void;
};
export const number = (value: number | string) =>
  typeof value === "string" && /^-?\d+$/.test(value)
    ? BigInt(value).toLocaleString(getLocale())
    : typeof value === "number" && Number.isFinite(value)
      ? value.toLocaleString(getLocale())
      : "—";
export const timestamp = (value: string) =>
  new Date(value).toLocaleString(getLocale(), {
    timeZone: "Asia/Shanghai",
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
      aria-label={t(label)}
      title={t(label)}
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
  return <span className={`pill ${tone}`}>{typeof children === "string" ? t(children) : children}</span>;
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
        aria-label={t(label || placeholder)}
        placeholder={t(placeholder)}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      {value && (
        <button aria-label={t("清空搜索")} onClick={() => onChange("")}>
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
      <div ref={ref} className="tabs" role="tablist" aria-label={t(label)}>
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
          {t(i.label)}
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
      <span>{t(label)}</span>
      {children}
      {hint && <small>{t(hint)}</small>}
    </label>
  );
}
export function Empty({ title, detail }: { title: string; detail?: string }) {
  return (
    <div className="empty">
      <Inbox size={30} />
      <strong>{t(title)}</strong>
      {detail && <p>{t(detail)}</p>}
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
      aria-label={t(label)}
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
      aria-label={t(title)}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (dismissOnBackdrop && e.target === ref.current) onClose();
      }}
    >
      <header>
        <h2>{t(title)}</h2>
        <IconButton label={t("关闭")} onClick={onClose}>
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
}: {
  state: State;
  character: Character;
  onChoose: () => void;
}) {
  const a = state.accounts.find((a) => a.id === character.accountId)!;
  return (
    <div className="identity glass">
      <button className="identity-person" onClick={onChoose}>
        <span className="identity-mark">
          <Check size={18} />
        </span>
        <div>
          <strong>{character.name}</strong>
          <span>
            Lv.{character.level} · {t(character.job)}
          </span>
        </div>
        <ChevronDown size={16} />
      </button>
      <span className={`status ${character.online ? "online" : ""}`}>
        <i />
        {character.online === null
          ? t("状态未知")
          : character.online
            ? t("在线")
            : t("离线")}
      </span>
      <div className="identity-data">
        <span>
          {t("账号")} <b>{a.name}</b>
        </span>
        <span>
          {t("角色 ID")} <b>{character.id}</b>
        </span>
        <span>
          {t("金币")} <b>{number(character.gold)}</b>
        </span>
        <span>
          {t("点券")} <b>{number(a.rawPoints ?? a.points)}</b>
        </span>
      </div>
      <button className="subtle" onClick={onChoose}>
        {t("切换角色")}
      </button>
    </div>
  );
}
