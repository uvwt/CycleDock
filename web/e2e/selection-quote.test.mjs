import test from "node:test";
import assert from "node:assert/strict";
import {
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
