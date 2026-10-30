import { ApiError, type HttpClient } from "../api/http";
import {
  latestNews,
  parseSnapshot,
  type DashboardSnapshot,
  type NewsItem,
} from "./model";

export type DashboardResult = {
  snapshot?: DashboardSnapshot;
  news?: NewsItem[];
  failures: string[];
};
/** One bounded request per data source. No directory or clear-history pagination. */
export class DashboardClient {
  private supported: boolean | undefined;
  constructor(
    private http: HttpClient,
    private fetcher: typeof fetch = (...args) => fetch(...args),
  ) {}
  async load(signal: AbortSignal): Promise<DashboardResult> {
    const result: DashboardResult = { failures: [] };
    let online: { count: number; at: string } | undefined;
    await Promise.all([
      (async () => {
        if (this.supported === false) return;
        try {
          const { data } = await this.http.get("/api/v1/dashboard", signal);
          const snapshot = parseSnapshot(data);
          if (!snapshot) throw new Error("统计格式无效");
          this.supported = true;
          result.snapshot = snapshot;
        } catch (e) {
          if (signal.aborted || (e as Error).name === "AbortError") return;
          if (e instanceof ApiError && [404, 405, 501].includes(e.status))
            this.supported = false;
          else if (e instanceof ApiError && e.status === 401) throw e;
          else result.failures.push("统计");
        }
      })(),
      (async () => {
        try {
          const { data } = await this.http.get("/api/v1/presence", signal);
          if (
            data?.complete !== true ||
            !Array.isArray(data.characterIds) ||
            !data.characterIds.every((v: unknown) => typeof v === "string") ||
            !Number.isFinite(Date.parse(data.observedAt))
          )
            throw new Error("快照不可用");
          // Assign after all requests settle so the newer presence always wins.
          online = {
            count: new Set(data.characterIds).size,
            at: data.observedAt,
          };
        } catch (e) {
          if (signal.aborted || (e as Error).name === "AbortError") return;
          if (e instanceof ApiError && e.status === 401) throw e;
          if (!(e instanceof ApiError && [404, 405, 501].includes(e.status)))
            result.failures.push("在线快照");
        }
      })(),
      (async () => {
        try {
          // Display-only HTTPS publication; no release/profile fields are retained.
          const response = await this.fetcher("/launcher/v2/bootstrap.json", {
            credentials: "omit",
            cache: "no-store",
            signal: AbortSignal.any([signal, AbortSignal.timeout(12000)]),
          });
          if ([404, 405, 501].includes(response.status)) return;
          if (!response.ok) throw new Error("公告不可用");
          const envelope = await response.json();
          if (
            typeof envelope.Payload !== "string" ||
            envelope.Payload.length > 12 * 1024 * 1024
          )
            throw new Error("公告格式无效");
          const payload = JSON.parse(
            new TextDecoder().decode(
              Uint8Array.from(atob(envelope.Payload), (c) => c.charCodeAt(0)),
            ),
          );
          if (payload.product !== "DNF90-Flutter" || payload.format !== 1)
            throw new Error("公告格式无效");
          result.news = latestNews(payload.news);
          if (!result.news) throw new Error("公告格式无效");
        } catch (e) {
          if (!signal.aborted) result.failures.push("公告");
        }
      })(),
    ]);
    signal.throwIfAborted();
    if (online) {
      result.snapshot ??= {
        observedAt: online.at,
        timezone: "Asia/Shanghai",
        metrics: {},
      };
      result.snapshot.metrics.online = online.count;
    }
    return result;
  }
}
