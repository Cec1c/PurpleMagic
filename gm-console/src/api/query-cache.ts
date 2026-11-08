export type QueryPolicy = { ttl: number; stale: number; tags: string[] };
type Entry = { value: unknown; at: number; bytes: number; policy: QueryPolicy };
type Flight = { controller: AbortController; promise: Promise<unknown>; users: number; tags: string[] };
const cancelled = () => new DOMException("读取已取消", "AbortError");

/** Session-local LRU with a data-size budget and independently cancellable readers. */
export class QueryCache {
  private entries = new Map<string, Entry>();
  private flights = new Map<string, Flight>();
  private bytes = 0;
  readonly stats = { hits: 0, misses: 0, shared: 0, evictions: 0 };
  constructor(private capacity = 200, private budget = 32 * 1024 * 1024,
    private clock = () => Date.now()) {}
  peek<T>(key: string, stale = false): T | undefined {
    const entry = this.entries.get(key);
    if (!entry) return;
    if (this.clock() - entry.at >= (stale ? entry.policy.stale : entry.policy.ttl)) return;
    this.entries.delete(key); this.entries.set(key, entry);
    return structuredClone(entry.value) as T;
  }
  private remove(key: string) {
    const old = this.entries.get(key);
    if (old) this.bytes -= old.bytes;
    this.entries.delete(key);
  }
  invalidate(tags?: string[], reason: unknown = cancelled()) {
    const matches = (owned: string[]) => !tags || owned.some(t => tags.includes(t));
    for (const [key, entry] of this.entries) if (matches(entry.policy.tags)) this.remove(key);
    for (const [key, flight] of this.flights) if (matches(flight.tags)) {
      this.flights.delete(key); flight.controller.abort(reason);
    }
  }
  private put(key: string, value: unknown, policy: QueryPolicy) {
    const bytes = new TextEncoder().encode(JSON.stringify(value)).byteLength;
    this.remove(key);
    if (bytes > Math.min(this.budget, 8 * 1024 * 1024)) return;
    while (this.entries.size >= this.capacity || this.bytes + bytes > this.budget) {
      this.remove(this.entries.keys().next().value!); this.stats.evictions++;
    }
    this.entries.set(key, { value: structuredClone(value), at: this.clock(), bytes, policy });
    this.bytes += bytes;
  }
  async get<T>(key: string, policy: QueryPolicy, load: (signal: AbortSignal) => Promise<T>, signal: AbortSignal): Promise<T> {
    signal.throwIfAborted();
    const value = this.peek<T>(key);
    if (value !== undefined) { this.stats.hits++; return value; }
    let flight = this.flights.get(key);
    if (!flight) {
      this.stats.misses++;
      const controller = new AbortController();
      flight = { controller, users: 0, tags: policy.tags, promise: Promise.resolve() };
      const owner = flight;
      flight.promise = Promise.resolve().then(() => load(controller.signal)).then(result => {
        controller.signal.throwIfAborted();
        this.put(key, result, policy);
        return result;
      }).finally(() => { if (this.flights.get(key) === owner) this.flights.delete(key); });
      this.flights.set(key, flight);
    } else this.stats.shared++;
    const shared = flight; shared.users++;
    return new Promise<T>((resolve, reject) => {
      let settled = false;
      const finish = (error?: unknown, result?: unknown) => {
        if (settled) return; settled = true;
        signal.removeEventListener("abort", abort);
        shared.controller.signal.removeEventListener("abort", invalidated);
        if (--shared.users === 0 && this.flights.get(key) === shared) {
          this.flights.delete(key); shared.controller.abort(cancelled());
        }
        if (error !== undefined) reject(error); else resolve(structuredClone(result) as T);
      };
      const abort = () => finish(signal.reason || cancelled());
      const invalidated = () => finish(shared.controller.signal.reason || cancelled());
      shared.controller.signal.addEventListener("abort", invalidated, { once: true });
      signal.addEventListener("abort", abort, { once: true });
      if (signal.aborted) abort();
      shared.promise.then(v => finish(undefined, v), e => finish(e));
    });
  }
}
