import { z } from "zod";
import type {
  CardKind,
  CreateTriggerInput,
  TriggerAuthMode,
  TriggerFilterConfig,
  TriggerProvider,
  UpdateTriggerInput,
  WorkspaceTrigger,
} from "../../shared/api/types";

export const DEFAULT_GENERIC_SIGNATURE_HEADER = "X-Webhook-Signature-256";

export function defaultAuthModeForProvider(
  provider: TriggerProvider,
): TriggerAuthMode {
  if (provider === "github") return "github_hmac_sha256";
  if (provider === "circleci") return "circleci_hmac_sha256";
  return "generic_hmac_sha256";
}

export function parseCommaList(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(/[,，\n]/)) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const key = trimmed.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(trimmed);
  }
  return out;
}

export function parseJSONMatchesText(raw: string): {
  value: Record<string, string>;
  error?: string;
} {
  const out: Record<string, string> = {};
  const lines = raw.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (!line) continue;
    const idx = line.indexOf("=");
    if (idx <= 0) {
      return {
        value: {},
        error: `第 ${i + 1} 行格式无效，请使用 path=value 格式`,
      };
    }
    const key = line.slice(0, idx).trim();
    const val = line.slice(idx + 1).trim();
    if (!key) {
      return {
        value: {},
        error: `第 ${i + 1} 行字段路径不能为空`,
      };
    }
    out[key] = val;
  }
  if (Object.keys(out).length > 20) {
    return {
      value: {},
      error: "JSON 字段匹配最多支持 20 条规则",
    };
  }
  return { value: out };
}

export function formatJSONMatchesText(
  matches?: Record<string, string>,
): string {
  if (!matches) return "";
  return Object.entries(matches)
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

export const triggerFormSchema = z
  .object({
    name: z.string().trim().min(1, "请输入触发器名称").max(120, "名称过长"),
    provider: z.enum(["github", "circleci", "generic"]),
    auth_mode: z.enum([
      "github_hmac_sha256",
      "circleci_hmac_sha256",
      "generic_hmac_sha256",
      "bearer",
    ]),
    signature_header: z.string().trim().max(128, "签名头名称过长"),
    secret: z.string(),
    enabled: z.boolean(),
    routing_mode: z.enum(["match_watchers", "fixed_card", "create_todo"]),
    target_project_id: z.string().trim(),
    target_card_id: z.string().trim(),
    dispatch_mode: z.enum(["enqueue", "interrupt"]),
    cooldown_seconds: z
      .number()
      .int("冷却时间必须为整数")
      .min(0, "冷却时间不能小于 0 秒")
      .max(86400, "冷却时间不能超过 86400 秒"),
    events_text: z.string(),
    actions_text: z.string(),
    conclusions_text: z.string(),
    branches_text: z.string(),
    repositories_text: z.string(),
    match_key_path: z.string().trim().max(128, "Match Key 路径不能超过 128 字符"),
    json_matches_text: z.string(),
  })
  .superRefine((value, ctx) => {
    if (value.routing_mode === "fixed_card" && !value.target_card_id) {
      ctx.addIssue({
        code: "custom",
        path: ["target_card_id"],
        message: "请选择要唤醒的目标卡片",
      });
    }
    if (value.routing_mode === "create_todo" && !value.target_project_id) {
      ctx.addIssue({
        code: "custom",
        path: ["target_project_id"],
        message: "请选择创建待办的目标项目",
      });
    }
    if (value.secret && value.secret.trim().length > 0 && value.secret.trim().length < 16) {
      ctx.addIssue({
        code: "custom",
        path: ["secret"],
        message: "自定义密钥至少需要 16 个字符，或留空自动生成",
      });
    }
    const parsedMatches = parseJSONMatchesText(value.json_matches_text);
    if (parsedMatches.error) {
      ctx.addIssue({
        code: "custom",
        path: ["json_matches_text"],
        message: parsedMatches.error,
      });
    }
  });

export type TriggerFormValues = z.infer<typeof triggerFormSchema>;

export function emptyTriggerFormValues(
  defaultProjectID = "",
): TriggerFormValues {
  return {
    name: "",
    provider: "github",
    auth_mode: "github_hmac_sha256",
    signature_header: "",
    secret: "",
    enabled: true,
    routing_mode: "match_watchers",
    target_project_id: defaultProjectID,
    target_card_id: "",
    dispatch_mode: "enqueue",
    cooldown_seconds: 0,
    events_text: "",
    actions_text: "",
    conclusions_text: "",
    branches_text: "",
    repositories_text: "",
    match_key_path: "",
    json_matches_text: "",
  };
}

export function triggerToFormValues(
  trigger: WorkspaceTrigger,
  fallbackProjectID = "",
): TriggerFormValues {
  const f = trigger.filter_config || {};
  return {
    name: trigger.name,
    provider: trigger.provider,
    auth_mode: trigger.auth_mode,
    signature_header: trigger.signature_header || "",
    secret: "",
    enabled: trigger.enabled,
    routing_mode: trigger.routing_mode,
    target_project_id:
      trigger.target_project_id ||
      trigger.target_card_project_id ||
      fallbackProjectID,
    target_card_id: trigger.target_card_id || "",
    dispatch_mode: trigger.dispatch_mode,
    cooldown_seconds: trigger.cooldown_seconds || 0,
    events_text: (f.events || []).join(", "),
    actions_text: (f.actions || []).join(", "),
    conclusions_text: (f.conclusions || []).join(", "),
    branches_text: (f.branches || []).join(", "),
    repositories_text: (f.repositories || []).join(", "),
    match_key_path: f.match_key_path || "",
    json_matches_text: formatJSONMatchesText(f.json_matches),
  };
}

function buildFilterConfig(values: TriggerFormValues): TriggerFilterConfig {
  const filter: TriggerFilterConfig = {};
  const events = parseCommaList(values.events_text);
  const actions = parseCommaList(values.actions_text);
  const conclusions = parseCommaList(values.conclusions_text);
  const branches = parseCommaList(values.branches_text);
  const repositories = parseCommaList(values.repositories_text);
  const { value: jsonMatches } = parseJSONMatchesText(values.json_matches_text);

  if (events.length > 0) filter.events = events;
  if (actions.length > 0) filter.actions = actions;
  if (conclusions.length > 0) filter.conclusions = conclusions;
  if (branches.length > 0) filter.branches = branches;
  if (repositories.length > 0) filter.repositories = repositories;
  if (Object.keys(jsonMatches).length > 0) filter.json_matches = jsonMatches;
  if (values.match_key_path.trim()) {
    filter.match_key_path = values.match_key_path.trim();
  }
  return filter;
}

export function toCreateTriggerInput(
  values: TriggerFormValues,
): CreateTriggerInput {
  const authMode =
    values.provider === "generic"
      ? values.auth_mode === "bearer"
        ? "bearer"
        : "generic_hmac_sha256"
      : defaultAuthModeForProvider(values.provider);

  const input: CreateTriggerInput = {
    name: values.name.trim(),
    provider: values.provider,
    auth_mode: authMode,
    enabled: values.enabled,
    routing_mode: values.routing_mode,
    dispatch_mode: values.dispatch_mode,
    cooldown_seconds: values.cooldown_seconds,
    filter_config: buildFilterConfig(values),
  };
  if (authMode === "generic_hmac_sha256" && values.signature_header.trim()) {
    input.signature_header = values.signature_header.trim();
  }
  if (values.secret.trim()) {
    input.secret = values.secret.trim();
  }
  if (values.routing_mode === "fixed_card") {
    input.target_card_id = values.target_card_id;
  } else if (values.routing_mode === "create_todo") {
    input.target_project_id = values.target_project_id;
  }
  return input;
}

export function toUpdateTriggerInput(
  values: TriggerFormValues,
): UpdateTriggerInput {
  const input: UpdateTriggerInput = {
    name: values.name.trim(),
    enabled: values.enabled,
    routing_mode: values.routing_mode,
    dispatch_mode: values.dispatch_mode,
    cooldown_seconds: values.cooldown_seconds,
    filter_config: buildFilterConfig(values),
  };
  if (values.provider === "generic" && values.auth_mode === "generic_hmac_sha256") {
    input.signature_header = values.signature_header.trim();
  }
  if (values.routing_mode === "fixed_card") {
    input.target_card_id = values.target_card_id;
    input.target_project_id = "";
  } else if (values.routing_mode === "create_todo") {
    input.target_project_id = values.target_project_id;
    input.target_card_id = "";
  } else {
    input.target_project_id = "";
    input.target_card_id = "";
  }
  return input;
}

export function hasAdvancedTriggerConfig(trigger: WorkspaceTrigger): boolean {
  if (
    trigger.routing_mode !== "match_watchers" ||
    trigger.dispatch_mode !== "enqueue" ||
    trigger.cooldown_seconds > 0
  ) {
    return true;
  }
  if (
    trigger.provider === "generic" &&
    (trigger.auth_mode !== "generic_hmac_sha256" ||
      (trigger.signature_header &&
        trigger.signature_header !== DEFAULT_GENERIC_SIGNATURE_HEADER))
  ) {
    return true;
  }
  const f = trigger.filter_config;
  if (!f) return false;
  return Boolean(
    (f.events && f.events.length > 0) ||
      (f.actions && f.actions.length > 0) ||
      (f.conclusions && f.conclusions.length > 0) ||
      (f.branches && f.branches.length > 0) ||
      (f.repositories && f.repositories.length > 0) ||
      (f.match_key_path && f.match_key_path.trim() !== "") ||
      (f.json_matches && Object.keys(f.json_matches).length > 0),
  );
}

export function getCardRoute(
  projectID: string,
  cardID: string,
  kind?: CardKind,
): string {
  const pid = encodeURIComponent(projectID);
  const cid = encodeURIComponent(cardID);
  if (kind === "discussion") return `/chat/${pid}/${cid}`;
  if (kind === "ongoing") return `/plans/${pid}/${cid}`;
  return `/cards/${pid}/${cid}`;
}

export function formatRemainingCountdown(
  expiresAtISO: string,
  nowMs: number,
  isEnglish: boolean,
): string {
  const targetMs = new Date(expiresAtISO).getTime();
  if (!Number.isFinite(targetMs)) return "";
  const diffSec = Math.round((targetMs - nowMs) / 1000);
  if (diffSec <= 0) {
    return isEnglish ? "Due now" : "即将触发";
  }
  const totalMinutes = Math.ceil(diffSec / 60);
  if (totalMinutes < 60) {
    return isEnglish ? `in ${totalMinutes}m` : `${totalMinutes} 分钟后`;
  }
  const hours = Math.floor(totalMinutes / 60);
  const mins = totalMinutes % 60;
  if (hours < 24) {
    if (mins === 0) {
      return isEnglish ? `in ${hours}h` : `${hours} 小时后`;
    }
    return isEnglish ? `in ${hours}h ${mins}m` : `${hours} 小时 ${mins} 分钟后`;
  }
  const days = Math.floor(hours / 24);
  const remHours = hours % 24;
  if (remHours === 0) {
    return isEnglish ? `in ${days}d` : `${days} 天后`;
  }
  return isEnglish ? `in ${days}d ${remHours}h` : `${days} 天 ${remHours} 小时后`;
}
