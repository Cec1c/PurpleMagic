import { useEnterMotion } from "../motion";
import { useState, useEffect } from "react";
import { BookOpen, CheckCircle2, RotateCcw, ChevronRight } from "lucide-react";
import { PlayerPicker } from "./Players";
import { resolveRecipients } from "../domain";
import { quests as demoQuests } from "../fixtures";
import { useLive } from "../api/context";
import { LoadMore } from "../api/controls";
import {
  Modal,
  Tabs,
  SearchField,
  CheckBox,
  Pill,
  Empty,
  toggle,
  type PageProps,
} from "../ui";
export function QuestPage({ state, character, request, selection, setSelection }: PageProps) {
  const live = useLive();
  const [picker, setPicker] = useState(false);
  const targets = resolveRecipients(state, selection);
  const quests = state.api?.quests || demoQuests;
  const [status, setStatus] = useState<
      "active" | "all" | "completed" | "catalog"
    >(character.id ? "active" : "catalog"),
    [chapter, setChapter] = useState("全部章节"),
    [q, setQ] = useState(""),
    [selected, setSelected] = useState<string[]>([]),
    [focus, setFocus] = useState("");
  useEffect(() => {
    if (!live) return;
    const timer = setTimeout(() => void live.guard(() => live.quests(status, q)), status === "catalog" ? 250 : 0);
    return () => { clearTimeout(timer); live.cancelScope("quests"); };
  }, [live, character.id, status, status === "catalog" ? q : ""]);
  const states = state.questStates[character.id] || {},
    chapters = ["全部章节", ...new Set(quests.map((q) => q.chapter))];
  const rows = quests.filter(
      (t) =>
        (status === "all" ||
          status === "catalog" ||
          states[t.id] === status ||
          (status === "active" &&
            ["accepted", "ready", "in_progress"].includes(states[t.id]))) &&
        (chapter === "全部章节" || t.chapter === chapter) &&
        `${t.name} ${t.id}`.includes(q),
    ),
    detail = rows.find((t) => t.id === focus) || rows[0];
  const selectedVisible = selected.filter((id) =>
    rows.some((q) => q.id === id),
  );
  const motion = useEnterMotion(status);
  return (
    <>
      <div className="section-tabs">
        <Tabs
          label="任务状态"
          items={[
            { id: "active", label: "进行中" },
            { id: "all", label: "全部任务" },
            { id: "completed", label: "已完成" },
            ...(live ? [{ id: "catalog" as const, label: "任务库" }] : []),
          ]}
          value={status}
          onChange={(v) => {
            live?.navigate("quests", true);
            setStatus(v);
            setSelected([]);
          }}
        />
      </div>
      {status === "catalog" && <div className="toolbar">
        <button className="secondary" onClick={() => setPicker(true)}>选择操作角色（{targets.length}）</button>
        <span className="muted">完成任务不发放任务奖励</span>
      </div>}
      {picker && <Modal title="选择任务操作角色" wide onClose={() => setPicker(false)}><PlayerPicker state={state} selection={selection} setSelection={setSelection} /></Modal>}
      <div ref={motion} className="quest-layout">
        <aside className="glass chapter-list">
          <h3>任务章节</h3>
          {chapters.map((c) => (
            <button
              key={c}
              className={chapter === c ? "active" : ""}
              onClick={() => {
                setChapter(c);
                setSelected([]);
              }}
            >
              <BookOpen size={15} />
              {c}
              <ChevronRight size={14} />
            </button>
          ))}
        </aside>
        <section>
          <div className="toolbar">
            <SearchField
              placeholder="搜索任务名称或 ID"
              value={q}
              onChange={setQ}
            />
            <span className="muted">{rows.length} 个任务</span>
          </div>
          <div className="table-panel glass">
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th className="check-cell">
                      <CheckBox
                        label="全选任务"
                        checked={
                          rows.length > 0 &&
                          rows.every((t) => selected.includes(t.id))
                        }
                        onChange={() =>
                          setSelected(
                            rows.every((t) => selected.includes(t.id))
                              ? []
                              : rows.map((t) => t.id),
                          )
                        }
                      />
                    </th>
                    <th>任务名称</th>
                    <th>等级</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((t) => (
                    <tr
                      key={t.id}
                      className={detail?.id === t.id ? "selected" : ""}
                      onClick={() => setFocus(t.id)}
                    >
                      <td onClick={(e) => e.stopPropagation()}>
                        <CheckBox
                          label={`选择任务 ${t.name}`}
                          checked={selected.includes(t.id)}
                          onChange={() => setSelected(toggle(selected, t.id))}
                        />
                      </td>
                      <td>
                        <button
                          className="text-button"
                          onClick={() => setFocus(t.id)}
                        >
                          {t.name}
                        </button>
                        <small className="cell-meta">
                          {t.kind} · {t.chapter} · #{t.id}
                        </small>
                      </td>
                      <td>Lv.{t.level}</td>
                      <td>
                        <Pill
                          tone={
                            states[t.id] === "completed"
                              ? "green"
                              : states[t.id] === "active"
                                ? "cyan"
                                : "neutral"
                          }
                        >
                          {states[t.id] === "completed"
                            ? "已完成"
                            : states[t.id] === "active"
                              ? "进行中"
                              : live && status === "catalog" && !states[t.id]
                                ? "未查询"
                                : "未开始"}
                        </Pill>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {!rows.length && <Empty title="没有符合条件的任务" />}
            <div className="table-footer">
              <span>已选 {selectedVisible.length} 项</span>
              <div className="button-row">
                <button
                  className="secondary"
                  disabled={!selectedVisible.length || (live ? !state.api?.features["quests.write"] : false) || (status === "catalog" ? !targets.length : !character.id)}
                  onClick={() =>
                    request({
                      kind: "quest",
                      selectedTargets: status === "catalog",
                      characterId: character.id,
                      ids: selectedVisible,
                      status: "active",
                    })
                  }
                >
                  <RotateCcw size={14} />
                  重置选中
                </button>
                <button
                  className="primary"
                  disabled={!selectedVisible.length || (live ? !state.api?.features["quests.write"] : false) || (status === "catalog" ? !targets.length : !character.id)}
                  onClick={() =>
                    request({
                      kind: "quest",
                      selectedTargets: status === "catalog",
                      characterId: character.id,
                      ids: selectedVisible,
                      status: "completed",
                    })
                  }
                >
                  <CheckCircle2 size={15} />
                  完成选中
                </button>
              </div>
            </div>
          </div>
          {detail && (
            <div className="glass quest-detail">
              <BookOpen size={21} />
              <div>
                <h3>{detail.name}</h3>
                <p>
                  {detail.chapter} / {detail.kind} · 任务 ID {detail.id} · Lv.
                  {detail.level}
                </p>
              </div>
              <Pill>
                {states[detail.id] === "completed"
                  ? "已完成"
                  : states[detail.id] === "active"
                    ? "进行中"
                    : live && status === "catalog" && !states[detail.id]
                      ? "未查询"
                      : "未开始"}
              </Pill>
            </div>
          )}
        </section>
      </div>
      <LoadMore
        show={!!state.api?.questNext}
        onClick={() => void live?.guard(() => live.quests(status, q, true))}
      />
    </>
  );
}
