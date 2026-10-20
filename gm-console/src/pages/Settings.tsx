import { useEnterMotion } from "../motion";
import { useEffect, useState } from "react";
import {
  Megaphone,
  Download,
  Save,
  Send,
  Plus,
  Server,
  Globe,
  FileText,
} from "lucide-react";
import { Tabs, Field, Pill, type PageProps } from "../ui";
import { HubLogin } from "../api/controls";
import { useLive } from "../api/context";
function HubSettings({
  state,
  request,
  tab,
}: PageProps & { tab: "news" | "source" }) {
  const live = useLive();
  const sources = state.api?.hub?.sources || [];
  const [sourceId, setSourceId] = useState(sources[0]?.id || "");
  const source = sources.find((s: any) => s.id === sourceId);
  const [sourceName, setSourceName] = useState(source?.name || ""),
    [publicKey, setPublicKey] = useState(source?.publicKey || ""),
    [certificate, setCertificate] = useState(source?.certificateSha256 || ""),
    [sourceEnabled, setSourceEnabled] = useState(source?.enabled ?? true);
  const [newsId, setNewsId] = useState(state.news[0]?.id || ""),
    [title, setTitle] = useState(state.news[0]?.title || ""),
    [body, setBody] = useState(state.news[0]?.body || "");
  const [download, setDownload] = useState(state.sources.download),
    [gameHost, setGameHost] = useState(state.sources.gameHost),
    [gamePort, setGamePort] = useState(state.sources.gamePort);
  function chooseSource(id: string) {
    const s = sources.find((s: any) => s.id === id);
    setSourceId(id);
    setSourceName(s?.name || "");
    setDownload(s?.endpoint || "");
    setPublicKey(s?.publicKey || "");
    setCertificate(s?.certificateSha256 || "");
    setSourceEnabled(s?.enabled ?? true);
  }
  useEffect(() => {
    if (title.trim()) {
      const saved = state.news.find(
        (n) =>
          n.title === title && n.body === body && (live ? !n.published : true),
      );
      if (saved) setNewsId(saved.id);
    }
  }, [state.news, newsId, title, body]);
  function choose(id: string) {
    const n = state.news.find((n) => n.id === id);
    setNewsId(id);
    setTitle(n?.title || "");
    setBody(n?.body || "");
  }
  const motion = useEnterMotion(tab);
  return (
    <div ref={motion} className="motion-section">
      {tab === "news" ? (
        <div className="news-layout">
          <aside className="glass content-panel news-list">
            <div className="panel-heading">
              <h3>公告列表</h3>
              <button
                className="icon-button"
                aria-label="新建公告"
                onClick={() => choose("")}
              >
                <Plus size={18} />
              </button>
            </div>
            {state.news.map((n) => (
              <button
                key={n.id}
                className={n.id === newsId ? "active" : ""}
                onClick={() => choose(n.id)}
              >
                <FileText size={17} />
                <span>
                  <strong>{n.title}</strong>
                  <small>{n.published ? "已发布" : "草稿"}</small>
                </span>
              </button>
            ))}
          </aside>
          <form
            className="glass content-panel"
            onSubmit={(e) => {
              e.preventDefault();
              request({
                kind: "news",
                id: newsId,
                title,
                body,
                published: false,
              });
            }}
          >
            <div className="panel-heading">
              <h2>
                <Megaphone size={20} />
                {newsId ? "编辑公告" : "新建公告"}
              </h2>
              <Pill>
                {state.news.find((n) => n.id === newsId)?.published
                  ? "已发布"
                  : "草稿"}
              </Pill>
            </div>
            <Field label="标题">
              <input
                required
                maxLength={100}
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="公告标题"
              />
            </Field>
            <Field label="正文">
              <textarea
                required
                rows={10}
                value={body}
                onChange={(e) => setBody(e.target.value)}
                placeholder="输入公告内容"
              />
            </Field>
            <div className="form-footer">
              <span>显示于登录器公告区</span>
              <div className="button-row">
                <button className="secondary">
                  <Save size={15} />
                  保存草稿
                </button>
                <button
                  type="button"
                  className="primary"
                  disabled={!title.trim() || !body.trim()}
                  onClick={() =>
                    request({
                      kind: "news",
                      id: newsId,
                      title,
                      body,
                      published: true,
                    })
                  }
                >
                  <Send size={15} />
                  发布公告
                </button>
              </div>
            </div>
          </form>
        </div>
      ) : (
        <div className="form-layout">
          <form
            className="glass content-panel"
            onSubmit={(e) => {
              e.preventDefault();
              request({
                kind: "sources",
                download,
                gameHost,
                gamePort,
                ...(live && download
                  ? {
                      source: {
                        id: sourceId,
                        name: sourceName,
                        endpoint: download,
                        publicKey,
                        certificateSha256: certificate,
                        enabled: sourceEnabled,
                      },
                    }
                  : {}),
              });
            }}
          >
            <h2>
              <Download size={20} />
              下载源
            </h2>
            {live && (
              <>
                <Field label="下载源列表">
                  <select
                    value={source?.id || ""}
                    onChange={(e) => chooseSource(e.target.value)}
                  >
                    <option value="">新增下载源</option>
                    {sources.map((s: any) => (
                      <option value={s.id} key={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                </Field>
                <div className="field-grid">
                  <Field label="来源标识">
                    <input
                      value={sourceId}
                      disabled={!!source}
                      pattern="[a-z0-9][a-z0-9-]{0,47}"
                      onChange={(e) => setSourceId(e.target.value)}
                      placeholder="primary"
                    />
                  </Field>
                  <Field label="来源名称">
                    <input
                      value={sourceName}
                      onChange={(e) => setSourceName(e.target.value)}
                      placeholder="主下载源"
                    />
                  </Field>
                </div>
              </>
            )}
            <Field label="更新资源地址">
              <input
                required={!live}
                type="url"
                value={download}
                onChange={(e) => setDownload(e.target.value)}
                placeholder="https://downloads.example.com"
              />
            </Field>
            {live && (
              <details className="source-trust">
                <summary>签名与证书配置</summary>
                <Field label="下载源签名公钥">
                  <textarea
                    rows={5}
                    value={publicKey}
                    onChange={(e) => setPublicKey(e.target.value)}
                    placeholder="-----BEGIN PUBLIC KEY-----"
                  />
                </Field>
                <Field label="证书 SHA256（可选）">
                  <input
                    value={certificate}
                    onChange={(e) => setCertificate(e.target.value)}
                  />
                </Field>
                <label className="checkline">
                  <input
                    type="checkbox"
                    checked={sourceEnabled}
                    onChange={(e) => setSourceEnabled(e.target.checked)}
                  />
                  启用此来源
                </label>
              </details>
            )}
            <div className="form-divider" />
            <h2>
              <Server size={20} />
              游戏连接
            </h2>
            <div className="field-grid">
              <Field label="游戏地址">
                <input
                  required
                  value={gameHost}
                  onChange={(e) => setGameHost(e.target.value)}
                  placeholder="game.example.com"
                />
              </Field>
              <Field label="端口">
                <input
                  type="number"
                  min="1"
                  max="65535"
                  value={gamePort}
                  onChange={(e) => setGamePort(Number(e.target.value))}
                />
              </Field>
            </div>
            <div className="form-footer">
              <span>保存来源；游戏连接发布到登录器配置</span>
              <button className="primary">
                <Save size={15} />
                保存配置
              </button>
            </div>
          </form>
          <aside className="glass content-panel">
            <h3>
              <Globe size={18} />
              当前配置
            </h3>
            <dl className="detail-list stacked">
              <div>
                <dt>下载源</dt>
                <dd>{state.sources.download}</dd>
              </div>
              <div>
                <dt>游戏地址</dt>
                <dd>
                  {state.sources.gameHost}:{state.sources.gamePort}
                </dd>
              </div>
            </dl>
          </aside>
        </div>
      )}
    </div>
  );
}

export function Settings(props: PageProps) {
  const live = useLive();
  const [tab, setTab] = useState<"news" | "source" | "pvf">("news");
  useEffect(() => {
    if (!live || tab === "pvf" || live.meta.hub) return;
    void live.guard(() => live.hub());
    return () => live.cancelScope("hub");
  }, [live, tab]);
  return (
    <>
      <div className="section-tabs">
        <Tabs
          label="公告与连接设置"
          items={[
            { id: "news", label: "公告管理" },
            { id: "source", label: "下载源与游戏连接" },
            ...(live ? [{ id: "pvf" as const, label: "PVF 数据模式" }] : []),
          ]}
          value={tab}
          onChange={(value) => {
            live?.navigate("settings", true);
            setTab(value);
          }}
        />
      </div>
      {tab === "pvf" ? (
        <PVFSettings {...props} />
      ) : live && !props.state.api?.hub ? (
        <HubLogin />
      ) : (
        <HubSettings {...props} tab={tab} />
      )}
    </>
  );
}
function PVFSettings({ state }: PageProps) {
  const live = useLive()!;
  const mode = state.api?.resources.pvfMode;
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    void live.guard(() => live.pvfMode());
    return () => live.cancelScope("pvfMode");
  }, [live]);
  useEffect(() => {
    if (!mode?.loading) return;
    const timer = setTimeout(() => void live.guard(() => live.pvfMode()), 1500);
    return () => clearTimeout(timer);
  }, [live, mode]);
  const change = (value: "disk" | "memory") => {
    setSaving(true);
    void live.guard(async () => {
      try {
        await live.pvfMode(value);
      } finally {
        setSaving(false);
      }
    });
  };
  return (
    <section className="glass content-panel">
      <h3>PVF 数据模式</h3>
      <p>
        磁盘索引使用较少内存。直接加载 PVF
        将完整文件与目录放入内存，加快物品、任务等定义的读取。
      </p>
      <p>
        直接加载很快，但内存开销较高：完整文件约{" "}
        {Math.ceil((mode?.archiveBytes || 0) / 1048576)} MiB，另需目录和最多 96
        MiB 的解码缓存。
      </p>
      <div className="button-row">
        <button
          className={mode?.desired === "disk" ? "primary" : "secondary"}
          disabled={saving || !mode}
          onClick={() => change("disk")}
        >
          磁盘索引
        </button>
        <button
          className={mode?.desired === "memory" ? "primary" : "secondary"}
          disabled={saving || !mode || !!mode.loading}
          onClick={() => change("memory")}
        >
          直接加载 PVF
        </button>
        <button
          className="secondary"
          onClick={() => void live.guard(() => live.pvfMode())}
        >
          刷新状态
        </button>
      </div>
      {mode && (
        <p role="status">
          当前使用：{mode.mode === "memory" ? "内存 PVF" : "磁盘索引"}
          {mode.loading ? " · 正在后台加载 PVF" : ""}
        </p>
      )}
      {mode?.error && (
        <p className="inline-error" role="alert">
          {mode.error}
        </p>
      )}
      <p className="muted">
        设置自动保存。加载期间继续使用磁盘索引，切回后释放内存归档。
      </p>
      {mode && (
        <small className="muted" style={{ overflowWrap: "anywhere" }}>
          PVF {mode.pvfChecksum}
        </small>
      )}
    </section>
  );
}
