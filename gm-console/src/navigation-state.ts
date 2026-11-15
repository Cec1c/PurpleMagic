import type { ConsoleAPI } from "./api/console";
import type { DTO } from "./api/model";
import type { InventoryTab } from "./domain";

export type DirectoryMemory = {
  initialized?: boolean;
  mode: "accounts" | "characters";
  q: string;
  next: string;
  ids: string[];
  accountIds: string[];
  online?: string;
  accountId?: string;
};

export type GmTabId = "growth" | "quotas" | "currency" | "premium" | "history";

let demoDirectory: DirectoryMemory | null = null;
let demoAntiCheat: DTO = {};
let demoInventoryTab: InventoryTab = "bag";
let demoGmTab: GmTabId = "growth";

export function readDirectoryMemory(live: ConsoleAPI | null): DirectoryMemory | undefined {
  return live?.meta?.directory ? (live.meta.directory as DirectoryMemory) : (demoDirectory ?? undefined);
}

export function writeDirectoryMemory(live: ConsoleAPI | null, patch: Partial<DirectoryMemory>) {
  if (live?.meta?.directory) {
    live.meta.directory = {
      ...live.meta.directory,
      ...patch,
      initialized: true,
    } as typeof live.meta.directory;
  } else {
    demoDirectory = {
      mode: "accounts",
      q: "",
      next: "",
      ids: [],
      accountIds: [],
      online: "all",
      accountId: "",
      ...(demoDirectory || {}),
      ...patch,
      initialized: true,
    };
  }
}

export function readAntiCheatMemory(live: ConsoleAPI | null): DTO {
  return live?.meta ? (live.meta.antiCheatState || {}) : demoAntiCheat;
}

export function writeAntiCheatMemory(live: ConsoleAPI | null, patch: DTO) {
  if (live && typeof live.setAntiCheatState === "function") {
    live.setAntiCheatState(patch);
  } else if (live?.meta) {
    live.meta.antiCheatState = { ...(live.meta.antiCheatState || {}), ...patch };
  } else {
    demoAntiCheat = { ...demoAntiCheat, ...patch };
  }
}

export function resetNavigationMemoryForTests() {
  demoDirectory = null;
  demoAntiCheat = {};
  demoInventoryTab = "bag";
  demoGmTab = "growth";
}

export function readInventoryTabMemory(live: ConsoleAPI | null): InventoryTab {
  return live?.meta?.inventory ? ((live.meta.inventory.tab || "bag") as InventoryTab) : demoInventoryTab;
}

export function writeInventoryTabMemory(live: ConsoleAPI | null, tab: InventoryTab) {
  if (live && typeof live.setInventoryTab === "function") {
    live.setInventoryTab(tab);
  } else {
    demoInventoryTab = tab;
  }
}

export function readGmTabMemory(live: ConsoleAPI | null): GmTabId {
  return live?.meta ? (live.meta.gmTab || "growth") : demoGmTab;
}

export function writeGmTabMemory(live: ConsoleAPI | null, tab: GmTabId) {
  if (live && typeof live.setGmTab === "function") {
    live.setGmTab(tab);
  } else {
    demoGmTab = tab;
  }
}
