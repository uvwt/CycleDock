import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * 安全生成 RFC 4122 v4 UUID。
 *
 * 为什么需要 fallback：
 * 浏览器的原生 `crypto.randomUUID()` 依赖于「安全上下文 (Secure Context)」，
 * 即仅在 HTTPS 或 localhost 环境下可用。
 * 当通过局域网 IP（如 http://192.168.x.x:15173）在移动设备或非本地机器上测试调试时，
 * `crypto.randomUUID` 会被浏览器置为 undefined，直接调用会抛出 "crypto.randomUUID is not a function"。
 *
 * 本函数提供三级兜底：
 * 1. 优先使用标准原生 crypto.randomUUID()；
 * 2. 其次使用密码学安全的 crypto.getRandomValues() 生成符合 v4 规范的 UUID；
 * 3. 最终兜底使用 Math.random()，确保任何非安全上下文与旧环境永远不抛异常中断执行。
 */
export function safeRandomUUID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }

  if (typeof crypto !== "undefined" && typeof crypto.getRandomValues === "function") {
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    // RFC 4122 v4: 将第 6 字节高 4 位设为 0100 (4)，第 8 字节高 2 位设为 10 (variant)
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }

  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === "x" ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}
