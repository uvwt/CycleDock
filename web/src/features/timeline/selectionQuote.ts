export const MAX_QUOTE_TEXT_LENGTH = 1000;
export const MAX_QUOTE_SENDER_LENGTH = 100;
export const MAX_QUOTE_PREVIEW_LENGTH = 140;

export interface TextSelectionAnchor {
  startContainer: Node | null;
  endContainer: Node | null;
  text: string;
}

export interface SelectionQuoteCandidate {
  eventId: string;
  sender: string;
  text: string;
}

export interface SelectionPopoverBounds {
  top: number;
  left: number;
  width: number;
  height: number;
}

export interface PopoverPlacement {
  top: number;
  left: number;
  placement: "top" | "bottom";
}

/** 清理并截断选区或整条引用正文，保证与后端长度上限和换行规范一致。 */
export function normalizeQuoteText(
  raw: string,
  maxLength = MAX_QUOTE_TEXT_LENGTH,
): string {
  const cleaned = raw
    .replace(/\u200B/g, "")
    .replace(/\r\n?/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
  if (!cleaned) return "";
  const chars = Array.from(cleaned);
  if (chars.length <= maxLength) return cleaned;
  return `${chars.slice(0, Math.max(1, maxLength - 1)).join("").trimEnd()}…`;
}

/** 将可能包含多行的引用内容折叠为适合单行预览条或动作条展示的摘要文本。 */
export function formatQuotePreview(
  raw: string,
  maxLength = MAX_QUOTE_PREVIEW_LENGTH,
): string {
  const singleLine = raw.replace(/\s+/g, " ").trim();
  if (!singleLine) return "";
  const chars = Array.from(singleLine);
  if (chars.length <= maxLength) return singleLine;
  return `${chars.slice(0, Math.max(1, maxLength - 1)).join("").trimEnd()}…`;
}

/**
 * 校验节点是否落在单条消息的可划选正文容器内，且不在被禁止误触的操作按钮/时间戳区域内。
 * 通过纯 DOM 属性读取，便于在单元测试和浏览器运行时复用。
 */
export function findMessageBodyContainer(
  node: Node | null,
  root: HTMLElement | null,
): HTMLElement | null {
  if (!node || !root) return null;
  let current: HTMLElement | null =
    node.nodeType === 1
      ? (node as HTMLElement)
      : (node.parentElement ?? null);
  while (current && current !== root) {
    if (current.hasAttribute("data-quote-ignore")) {
      return null;
    }
    if (current.hasAttribute("data-quote-message-body")) {
      return root.contains(current) ? current : null;
    }
    current = current.parentElement;
  }
  return null;
}

/**
 * 从当前选区解析单条消息内的引用候选。
 * 严格要求起止点位于同一条消息的正文容器内，排除跨消息选区和空选区。
 */
export function resolveSelectionQuote(
  selection: TextSelectionAnchor | null,
  root: HTMLElement | null,
): SelectionQuoteCandidate | null {
  if (!selection || !root) return null;
  const text = normalizeQuoteText(selection.text);
  if (!text) return null;

  const startBody = findMessageBodyContainer(selection.startContainer, root);
  if (!startBody) return null;
  const endBody = findMessageBodyContainer(selection.endContainer, root);
  if (!endBody || startBody !== endBody) return null;

  const eventId = startBody.getAttribute("data-quote-event-id")?.trim() || "";
  if (!eventId) return null;

  const sender =
    startBody.getAttribute("data-quote-sender")?.trim() || "Cycle";
  return {
    eventId,
    sender: Array.from(sender).slice(0, MAX_QUOTE_SENDER_LENGTH).join(""),
    text,
  };
}

/**
 * 计算桌面端「引用」浮钮位置：优先居中置于选区正上方，顶部空间不足时自动翻转到选区下方，
 * 同时对左右边界做视口内夹紧，避免贴边溢出。
 */
export function computeSelectionPopoverPosition(
  selectionRect: SelectionPopoverBounds,
  viewport: { width: number; height: number },
  popoverSize: { width: number; height: number } = { width: 76, height: 34 },
  gap = 8,
  edgePadding = 12,
): PopoverPlacement {
  const rawLeft = selectionRect.left + selectionRect.width / 2;
  const halfWidth = popoverSize.width / 2;
  const minLeft = edgePadding + halfWidth;
  const maxLeft = Math.max(minLeft, viewport.width - edgePadding - halfWidth);
  const left = Math.min(Math.max(rawLeft, minLeft), maxLeft);

  const topCandidate = selectionRect.top - popoverSize.height - gap;
  if (topCandidate >= edgePadding) {
    return { top: topCandidate, left, placement: "top" };
  }

  const bottomCandidate = selectionRect.top + selectionRect.height + gap;
  const maxTop = Math.max(
    edgePadding,
    viewport.height - popoverSize.height - edgePadding,
  );
  return {
    top: Math.min(bottomCandidate, maxTop),
    left,
    placement: "bottom",
  };
}
