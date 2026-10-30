import { Fragment, useEffect, useMemo, useState } from "react";
import {
  Activity,
  ArrowUpRight,
  CalendarDays,
  CircleGauge,
  Footprints,
  Gauge,
  Gift,
  Megaphone,
  RefreshCw,
  ShieldCheck,
  Swords,
  Users,
  UserPlus,
  LibraryBig,
} from "lucide-react";
import { useLive } from "../api/context";
import { useEnterMotion } from "../motion";
import { Modal, type PageProps } from "../ui";
import { ItemLibrary } from "./Query";
import { DashboardClient, type DashboardResult } from "../dashboard/client";
import {
  dashboardLayout,
  heatLevel,
  heatmapCells,
  visibleMetrics,
  type DashboardSnapshot,
} from "../dashboard/model";
import "../dashboard/dashboard.css";

const icons = {
  users: Users,
  online: Activity,
  new: UserPlus,
  activity: CalendarDays,
  fatigue: Footprints,
  clear: Swords,
  speed: Gauge,
};
const format = (n: number) => n.toLocaleString("zh-CN");
const clock = (date: string) =>
  new Date(date).toLocaleTimeString("zh-CN", {
    timeZone: "Asia/Shanghai",
    hour12: false,
  });
function demoSnapshot(): DashboardSnapshot {
  const now = new Date(),
    end = new Date(
      Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()),
    );
  return {
    timezone: "Asia/Shanghai",
    observedAt: now.toISOString(),
    fastThresholdMs: 30000,
    fatigueSamples: 1286,
    metrics: {
      accounts: 2864,
      characters: 5932,
      online: 128,
      registrationsToday: 36,
      dailyActive: 486,
      weeklyActive: 1240,
      fatigueToday: 7926,
      clearsToday: 1286,
      fastClearsToday: 12,
    },
    registrations: Array.from({ length: 364 }, (_, i) => ({
      date: new Date(end.getTime() - (363 - i) * 86400000)
        .toISOString()
        .slice(0, 10),
      count: (i * 13 + Math.floor(i / 7) * 3) % 37,
    })),
  };
}

export function Dashboard(props: PageProps) {
  const live = useLive();
  const client = useMemo(
    () => (live ? new DashboardClient(live.http) : null),
    [live],
  );
  const [data, setData] = useState<DashboardResult>(() =>
    live
      ? { failures: [] }
      : {
          snapshot: demoSnapshot(),
          news: [
            {
              title: "冒险不停，福利常在",
              body: "新角色进入游戏后，符合条件的福利将自动送达。",
              date: "2026-10-31",
              category: "综合",
            },
            {
              title: "本周游戏活动",
              body: "查看活动管理，了解正在进行的全服活动。",
              date: "2026-10-30",
              category: "更新",
            },
            {
              title: "反馈与帮助",
              body: "遇到问题时，请记录角色、时间与操作步骤。",
              date: "2026-10-29",
              category: "指南",
            },
          ],
          failures: [],
        },
  );
  const [loading, setLoading] = useState(!!live),
    [revision, setRevision] = useState(0),
    [library, setLibrary] = useState(false);
  const refreshRevision = props.state.api?.resources.dashboardRefresh?.revision;
  const motion = useEnterMotion("dashboard");
  useEffect(() => {
    if (!client || !live) return;
    let disposed = false,
      running = false;
    const controller = new AbortController();
    const load = async () => {
      if (running || document.hidden) return;
      running = true;
      setLoading(true);
      try {
        const next = await client.load(controller.signal);
        if (!disposed)
          setData((previous) => ({
            ...next,
            snapshot:
              next.failures.includes("统计") && previous.snapshot
                ? {
                    ...previous.snapshot,
                    metrics: {
                      ...previous.snapshot.metrics,
                      ...(next.snapshot?.metrics ?? {}),
                    },
                  }
                : next.snapshot,
            news: next.failures.includes("公告") ? previous.news : next.news,
          }));
      } catch (e) {
        if (!disposed && (e as Error).name !== "AbortError") live.error(e);
      } finally {
        running = false;
        if (!disposed) setLoading(false);
      }
    };
    void load();
    const timer = setInterval(() => void load(), 60000);
    const visible = () => {
      if (!document.hidden) void load();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      disposed = true;
      controller.abort();
      clearInterval(timer);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [client, live, revision, refreshRevision]);
  const snapshot = data.snapshot,
    metrics = visibleMetrics(snapshot),
    days = snapshot?.registrations;
  const widgets = {
    overview: (
      <div className="dash-overview">
        <section className="glass dash-welcome">
          <div className="dash-kicker">
            <CircleGauge size={17} /> PURPLEMAGIC
          </div>
          <h2>今天的冒险，一眼掌握</h2>
          <p>从账号增长到副本表现，关注正在发生的变化。</p>
          <div className="dash-shortcuts">
            <a href="#accounts">
              <Users size={16} />
              账号与角色
              <ArrowUpRight size={14} />
            </a>
            <a href="#anti-cheat">
              <ShieldCheck size={16} />
              反作弊
              <ArrowUpRight size={14} />
            </a>
            {live && (
              <button onClick={() => setLibrary(true)}>
                <LibraryBig size={16} />
                物品库
                <ArrowUpRight size={14} />
              </button>
            )}
          </div>
        </section>
        <section className="glass dash-operations">
          <div className="dash-section-title">
            <h2>运营工作区</h2>
            <span>常用入口</span>
          </div>
          <a href="#benefits">
            <span className="dash-link-icon">
              <Gift size={21} />
            </span>
            <span>
              <strong>福利规则</strong>
              <small>自动发放 · 发放详情 · 补发</small>
            </span>
            <ArrowUpRight size={17} />
          </a>
          <a href="#activities">
            <span className="dash-link-icon">
              <CalendarDays size={21} />
            </span>
            <span>
              <strong>游戏活动</strong>
              <small>活动配置与生效状态</small>
            </span>
            <ArrowUpRight size={17} />
          </a>
        </section>
      </div>
    ),
    metrics: (metrics.length > 0 || loading) && (
      <section aria-label="核心指标">
        <div className="dash-section-title">
          <h2>核心指标</h2>
          <span>点击卡片进入对应模块</span>
        </div>
        <div className="dash-metrics">
          {metrics.map((m) => {
            const Icon = icons[m.icon];
            const value = snapshot!.metrics[m.id]!;
            const detail =
              m.id === "fastClearsToday" && snapshot?.fastThresholdMs
                ? `耗时 < ${snapshot.fastThresholdMs / 1000} 秒`
                : m.id === "fatigueToday" &&
                    snapshot?.fatigueSamples !== undefined
                  ? `来自 ${format(snapshot.fatigueSamples)} 次有记录通关`
                  : m.detail;
            return (
              <a
                className={`glass dash-metric dash-metric-${m.icon}`}
                href={`#${m.section}`}
                key={m.id}
              >
                <div className="dash-metric-label">
                  <span>{m.label}</span>
                  <Icon size={18} />
                </div>
                <div className="dash-metric-value">
                  {format(value)}
                  <small>{m.unit}</small>
                </div>
                <div className="dash-metric-foot">
                  <span>{detail}</span>
                  <ArrowUpRight size={14} />
                </div>
              </a>
            );
          })}
          {!snapshot &&
            loading &&
            Array.from({ length: 4 }, (_, i) => (
              <div
                className="glass dash-metric dash-skeleton"
                aria-hidden="true"
                key={i}
              >
                <span />
                <strong />
                <span />
              </div>
            ))}
        </div>
      </section>
    ),
    registrations: days && days.length > 0 && (
      <RegistrationHistory days={days} />
    ),
    news: data.news !== undefined && (
      <section className="glass dash-news">
        <div className="dash-section-title">
          <h2>
            <Megaphone size={18} />
            最新公告
          </h2>
          <a href="#settings">
            管理公告
            <ArrowUpRight size={14} />
          </a>
        </div>
        <div className="dash-news-grid">
          {data.news.length ? (
            data.news.map((n, i) => (
              <article key={`${i}-${n.title}`}>
                <div className="dash-news-meta">
                  <span>{n.category}</span>
                  <time>{n.date}</time>
                </div>
                <h3>{n.title}</h3>
                <p>{n.body}</p>
              </article>
            ))
          ) : (
            <p className="muted">暂无已发布公告</p>
          )}
        </div>
      </section>
    ),
  };
  return (
    <div className="dashboard" ref={motion}>
      <div className="glass dash-statusbar">
        <span className="dash-status">
          <i />
          <strong>运营总览</strong>
          <span>北京时间 · 周一开始</span>
        </span>
        <span className="dash-refresh">
          <span>
            {snapshot ? `${clock(snapshot.observedAt)} 更新` : "每分钟自动刷新"}
          </span>
          <button
            className="icon-button"
            aria-label="刷新仪表盘"
            disabled={loading}
            onClick={() => setRevision((v) => v + 1)}
          >
            <RefreshCw size={16} className={loading ? "is-spinning" : ""} />
          </button>
        </span>
      </div>
      {data.failures.length > 0 && (
        <div className="dash-read-error" role="status">
          {data.failures.join("、")}暂未刷新
          {data.snapshot || data.news ? "，保留上次结果" : ""}。
          <button
            className="text-button"
            onClick={() => setRevision((v) => v + 1)}
          >
            重试
          </button>
        </div>
      )}
      {dashboardLayout.map((id) => (
        <Fragment key={id}>{widgets[id]}</Fragment>
      ))}
      {library && (
        <Modal title="PVF 物品库" wide onClose={() => setLibrary(false)}>
          <ItemLibrary {...props} />
        </Modal>
      )}
    </div>
  );
}
function RegistrationHistory({
  days,
}: {
  days: NonNullable<DashboardSnapshot["registrations"]>;
}) {
  const recent = days.slice(-14),
    maximum = Math.max(1, ...days.map((d) => d.count)),
    maxRecent = Math.max(1, ...recent.map((d) => d.count));
  const total = days.reduce((s, d) => s + d.count, 0),
    cells = heatmapCells(days);
  const [focused, setFocused] = useState(days[days.length - 1]);
  return (
    <div className="dash-charts">
      <section className="glass dash-trend">
        <div className="dash-section-title">
          <h2>注册趋势</h2>
          <span>近 14 天</span>
        </div>
        <div className="dash-chart-total">
          <strong>{format(recent.reduce((s, d) => s + d.count, 0))}</strong>
          <span>个新账号</span>
        </div>
        <div
          className="dash-bars"
          role="img"
          aria-label={`近14天注册${recent.reduce((s, d) => s + d.count, 0)}个账号`}
        >
          {recent.map((d) => (
            <div
              className="dash-bar-column"
              key={d.date}
              tabIndex={0}
              title={`${d.date} · ${d.count} 个账号`}
              aria-label={`${d.date}，${d.count} 个账号`}
            >
              <span className="dash-bar-value">{d.count}</span>
              <i
                style={{
                  height: `${Math.max(2, (d.count / maxRecent) * 100)}%`,
                }}
              />
              <small>{d.date.slice(5)}</small>
            </div>
          ))}
        </div>
      </section>
      <section className="glass dash-heatmap">
        <div className="dash-section-title">
          <h2>注册热力图</h2>
          <span>
            {format(total)} 个账号 · {days.length} 天
          </span>
        </div>
        <div className="dash-heat-scroll">
          <div className="dash-heat-months">
            <span>{days[0].date}</span>
            <span>{days[days.length - 1].date}</span>
          </div>
          <div className="dash-heat-layout">
            <div className="dash-heat-week">
              {["一", "", "三", "", "五", "", "日"].map((d, i) => (
                <span key={i}>{d}</span>
              ))}
            </div>
            <div className="dash-heat-cells">
              {cells.map((d, i) =>
                d ? (
                  <button
                    key={d.date}
                    className={`heat-${heatLevel(d.count, maximum)}`}
                    aria-label={`${d.date} 注册 ${d.count} 个账号`}
                    title={`${d.date} · ${d.count} 个账号`}
                    onFocus={() => setFocused(d)}
                    onMouseEnter={() => setFocused(d)}
                    onClick={() => setFocused(d)}
                  />
                ) : (
                  <span className="heat-unknown" key={`unknown-${i}`} />
                ),
              )}
            </div>
          </div>
        </div>
        <div className="dash-heat-footer">
          <span aria-live="polite">
            {focused.date} · {focused.count} 个新账号
          </span>
          <span className="dash-heat-legend">
            少
            {[0, 1, 2, 3, 4].map((n) => (
              <i key={n} className={`heat-${n}`} />
            ))}
            多
          </span>
        </div>
      </section>
    </div>
  );
}
