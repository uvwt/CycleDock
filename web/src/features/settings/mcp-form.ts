import { z } from "zod";

export const mcpFormSchema = z
  .object({
    name: z.string().trim().min(1, "请输入连接名称"),
    url: z
      .string()
      .trim()
      .url("请输入有效的 MCP URL")
      .refine((value) => {
        try {
          return ["http:", "https:"].includes(new URL(value).protocol);
        } catch {
          return false;
        }
      }, "MCP URL 必须使用 HTTP 或 HTTPS"),
    enabled: z.boolean(),
    timeout_ms: z
      .number()
      .int("超时必须为整数")
      .min(1000, "超时不能少于 1000 毫秒")
      .max(300000, "超时不能超过 300000 毫秒"),
    bearer_token: z.string(),
    clear_bearer_token: z.boolean(),
    tool_guard_mode: z.enum(["disabled", "block_high_risk", "strict"]).optional(),
    tool_guard_model_route_id: z.string().optional(),
    tool_guard_timeout_seconds: z.number().int().min(2).max(60).optional(),
    tool_guard_custom_rules: z.string().optional(),
  })
  .refine((value) => !value.clear_bearer_token || !value.bearer_token, {
    path: ["bearer_token"],
    message: "不能同时填写和清除 Token",
  });

export type MCPFormValues = z.infer<typeof mcpFormSchema>;
