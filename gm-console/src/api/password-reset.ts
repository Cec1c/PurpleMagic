type Receipt = { requestId: string; accountId: string; account: string; resetAt: number };
type Post = (path: string, body: unknown, key?: string) => Promise<Receipt>;

// This operation lives only in the open dialog. It never enters the generic
// pending-operation store, localStorage, sessionStorage, or exported logs.
export function createPasswordReset(accountId: string, account: string, post: Post) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-";
  const password = Array.from(crypto.getRandomValues(new Uint8Array(20)), n => alphabet[n & 63]).join("");
  const request = Object.freeze({ requestId: crypto.randomUUID(), account, password });
  let result: string | undefined;
  let running: Promise<string> | undefined;
  return {
    submit(): Promise<string> {
      if (result) return Promise.resolve(result);
      if (running) return running;
      running = (async () => {
        const receipt = await post(`/api/v1/accounts/${encodeURIComponent(accountId)}/password-reset`, request, request.requestId);
        if (!receipt || receipt.requestId !== request.requestId || receipt.accountId !== accountId || receipt.account !== account || !(receipt.resetAt > 0))
          throw new Error("暂未取得重置结果，请保留此窗口并重试");
        result = password;
        return result;
      })().finally(() => { running = undefined; });
      return running;
    },
  };
}
