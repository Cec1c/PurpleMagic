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
  hubCsrf = "";
  readTimeout = 12000;
  writeTimeout = 45000;
  private view = new AbortController();
  private active = 0;
  private waiting = new Set<() => void>();
  private cache = new Map<string, { at: number; value: any }>();
  cancelReads() {
    this.view.abort();
    this.view = new AbortController();
  }
  clearCache() {
    this.cache.clear();
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
  constructor(private fetcher: typeof fetch = (...args) => fetch(...args)) {}
  async raw<T = any>(
    path: string,
    method = "GET",
    body?: unknown,
    key?: string,
    signal?: AbortSignal,
    headers: Record<string, string> = {},
  ): Promise<T> {
    if (!path.startsWith("/api/") && !path.startsWith("/admin/api/"))
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
      else this.clearCache();
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
                    "X-CSRF-Token": path.startsWith("/admin/")
                      ? this.hubCsrf
                      : this.csrf,
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
        throw new ApiError(
          typeof value.error === "string"
            ? value.error
            : value.error?.message || value.message || "请求未完成",
          response.status,
          value.error?.code || "request_failed",
          value.requestId || "",
        );
      }
      return value;
    } finally {
      release?.();
      clearTimeout(timeout);
      signal?.removeEventListener("abort", abort);
    }
  }
  async get<T = any>(path: string, signal?: AbortSignal) {
    const combined = signal
      ? AbortSignal.any([this.view.signal, signal])
      : this.view.signal;
    combined.throwIfAborted();
    // Only immutable PVF views get a short bounded cache. Transaction previews
    // and player balances always read fresh data from the server.
    const staticView = /^\/api\/v1\/(items|quests)(\?.*|\/[0-9]+)?$/.test(path);
    const cached = staticView ? this.cache.get(path) : undefined;
    if (cached && Date.now() - cached.at < 20000)
      return structuredClone(cached.value) as Envelope<T>;
    const value = await this.raw<Envelope<T>>(
      path,
      "GET",
      undefined,
      undefined,
      combined,
    );
    combined.throwIfAborted();
    if (staticView) {
      if (this.cache.size >= 32)
        this.cache.delete(this.cache.keys().next().value!);
      this.cache.set(path, { at: Date.now(), value: structuredClone(value) });
    }
    return value;
  }
  async post<T = any>(path: string, body: unknown, key?: string) {
    return (await this.raw<Envelope<T>>(path, "POST", body, key)).data;
  }
  async capabilities() {
    const { data } = await this.get<{
      features: Record<string, boolean>;
      csrfToken: string;
      backgroundMail?: boolean;
    }>("/api/v1/capabilities");
    this.csrf = data.csrfToken;
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
