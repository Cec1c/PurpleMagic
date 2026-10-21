import type { Command, Selection } from "../domain";
import { benefitReward } from "../domain";
import type { ConsoleAPI } from "./console";
import type { DTO, Pending, Step } from "./model";
import { segment, ApiError } from "./http";
const reason = "PurpleMagic 管理操作";
function changed(a: unknown, b: unknown) {
  return !(Object.is(a, b) || (Number.isNaN(a) && Number.isNaN(b)));
}
function integer(n: number, min = 0, max = 2147483647) {
  if (!Number.isSafeInteger(n) || n < min || n > max)
    throw new Error(`请输入 ${min}–${max} 之间的整数`);
}
export async function prepareOperation(
  api: ConsoleAPI,
  command: Command,
  selection: Selection,
): Promise<Pending> {
  const id = crypto.randomUUID();
  const c = api.state.characters.find((c) => c.id === api.current);
  const base = c ? `/api/v1/characters/${segment(c.id)}` : "";
  const p: Pending = {
    id,
    title: "确认本次修改",
    target: c?.name || "运营配置",
    summary: "",
    steps: [],
    confirmed: false,
  };
  const add = (
    label: string,
    path: string,
    body: DTO,
    extra: Partial<Step> = {},
  ) => {
    const step = { id: `${id}-${p.steps.length}`, label, path, body, ...extra };
    p.steps.push(step);
    return step;
  };
  const preview = async (
    label: string,
    path: string,
    body: DTO,
    refreshRead?: string,
  ) => {
    const data = await api.http.post(path + "/preview", body);
    return add(
      label,
      path,
      {
        ...body,
        ...(data.planToken ? { planToken: data.planToken } : {}),
        ...(data.definitionRevision
          ? { definitionRevision: data.definitionRevision }
          : {}),
        ...(data.items ? { items: data.items } : {}),
        ...(!data.receipt && data.revision ? { revision: data.revision } : {}),
        ...(data.pvfChecksum ? { pvfChecksum: data.pvfChecksum } : {}),
      },
      { result: data.receipt || data, refreshRead },
    );
  };
  if (command.kind === "item" || command.kind === "delete-items" || command.kind === "replace-item") {
    if (!c) throw new Error("请先选择角色");
    const keys = command.kind === "delete-items" ? command.keys : [command.key];
    if (!keys.length) throw new Error("请先选择物品");
    const selected = [];
    const labels: string[] = [];
    const scopes = new Set<string>();
    let checksum = "";
    let options: DTO = {};
    for (const key of keys) {
      const item = api.state.items.find((i) => i.key === key);
      if (!item?.remote) throw new Error("物品列表已变更，请重新选择");
      labels.push(`${item.name}（${item.remote.container} / ${item.slot}）`);
      const r = await api.http.get(
        `${base}/inventory/${segment(item.remote.container)}/${item.slot}/options`,
      );
      const o = r.data,
        v = o.item;
      if (String(v.itemId) !== item.id || Number(v.count) !== item.count || v.bound !== item.bound)
        throw new Error("物品已变更，请刷新详情后重试");
      if (item.remote.revision && item.remote.revision !== v.revision)
        throw new Error("物品已变更，请刷新详情后重试");
      if (checksum && checksum !== v.pvfChecksum)
        throw new Error("PVF 已变更，请刷新");
      checksum = v.pvfChecksum;
      scopes.add(o.ownerScope);
      if (command.kind === "delete-items" && !o.canDelete)
        throw new Error(`${item.name} 暂不支持删除`);
      selected.push({
        container: item.remote.container,
        slot: item.slot,
        revision: v.revision,
        ...(v.relatedRevision ? { relatedRevision: v.relatedRevision } : {}),
      });
      if (command.kind === "replace-item") {
        if (!o.canReplace) throw new Error("此物品不支持 ID 替换");
        if (command.version !== item.version) throw new Error("详情已刷新，请核对后再替换");
        if (!/^[1-9][0-9]*$/.test(command.itemId)) throw new Error("请输入有效的物品 ID");
        integer(Number(command.itemId), 1, 4294967295);
        options.itemId = command.itemId;
        p.summary = `${labels[0]}：ID ${item.id} → ${command.itemId}；保留数量、绑定、期限和实例属性`;
      }
      if (command.kind === "item") {
        if (command.version !== item.version)
          throw new Error("详情已刷新，请核对修改后再保存");
        const d = command.patch,
          e = o.configuration?.equipment;
        if (changed(d.bound, item.bound))
          throw new Error("当前物品的绑定状态由游戏规则维护");
        if (changed(d.count, item.count)) {
          integer(
            d.count,
            Number(o.quantity?.min ?? 1),
            Number(o.quantity?.max ?? 0),
          );
          if (!o.quantity) throw new Error("此物品数量固定");
          options.count = String(d.count);
        }
        if (changed(d.enhance, item.enhance)) {
          if (!e?.canUpgrade) throw new Error("此物品不支持强化");
          integer(d.enhance, 0, e.maxUpgradeLevel);
          options.upgradeLevel = d.enhance;
        }
        if (changed(d.amplify, item.amplify)) {
          if (!e?.canAmplify) throw new Error("此物品不支持增幅");
          integer(d.amplify, 0, 4);
          options.amplifyType = d.amplify;
        }
        if (changed(d.forge, item.forge)) {
          if (!e?.canForge) throw new Error("此物品不支持锻造");
          integer(d.forge, 0, e.maxForgingLevel);
          options.forgingLevel = d.forge;
        }
        if (command.expirationDays !== undefined) {
          const expiration = o.configuration?.expiration;
          if (!expiration?.canOverride)
            throw new Error("此物品没有可调整的期限");
          integer(command.expirationDays, 1, expiration.maxDays);
          options.expirationDays = command.expirationDays;
        }
        const changes = [
          options.count !== undefined
            ? `数量 ${item.count} → ${options.count}`
            : "",
          options.upgradeLevel !== undefined
            ? `强化 ${item.enhance} → ${options.upgradeLevel}`
            : "",
          options.amplifyType !== undefined
            ? `增幅属性 → ${e.amplifyTypes.find((x: DTO) => x.value === options.amplifyType)?.label}`
            : "",
          options.forgingLevel !== undefined
            ? `锻造 ${item.forge} → ${options.forgingLevel}`
            : "",
          options.expirationDays !== undefined
            ? `有效期重设为 ${options.expirationDays} 天`
            : "",
        ].filter(Boolean);
        p.summary = `${labels[0]}：${changes.join("；")}`;
        if (!Object.keys(options).length) throw new Error("没有需要保存的修改");
      }
    }
    if (scopes.size !== 1) throw new Error("请分别处理角色物品和账号共享资产");
    if (command.kind === "delete-items")
      p.summary = `删除 ${labels.length} 件物品：${labels.join("、")}`;
    await preview(p.summary, base + "/inventory-operations", {
      accountId: c.accountId,
      action: command.kind === "delete-items" ? "delete" : "configure",
      items: selected,
      ...(command.kind !== "delete-items" ? { options } : {}),
      pvfChecksum: checksum,
      reason,
    });
  } else if (command.kind === "mail") {
    integer(command.gold);
    if (!command.title.trim()) throw new Error("请填写邮件标题");
    const recipients = await api.resolveSelection(selection);
    if (!recipients.length) throw new Error("请先选择收件人");
    p.recipients = recipients.map((c) => ({
      id: c.id,
      name: c.name,
      accountId: c.accountId,
    }));
    p.target = `${recipients.length} 个收件角色`;
    const attachments = command.items.length ? command.items : [null];
    for (let i = 0; i < attachments.length; i++) {
      const a = attachments[i];
      let reward: DTO = {
        title: command.title,
        body: command.body,
        gold: i === 0 ? command.gold : 0,
        recipients: recipients.map((c) => ({
          accountId: c.accountId,
          characterId: c.id,
        })),
      };
      if (a) {
        integer(a.count, 1);
        const { data } = await api.http.get(
          `/api/v1/items/${segment(a.id)}/grant-options`,
        );
        reward = {
          ...reward,
          itemId: a.id,
          count: a.count,
          pvfChecksum: data.pvfChecksum,
          options: {
            reinforce: a.enhance,
            amplifyType: 0,
            forgingLevel: 0,
            expireDays: 0,
            equipmentQuality: "default",
          },
        };
      }
      const job = await api.http.post(
        "/api/v1/mail/preview",
        reward,
        `${id}-preview-${i}`,
      );
      const { data } = await api.http.get(
        `/api/v1/tasks/${job.id}/confirmation`,
      );
      const names = (data.recipients || []).map((v: DTO) => ({
        id: v.characterId,
        name: v.name,
        accountId: v.accountId,
      }));
      if (
        names.length !== recipients.length ||
        names.some(
          (v: DTO) =>
            !recipients.some(
              (c) => c.id === v.id && c.accountId === v.accountId,
            ),
        )
      )
        throw new Error("收件名单已变更，请重新选择");
      p.recipients = names;
      add(
        `邮件 ${i + 1} / ${attachments.length}${a ? ` · ${a.name} × ${a.count}` : ""}`,
        `/api/v1/tasks/${job.id}/run`,
        { revision: job.revision, confirm: true, retry: false },
        { kind: "task", result: job },
      );
    }
    p.summary = `${command.title}；每人金币 ${command.gold.toLocaleString()}；${attachments.length} 封邮件${attachments.length > 1 ? "（每种附件一封，金币仅随第一封）" : ""}`;
  } else if (command.kind === "delete-mail") {
    if (!c) throw new Error("请先选择角色");
    p.summary = `删除 ${command.ids.length} 封邮件及其中未领取的附件和金币：${command.ids.map((id) => api.state.mails.find((m) => m.id === id)?.title || id).join("、")}`;
    await preview(p.summary, base + "/mailbox-operations", {
      accountId: c.accountId,
      items: command.ids.map((mailId) => ({ mailId, revision: "" })),
      discardUnclaimed: true,
      reason,
    });
  } else if (command.kind === "quest") {
    if (!command.selectedTargets && (!c || command.characterId !== c.id))
      throw new Error("角色已切换，请重新选择任务");
    const recipients = command.selectedTargets ? await api.resolveSelection(selection) : [c!];
    if (!recipients.length || recipients.length > 100) throw new Error("请选择 1–100 个角色");
    if (!command.ids.length || command.ids.length > 100) throw new Error("请选择 1–100 个任务");
    p.recipients = recipients.map(({id, name, accountId}) => ({id, name, accountId}));
    p.target = `${recipients.length} 个角色`;

    const action = command.status === "completed" ? "complete" : "reset";
    p.summary =
      action === "complete"
        ? "完成选中任务（不发任务奖励）"
        : "恢复选中任务为未完成";
    p.summary += `：${command.ids.map((id) => api.meta.quests.find((q) => q.id === id)?.name || id).join("、")}`;
    for (const target of recipients) await preview(`${target.name}：${p.summary}`, `/api/v1/characters/${segment(target.id)}/quest-operations`, {
      accountId: target.accountId,
      action,
      questIds: command.ids,
      revision: "",
      pvfChecksum: api.meta.questPVF,
      reason,
    });
  } else if (command.kind === "reset-skill-points") {
    if (!c || command.characterId !== c.id) throw new Error("角色已切换");
    const { data } = await api.http.get(base + "/skill-points");
    p.summary = `按 Lv.${data.level} 恢复 SP / TP 总量为 ${data.definition.sp} / ${data.definition.tp}，清除额外点数，保留已学技能和已用点数`;
    await preview(p.summary, base + "/skill-point-operations", {
      accountId: c.accountId, action: "reset_defaults", target: "both", amount: "0",
      revision: data.revision, definitionRevision: data.definitionRevision, reason,
    });
  } else if (command.kind === "growth") {
    if (!c || command.characterId !== c.id) throw new Error("角色已切换");
    integer(command.level, 1, 90);
    integer(command.sp, 0, 65535);
    integer(command.tp, 0, 65535);
    const r = api.meta.resources;
    const job = api.meta.jobs.find(
      (j) => j.value === command.job || j.label === command.job,
    );
    p.summary = `等级 ${command.level} · ${job?.label || command.job} · 额外 SP ${command.sp} / TP ${command.tp}`;
    if (command.level !== c.level) {
      if (!r.level) throw new Error("等级数据尚未读取");
      await preview(`等级 → ${command.level}`, base + "/level-operations", {
        accountId: c.accountId,
        targetLevel: command.level,
        revision: r.level.revision,
        pvfChecksum: r.level.definition.pvfChecksum,
        definitionRevision: "",
        reason,
      });
    }
    if (
      job &&
      job.value !==
        `${r.profession?.balance.job}:${r.profession?.balance.growType}`
    ) {
      const [targetJob, targetGrowType] = job.value.split(":").map(Number);
      await preview(
        `职业 → ${job.label}`,
        base + "/profession-operations",
        {
          accountId: c.accountId,
          targetJob,
          targetGrowType,
          revision: r.profession.revision,
          pvfChecksum: r.profession.pvfChecksum,
          definitionRevision: "",
          reason,
        },
        p.steps.length ? base + "/profession" : undefined,
      );
    }
    for (const [target, value, old] of [
      ["sp", command.sp, c.sp],
      ["tp", command.tp, c.tp],
    ] as const) {
      if (value === old) continue;
      if (!r.skillPoints) throw new Error("技能点数据尚未读取");
      await preview(
        `额外 ${target.toUpperCase()} → ${value}`,
        base + "/skill-point-operations",
        {
          accountId: c.accountId,
          action: "set",
          target,
          amount: String(value),
          revision: r.skillPoints.revision,
          definitionRevision: r.skillPoints.definitionRevision,
          reason,
        },
        p.steps.length ? base + "/skill-points" : undefined,
      );
    }
  } else if (command.kind === "currency") {
    if (!c || command.characterId !== c.id) throw new Error("角色已切换");
    const a = api.state.accounts.find((a) => a.id === c.accountId)!;
    const r = api.meta.resources;
    if (changed(command.tokens, a.tokens))
      throw new Error("代币券暂无管理写入契约");
    if (changed(command.gold, c.gold)) {
      integer(command.gold);
      await preview(
        `金币 → ${command.gold.toLocaleString()}`,
        base + "/gold-operations",
        {
          accountId: c.accountId,
          action: "set",
          amount: String(command.gold),
          revision: r.gold.revision,
          reason,
        },
      );
    }
    const pointsText = command.pointsText ?? String(command.points);
    if (
      pointsText !== a.rawPoints &&
      (command.pointsText !== undefined || changed(command.points, a.points))
    ) {
      if (
        !/^\d+$/.test(pointsText) ||
        BigInt(pointsText) > 9223372036854775807n
      )
        throw new Error("请输入有效的点券整数金额");
      const amount = (
        BigInt(pointsText) - BigInt(r.currency.balance)
      ).toString();
      add(
        `点券 → ${BigInt(pointsText).toLocaleString()}`,
        `/api/v1/accounts/${segment(c.accountId)}/currency-adjustments`,
        { currency: "cera", amount, revision: r.currency.revision, reason },
      );
    }
    p.summary = p.steps.map((s) => s.label).join("；");
  } else if (command.kind === "benefit-create") {
    const reward = benefitReward(command);
    let itemFields: DTO = {};
    if (reward.item) {
      const { data } = await api.http.get(`/api/v1/items/${segment(reward.item.id)}/grant-options`);
      if (data.kind === "equipment" && reward.item.count !== 1)
        throw new Error("装备每封邮件数量为 1");
      reward.summary = `${data.name}（${reward.item.id}）× ${reward.item.count.toLocaleString()}`;
      itemFields = { itemId: reward.item.id, count: reward.item.count, pvfChecksum: data.pvfChecksum,
        options: { reinforce: 0, amplifyType: 0, forgingLevel: 0, expireDays: 0, equipmentQuality: "default" } };
    }
    p.target =
      "全部现有角色" + (command.scope === "future" ? "及未来角色" : "") +
      (reward.grantScope === "account" ? "，同账号仅发一次" : "");
    p.summary = `${command.name} · ${reward.grantScope === "account" ? "每账号一次" : "每角色一次"} · ${reward.summary}${reward.cera ? " · 直接存入账号" : ""}${command.enabled ? " · 创建后立即启用" : ""}`;
    add("创建福利规则", "/api/v1/benefits", {
      title: command.name,
      body: command.name,
      gold: reward.gold,
      ...(reward.cera ? { cera: reward.cera } : {}),
      ...itemFields,
      scope: reward.grantScope,
      autoNewCharacters: command.scope === "future",
    });
    if (command.enabled)
      add(
        "启用自动发放",
        "",
        { confirm: true, enabled: true },
        { kind: "benefit-enable" },
      );
  } else if (command.kind === "benefit-redeliver") {
    if (!api.meta.features.backgroundBenefits) throw new Error("补发服务尚未就绪，请刷新页面后重试");
    const path = `/api/v1/benefits/${segment(command.id)}`;
    const { data: rule } = await api.http.get(path);
    if (!rule.enabled) throw new Error("请先启用福利规则再补发");
    p.title = "确认补发未收到的福利";
    p.target = rule.reward.title;
    p.summary = `按原规则检查本次开始前已创建的${rule.scope === "account" ? "账号及其收件角色" : "角色"}，补发符合条件且尚未收到的奖励。已领取记录保持，${rule.scope === "account" ? "每账号" : "每角色"}仅发一次。任务在后台继续，进度可在发放详情查看。`;
    add("开始后台补发", path + "/redelivery", { revision: rule.revision, confirm: true });
  } else if (command.kind === "benefit-toggle") {
    const b = api.state.benefits.find((b) => b.id === command.id);
    if (!b) throw new Error("规则不存在");
    p.target = b.name;
    p.summary = b.enabled ? "暂停自动发放" : "启用后自动发放";
    add(p.summary, `/api/v1/benefits/${segment(b.id)}/state`, {
      revision: b.revision,
      enabled: !b.enabled,
      confirm: true,
    });
  } else if (command.kind === "task-control") {
    const path = `/api/v1/tasks/${segment(command.id)}`;
    const { data: task } = await api.http.get(path);
    p.target = task.reward.title;
    p.summary =
      command.action === "pause"
        ? "暂停后续批次"
        : command.action === "retry"
          ? `重试 ${task.failed} 个失败收件人`
          : `继续发放 ${task.pending} 个收件人`;
    if (command.action === "pause") {
      if (!api.meta.features.backgroundMail)
        throw new Error("当前服务按本次操作连续发放");
      add(p.summary, path + "/pause", {
        revision: task.revision,
        confirm: true,
      });
    } else
      add(
        p.summary,
        path + "/run",
        {
          revision: task.revision,
          confirm: true,
          retry: command.action === "retry",
        },
        { kind: "task", result: task },
      );
  } else if (command.kind === "news") {
    if (!api.meta.hub) throw new Error("请先登录公告与下载源管理");
    if (!command.title.trim()) throw new Error("请填写公告标题");
    const news = structuredClone(api.meta.hub.published?.news || []);
    const existingDraft = api.state.news.find(
      (n) => n.id === command.id && !n.published,
    );
    const originId =
      existingDraft?.originId ??
      (command.id.startsWith("draft-") ? "" : command.id);
    if (
      command.published &&
      existingDraft?.sourceRevision &&
      existingDraft.sourceRevision !== api.meta.hub.newsRevision
    )
      throw new Error("公告列表已更新，请重新选择要编辑的公告后发布");
    const item = {
      title: command.title,
      body: command.body,
      category: "综合",
      date: new Date().toISOString().slice(0, 10),
    };
    if (originId !== "" && news[Number(originId)])
      news[Number(originId)] = { ...news[Number(originId)], ...item };
    else news.unshift(item);
    p.target = command.title;
    p.summary = command.published ? "发布公告到登录器" : "保存公告草稿";
    if (!command.published) {
      add("保存草稿", "local-draft", {
        ...command,
        originId,
        sourceRevision:
          existingDraft?.sourceRevision || api.meta.hub.newsRevision,
      });
    } else {
      p.draftId = command.id;
      add("发布公告", "/admin/api/news", news, {
        method: "PUT",
        headers: { "If-Match": `"${api.meta.hub.newsRevision}"` },
      });
    }
  } else if (command.kind === "sources") {
    const hub = api.meta.hub;
    if (!hub) throw new Error("请先登录公告与下载源管理");
    const source = command.source || hub.sources?.[0];
    if (source) {
      const u = new URL(command.download);
      if (
        u.protocol !== "https:" ||
        u.username ||
        u.password ||
        u.search ||
        u.hash ||
        (u.pathname !== "/" && u.pathname !== "")
      )
        throw new Error("下载源须为 HTTPS 站点地址，不含路径、参数或凭据");
      const next = { ...source, endpoint: u.origin };
      const old = hub.sources?.find((s: DTO) => s.id === next.id);
      if (JSON.stringify(old) !== JSON.stringify(next))
        add("保存下载源", "/admin/api/source", next, { method: "PUT" });
    }
    if (
      command.gameHost !== api.state.sources.gameHost ||
      command.gamePort !== api.state.sources.gamePort
    )
      add(
        "发布游戏连接",
        "/admin/api/connection",
        { gameHost: command.gameHost, bootstrapPort: command.gamePort },
        {
          method: "PUT",
          headers: { "If-Match": `"${hub.connectionRevision}"` },
        },
      );
    p.summary = p.steps.map((s) => s.label).join("；");
  }
  if (!p.steps.length) throw new Error("没有需要保存的修改");
  return p;
}

export async function executeOperation(api: ConsoleAPI, p: Pending) {
  for (let i = 0; i < p.steps.length; i++) {
    const step = p.steps[i];
    if (step.done) continue;
    if (step.kind === "benefit-enable") {
      const created = p.steps[i - 1].result;
      if (!created?.id) throw new Error("尚未取得规则创建回执");
      step.path = `/api/v1/benefits/${created.id}/state`;
      step.body.revision = created.revision;
    }
    if (step.refreshRead && !step.refreshed) {
      const { data } = await api.http.get(step.refreshRead);
      step.body.revision = data.revision;
      delete step.body.planToken;
      step.body.definitionRevision = data.definitionRevision || "";
      const preview = await api.http.post(step.path + "/preview", step.body);
      step.body = {
        ...step.body,
        planToken: preview.planToken,
        definitionRevision:
          preview.definitionRevision || data.definitionRevision,
      };
      step.refreshed = true;
      api.persistPending();
    }
    if (step.path === "local-draft") {
      const n = step.body;
      const draft = {
        id: n.id?.startsWith("draft-") ? n.id : `draft-${n.id || p.id}`,
        title: n.title,
        body: n.body,
        published: false,
        originId: n.originId,
        sourceRevision: n.sourceRevision,
      };
      api.patch({
        news: [draft, ...api.state.news.filter((x) => x.id !== draft.id)],
      });
      sessionStorage.setItem(
        "purplemagic-news-drafts-v1",
        JSON.stringify(api.state.news.filter((x) => !x.published)),
      );
      step.done = true;
      api.persistPending();
      continue;
    }
    if (step.kind === "task") {
      const taskPath = step.path.replace(/\/run$/, "");
      let task = (await api.http.get(taskPath)).data;
      // Read the permanent task before resubmitting; a lost response must not create a new job.
      while (task.pending > 0 || (step.body.retry && task.failed > 0)) {
        step.body.revision = task.revision;
        api.persistPending();
        try {
          await api.http.post(step.path, step.body, step.id);
        } catch (e) {
          if (!(e instanceof ApiError && e.status === 409)) throw e;
        }
        const next = (await api.http.get(taskPath)).data;
        step.body.retry = false;
        if (next.schedule === "active" || next.schedule === "completed") {
          task = next;
          break;
        }
        if (next.revision === task.revision)
          throw new Error("发放仍在处理中，请稍后查询回执");
        task = next;
      }
      step.result = task;
    } else {
      api.persistPending();
      try {
        const response = await api.http.raw(
          step.path,
          step.method || "POST",
          step.body,
          step.id,
          undefined,
          step.headers,
        );
        step.result = response.data ?? response;
      } catch (e) {
        // Campaign state writes are revision guarded; recover only the exact desired readback.
        if (
          e instanceof ApiError &&
          e.status === 409 &&
          step.path.match(/\/benefits\/[^/]+\/state$/)
        ) {
          const { data } = await api.http.get(
            step.path.replace(/\/state$/, ""),
          );
          if (data.enabled !== step.body.enabled) throw e;
          step.result = data;
        } else throw e;
      }
    }
    step.done = true;
    api.persistPending();
  }
}
