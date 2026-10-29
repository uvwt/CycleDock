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
  })
  .refine((value) => !value.clear_bearer_token || !value.bearer_token, {
    path: ["bearer_token"],
    message: "不能同时填写和清除 Token",
  });

export type MCPFormValues = z.infer<typeof mcpFormSchema>;
