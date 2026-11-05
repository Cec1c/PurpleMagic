import { useEffect, useState } from "react";
import { Atom, LoaderCircle, Settings2 } from "lucide-react";
import { useLive } from "../api/context";
import type { PageProps } from "../ui";

export function SystemSettings({ state }: PageProps) {
  const live = useLive();
  const [demoMode, setDemoMode] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const supported = !live || !!state.api?.features["system.settings"];
  const policy = state.api?.systemSettings;
  const enabled = live ? !!policy?.carbonMode : demoMode;
  useEffect(() => {
    if (live && supported) void live.guard(() => live.systemSettings());
    return () => live?.cancelScope("systemSettings");
  }, [live, supported]);
  async function change(enabled: boolean) {
    setSaving(true); setMessage("");
    try {
      if (live) await live.systemSettings(enabled);
      else setDemoMode(enabled);
      setMessage(enabled ? "碳铀模式已开启，全局生效" : "碳铀模式已关闭");
    } catch (e) { setMessage((e as Error).message); }
    finally { setSaving(false); }
  }
  return <div className="form-layout">
    <section className="glass content-panel">
      <div className="panel-heading"><h2><Settings2 size={18} /> 系统设置</h2></div>
      <div className="form-section">
        <h3><Atom size={18} /> 碳铀模式</h3>
        <p>开启后，角色在线时也可修改物品、任务、名字、成长、金币、邮件和仓库。保存后对所有 GM 会话生效。</p>
        <p className="muted">直接写入数据库，不踢下线。游戏内存不会同步刷新，后续游戏存档可能覆盖修改。</p>
        <label className="checkline">
          <input className="checkbox" type="checkbox" role="switch" aria-label="碳铀模式"
            checked={enabled} disabled={!supported || saving || (!!live && !policy)}
            onChange={e => void change(e.target.checked)} />
          {enabled ? "已开启" : "已关闭"}
          {saving && <LoaderCircle size={16} className="spin" />}
        </label>
        {!supported && <p className="muted">更新管理网关后可使用系统设置。</p>}
        {message && <p role="status">{message}</p>}
      </div>
    </section>
  </div>;
}
