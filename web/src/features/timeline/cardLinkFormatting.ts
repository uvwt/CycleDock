export interface CardLinkTarget {
  cardId: string;
  projectId?: string;
}

/**
 * 解析卡片协议链接，返回对应的 cardId（以及可选的 projectId）。
 * 支持格式：
 * - cycledock://card/<card_id>
 * - cycledock://card/<project_id>/<card_id>
 * - card:<card_id>
 * - /cards/<project_id>/<card_id> 或 /chat/<project_id>/<card_id>
 */
export function parseCardLink(href?: string): CardLinkTarget | null {
  if (!href) return null;

  let path = href.trim();
  // 兼容同源完整 URL 提取 pathname
  if (path.startsWith("http://") || path.startsWith("https://")) {
    try {
      const url = new URL(path);
      path = url.pathname;
    } catch {
      // 忽略无法解析的 URL，保持原 path
    }
  }

  // Match cycledock://card/:cardId or cycledock://card/:projectId/:cardId
  if (path.startsWith("cycledock://card/")) {
    const parts = path.replace("cycledock://card/", "").split("/").filter(Boolean);
    if (parts.length === 1) {
      return { cardId: parts[0] };
    }
    if (parts.length >= 2) {
      return { projectId: parts[0], cardId: parts[1] };
    }
  }

  // Match [card:UUID] or card:UUID
  if (path.startsWith("card:")) {
    const cardId = path.replace("card:", "").trim();
    if (cardId) return { cardId };
  }

  // Match /cards/:projectId/:cardId or /cards/:cardId 以及 /chat/:projectId/:cardId 或 /chat/:cardId
  const match = path.match(/^\/(?:cards|chat)\/([^/?#]+)(?:\/([^/?#]+))?/);
  if (match) {
    if (match[2]) {
      return { projectId: match[1], cardId: match[2] };
    }
    return { cardId: match[1] };
  }

  return null;
}

interface CardLinkSpan {
  start: number;
  end: number;
  markdown: string;
}

const FENCE_OPEN_RE = /^([ ]{0,3})(`{3,}|~{3,})(.*)$/;
const LIST_ITEM_PREFIX_RE =
  /^((?:[ \t]*>[ \t]*)*)([ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+(?:\[[ xX]\][ \t]+)?)/;
const BLOCKQUOTE_PREFIX_RE = /^((?:[ \t]*>[ \t]*)+)/;
const LEADING_PUNCT_RE = /^[：:、，,；;。—–]+/;
const LEADING_ASCII_DOT_RE = /^\.(?=\s|$)/;
const TRAILING_PUNCT_RE = /[、，,；;—–]+$/;
const ONLY_PUNCT_RE = /^[：:、，,；;。.\-—–\s]+$/;

/**
 * 将 Markdown 文本切分为围栏代码块与普通文本段，确保代码块内部内容完全不被改写。
 */
function splitFencedCodeBlocks(
  text: string,
): Array<{ isCode: boolean; text: string }> {
  const lines = text.split(/\r?\n/);
  const segments: Array<{ isCode: boolean; text: string }> = [];
  let buffer: string[] = [];
  let inFence = false;
  let fenceChar = "";
  let fenceLen = 0;

  const flush = (isCode: boolean) => {
    if (buffer.length === 0) return;
    segments.push({ isCode, text: buffer.join("\n") });
    buffer = [];
  };

  for (const line of lines) {
    if (!inFence) {
      const openMatch = line.match(FENCE_OPEN_RE);
      if (openMatch) {
        const marker = openMatch[2];
        const info = openMatch[3] ?? "";
        // CommonMark 规定反引号围栏的 info string 不能包含反引号
        if (!(marker[0] === "`" && info.includes("`"))) {
          flush(false);
          inFence = true;
          fenceChar = marker[0];
          fenceLen = marker.length;
          buffer.push(line);
          continue;
        }
      }
      buffer.push(line);
    } else {
      buffer.push(line);
      const trimmed = line.trim();
      if (
        trimmed.length >= fenceLen &&
        trimmed[0] === fenceChar &&
        new RegExp(`^\\${fenceChar}{${fenceLen},}$`).test(trimmed)
      ) {
        flush(true);
        inFence = false;
        fenceChar = "";
        fenceLen = 0;
      }
    }
  }

  flush(inFence);
  return segments;
}

/**
 * 用无冲突占位符临时保护行内代码 span（`...` / ``...``），避免误改其中的示例链接或标点。
 */
function protectInlineCodeSpans(prose: string): {
  masked: string;
  restore: (input: string) => string;
} {
  let prefix = "\uE000CDCODE";
  while (prose.includes(prefix)) {
    prefix += "_";
  }

  const spans: string[] = [];
  let out = "";
  let i = 0;

  while (i < prose.length) {
    if (prose[i] === "\\" && i + 1 < prose.length) {
      out += prose.slice(i, i + 2);
      i += 2;
      continue;
    }

    if (prose[i] === "`") {
      let tickLen = 0;
      while (i + tickLen < prose.length && prose[i + tickLen] === "`") {
        tickLen++;
      }

      // 在当前段落内查找成对的等长反引号闭合点
      let j = i + tickLen;
      let closeStart = -1;
      while (j < prose.length) {
        if (prose[j] === "\n" && prose[j + 1] === "\n") {
          break;
        }
        if (prose[j] === "`") {
          let closeLen = 0;
          while (j + closeLen < prose.length && prose[j + closeLen] === "`") {
            closeLen++;
          }
          if (closeLen === tickLen) {
            closeStart = j;
            break;
          }
          j += closeLen;
          continue;
        }
        j++;
      }

      if (closeStart !== -1) {
        const token = `${prefix}_${spans.length}\uE001`;
        spans.push(prose.slice(i, closeStart + tickLen));
        out += token;
        i = closeStart + tickLen;
        continue;
      }

      out += prose.slice(i, i + tickLen);
      i += tickLen;
      continue;
    }

    out += prose[i];
    i++;
  }

  return {
    masked: out,
    restore: (input: string) => {
      if (spans.length === 0) return input;
      let restored = input;
      for (let idx = 0; idx < spans.length; idx++) {
        restored = restored.replaceAll(`${prefix}_${idx}\uE001`, spans[idx]);
      }
      return restored;
    },
  };
}

/**
 * 将正文中裸露的 card:UUID 或 cycledock://card/... 规范化为 Markdown 链接，
 * 避免因缺少 Markdown 括号而无法触发 parseCardLink 拦截渲染。
 */
function normalizeBareCardUrls(prose: string): string {
  // 0. 将尖括号自动链接 <cycledock://card/...> 转为标准 Markdown 链接
  let result = prose.replace(
    /<(cycledock:\/\/card\/[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)?)>/g,
    "[查看卡片详情]($1)",
  );

  // 1. 将未被 Markdown 链接包裹的 cycledock://card/... 转为 [查看卡片详情](cycledock://card/...)
  result = result.replace(
    /(^|[^([="'<])(cycledock:\/\/card\/[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)?)/g,
    "$1[查看卡片详情]($2)",
  );

  // 2. 将未被括号链接包裹的 [card:UUID] 转为 [查看卡片详情](card:UUID)
  result = result.replace(
    /\[(card:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}))\](?!\()/g,
    "[查看卡片详情]($1)",
  );

  // 3. 将未被 Markdown 链接包裹的裸 card:UUID 转为 [查看卡片详情](card:UUID)
  result = result.replace(
    /(^|[^([="'<])(card:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}))/g,
    "$1[查看卡片详情]($2)",
  );

  return result;
}

/**
 * 扫描单行文本中所有会渲染为块级 ChatActionCard 的 Markdown 链接（支持标题内嵌套方括号与外围加粗包裹）。
 */
function findCardLinksInLine(line: string): CardLinkSpan[] {
  const spans: CardLinkSpan[] = [];
  let i = 0;

  while (i < line.length) {
    if (line[i] === "\\" && i + 1 < line.length) {
      i += 2;
      continue;
    }

    if (line[i] === "[" && (i === 0 || line[i - 1] !== "!")) {
      // 扫描链接文本，允许单层嵌套方括号（如 [[Fix] 优化排版](cycledock://card/1)）
      let j = i + 1;
      let bracketDepth = 1;
      while (j < line.length && bracketDepth > 0) {
        if (line[j] === "\\" && j + 1 < line.length) {
          j += 2;
          continue;
        }
        if (line[j] === "[") {
          bracketDepth++;
        } else if (line[j] === "]") {
          bracketDepth--;
          if (bracketDepth === 0) break;
        }
        j++;
      }

      if (bracketDepth === 0 && line[j + 1] === "(") {
        // 扫描链接目标 URL，允许 URL 内部成对圆括号
        let k = j + 2;
        let parenDepth = 1;
        let hasSpace = false;
        while (k < line.length && parenDepth > 0) {
          if (line[k] === "\\" && k + 1 < line.length) {
            k += 2;
            continue;
          }
          if (/\s/.test(line[k])) {
            hasSpace = true;
            break;
          }
          if (line[k] === "(") {
            parenDepth++;
          } else if (line[k] === ")") {
            parenDepth--;
            if (parenDepth === 0) break;
          }
          k++;
        }

        if (!hasSpace && parenDepth === 0) {
          const href = line.slice(j + 2, k);
          if (parseCardLink(href)) {
            let start = i;
            let end = k + 1;
            const rawLink = line.slice(i, end);

            // 若整张卡片链接紧贴成对加粗标记（**[标题](url)** 或 __[标题](url)__），
            // 在拆分块级卡片时一并剥离外围多余强调符。
            for (const marker of ["**", "__"]) {
              if (
                start >= marker.length &&
                line.slice(start - marker.length, start) === marker &&
                line.slice(end, end + marker.length) === marker
              ) {
                start -= marker.length;
                end += marker.length;
                break;
              }
            }

            spans.push({ start, end, markdown: rawLink });
            i = end;
            continue;
          }
        }
      }
    }

    i++;
  }

  return spans;
}

/**
 * 若文本以紧贴卡片的完整括号补充说明开头（如 `（补充说明）` 或 `(note)`），
 * 拆掉外层括号并返回整理后的正文；未闭合的流式括号保持原样不动。
 */
function unwrapLeadingParenthetical(text: string): string {
  const leadingMatch = text.match(/^[ \t]*([（(])/);
  if (!leadingMatch) return text;

  const openChar = leadingMatch[1];
  const closeChar = openChar === "（" ? "）" : ")";
  const openIdx = text.indexOf(openChar);
  let depth = 0;
  let closeIdx = -1;

  for (let i = openIdx; i < text.length; i++) {
    const ch = text[i];
    if (ch === openChar) {
      depth++;
    } else if (ch === closeChar) {
      depth--;
      if (depth === 0) {
        closeIdx = i;
        break;
      }
    }
  }

  if (closeIdx === -1) return text;

  const inner = text.slice(openIdx + 1, closeIdx).trim();
  const rest = text.slice(closeIdx + 1);
  if (!inner) {
    return rest;
  }

  // 若内部已有句末标点且紧跟孤立句号，避免出现重复句号
  if (/[。！？.!?]$/.test(inner) && /^[ \t]*[。.][ \t]*$/.test(rest)) {
    return inner;
  }
  return inner + rest;
}

/**
 * 清理紧接在块级卡片后面的前导孤立标点与括号说明。
 */
function cleanTextAfterCard(raw: string): string {
  let text = raw.replace(/^[ \t]+/, "");
  if (!text) return "";

  text = unwrapLeadingParenthetical(text);

  // 循环剥离新行开头无意义的冒号、顿号、逗号、分号、句号及破折号
  let changed = true;
  while (changed && text.length > 0) {
    changed = false;
    const trimmed = text.replace(/^[ \t]+/, "");
    if (trimmed !== text) {
      text = trimmed;
      changed = true;
    }
    const punct = text.match(LEADING_PUNCT_RE);
    if (punct) {
      text = text.slice(punct[0].length);
      changed = true;
      continue;
    }
    const asciiDot = text.match(LEADING_ASCII_DOT_RE);
    if (asciiDot) {
      text = text.slice(1);
      changed = true;
    }
  }

  return text.trim();
}

/**
 * 清理紧贴在块级卡片前面的孤立标点或多余顿号/逗号；保留有意义的前导冒号或完整句号（如 `已派发：`）。
 */
function cleanTextBeforeCard(raw: string): string {
  let text = raw.replace(/[ \t]+$/, "");
  if (!text || ONLY_PUNCT_RE.test(text)) {
    return "";
  }
  text = text.replace(TRAILING_PUNCT_RE, "").replace(/[ \t]+$/, "");
  if (!text || ONLY_PUNCT_RE.test(text)) {
    return "";
  }
  return text;
}

function countMarkerOccurrences(text: string, marker: string): number {
  let count = 0;
  let pos = 0;
  while ((pos = text.indexOf(marker, pos)) !== -1) {
    count++;
    pos += marker.length;
  }
  return count;
}

/**
 * 将正文中紧邻卡片链接的标点与同行正文拆分为独立段落/列表续行，
 * 避免块级 ChatActionCard 前后出现孤立的冒号、逗号、顿号、句号或括号。
 */
function formatProseCardBlocks(prose: string): string {
  const lines = prose.split("\n");
  const outLines: string[] = [];
  let prevLineEndedWithCard = false;

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      outLines.push("");
      continue;
    }

    // 不拆解 Markdown 表格行，以免破坏表格结构
    if (trimmed.startsWith("|")) {
      outLines.push(line);
      prevLineEndedWithCard = false;
      continue;
    }

    // 提取当前行的 Markdown 结构前缀（引用前缀 / 列表标记 / 缩进）
    let structuralPrefix = "";
    let continuationPrefix = "";
    let isListItem = false;
    let body = line;

    const listMatch = line.match(LIST_ITEM_PREFIX_RE);
    if (listMatch) {
      isListItem = true;
      const quotePrefix = listMatch[1] ?? "";
      const listMarker = listMatch[2] ?? "";
      structuralPrefix = quotePrefix + listMarker;
      continuationPrefix = quotePrefix + " ".repeat(listMarker.length);
      body = line.slice(structuralPrefix.length);
    } else {
      const quoteMatch = line.match(BLOCKQUOTE_PREFIX_RE);
      if (quoteMatch) {
        structuralPrefix = quoteMatch[1];
        continuationPrefix = quoteMatch[1];
        body = line.slice(structuralPrefix.length);
      } else {
        const indentMatch = line.match(/^([ \t]+)/);
        if (indentMatch) {
          structuralPrefix = indentMatch[1];
          continuationPrefix = indentMatch[1];
          body = line.slice(structuralPrefix.length);
        }
      }
    }

    // 若上一非空行以卡片结尾，且当前行以孤立标点或紧贴括号开头，先做跨行兜底清洗
    if (prevLineEndedWithCard && !isListItem) {
      const cleaned = cleanTextAfterCard(body);
      if (!cleaned) {
        // 丢弃仅剩孤立标点的行（如卡片下一行单独挂着的 `。`）
        continue;
      }
      body = cleaned;
    }

    const cardSpans = findCardLinksInLine(body);
    if (cardSpans.length === 0) {
      outLines.push(structuralPrefix + body);
      prevLineEndedWithCard = false;
      continue;
    }

    // 切分文本片段与卡片链接
    const textParts: string[] = [];
    let cursor = 0;
    for (const span of cardSpans) {
      textParts.push(body.slice(cursor, span.start));
      cursor = span.end;
    }
    textParts.push(body.slice(cursor));

    // 对卡片左右两侧相邻片段做配对清理
    for (let idx = 0; idx < cardSpans.length; idx++) {
      let before = textParts[idx];
      let after = textParts[idx + 1];

      // 1. 剥离包裹卡片本身的成对括号，如 `任务（[A](cycledock://card/1)）。`
      const wrapOpenMatch = before.match(/([（(])[ \t]*$/);
      if (wrapOpenMatch) {
        const expectedClose = wrapOpenMatch[1] === "（" ? "）" : ")";
        const closeLead = new RegExp(`^[ \\t]*\\${expectedClose}`);
        if (closeLead.test(after)) {
          before = before.slice(0, before.length - wrapOpenMatch[0].length);
          after = after.replace(closeLead, "");
        }
      }

      // 2. 修复跨卡片分割导致的未闭合加粗标记（如 `**已派发：[A](...)` + `**`）
      for (const marker of ["**", "__"]) {
        if (
          countMarkerOccurrences(before, marker) % 2 === 1 &&
          after.trimStart().startsWith(marker)
        ) {
          before = before.replace(/[ \t]+$/, "") + marker;
          after = after.trimStart().slice(marker.length);
        }
      }

      textParts[idx] = cleanTextBeforeCard(before);
      textParts[idx + 1] = cleanTextAfterCard(after);
    }

    const chunks: string[] = [];
    for (let idx = 0; idx < cardSpans.length; idx++) {
      if (textParts[idx]) {
        chunks.push(textParts[idx]);
      }
      chunks.push(cardSpans[idx].markdown);
    }
    if (textParts[cardSpans.length]) {
      chunks.push(textParts[cardSpans.length]);
    }

    if (chunks.length === 0) {
      prevLineEndedWithCard = false;
      continue;
    }

    const lastChunkIsCard =
      !textParts[cardSpans.length] && cardSpans.length > 0;

    if (isListItem) {
      outLines.push(structuralPrefix + chunks[0]);
      for (let idx = 1; idx < chunks.length; idx++) {
        outLines.push(continuationPrefix + chunks[idx]);
      }
    } else if (continuationPrefix) {
      // 缩进续行或引用块内保持所属上下文前缀
      const sep = structuralPrefix.includes(">")
        ? `\n${continuationPrefix.trimEnd()}\n`
        : "\n";
      outLines.push(
        chunks
          .map((chunk, idx) =>
            idx === 0 ? structuralPrefix + chunk : continuationPrefix + chunk,
          )
          .join(sep),
      );
    } else {
      outLines.push(chunks.join("\n\n"));
    }

    prevLineEndedWithCard = lastChunkIsCard;
  }

  return outLines.join("\n").replace(/\n{3,}/g, "\n\n");
}

/**
 * 规范化正文中卡片引用标记：
 * 1. 保护围栏代码块与行内代码，绝不误改示例或源码；
 * 2. 将裸露的 cycledock://card/... 与 card:UUID 转为标准 Markdown 卡片链接；
 * 3. 将卡片链接与紧邻正文拆分为独立块级段落，剥离卡片前后孤立冒号、逗号、顿号、句号及括号说明。
 */
export function normalizeCardMentions(text: string): string {
  if (!text) return "";

  const segments = splitFencedCodeBlocks(text);
  return segments
    .map((segment) => {
      if (segment.isCode) {
        return segment.text;
      }
      const { masked, restore } = protectInlineCodeSpans(segment.text);
      const withLinks = normalizeBareCardUrls(masked);
      const formatted = formatProseCardBlocks(withLinks);
      return restore(formatted);
    })
    .join("\n");
}
