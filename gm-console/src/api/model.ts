import type { State, Quest, Item } from "../domain";
export type DTO = Record<string, any>;
export type LiveData = {
  authenticated: boolean;
  authReady: boolean;
  features: Record<string, boolean>;
  busy: boolean;
  reading: number;
  error: string;
  directory: {
    mode: "accounts" | "characters";
    q: string;
    next: string;
    ids: string[];
    accountIds: string[];
    online?: string;
  };
  inventory: {
    tab: string;
    next: Record<string, string>;
    pvf: string;
    loading: boolean;
  };
  quests: Quest[];
  questNext: string;
  questPVF: string;
  questFilter: { status: string; q: string };
  catalog: Item[];
  catalogNext: string;
  catalogQuery: string;
  catalogTotal: number;
  catalogLoading: boolean;
  jobs: { value: string; label: string }[];
  jobsRaw: import("../professions").ProfessionDefinition[];
  resources: Record<string, DTO>;
  taskNext: string;
  historyNext: Record<string, string>;
  benefitNext: string;
  mailNext: string;
  hub?: DTO;
  hubError?: string;
  pending?: Pending;
};
export type Step = {
  id: string;
  label: string;
  path: string;
  body: DTO;
  method?: string;
  headers?: Record<string, string>;
  result?: any;
  done?: boolean;
  kind?: "task" | "benefit-enable" | "activities";
  attempted?: boolean;
  refreshRead?: string;
  refreshed?: boolean;
};
export type Pending = {
  activityChanges?: { id: number; name: string; before: boolean; enabled: boolean }[];
  id: string;
  title: string;
  target: string;
  summary: string;
  steps: Step[];
  recipients?: { id: string; name: string; accountId: string }[];
  confirmed: boolean;
  hidden?: boolean;
  completed?: boolean;
  rejected?: boolean;
  draftId?: string;
  error?: string;
  errorCode?: string;
};
export const emptyState = (): State => ({
  schema: 1,
  accounts: [],
  characters: [],
  items: [],
  mails: [],
  questStates: {},
  benefits: [],
  receipts: [],
  news: [],
  sources: { download: "", gameHost: "", gamePort: 0 },
  api: {
    authenticated: false,
    authReady: false,
    features: {},
    busy: false,
    reading: 0,
    error: "",
    directory: { mode: "characters", q: "", next: "", ids: [], accountIds: [] },
    inventory: { tab: "bag", next: {}, pvf: "", loading: false },
    quests: [],
    questNext: "",
    questPVF: "",
    questFilter: { status: "active", q: "" },
    catalog: [],
    catalogNext: "",
    catalogQuery: "",
    catalogTotal: 0,
    catalogLoading: false,
    jobs: [],
    jobsRaw: [],
    resources: {},
    taskNext: "",
    historyNext: {},
    benefitNext: "",
    mailNext: "",
  },
});
