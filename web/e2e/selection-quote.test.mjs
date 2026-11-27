import test from "node:test";
import assert from "node:assert/strict";
import {
  canRenderInlineQuote,
  computeQuotePopoverPosition,
  computeSelectionPopoverPosition,
  formatQuotePreview,
  normalizeQuoteText,
  resolveSelectionQuote,
} from "../src/features/timeline/selectionQuote.ts";

class MockElement {
  constructor(tag, attrs = {}, parent = null) {
    this.nodeType = 1;
    this.tag = tag;
    this.attrs = { ...attrs };
    this.parentElement = parent;
    this.parentNode = parent;
    this.children = [];
    if (parent) parent.children.push(this);
  }

  hasAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attrs, name);
  }

  getAttribute(name) {
    return this.hasAttribute(name) ? this.attrs[name] : null;
  }

  contains(node) {
    let cur = node;
    while (cur) {
      if (cur === this) return true;
      cur = cur.parentNode || cur.parentElement || null;
    }
    return false;
  }
}

class MockTextNode {
  constructor(parent) {
    this.nodeType = 3;
    this.parentElement = parent;
    this.parentNode = parent;
  }
}

test("normalizeQuoteText 清理多余空白、零宽字符并截断超长文本", () => {
  assert.equal(normalizeQuoteText("   \u200B  \n\t  "), "");
  assert.equal(
    normalizeQuoteText("  第一行  \r\n\r\n\r\n  第二行\u200B  "),
    "第一行  \n\n  第二行",
  );
  const longText = "a".repeat(2500);
  const normalized = normalizeQuoteText(longText);
  assert.equal(normalized.length, 1000);
  assert.ok(normalized.endsWith("…"));
});

test("formatQuotePreview 折叠换行为单行并按上限追加省略号", () => {
  assert.equal(formatQuotePreview("第一行\n第二行   第三行", 40), "第一行 第二行 第三行");
  assert.equal(formatQuotePreview("这是一段特别长的引用预览文字内容", 8), "这是一段特别长…");
});

test("resolveSelectionQuote 仅在单条消息正文容器内选区生效", () => {
  const listRoot = new MockElement("div");
  const msg1Body = new MockElement(
    "div",
    {
      "data-quote-message-body": "true",
      "data-quote-event-id": "evt-1",
      "data-quote-sender": "Cycle",
    },
    listRoot,
  );
  const msg1Paragraph = new MockElement("p", {}, msg1Body);
  const msg1TextStart = new MockTextNode(msg1Paragraph);
  const msg1TextEnd = new MockTextNode(msg1Paragraph);

  const msg2Body = new MockElement(
    "div",
    {
      "data-quote-message-body": "true",
      "data-quote-event-id": "evt-2",
      "data-quote-sender": "你",
    },
    listRoot,
  );
  const msg2Text = new MockTextNode(msg2Body);

  const timestampBox = new MockElement("div", { "data-quote-ignore": "true" }, msg1Body);
  const timestampText = new MockTextNode(timestampBox);

  const outsideRoot = new MockElement("div");
  const outsideBody = new MockElement(
    "div",
    {
      "data-quote-message-body": "true",
      "data-quote-event-id": "evt-outside",
      "data-quote-sender": "Other",
    },
    outsideRoot,
  );
  const outsideText = new MockTextNode(outsideBody);

  // 正常单条消息正文内选区
  assert.deepEqual(
    resolveSelectionQuote(
      {
        startContainer: msg1TextStart,
        endContainer: msg1TextEnd,
        text: "  核心结论 A  ",
      },
      listRoot,
    ),
    {
      eventId: "evt-1",
      sender: "Cycle",
      text: "核心结论 A",
    },
  );

  // 跨消息选区应忽略
  assert.equal(
    resolveSelectionQuote(
      {
        startContainer: msg1TextStart,
        endContainer: msg2Text,
        text: "跨消息文本",
      },
      listRoot,
    ),
    null,
  );

  // 命中 data-quote-ignore（时间戳/操作栏/既有引用块）应忽略
  assert.equal(
    resolveSelectionQuote(
      {
        startContainer: timestampText,
        endContainer: timestampText,
        text: "12:00",
      },
      listRoot,
    ),
    null,
  );

  // 选区在当前列表容器外应忽略
  assert.equal(
    resolveSelectionQuote(
      {
        startContainer: outsideText,
        endContainer: outsideText,
        text: "其他列表内容",
      },
      listRoot,
    ),
    null,
  );

  // 空文本选区应忽略
  assert.equal(
    resolveSelectionQuote(
      {
        startContainer: msg1TextStart,
        endContainer: msg1TextEnd,
        text: "   ",
      },
      listRoot,
    ),
    null,
  );
});

test("computeSelectionPopoverPosition 默认顶部居中并在顶部空间不足时自动翻转至下方", () => {
  const topPlaced = computeSelectionPopoverPosition(
    { top: 200, left: 300, width: 120, height: 22 },
    { width: 1280, height: 800 },
  );
  assert.equal(topPlaced.placement, "top");
  assert.equal(topPlaced.left, 360);
  assert.equal(topPlaced.top, 200 - 34 - 8);

  const flippedBottom = computeSelectionPopoverPosition(
    { top: 20, left: 10, width: 40, height: 20 },
    { width: 1280, height: 800 },
  );
  assert.equal(flippedBottom.placement, "bottom");
  assert.equal(flippedBottom.top, 20 + 20 + 8);
  // 贴近左边缘时自动向内收敛不越界（edgePadding=12 + halfWidth=38 = 50）
  assert.ok(flippedBottom.left >= 50);

  const clampedRight = computeSelectionPopoverPosition(
    { top: 200, left: 1260, width: 30, height: 20 },
    { width: 1280, height: 800 },
  );
  assert.ok(clampedRight.left <= 1280 - 50);
});

test("canRenderInlineQuote 仅允许单行简单纯文本短句与轻引用同行混排", () => {
  // 简单短句（含常见行内强调/行内代码）允许行内混排
  assert.equal(canRenderInlineQuote("这个方案不错，可以按这个改"), true);
  assert.equal(canRenderInlineQuote("顺便看下 `ChatReplyBar` 的对比度"), true);

  // 空文本、换行多段与超长段落自动退回顶部单行
  assert.equal(canRenderInlineQuote("   "), false);
  assert.equal(canRenderInlineQuote("第一段\n第二段"), false);
  assert.equal(canRenderInlineQuote("第一行\r\n第二行"), false);
  assert.equal(canRenderInlineQuote("长".repeat(121)), false);
  assert.equal(canRenderInlineQuote("长".repeat(120)), true);

  // 块级 Markdown（标题、列表、引用、代码块、表格、分隔线、图片）禁止行内混排
  assert.equal(canRenderInlineQuote("## 修改方案"), false);
  assert.equal(canRenderInlineQuote("- 第一条建议"), false);
  assert.equal(canRenderInlineQuote("1. 先跑单测"), false);
  assert.equal(canRenderInlineQuote("> 嵌套引用"), false);
  assert.equal(canRenderInlineQuote("```ts const a = 1 ```"), false);
  assert.equal(canRenderInlineQuote("| col1 | col2 |"), false);
  assert.equal(canRenderInlineQuote("---"), false);
  assert.equal(canRenderInlineQuote("![预览](https://example.com/a.png)"), false);

  // 卡片链接会渲染成块级卡片组件，禁止行内混排
  assert.equal(
    canRenderInlineQuote(
      "[任务卡](cycledock://card/12345678-1234-1234-1234-1234567890ab)",
    ),
    false,
  );
  assert.equal(canRenderInlineQuote("参考 cycledock://card/abc-123"), false);
  assert.equal(canRenderInlineQuote("见 [card:abc-123456]"), false);
  assert.equal(canRenderInlineQuote("点这里 [卡片](/cards/proj-1/card-1)"), false);

  // 含 finding/result 前缀、图片或附件时禁止行内混排
  assert.equal(canRenderInlineQuote("简短结论", { hasPrefix: true }), false);
  assert.equal(canRenderInlineQuote("附日志", { hasAttachments: true }), false);
  assert.equal(canRenderInlineQuote("见截图", { hasImages: true }), false);
});

test("computeQuotePopoverPosition 优先置顶并防视口左右及上下裁切", () => {
  // 常规居中置顶
  const normal = computeQuotePopoverPosition(
    { top: 300, left: 400, width: 140, height: 22 },
    { width: 1280, height: 800 },
    { width: 300, height: 100 },
  );
  assert.equal(normal.placement, "top");
  assert.equal(normal.top, 300 - 100 - 8);
  assert.equal(normal.left, 400 + 70 - 150);

  // 右侧气泡贴边时向左夹紧，不溢出视口右边界
  const rightClamped = computeQuotePopoverPosition(
    { top: 300, left: 1120, width: 130, height: 22 },
    { width: 1280, height: 800 },
    { width: 320, height: 100 },
  );
  assert.equal(rightClamped.placement, "top");
  assert.equal(rightClamped.left, 1280 - 12 - 320);

  // 靠近顶部边缘时自动翻转到引用下方
  const flippedBottom = computeQuotePopoverPosition(
    { top: 40, left: 20, width: 120, height: 22 },
    { width: 375, height: 667 },
    { width: 351, height: 120 },
  );
  assert.equal(flippedBottom.placement, "bottom");
  assert.equal(flippedBottom.top, 40 + 22 + 8);
  assert.equal(flippedBottom.left, 12);
});
