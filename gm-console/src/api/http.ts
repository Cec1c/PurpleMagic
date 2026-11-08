import { QueryCache } from "./query-cache";
import { queryKey, queryPolicy, mutationTags } from "./query-policy";
export type Envelope<T> = {
  data: T;
  page?: { limit: number; nextCursor?: string };
  requestId?: string;
  error?: { code: string; message: string; retryable: boolean };
};
export class ApiError extends Error {
  constructor(
    message: string,
    public status = 0,
    public code = "network_error",
    public requestId = "",
  ) {
    super(message);
  }
}
export class HttpClient {
  csrf = "";
  readTimeout = 12000;
  writeTimeout = 45000;
  private view = new AbortController();
  private active = 0;
  private waiting = new Set<() => void>();
  readonly cache = new QueryCache();
  private generation = "";
  private generationAt = 0;
  private generationEnabled = false;
  private generationRead?: Promise<void>;
  private generationEpoch = 0;
  private channel?: BroadcastChannel;
  constructor(private fetcher: typeof fetch = (...args) => fetch(...args)) {
    if (typeof window !== "undefined" && typeof BroadcastChannel !== "undefined") {
      this.channel = new BroadcastChannel("purplemagic-query-invalidation-v1");
      this.channel.onmessage = event => {
        if (event.data?.clear === true) { this.resetGenerationCheck(); this.cache.invalidate(); }
        else if (Array.isArray(event.data?.tags) && event.data.tags.every((t: unknown) => typeof t === "string"))
          this.cache.invalidate(event.data.tags);
      };
    }
  }
  invalidate(tags?: string[], reason?: unknown) {
    if (!tags) this.resetGenerationCheck();
    this.cache.invalidate(tags, reason);
    this.channel?.postMessage(tags ? { tags } : { clear: true });
  }
  private resetGenerationCheck() {
    this.generationEpoch++;
    this.generationAt = 0;
    this.generationRead = undefined;
  }
  peek<T = any>(path: string) {
    return this.cache.peek<Envelope<T>>(this.cacheKey(path), true);
  }
  private cacheKey(path: string) { return this.generation + ":" + queryKey(path); }
  private async checkGeneration() {
    if (!this.generationEnabled || Date.now() - this.generationAt < 30000) return;
    if (!this.generationRead) {
      const epoch = this.generationEpoch;
      this.generationRead = this.transport<Envelope<{pvfChecksum:string}>>("/api/v1/query-generation").then(v => {
        if (epoch !== this.generationEpoch) throw new DOMException("读取已取消", "AbortError");
        const hash = v.data.pvfChecksum;
        if (!/^[a-f0-9]{64}$/.test(hash)) throw new ApiError("PVF 索引版本暂不可用");
        if (this.generation !== hash) { this.cache.invalidate(); this.generation = hash; }
        this.generationAt = Date.now();
      }).finally(() => { if (epoch === this.generationEpoch) this.generationRead = undefined; });
    }
    await this.generationRead;
  }
  cancelReads() {
    this.view.abort();
    this.view = new AbortController();
  }
  clearCache(reason?: unknown) {
    this.invalidate(undefined, reason);
  }
  private async acquire(signal: AbortSignal) {
    while (this.active >= 4) {
      await new Promise<void>((resolve, reject) => {
        const cleanup = () => {
          this.waiting.delete(wake);
          signal.removeEventListener("abort", abort);
        };
        const wake = () => {
          cleanup();
          resolve();
        };
        const abort = () => {
          cleanup();
          reject(signal.reason);
        };
        this.waiting.add(wake);
        signal.addEventListener("abort", abort, { once: true });
        if (signal.aborted) abort();
      });
    }
    signal.throwIfAborted();
    this.active++;
    return () => {
      this.active--;
      [...this.waiting].forEach((wake) => wake());
    };
  }
  async raw<T = any>(path: string, method = "GET", body?: unknown, key?: string,
    signal?: AbortSignal, headers: Record<string, string> = {}): Promise<T> {
    if (["/admin/api/news", "/admin/api/news/entries", "/admin/api/source", "/admin/api/connection"].includes(path)) path = path.replace("/admin/api/", "/api/v1/hub/");
    if (method === "GET") {
      const combined = signal ? AbortSignal.any([this.view.signal, signal]) : this.view.signal;
      combined.throwIfAborted();
      const policy = queryPolicy(path);
      if (policy && !Object.keys(headers).length) {
        await this.checkGeneration();
        combined.throwIfAborted();
        const p = this.generation && policy.tags.includes("definitions")
          ? { ...policy, ttl: 86400000, stale: 86400000 } : policy;
        return this.cache.get(this.cacheKey(path), p,
          shared => this.transport<T>(path, method, body, key, shared, headers), combined);
      }
      return this.transport<T>(path, method, body, key, combined, headers);
    }
    const tags = mutationTags(path);
    this.invalidate(tags);
    try { return await this.transport<T>(path, method, body, key, signal, headers); }
    finally { this.invalidate(tags); }
  }
  private async transport<T = any>(
    path: string,
    method = "GET",
    body?: unknown,
    key?: string,
    signal?: AbortSignal,
    headers: Record<string, string> = {},
  ): Promise<T> {
    // Resume stored announcement operations with their original request IDs.
    if (["/admin/api/news", "/admin/api/news/entries", "/admin/api/source", "/admin/api/connection"].includes(path))
      path = path.replace("/admin/api/", "/api/v1/hub/");
    if (!path.startsWith("/api/"))
      throw new Error("接口路径无效");
    const controller = new AbortController();
    const abort = () => controller.abort(signal?.reason);
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) abort();
    const timeout = setTimeout(
      () =>
        controller.abort(
          new ApiError(
            method === "GET"
              ? "读取超时，请重试"
              : "等待回执超时，请使用原请求查询回执",
            0,
            "timeout",
          ),
        ),
      method === "GET" ? this.readTimeout : this.writeTimeout,
    );
    const bounded = async <V>(run: () => Promise<V>): Promise<V> => {
      controller.signal.throwIfAborted();
      let stop: () => void = () => {};
      const interrupted = new Promise<never>((_, reject) => {
        stop = () => reject(controller.signal.reason);
        controller.signal.addEventListener("abort", stop, { once: true });
      });
      try {
        return await Promise.race([run(), interrupted]);
      } finally {
        controller.signal.removeEventListener("abort", stop);
      }
    };
    let release: (() => void) | undefined;
    try {
      if (method === "GET") release = await this.acquire(controller.signal);

      let response: Response;
      try {
        response = await bounded(() =>
          this.fetcher(path, {
            method,
            credentials: "same-origin",
            cache: "no-store",
            signal: controller.signal,
            headers: {
              Accept: "application/json",
              ...(method !== "GET"
                ? {
                    "Content-Type": "application/json",
                    "X-CSRF-Token": this.csrf,
                  }
                : {}),
              ...(key ? { "Idempotency-Key": key } : {}),
              ...headers,
            },
            body: body === undefined ? undefined : JSON.stringify(body),
          }),
        );
      } catch (e) {
        if (controller.signal.aborted) throw controller.signal.reason;
        if ((e as Error).name === "AbortError") throw e;
        throw new ApiError("连接中断，可使用原请求重试并查询回执");
      }
      let value: any;
      try {
        value = await bounded(() => response.json());
      } catch (e) {
        if (controller.signal.aborted) throw controller.signal.reason;
        if ((e as Error).name === "AbortError") throw e;
        throw new ApiError(
          "接口未返回有效数据",
          response.status,
          "invalid_response",
        );
      }
      if (
        !response.ok ||
        value.error ||
        value.success === false ||
        value.ok === false
      ) {
        const failure = new ApiError(
          typeof value.error === "string"
            ? value.error
            : value.error?.message || value.message || "请求未完成",
          response.status,
          value.error?.code || "request_failed",
          value.requestId || "",
        );
        if (response.status === 401 || response.status === 403) this.clearCache(failure);
        throw failure;
      }
      return value;
    } finally {
      release?.();
      clearTimeout(timeout);
      signal?.removeEventListener("abort", abort);
    }
  }
  async get<T = any>(path: string, signal?: AbortSignal) {
    return this.raw<Envelope<T>>(path, "GET", undefined, undefined, signal);
  }
  async fresh<T = any>(path: string, signal?: AbortSignal) {
    const combined = signal ? AbortSignal.any([this.view.signal, signal]) : this.view.signal;
    return this.transport<Envelope<T>>(path, "GET", undefined, undefined, combined);
  }
  async observe<T = any>(path: string, signal: AbortSignal, apply: (value: Envelope<T>) => void) {
    signal.throwIfAborted();
    await this.checkGeneration();
    signal.throwIfAborted();
    const fresh = this.cache.peek<Envelope<T>>(this.cacheKey(path));
    if (fresh) { apply(fresh); return; }
    const cached = this.peek<T>(path);
    if (cached) apply(cached);
    const value = await this.get<T>(path, signal);
    signal.throwIfAborted();
    apply(value);
  }
  async post<T = any>(path: string, body: unknown, key?: string) {
    return (await this.raw<Envelope<T>>(path, "POST", body, key)).data;
  }
  async capabilities() {
    const { data } = await this.get<{
      features: Record<string, boolean>;
      csrfToken: string;
      backgroundMail?: boolean;
      backgroundBenefits?: boolean;
    }>("/api/v1/capabilities");
    this.csrf = data.csrfToken;
    this.generationEnabled = !!data.features["query.cacheGeneration"];
    return data;
  }
  async login(key: string, remember = true) {
    await this.raw("/api/auth/login", "POST", { password: key, remember });
    return this.capabilities();
  }
}
export function query(
  path: string,
  params: Record<string, string | number | undefined>,
) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params))
    if (v !== undefined && v !== "") q.set(k, String(v));
  return path + "?" + q;
}
export const segment = encodeURIComponent;
export function safeNumber(value: unknown): number {
  const n = Number(value);
  if (value == null || !Number.isSafeInteger(n)) return Number.NaN;
  return n;
}
