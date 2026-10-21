import { activityStatesMatch, type ActivitySnapshot, type ActivityChange } from "../activities";
import { ApiError } from "./http";
import type { ConsoleAPI } from "./console";
import type { Step } from "./model";

export function publishActivities(api: ConsoleAPI, snapshot: ActivitySnapshot) {
  api.meta.resources.activities = snapshot;
  api.publish();
}
export async function executeActivitySave(api: ConsoleAPI, step: Step) {
  const changes = step.body.activities as ActivityChange[];
  const read = async () => {
    const { data } = await api.http.get<ActivitySnapshot>("/api/v1/activities");
    publishActivities(api, data);
    return data;
  };
  const conflict = () => new ApiError("活动配置已更新，请按当前状态重新选择并确认", 409, "revision_conflict");
  const recovered = (snapshot: ActivitySnapshot) => step.attempted &&
    snapshot.revision > step.body.revision && activityStatesMatch(snapshot, changes);
  const current = await read();
  if (current.revision !== step.body.revision) {
    if (recovered(current)) { step.result = current; return; }
    throw conflict();
  }
  step.attempted = true;
  api.persistPending();
  try {
    const data = await api.http.post("/api/v1/activities", step.body, step.id);
    step.result = data;
    publishActivities(api, data);
  } catch (error) {
    if (error instanceof ApiError && [400, 401, 403, 404, 422].includes(error.status)) throw error;
    // Read before any retry. Never replace the confirmed revision with a newer one.
    let latest: ActivitySnapshot;
    try { latest = await read(); } catch { throw error; }
    if (recovered(latest)) { step.result = latest; return; }
    if (latest.revision !== step.body.revision) throw conflict();
    throw error;
  }
}
